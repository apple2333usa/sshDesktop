// Package files 是文件管理模块：浏览、编辑、上传下载、复制移动、回收站、压缩解压、权限。
package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/gin-gonic/gin"
)

const maxEditSize = 10 << 20

type Deps struct {
	Nodes *node.Registry
	Tasks *task.Manager
	// Confirm 校验二级危险操作的密码确认，失败时已输出响应并返回 false。
	Confirm func(c *gin.Context) bool
	// Audit 返回记录审计日志的中间件。
	Audit func(module, action string) gin.HandlerFunc
}

type Service struct {
	d      Deps
	owners *ownerSet
}

func New(d Deps) *Service { return &Service{d: d, owners: newOwnerSet()} }

// Register 注册到 /api/v1/nodes/:node 下。view / manage 是权限校验中间件。
func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/files")
	g.GET("/list", view, s.list)
	g.GET("/stat", view, s.stat)
	g.GET("/mounts", view, s.mounts)
	g.GET("/properties", view, s.properties)
	g.GET("/search", view, s.search)
	g.GET("/content", view, s.readContent)
	g.GET("/raw", view, s.raw)
	g.GET("/download", view, a("files", "download"), s.download)
	g.GET("/owners", view, s.ownerList)
	g.GET("/trash", view, s.trashList)

	g.PUT("/content", manage, a("files", "edit"), s.writeContent)
	g.POST("/mkdir", manage, a("files", "mkdir"), s.mkdir)
	g.POST("/create", manage, a("files", "create"), s.create)
	g.POST("/rename", manage, a("files", "rename"), s.rename)
	g.POST("/paste", manage, a("files", "paste"), s.paste)
	g.POST("/delete", manage, a("files", "delete"), s.delete)
	g.POST("/chmod", manage, a("files", "chmod"), s.chmod)
	g.POST("/chown", manage, a("files", "chown"), s.chown)
	g.POST("/compress", manage, a("files", "compress"), s.compress)
	g.POST("/extract", manage, a("files", "extract"), s.extract)
	g.POST("/upload/init", manage, s.uploadInit)
	g.PUT("/upload/chunk", manage, s.uploadChunk)
	g.POST("/upload/complete", manage, a("files", "upload"), s.uploadComplete)
	g.POST("/trash/restore", manage, a("files", "trash-restore"), s.trashRestore)
	g.POST("/trash/delete", manage, a("files", "trash-delete"), s.trashDelete)
	g.POST("/trash/empty", manage, a("files", "trash-empty"), s.trashEmpty)
}

func (s *Service) fs(c *gin.Context) (vfs.FS, bool) {
	n, err := s.d.Nodes.Get(c.Param("node"))
	if err != nil {
		api.Fail(c, api.NotFound("节点不存在"))
		return nil, false
	}
	return n.FS(), true
}

// cleanPath 规范化路径：必须是绝对路径，不能包含空字节。
func cleanPath(p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) || !strings.HasPrefix(p, "/") {
		return "", api.BadRequest("路径无效：" + p)
	}
	return path.Clean(p), nil
}

func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") || len(name) > 255 {
		return api.BadRequest("文件名无效")
	}
	return nil
}

// Entry 是目录列表中的一项。
type Entry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`  // dir / file / link / other
	IsDir      bool   `json:"isDir"` // 链接指向目录时也为 true
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	Perm       string `json:"perm"`
	Owner      string `json:"owner"`
	Group      string `json:"group"`
	UID        int    `json:"uid"`
	GID        int    `json:"gid"`
	MTime      int64  `json:"mtime"` // 毫秒
	LinkTarget string `json:"linkTarget,omitempty"`
	Broken     bool   `json:"broken,omitempty"`
}

func (s *Service) entry(f vfs.FS, dir string, fi fs.FileInfo) Entry {
	p := path.Join(dir, fi.Name())
	e := Entry{
		Name: fi.Name(), Path: p, Size: fi.Size(), Mode: fi.Mode().String(),
		Perm: fmt.Sprintf("%04o", fi.Mode().Perm()|specialBits(fi.Mode())), MTime: fi.ModTime().UnixMilli(),
	}
	e.UID, e.GID = vfs.Owner(fi)
	oc := s.owners.of(f)
	e.Owner, e.Group = oc.user(e.UID), oc.group(e.GID)
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		e.Type = "link"
		e.LinkTarget, _ = f.Readlink(p)
		if t, err := f.Stat(p); err == nil {
			e.IsDir = t.IsDir()
			if !t.IsDir() {
				e.Size = t.Size()
			}
		} else {
			e.Broken = true
		}
	case fi.IsDir():
		e.Type, e.IsDir = "dir", true
	case fi.Mode().IsRegular():
		e.Type = "file"
	default:
		e.Type = "other"
	}
	return e
}

func specialBits(m fs.FileMode) fs.FileMode {
	var b fs.FileMode
	if m&fs.ModeSetuid != 0 {
		b |= 0o4000
	}
	if m&fs.ModeSetgid != 0 {
		b |= 0o2000
	}
	if m&fs.ModeSticky != 0 {
		b |= 0o1000
	}
	return b
}

func (s *Service) list(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	dir, err := cleanPath(c.Query("path"))
	if err != nil {
		api.Fail(c, err)
		return
	}
	fi, err := f.Stat(dir)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if !fi.IsDir() {
		api.Fail(c, api.Err(http.StatusBadRequest, api.CodeNotDir, "不是目录"))
		return
	}
	infos, err := f.ReadDir(dir)
	if err != nil {
		api.Fail(c, err)
		return
	}
	out := make([]Entry, 0, len(infos))
	for _, fi := range infos {
		if fi.Name() == trashDirName {
			continue
		}
		out = append(out, s.entry(f, dir, fi))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	api.OK(c, gin.H{"path": dir, "entries": out})
}

func (s *Service) stat(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	p, err := cleanPath(c.Query("path"))
	if err != nil {
		api.Fail(c, err)
		return
	}
	fi, err := f.Lstat(p)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, s.entry(f, path.Dir(p), fi))
}

type mountInfo struct {
	Point  string `json:"point"`
	Device string `json:"device"`
	FSType string `json:"fsType"`
	Total  uint64 `json:"total"`
	Used   uint64 `json:"used"`
	Free   uint64 `json:"free"`
}

// mounts 是“此电脑”中的磁盘列表。
func (s *Service) mounts(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	out := []mountInfo{}
	for _, m := range f.MountList() {
		out = append(out, mountInfo{Point: m.Point, Device: m.Device, FSType: m.FSType, Total: m.Total, Used: m.Used, Free: m.Free})
	}
	api.OK(c, gin.H{"mounts": out, "home": f.HomeDir()})
}

func (s *Service) properties(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	p, err := cleanPath(c.Query("path"))
	if err != nil {
		api.Fail(c, err)
		return
	}
	fi, err := f.Lstat(p)
	if err != nil {
		api.Fail(c, err)
		return
	}
	res := gin.H{"entry": s.entry(f, path.Dir(p), fi)}
	if fi.IsDir() {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		size, files, dirs, err := walkSize(ctx, f, p)
		res["size"], res["files"], res["dirs"] = size, files, dirs
		res["partial"] = err != nil
	}
	if t, err := f.Stat(p); err == nil {
		res["atime"], res["ctime"] = times(t)
	}
	api.OK(c, res)
}

// walkSize 统计目录总大小、文件数和子目录数（不跟随符号链接）。
func walkSize(ctx context.Context, f vfs.FS, root string) (size int64, files, dirs int, err error) {
	var walk func(dir string) error
	walk = func(dir string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		infos, err := f.ReadDir(dir)
		if err != nil {
			return nil // 无法读取的子目录跳过
		}
		for _, fi := range infos {
			if fi.IsDir() {
				dirs++
				if err := walk(path.Join(dir, fi.Name())); err != nil {
					return err
				}
			} else {
				files++
				size += fi.Size()
			}
		}
		return nil
	}
	err = walk(root)
	return
}

func (s *Service) search(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	root, err := cleanPath(c.DefaultQuery("path", "/"))
	if err != nil {
		api.Fail(c, err)
		return
	}
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	if q == "" {
		api.OK(c, gin.H{"entries": []Entry{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	const limit = 200
	var out []Entry
	skip := map[string]bool{"/proc": true, "/sys": true, "/dev": true, "/run": true}
	queue := []string{root}
	for len(queue) > 0 && len(out) < limit && ctx.Err() == nil {
		dir := queue[0]
		queue = queue[1:]
		infos, err := f.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fi := range infos {
			p := path.Join(dir, fi.Name())
			if strings.Contains(strings.ToLower(fi.Name()), q) {
				out = append(out, s.entry(f, dir, fi))
				if len(out) >= limit {
					break
				}
			}
			if fi.IsDir() && !skip[p] && fi.Name() != trashDirName {
				queue = append(queue, p)
			}
		}
	}
	api.OK(c, gin.H{"entries": out, "partial": ctx.Err() != nil || len(out) >= limit})
}

func version(fi fs.FileInfo) string {
	return fmt.Sprintf("%d-%d", fi.ModTime().UnixNano(), fi.Size())
}

func (s *Service) readContent(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	p, err := cleanPath(c.Query("path"))
	if err != nil {
		api.Fail(c, err)
		return
	}
	fi, err := f.Stat(p)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if fi.IsDir() {
		api.Fail(c, api.BadRequest("这是一个目录"))
		return
	}
	if fi.Size() > maxEditSize {
		api.Fail(c, api.Err(http.StatusRequestEntityTooLarge, api.CodeFileTooLarge, "文件超过 10 MB，无法在线编辑，请下载后编辑"))
		return
	}
	fh, err := f.Open(p)
	if err != nil {
		api.Fail(c, err)
		return
	}
	defer fh.Close()
	b, err := io.ReadAll(io.LimitReader(fh, maxEditSize+1))
	if err != nil {
		api.Fail(c, err)
		return
	}
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	if strings.ContainsRune(string(head), 0) || !utf8.Valid(b) {
		api.Fail(c, api.Err(http.StatusUnsupportedMediaType, api.CodeBinaryFile, "不是 UTF-8 文本文件，无法在线编辑"))
		return
	}
	api.OK(c, gin.H{"path": p, "content": string(b), "version": version(fi), "size": fi.Size()})
}

func (s *Service) writeContent(c *gin.Context) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Version string `json:"version"` // 打开时的版本；为空表示不检查（新建）
		Force   bool   `json:"force"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	p, err := cleanPath(req.Path)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, p)
	// 编辑符号链接时写入其指向的文件
	if real, err := evalSymlinks(f, p); err == nil {
		p = real
	}
	var mode fs.FileMode = 0o644
	uid, gid := -1, -1
	if fi, err := f.Stat(p); err == nil {
		if !req.Force && req.Version != "" && version(fi) != req.Version {
			e := api.Conflict(api.CodeFileModified, "文件在你打开后已被修改，保存会覆盖对方的修改")
			e.Data = gin.H{"version": version(fi)}
			api.Fail(c, e)
			return
		}
		mode = fi.Mode().Perm() | specialBits(fi.Mode())
		uid, gid = vfs.Owner(fi)
	} else if !errors.Is(err, fs.ErrNotExist) {
		api.Fail(c, err)
		return
	}
	tmp := path.Join(path.Dir(p), "."+path.Base(p)+".sdsave")
	fh, err := f.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if _, err := io.WriteString(fh, req.Content); err != nil {
		fh.Close()
		f.Remove(tmp)
		api.Fail(c, err)
		return
	}
	fh.Close()
	f.Chmod(tmp, mode)
	if uid >= 0 {
		f.Chown(tmp, uid, gid)
	}
	if err := f.Rename(tmp, p); err != nil {
		f.Remove(tmp)
		api.Fail(c, err)
		return
	}
	fi, _ := f.Stat(p)
	api.OK(c, gin.H{"version": version(fi)})
}

func evalSymlinks(f vfs.FS, p string) (string, error) {
	for i := 0; i < 40; i++ {
		fi, err := f.Lstat(p)
		if err != nil {
			return "", err
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			return p, nil
		}
		t, err := f.Readlink(p)
		if err != nil {
			return "", err
		}
		if !path.IsAbs(t) {
			t = path.Join(path.Dir(p), t)
		}
		p = path.Clean(t)
	}
	return "", errors.New("符号链接层级过多")
}

func (s *Service) mkdir(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	p, err := cleanPath(req.Path)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, p)
	if err := f.Mkdir(p, 0o755); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) create(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	p, err := cleanPath(req.Path)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, p)
	fh, err := f.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		api.Fail(c, err)
		return
	}
	fh.Close()
	api.OK(c, nil)
}

func (s *Service) rename(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	p, err := cleanPath(req.Path)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if err := validName(req.Name); err != nil {
		api.Fail(c, err)
		return
	}
	dst := path.Join(path.Dir(p), req.Name)
	api.Target(c, p+" -> "+dst)
	if dst == p {
		api.OK(c, nil)
		return
	}
	// 只改大小写时允许覆盖自身；否则目标存在即报错，避免误覆盖
	if _, err := f.Lstat(dst); err == nil && !strings.EqualFold(dst, p) {
		api.Fail(c, api.Conflict(api.CodeFileExists, "已存在同名文件或目录"))
		return
	}
	if err := f.Rename(p, dst); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, gin.H{"path": dst})
}

// uniqueName 在目录中为 name 找一个不冲突的名字：a.txt → a (2).txt。
func uniqueName(f vfs.FS, dir, name string) string {
	if _, err := f.Lstat(path.Join(dir, name)); err != nil {
		return name
	}
	base, ext := splitExt(name)
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := f.Lstat(path.Join(dir, n)); err != nil {
			return n
		}
	}
}

func splitExt(name string) (string, string) {
	lower := strings.ToLower(name)
	for _, e := range []string{".tar.gz", ".tar.xz", ".tar.bz2"} {
		if strings.HasSuffix(lower, e) && len(name) > len(e) {
			return name[:len(name)-len(e)], name[len(name)-len(e):]
		}
	}
	ext := path.Ext(name)
	if ext == name {
		return name, ""
	}
	return strings.TrimSuffix(name, ext), ext
}
