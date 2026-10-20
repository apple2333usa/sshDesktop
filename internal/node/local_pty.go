package node

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

type localPTY struct {
	f    *os.File
	cmd  *exec.Cmd
	once sync.Once
}

func (p *localPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *localPTY) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *localPTY) Resize(cols, rows uint16) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: cols, Rows: rows})
}

func (p *localPTY) Wait() (int, error) {
	err := p.cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (p *localPTY) Close() error {
	p.once.Do(func() {
		if p.cmd.Process != nil {
			// 向整个会话进程组发送 SIGHUP，与关闭 SSH 终端的效果一致
			syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP)
		}
		p.f.Close()
	})
	return nil
}

func loginShell() (shell, home, name string) {
	shell, home, name = "/bin/sh", "/root", "root"
	u, err := user.Current()
	if err != nil {
		return
	}
	name, home = u.Username, u.HomeDir
	if b, err := os.ReadFile("/etc/passwd"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(line, ":")
			if len(f) >= 7 && f[2] == u.Uid && f[6] != "" {
				if _, err := os.Stat(f[6]); err == nil && !strings.HasSuffix(f[6], "nologin") && !strings.HasSuffix(f[6], "false") {
					return f[6], home, name
				}
			}
		}
	}
	for _, s := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(s); err == nil {
			return s, home, name
		}
	}
	return
}

func (*Local) Shell(ctx context.Context, opt ShellOptions) (PTY, error) {
	shell, home, name := loginShell()
	cmd := exec.Command(shell, "-l")
	if len(opt.Command) > 0 {
		cmd = exec.Command(opt.Command[0], opt.Command[1:]...)
	}
	cmd.Dir = home
	if opt.Dir != "" {
		if fi, err := os.Stat(opt.Dir); err == nil && fi.IsDir() {
			cmd.Dir = opt.Dir
		}
	}
	env := []string{
		"TERM=xterm-256color", "COLORTERM=truecolor",
		"HOME=" + home, "USER=" + name, "LOGNAME=" + name, "SHELL=" + shell,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin",
	}
	lang := os.Getenv("LANG")
	if lang == "" {
		lang = "C.UTF-8"
	}
	env = append(env, "LANG="+lang)
	cmd.Env = env
	size := &pty.Winsize{Cols: opt.Size.Cols, Rows: opt.Size.Rows}
	if size.Cols == 0 || size.Rows == 0 {
		size.Cols, size.Rows = 120, 32
	}
	f, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return nil, err
	}
	return &localPTY{f: f, cmd: cmd}, nil
}
