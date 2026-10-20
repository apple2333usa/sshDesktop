package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"syscall"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/gin-gonic/gin"
)

// paste 复制或移动（剪切）文件到目标目录，作为后台任务执行。srcNode 为源文件所在节点（默认与目标相同）；
// 跨节点时由面板中转流式传输，移动等于复制后删除源文件。
func (s *Service) paste(c *gin.Context) {
	var req struct {
		Sources  []string `json:"sources"`
		SrcNode  string   `json:"srcNode"`
		Dest     string   `json:"dest"`
		Mode     string   `json:"mode"`     // copy / move
		Conflict string   `json:"conflict"` // rename（默认）/ overwrite / skip
	}
	if !api.Bind(c, &req) {
		return
	}
	dstNode, err := s.d.Nodes.Get(c.Param("node"))
	if err != nil {
		api.Fail(c, api.NotFound("节点不存在"))
		return
	}
	srcNode := dstNode
	if req.SrcNode != "" && req.SrcNode != dstNode.ID() {
		if srcNode, err = s.d.Nodes.Get(req.SrcNode); err != nil {
			api.Fail(c, api.NotFound("源节点不存在"))
			return
		}
	}
	cross := srcNode.ID() != dstNode.ID()
	f, sf := dstNode.FS(), srcNode.FS()
	dest, err := cleanPath(req.Dest)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if req.Mode != "copy" && req.Mode != "move" {
		api.Fail(c, api.BadRequest("mode 只能是 copy 或 move"))
		return
	}
	if len(req.Sources) == 0 {
		api.Fail(c, api.BadRequest("没有要粘贴的文件"))
		return
	}
	if fi, err := f.Stat(dest); err != nil || !fi.IsDir() {
		api.Fail(c, api.Err(400, api.CodeNotDir, "目标不是目录"))
		return
	}
	var srcs []string
	for _, p := range req.Sources {
		p, err := cleanPath(p)
		if err != nil {
			api.Fail(c, err)
			return
		}
		if !cross && (dest == p || strings.HasPrefix(dest, p+"/")) {
			api.Fail(c, api.BadRequest("不能把目录粘贴到它自身或其子目录中："+p))
			return
		}
		srcs = append(srcs, p)
	}
	from := ""
	if cross {
		from = srcNode.ID() + ":"
	}
	api.Target(c, fmt.Sprintf("%s %s%s -> %s", req.Mode, from, strings.Join(srcs, ", "), dest))
	verb := map[string]string{"copy": "复制", "move": "移动"}[req.Mode]
	title := fmt.Sprintf("%s %d 项到 %s", verb, len(srcs), dest)
	if cross {
		title = fmt.Sprintf("%s %d 项（%s → %s）到 %s", verb, len(srcs), srcNode.ID(), dstNode.ID(), dest)
	}
	t := s.d.Tasks.Start("files.paste", title, api.User(c).UserID, c.Param("node"), func(ctx context.Context, t *task.Task) error {
		if !cross {
			return doPaste(ctx, t, f, f, srcs, dest, req.Mode, req.Conflict)
		}
		// 跨节点传输占用两端的传输名额
		for _, n := range []node.Node{srcNode, dstNode} {
			release, err := node.AcquireTransfer(ctx, n)
			if err != nil {
				return err
			}
			defer release()
		}
		return doPaste(ctx, t, sf, f, srcs, dest, req.Mode, req.Conflict)
	})
	api.OK(c, t)
}

type progress struct {
	t           *task.Task
	total, done int64
}

func (p *progress) add(n int64) {
	p.done += n
	if p.total > 0 {
		p.t.Progress(float64(p.done) / float64(p.total) * 100)
	}
}

// doPaste 把 sf 上的 srcs 复制或移动到 f 的 dest 目录。sf 与 f 是同一个文件系统时移动直接改名。
func doPaste(ctx context.Context, t *task.Task, sf, f vfs.FS, srcs []string, dest, mode, conflict string) error {
	same := sf == f
	pr := &progress{t: t}
	for _, src := range srcs {
		if sz, _, _, err := walkSize(ctx, sf, src); err == nil {
			pr.total += sz
		}
		if fi, err := sf.Lstat(src); err == nil && !fi.IsDir() {
			pr.total += fi.Size()
		}
	}
	for _, src := range srcs {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := path.Base(src)
		target := path.Join(dest, name)
		if same && target == src {
			if mode == "move" {
				continue // 移动到原位置，无事可做
			}
			target = path.Join(dest, uniqueName(f, dest, name)) // 原地复制生成副本
		} else if _, err := f.Lstat(target); err == nil {
			switch conflict {
			case "skip":
				t.Log("跳过已存在的 %s", target)
				continue
			case "overwrite":
				if err := f.RemoveAll(target); err != nil {
					return err
				}
			default:
				target = path.Join(dest, uniqueName(f, dest, name))
			}
		}
		if mode == "move" {
			if same {
				err := f.Rename(src, target)
				if err == nil {
					t.Log("已移动 %s -> %s", src, target)
					continue
				}
				if !isCrossDevice(err) {
					return err
				}
			}
			// 跨文件系统或跨节点：先复制再删除
			if err := copyTree(ctx, sf, src, f, target, pr, same); err != nil {
				return err
			}
			if err := sf.RemoveAll(src); err != nil {
				return err
			}
			t.Log("已移动 %s -> %s", src, target)
			continue
		}
		if err := copyTree(ctx, sf, src, f, target, pr, same); err != nil {
			return err
		}
		t.Log("已复制 %s -> %s", src, target)
	}
	return nil
}

func isCrossDevice(err error) bool {
	var le *os.LinkError
	if errors.As(err, &le) {
		return errors.Is(le.Err, syscall.EXDEV)
	}
	return errors.Is(err, syscall.EXDEV)
}

// copyTree 递归复制，保留权限、修改时间（同一节点内还保留所有者）；符号链接按链接本身复制。
func copyTree(ctx context.Context, sf vfs.FS, src string, df vfs.FS, dst string, pr *progress, keepOwner bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fi, err := sf.Lstat(src)
	if err != nil {
		return err
	}
	uid, gid := vfs.Owner(fi)
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		t, err := sf.Readlink(src)
		if err != nil {
			return err
		}
		if err := df.Symlink(t, dst); err != nil {
			return err
		}
	case fi.IsDir():
		if err := df.Mkdir(dst, fi.Mode().Perm()|0o700); err != nil {
			return err
		}
		infos, err := sf.ReadDir(src)
		if err != nil {
			return err
		}
		for _, ci := range infos {
			if err := copyTree(ctx, sf, path.Join(src, ci.Name()), df, path.Join(dst, ci.Name()), pr, keepOwner); err != nil {
				return err
			}
		}
		df.Chmod(dst, fi.Mode().Perm()|specialBits(fi.Mode()))
	case fi.Mode().IsRegular():
		if err := copyFile(ctx, sf, src, df, dst, fi.Mode().Perm(), pr); err != nil {
			return err
		}
		df.Chmod(dst, fi.Mode().Perm()|specialBits(fi.Mode()))
	default:
		return nil // 设备文件、管道等不复制
	}
	if keepOwner && uid >= 0 {
		df.Chown(dst, uid, gid)
	}
	if fi.Mode()&fs.ModeSymlink == 0 {
		df.Chtimes(dst, fi.ModTime(), fi.ModTime())
	}
	return nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
	pr  *progress
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	if c.pr != nil {
		c.pr.add(int64(n))
	}
	return n, err
}

func copyFile(ctx context.Context, sf vfs.FS, src string, df vfs.FS, dst string, perm fs.FileMode, pr *progress) error {
	in, err := sf.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := df.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	// 只暴露 Read / Write，让 io.Copy 走普通的流式复制（SFTP 文件的 ReadFrom 会预先 Stat 源）
	_, err = io.Copy(struct{ io.Writer }{out}, &ctxReader{ctx: ctx, r: in, pr: pr})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		df.Remove(dst)
	}
	return err
}

// delete 默认移入回收站；permanent 为 true 时永久删除（二级危险操作，需要密码确认）。
func (s *Service) delete(c *gin.Context) {
	var req struct {
		Paths     []string `json:"paths"`
		Permanent bool     `json:"permanent"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	var paths []string
	for _, p := range req.Paths {
		p, err := cleanPath(p)
		if err != nil {
			api.Fail(c, err)
			return
		}
		if p == "/" {
			api.Fail(c, api.BadRequest("不能删除根目录"))
			return
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		api.Fail(c, api.BadRequest("没有要删除的文件"))
		return
	}
	if req.Permanent {
		api.Target(c, "永久删除 "+strings.Join(paths, ", "))
		if !s.d.Confirm(c) {
			return
		}
		for _, p := range paths {
			if err := f.RemoveAll(p); err != nil {
				api.Fail(c, err)
				return
			}
		}
		api.OK(c, nil)
		return
	}
	api.Target(c, "移入回收站 "+strings.Join(paths, ", "))
	var failed []string
	var lastErr error
	for _, p := range paths {
		if err := moveToTrash(f, p); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			failed = append(failed, p)
			lastErr = err
		}
	}
	if len(failed) > 0 {
		e := api.Conflict(api.CodeTrashUnavailable, "部分文件无法移入回收站："+lastErr.Error())
		e.Data = gin.H{"paths": failed}
		api.Fail(c, e)
		return
	}
	api.OK(c, nil)
}

func (s *Service) chmod(c *gin.Context) {
	var req struct {
		Paths     []string `json:"paths"`
		Mode      string   `json:"mode"` // 八进制，例如 "0755"
		Recursive bool     `json:"recursive"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	m, err := strconv.ParseUint(req.Mode, 8, 32)
	if err != nil || m > 0o7777 {
		api.Fail(c, api.BadRequest("权限格式错误，应为八进制，例如 755"))
		return
	}
	mode := fs.FileMode(m & 0o777)
	if m&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}
	if m&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}
	if m&0o1000 != 0 {
		mode |= fs.ModeSticky
	}
	api.Target(c, fmt.Sprintf("%s %s recursive=%v", req.Mode, strings.Join(req.Paths, ", "), req.Recursive))
	err = s.eachPath(c, f, req.Paths, req.Recursive, func(p string, fi fs.FileInfo) error {
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil // 符号链接本身没有权限位
		}
		return f.Chmod(p, mode)
	})
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) chown(c *gin.Context) {
	var req struct {
		Paths     []string `json:"paths"`
		Owner     string   `json:"owner"`
		Group     string   `json:"group"`
		Recursive bool     `json:"recursive"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	uid, gid, err := s.owners.of(f).lookup(req.Owner, req.Group)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, fmt.Sprintf("%s:%s %s recursive=%v", req.Owner, req.Group, strings.Join(req.Paths, ", "), req.Recursive))
	err = s.eachPath(c, f, req.Paths, req.Recursive, func(p string, _ fs.FileInfo) error {
		return f.Chown(p, uid, gid)
	})
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

// eachPath 对每个路径（递归时含其全部子项，不跟随符号链接）执行 fn。
func (s *Service) eachPath(c *gin.Context, f vfs.FS, paths []string, recursive bool, fn func(string, fs.FileInfo) error) error {
	var walk func(p string) error
	walk = func(p string) error {
		fi, err := f.Lstat(p)
		if err != nil {
			return err
		}
		if err := fn(p, fi); err != nil {
			return err
		}
		if recursive && fi.IsDir() {
			infos, err := f.ReadDir(p)
			if err != nil {
				return err
			}
			for _, ci := range infos {
				if err := walk(path.Join(p, ci.Name())); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, p := range paths {
		p, err := cleanPath(p)
		if err != nil {
			return err
		}
		if err := walk(p); err != nil {
			return err
		}
	}
	return nil
}
