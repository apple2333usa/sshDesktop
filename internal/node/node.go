// Package node 是节点抽象。所有功能模块只依赖 Node 接口，不关心节点是本机还是远程。
// 本机节点固定为 local，远程节点通过 SSH 管理（见 ssh.go）。
package node

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

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
	// Command 不为空时执行该命令而不是登录 shell（仅本机，用于进入容器终端）
	Command []string
}

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Node interface {
	ID() string
	Kind() Kind
	// Exec 以参数数组执行命令，不经过 shell 展开。
	Exec(ctx context.Context, name string, args ...string) (Result, error)
	// RunScript 用登录 shell 执行一段脚本（批量命令），输出实时写入 out。
	RunScript(ctx context.Context, script string, out io.Writer) (int, error)
	Shell(ctx context.Context, opt ShellOptions) (PTY, error)
	FS() vfs.FS
}

var ErrNotFound = errors.New("节点不存在")

// AcquireTransfer 为一次大块数据传输占用节点的传输名额（远程节点最多 8 个并发），返回释放函数。
func AcquireTransfer(ctx context.Context, n Node) (func(), error) {
	if t, ok := n.(interface {
		AcquireTransfer(context.Context) (func(), error)
	}); ok {
		return t.AcquireTransfer(ctx)
	}
	return func() {}, nil
}

// Registry 保存本机和所有远程节点。
type Registry struct {
	local *Local
	mu    sync.RWMutex
	ssh   map[string]*SSH
}

func NewRegistry() *Registry { return &Registry{local: &Local{}, ssh: map[string]*SSH{}} }

func (r *Registry) Get(id string) (Node, error) {
	if id == LocalID {
		return r.local, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if n, ok := r.ssh[id]; ok {
		return n, nil
	}
	return nil, ErrNotFound
}

func (r *Registry) Local() *Local { return r.local }

// SSH 返回远程节点。
func (r *Registry) SSH(id string) (*SSH, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.ssh[id]
	return n, ok
}

// Put 添加或替换远程节点（替换时关闭旧连接）。
func (r *Registry) Put(n *SSH) {
	r.mu.Lock()
	old := r.ssh[n.ID()]
	r.ssh[n.ID()] = n
	r.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

func (r *Registry) Remove(id string) {
	r.mu.Lock()
	old := r.ssh[id]
	delete(r.ssh, id)
	r.mu.Unlock()
	if old != nil {
		old.Close()
	}
}

// Remote 返回所有远程节点。
func (r *Registry) Remote() []*SSH {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*SSH, 0, len(r.ssh))
	for _, n := range r.ssh {
		out = append(out, n)
	}
	return out
}

// RunScript 用登录 shell 执行脚本。
func (*Local) RunScript(ctx context.Context, script string, out io.Writer) (int, error) {
	shell, home, _ := loginShell()
	cmd := exec.CommandContext(ctx, shell, "-c", script)
	cmd.Dir = home
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return ee.ExitCode(), nil
	}
	if ctx.Err() != nil {
		return -1, ctx.Err()
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// Local 是面板所在的本机。
type Local struct{}

func (*Local) ID() string { return LocalID }
func (*Local) Kind() Kind { return KindLocal }
func (*Local) FS() vfs.FS { return vfs.Local{} }

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
