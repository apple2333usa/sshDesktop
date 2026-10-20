package vfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/mountinfo"
	"github.com/pkg/sftp"
	"github.com/shirou/gopsutil/v4/disk"
)

// Local 是本机文件系统。
type Local struct{}

func (Local) Stat(n string) (fs.FileInfo, error)  { return os.Stat(n) }
func (Local) Lstat(n string) (fs.FileInfo, error) { return os.Lstat(n) }
func (Local) Readlink(n string) (string, error)   { return os.Readlink(n) }
func (Local) Open(n string) (File, error)         { return os.Open(n) }
func (Local) OpenFile(n string, flag int, perm os.FileMode) (File, error) {
	return os.OpenFile(n, flag, perm)
}
func (Local) Mkdir(n string, perm os.FileMode) error    { return os.Mkdir(n, perm) }
func (Local) MkdirAll(n string, perm os.FileMode) error { return os.MkdirAll(n, perm) }
func (Local) Rename(o, n string) error                  { return os.Rename(o, n) }
func (Local) Remove(n string) error                     { return os.Remove(n) }
func (Local) RemoveAll(n string) error                  { return os.RemoveAll(n) }
func (Local) Symlink(o, n string) error                 { return os.Symlink(o, n) }
func (Local) Chmod(n string, m os.FileMode) error       { return os.Chmod(n, m) }
func (Local) Chown(n string, uid, gid int) error        { return os.Lchown(n, uid, gid) }
func (Local) Chtimes(n string, a, m time.Time) error    { return os.Chtimes(n, a, m) }

func (Local) ReadDir(n string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(n)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue // 读取过程中被删除
		}
		out = append(out, fi)
	}
	return out, nil
}

// TrashRoot：本机按挂载点使用 <挂载点>/.sshdesktop-trash。
func (Local) TrashRoot(name string) (string, error) {
	mp := MountPointOf(name)
	return filepath.Join(mp, ".sshdesktop-trash"), nil
}

func (Local) MountList() []MountUsage {
	var out []MountUsage
	for _, m := range Mounts(false) {
		mu := MountUsage{Mount: m}
		if u, err := disk.Usage(m.Point); err == nil {
			mu.Total, mu.Used, mu.Free = u.Total, u.Used, u.Free
		}
		out = append(out, mu)
	}
	return out
}

func (Local) HomeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/root"
}

func ownerOf(fi fs.FileInfo) (int, int) {
	switch st := fi.Sys().(type) {
	case *syscall.Stat_t:
		return int(st.Uid), int(st.Gid)
	case *sftp.FileStat:
		return int(st.UID), int(st.GID)
	}
	type owner interface{ OwnerIDs() (int, int) }
	if o, ok := fi.Sys().(owner); ok {
		return o.OwnerIDs()
	}
	return -1, -1
}

// 挂载点解析在 mountinfo 包（独立 agent 也要用）。
type Mount = mountinfo.Mount

var (
	Mounts       = mountinfo.Mounts
	MountPointOf = mountinfo.MountPointOf
	HiddenMount  = mountinfo.HiddenMount
)
