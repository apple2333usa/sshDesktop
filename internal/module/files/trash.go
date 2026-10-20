package files

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/gin-gonic/gin"
)

// 回收站布局：<挂载点>/.sshdesktop-trash/files/<id> 存放文件，info/<id>.json 记录原路径。
const trashDirName = ".sshdesktop-trash"

type trashInfo struct {
	ID        string `json:"id"`
	Root      string `json:"root"`
	Name      string `json:"name"`
	Original  string `json:"original"`
	DeletedAt int64  `json:"deletedAt"` // 毫秒
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
}

func moveToTrash(f vfs.FS, p string) error {
	fi, err := f.Lstat(p)
	if err != nil {
		return err
	}
	root, err := f.TrashRoot(p)
	if err != nil {
		return err
	}
	if p == path.Dir(root) || strings.HasPrefix(root, p+"/") {
		return errors.New("挂载点本身不能移入回收站")
	}
	if err := f.MkdirAll(path.Join(root, "files"), 0o700); err != nil {
		return err
	}
	if err := f.MkdirAll(path.Join(root, "info"), 0o700); err != nil {
		return err
	}
	f.Chmod(root, 0o700)
	b := make([]byte, 8)
	rand.Read(b)
	id := time.Now().Format("20060102150405") + "-" + hex.EncodeToString(b)
	info := trashInfo{ID: id, Root: root, Name: path.Base(p), Original: p, DeletedAt: time.Now().UnixMilli(), IsDir: fi.IsDir(), Size: fi.Size()}
	if err := writeJSON(f, path.Join(root, "info", id+".json"), info); err != nil {
		return err
	}
	if err := f.Rename(p, path.Join(root, "files", id)); err != nil {
		f.Remove(path.Join(root, "info", id+".json"))
		return err
	}
	return nil
}

func writeJSON(f vfs.FS, p string, v any) error {
	b, _ := json.Marshal(v)
	fh, err := f.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fh.Write(b)
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	return err
}

// trashRoots 返回所有挂载点上已存在的回收站目录。
func trashRoots(f vfs.FS) []string {
	var out []string
	seen := map[string]bool{}
	points := []string{"/"}
	for _, m := range vfs.Mounts(false) {
		points = append(points, m.Point)
	}
	for _, mp := range points {
		root := path.Join(mp, trashDirName)
		if seen[root] {
			continue
		}
		seen[root] = true
		if fi, err := f.Stat(root); err == nil && fi.IsDir() {
			out = append(out, root)
		}
	}
	return out
}

func readTrash(f vfs.FS) []trashInfo {
	var out []trashInfo
	for _, root := range trashRoots(f) {
		infos, _ := f.ReadDir(path.Join(root, "info"))
		for _, fi := range infos {
			if !strings.HasSuffix(fi.Name(), ".json") {
				continue
			}
			fh, err := f.Open(path.Join(root, "info", fi.Name()))
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(io.LimitReader(fh, 64<<10))
			fh.Close()
			var ti trashInfo
			if json.Unmarshal(b, &ti) != nil {
				continue
			}
			ti.Root = root
			ti.ID = strings.TrimSuffix(fi.Name(), ".json")
			out = append(out, ti)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeletedAt > out[j].DeletedAt })
	return out
}

func (s *Service) trashList(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	items := readTrash(f)
	if items == nil {
		items = []trashInfo{}
	}
	api.OK(c, items)
}

type trashRef struct {
	Root string `json:"root"`
	ID   string `json:"id"`
}

// resolveRefs 校验客户端传来的回收站条目，防止借此访问任意路径。
func resolveRefs(f vfs.FS, refs []trashRef) ([]trashInfo, error) {
	all := map[string]trashInfo{}
	for _, ti := range readTrash(f) {
		all[ti.Root+"|"+ti.ID] = ti
	}
	var out []trashInfo
	for _, r := range refs {
		ti, ok := all[r.Root+"|"+r.ID]
		if !ok {
			return nil, api.NotFound("回收站中没有该项目：" + r.ID)
		}
		out = append(out, ti)
	}
	return out, nil
}

func (s *Service) trashRestore(c *gin.Context) {
	var req struct {
		Items []trashRef `json:"items"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	items, err := resolveRefs(f, req.Items)
	if err != nil {
		api.Fail(c, err)
		return
	}
	var restored []string
	for _, ti := range items {
		dir := path.Dir(ti.Original)
		if err := f.MkdirAll(dir, 0o755); err != nil {
			api.Fail(c, err)
			return
		}
		target := path.Join(dir, uniqueName(f, dir, path.Base(ti.Original)))
		if err := f.Rename(path.Join(ti.Root, "files", ti.ID), target); err != nil {
			api.Fail(c, err)
			return
		}
		f.Remove(path.Join(ti.Root, "info", ti.ID+".json"))
		restored = append(restored, target)
	}
	api.Target(c, strings.Join(restored, ", "))
	api.OK(c, gin.H{"paths": restored})
}

func purge(f vfs.FS, ti trashInfo) error {
	if err := f.RemoveAll(path.Join(ti.Root, "files", ti.ID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return f.Remove(path.Join(ti.Root, "info", ti.ID+".json"))
}

// trashDelete 永久删除回收站中的项目（二级危险操作）。
func (s *Service) trashDelete(c *gin.Context) {
	var req struct {
		Items []trashRef `json:"items"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	items, err := resolveRefs(f, req.Items)
	if err != nil {
		api.Fail(c, err)
		return
	}
	var names []string
	for _, ti := range items {
		names = append(names, ti.Original)
	}
	api.Target(c, strings.Join(names, ", "))
	if !s.d.Confirm(c) {
		return
	}
	for _, ti := range items {
		if err := purge(f, ti); err != nil {
			api.Fail(c, err)
			return
		}
	}
	api.OK(c, nil)
}

func (s *Service) trashEmpty(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	api.Target(c, "清空回收站")
	if !s.d.Confirm(c) {
		return
	}
	for _, ti := range readTrash(f) {
		if err := purge(f, ti); err != nil {
			api.Fail(c, err)
			return
		}
	}
	api.OK(c, nil)
}

// CleanupTrash 删除回收站中超过保留天数的项目。
func CleanupTrash(f vfs.FS, days int) {
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
	for _, ti := range readTrash(f) {
		if ti.DeletedAt < cutoff {
			purge(f, ti)
		}
	}
}
