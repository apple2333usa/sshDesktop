// Package vfs 是统一的文件系统接口：本机直接读写，远程节点（M2）走 SFTP。
package vfs

import (
	"io"
	"io/fs"
	"os"
	"time"
)

// File 是可读写、可定位的文件句柄。
type File interface {
	io.Reader
	io.Writer
	io.Seeker
	io.Closer
	io.ReaderAt
	Stat() (fs.FileInfo, error)
}

type FS interface {
	Stat(name string) (fs.FileInfo, error)
	Lstat(name string) (fs.FileInfo, error)
	ReadDir(name string) ([]fs.FileInfo, error)
	Readlink(name string) (string, error)
	Open(name string) (File, error)
	OpenFile(name string, flag int, perm os.FileMode) (File, error)
	Mkdir(name string, perm os.FileMode) error
	MkdirAll(name string, perm os.FileMode) error
	Rename(oldname, newname string) error
	Remove(name string) error
	RemoveAll(name string) error
	Symlink(oldname, newname string) error
	Chmod(name string, mode os.FileMode) error
	Chown(name string, uid, gid int) error
	Chtimes(name string, atime, mtime time.Time) error
	// TrashRoot 返回 name 所在位置应使用的回收站目录（不保证已存在）。
	TrashRoot(name string) (string, error)
}

// Owner 从 FileInfo 中取出 uid / gid（取不到时返回 -1）。
func Owner(fi fs.FileInfo) (uid, gid int) {
	return ownerOf(fi)
}
