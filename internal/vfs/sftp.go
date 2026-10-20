package vfs

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"time"

	"github.com/pkg/sftp"
)

// SFTP 是远程节点的文件系统。Client 每次调用时取当前连接上的客户端，断线重连后自动换新。
type SFTP struct {
	Client func() (*sftp.Client, error)
	Home   func() string
	// Mounts 返回远程的挂载点与用量（由节点通过 df 取得），为空时只给出根目录。
	Mounts func() []MountUsage
}

func (s *SFTP) c() (*sftp.Client, error) { return s.Client() }

func (s *SFTP) Stat(n string) (fs.FileInfo, error) {
	c, err := s.c()
	if err != nil {
		return nil, err
	}
	return wrapErr2(c.Stat(n))
}

func (s *SFTP) Lstat(n string) (fs.FileInfo, error) {
	c, err := s.c()
	if err != nil {
		return nil, err
	}
	return wrapErr2(c.Lstat(n))
}

func (s *SFTP) ReadDir(n string) ([]fs.FileInfo, error) {
	c, err := s.c()
	if err != nil {
		return nil, err
	}
	l, err := c.ReadDir(n)
	return l, wrapErr(err)
}

func (s *SFTP) Readlink(n string) (string, error) {
	c, err := s.c()
	if err != nil {
		return "", err
	}
	t, err := c.ReadLink(n)
	return t, wrapErr(err)
}

func (s *SFTP) Open(n string) (File, error) {
	c, err := s.c()
	if err != nil {
		return nil, err
	}
	f, err := c.Open(n)
	if err != nil {
		return nil, wrapErr(err)
	}
	return f, nil
}

func (s *SFTP) OpenFile(n string, flag int, perm os.FileMode) (File, error) {
	c, err := s.c()
	if err != nil {
		return nil, err
	}
	created := false
	if flag&os.O_CREATE != 0 {
		if _, err := c.Lstat(n); err != nil {
			created = true
		}
	}
	f, err := c.OpenFile(n, flag)
	if err != nil {
		return nil, wrapErr(err)
	}
	if created {
		c.Chmod(n, perm)
	}
	return f, nil
}

func (s *SFTP) Mkdir(n string, perm os.FileMode) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	if err := c.Mkdir(n); err != nil {
		return wrapErr(err)
	}
	return wrapErr(c.Chmod(n, perm))
}

func (s *SFTP) MkdirAll(n string, perm os.FileMode) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	if fi, err := c.Stat(n); err == nil && fi.IsDir() {
		return nil
	}
	if err := c.MkdirAll(n); err != nil {
		return wrapErr(err)
	}
	return wrapErr(c.Chmod(n, perm))
}

// Rename 优先用 posix-rename（目标存在时覆盖，与本机行为一致）。
func (s *SFTP) Rename(o, n string) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	if err := c.PosixRename(o, n); err != nil {
		var se *sftp.StatusError
		if errors.As(err, &se) && se.FxCode() == sftp.ErrSSHFxOpUnsupported {
			return wrapErr(c.Rename(o, n))
		}
		return wrapErr(err)
	}
	return nil
}

func (s *SFTP) Remove(n string) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	fi, err := c.Lstat(n)
	if err != nil {
		return wrapErr(err)
	}
	if fi.IsDir() {
		return wrapErr(c.RemoveDirectory(n))
	}
	return wrapErr(c.Remove(n))
}

func (s *SFTP) RemoveAll(n string) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	fi, err := c.Lstat(n)
	if errors.Is(wrapErr(err), fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return wrapErr(err)
	}
	if !fi.IsDir() {
		return wrapErr(c.Remove(n))
	}
	return wrapErr(c.RemoveAll(n))
}

func (s *SFTP) Symlink(o, n string) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	return wrapErr(c.Symlink(o, n))
}

func (s *SFTP) Chmod(n string, m os.FileMode) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	return wrapErr(c.Chmod(n, m))
}

func (s *SFTP) Chown(n string, uid, gid int) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	if uid < 0 || gid < 0 {
		fi, err := c.Lstat(n)
		if err != nil {
			return wrapErr(err)
		}
		u, g := ownerOf(fi)
		if uid < 0 {
			uid = u
		}
		if gid < 0 {
			gid = g
		}
	}
	return wrapErr(c.Chown(n, uid, gid))
}

func (s *SFTP) Chtimes(n string, a, m time.Time) error {
	c, err := s.c()
	if err != nil {
		return err
	}
	return wrapErr(c.Chtimes(n, a, m))
}

// TrashRoot：远程节点统一使用 SSH 账号家目录下的回收站。
func (s *SFTP) TrashRoot(string) (string, error) {
	return path.Join(s.Home(), ".sshdesktop-trash"), nil
}

func (s *SFTP) HomeDir() string { return s.Home() }

func (s *SFTP) MountList() []MountUsage {
	if s.Mounts != nil {
		if l := s.Mounts(); len(l) > 0 {
			return l
		}
	}
	m := MountUsage{Mount: Mount{Point: "/"}}
	if c, err := s.c(); err == nil {
		if st, err := c.StatVFS("/"); err == nil {
			m.Total, m.Free = st.TotalSpace(), st.FreeSpace()
			m.Used = m.Total - st.Frsize*st.Bfree
		}
	}
	return []MountUsage{m}
}

// wrapErr 把 SFTP 状态码转换成标准错误，使 errors.Is(err, fs.ErrNotExist) 等判断照常工作。
func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	var se *sftp.StatusError
	if errors.As(err, &se) {
		switch se.FxCode() {
		case sftp.ErrSSHFxNoSuchFile:
			return &fs.PathError{Op: "sftp", Path: "", Err: fs.ErrNotExist}
		case sftp.ErrSSHFxPermissionDenied:
			return &fs.PathError{Op: "sftp", Path: "", Err: fs.ErrPermission}
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		return &fs.PathError{Op: "sftp", Err: fs.ErrNotExist}
	}
	return err
}

func wrapErr2(fi fs.FileInfo, err error) (fs.FileInfo, error) { return fi, wrapErr(err) }
