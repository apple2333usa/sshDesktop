// Package node 是节点抽象。所有功能模块只依赖 Node 接口，不关心节点是本机还是远程。
// M1 只有本机节点；SSH 节点在 M2 加入。
package node

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"github.com/apple2333usa/sshdesktop/internal/vfs"
)

type Kind string

const (
	KindLocal Kind = "local"
	KindSSH   Kind = "ssh"
)

const LocalID = "local"

// PTY 是交互终端。
type PTY interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	// Wait 等待 shell 退出并返回退出码。
	Wait() (int, error)
	Close() error
}

type Size struct{ Cols, Rows uint16 }

type ShellOptions struct {
	Size Size
	Dir  string
}

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Node interface {
	ID() string
	Kind() Kind
	Exec(ctx context.Context, name string, args ...string) (Result, error)
	Shell(ctx context.Context, opt ShellOptions) (PTY, error)
	FS() vfs.FS
}

var ErrNotFound = errors.New("节点不存在")

type Registry struct{ local *Local }

func NewRegistry() *Registry { return &Registry{local: &Local{}} }

func (r *Registry) Get(id string) (Node, error) {
	if id == LocalID {
		return r.local, nil
	}
	return nil, ErrNotFound
}

func (r *Registry) Local() *Local { return r.local }

// Local 是面板所在的本机。
type Local struct{}

func (*Local) ID() string    { return LocalID }
func (*Local) Kind() Kind    { return KindLocal }
func (*Local) FS() vfs.FS    { return vfs.Local{} }

// Exec 以参数数组执行命令，不经过 shell。
func (*Local) Exec(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=")
	var stdout, stderr limitedBuffer
	stdout.max, stderr.max = 32<<20, 4<<20
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.b, Stderr: stderr.b}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err
}

type limitedBuffer struct {
	b   []byte
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - len(l.b); room > 0 {
		if len(p) > room {
			l.b = append(l.b, p[:room]...)
		} else {
			l.b = append(l.b, p...)
		}
	}
	return len(p), nil
}
