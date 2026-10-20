package nodes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agentbin"
	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
)

// 一键安装 agent：通过 SFTP 上传与对方架构匹配的 agent 二进制和 systemd 单元，远程服务器不需要访问外网。
// 需要 SSH 账号是 root，或者能用 sudo（免密，或密码认证时用登录密码）。

const (
	agentBin    = "/usr/local/bin/sshdesktop-agent"
	agentUnit   = "/etc/systemd/system/sshdesktop-agent.service"
	agentSocket = "/etc/systemd/system/sshdesktop-agent.socket"
)

// agent 以动态用户运行：它只读取普通用户也能读到的 /proc 信息，不需要 root。
// socket 由 sshdesktop-agent.socket 以 root 创建后交给 agent：动态用户的 RuntimeDirectory
// 在新版 systemd（如 Debian 13 的 257）下位于只有 root 能进入的 /run/private，非 root 的 SSH 账号连不上。
const unitFile = `[Unit]
Description=sshDesktop agent (read-only metrics)
After=network.target sshdesktop-agent.socket
Requires=sshdesktop-agent.socket

[Service]
ExecStart=/usr/local/bin/sshdesktop-agent
Restart=always
RestartSec=5
DynamicUser=yes
StateDirectory=sshdesktop-agent
NoNewPrivileges=yes
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
`

const socketFile = `[Unit]
Description=sshDesktop agent socket

[Socket]
ListenStream=/run/sshdesktop/agent.sock
SocketMode=0666
DirectoryMode=0755
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
`

// cleanupRuntime 清理旧版单元（RuntimeDirectory）留下的 /run/sshdesktop 符号链接和 /run/private/sshdesktop。
const cleanupRuntime = "if [ -L /run/sshdesktop ]; then rm -f /run/sshdesktop; fi\nrm -rf /run/private/sshdesktop"

func (s *Service) remoteNode(c *gin.Context) (*node.SSH, *store.Node, bool) {
	row, ok := s.row(c)
	if !ok {
		return nil, nil, false
	}
	n, ok := s.d.Nodes.SSH(row.ID)
	if !ok {
		api.Fail(c, api.BadRequest("本机自带 agent 接口，无需安装"))
		return nil, nil, false
	}
	return n, row, true
}

func (s *Service) agentInstall(c *gin.Context) {
	n, row, ok := s.remoteNode(c)
	if !ok {
		return
	}
	api.Target(c, row.Name)
	t := s.d.Tasks.Start("nodes.agent-install", "在 "+row.Name+" 上安装 agent", api.User(c).UserID, row.ID,
		func(ctx context.Context, t *task.Task) error {
			if err := s.installAgent(ctx, t, n); err != nil {
				return err
			}
			if s.OnAgentChanged != nil {
				s.OnAgentChanged(n.ID())
			}
			return nil
		})
	api.OK(c, t)
}

func (s *Service) agentUninstall(c *gin.Context) {
	n, row, ok := s.remoteNode(c)
	if !ok {
		return
	}
	api.Target(c, row.Name)
	t := s.d.Tasks.Start("nodes.agent-uninstall", "卸载 "+row.Name+" 上的 agent", api.User(c).UserID, row.ID,
		func(ctx context.Context, t *task.Task) error {
			if err := s.uninstallAgent(ctx, t, n); err != nil {
				return err
			}
			if s.OnAgentChanged != nil {
				s.OnAgentChanged(n.ID())
			}
			return nil
		})
	api.OK(c, t)
}

// lineLog 把命令输出按行写入任务日志。
type lineLog struct {
	t   *task.Task
	buf []byte
}

func (l *lineLog) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimRight(string(l.buf[:i]), "\r"); line != "" {
			l.t.Log("%s", line)
		}
		l.buf = l.buf[i+1:]
	}
	return len(p), nil
}

func (l *lineLog) Flush() {
	if s := strings.TrimSpace(string(l.buf)); s != "" {
		l.t.Log("%s", s)
	}
	l.buf = nil
}

// rootRunner 返回以 root 身份执行脚本的函数：root 直接执行，否则依次尝试免密 sudo 和用登录密码 sudo。
func rootRunner(ctx context.Context, n *node.SSH) (func(ctx context.Context, script string, out io.Writer) (int, error), error) {
	res, err := n.Exec(ctx, "id", "-u")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(res.Stdout)) == "0" {
		return func(ctx context.Context, script string, out io.Writer) (int, error) {
			return n.RunScript(ctx, "sh -c "+node.ShellQuote(script), out)
		}, nil
	}
	if res, err := n.Exec(ctx, "sudo", "-n", "true"); err == nil && res.ExitCode == 0 {
		return func(ctx context.Context, script string, out io.Writer) (int, error) {
			return n.RunScript(ctx, "sudo -n sh -c "+node.ShellQuote(script), out)
		}, nil
	}
	if pw := n.Password(); pw != "" {
		res, err := n.ExecInput(ctx, strings.NewReader(pw+"\n"), "sudo", "-S", "-p", "", "true")
		if err == nil && res.ExitCode == 0 {
			return func(ctx context.Context, script string, out io.Writer) (int, error) {
				r, err := n.ExecInput(ctx, strings.NewReader(pw+"\n"), "sudo", "-S", "-p", "", "sh", "-c", script)
				out.Write(r.Stdout)
				out.Write(r.Stderr)
				return r.ExitCode, err
			}, nil
		}
	}
	return nil, errors.New("SSH 账号 " + n.User() + " 不是 root，也无法使用 sudo；请改用 root 账号，或为该账号配置 sudo 权限")
}

func runRoot(ctx context.Context, t *task.Task, run func(context.Context, string, io.Writer) (int, error), script string) error {
	out := &lineLog{t: t}
	code, err := run(ctx, script, out)
	out.Flush()
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("命令失败（退出码 %d），详见任务日志", code)
	}
	return nil
}

// probeAgent 检查远程是否已有 agent 接口：返回 panel 表示对方装的是面板。
func probeAgent(ctx context.Context, n *node.SSH) (version string, panel bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := n.AgentClient().Info(ctx)
	if err != nil {
		return "", false, err
	}
	return info.AgentVersion, info.Panel, nil
}

func (s *Service) installAgent(ctx context.Context, t *task.Task, n *node.SSH) error {
	if _, err := n.Client(ctx); err != nil {
		return err
	}
	if _, panel, err := probeAgent(ctx, n); err == nil && panel {
		return errors.New("该服务器装有 sshDesktop 面板，面板自带 agent 接口，无需另装")
	}
	res, err := n.Exec(ctx, "uname", "-m")
	if err != nil {
		return err
	}
	arch := agentbin.Arch(string(res.Stdout))
	if arch == "" {
		return fmt.Errorf("不支持的架构：%s（只支持 amd64 和 arm64）", strings.TrimSpace(string(res.Stdout)))
	}
	t.Log("系统架构：%s", arch)
	if res, err := n.Exec(ctx, "test", "-d", "/run/systemd/system"); err != nil || res.ExitCode != 0 {
		return errors.New("远程服务器没有使用 systemd，暂不支持一键安装")
	}
	run, err := rootRunner(ctx, n)
	if err != nil {
		return err
	}
	t.Progress(10)
	bin, err := agentbin.Get(ctx, arch, s.d.Version, s.d.CacheDir)
	if err != nil {
		return err
	}
	t.Log("准备 agent 二进制（%.1f MB）", float64(len(bin))/(1<<20))
	t.Progress(20)

	fs := n.FS()
	home := fs.HomeDir()
	tmpBin := path.Join(home, ".sshdesktop-agent.upload")
	tmpUnit := path.Join(home, ".sshdesktop-agent.service.upload")
	tmpSocket := path.Join(home, ".sshdesktop-agent.socket.upload")
	release, err := node.AcquireTransfer(ctx, n)
	if err != nil {
		return err
	}
	err = func() error {
		defer release()
		for _, f := range []struct {
			p    string
			data []byte
		}{{tmpBin, bin}, {tmpUnit, []byte(unitFile)}, {tmpSocket, []byte(socketFile)}} {
			fh, err := fs.OpenFile(f.p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("上传失败：%w", err)
			}
			_, err = io.Copy(fh, bytes.NewReader(f.data))
			if cerr := fh.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				fs.Remove(f.p)
				return fmt.Errorf("上传失败：%w", err)
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}
	t.Log("已通过 SFTP 上传")
	t.Progress(60)

	q := node.ShellQuote
	script := strings.Join([]string{
		"set -e",
		"systemctl stop sshdesktop-agent.service sshdesktop-agent.socket >/dev/null 2>&1 || true",
		cleanupRuntime,
		"install -m 0755 " + q(tmpBin) + " " + agentBin,
		"install -m 0644 " + q(tmpUnit) + " " + agentUnit,
		"install -m 0644 " + q(tmpSocket) + " " + agentSocket,
		"rm -f " + q(tmpBin) + " " + q(tmpUnit) + " " + q(tmpSocket),
		"systemctl daemon-reload",
		"systemctl enable sshdesktop-agent.socket sshdesktop-agent.service >/dev/null 2>&1",
		"systemctl start sshdesktop-agent.socket",
		"systemctl start sshdesktop-agent.service",
		"echo 已启动 sshdesktop-agent 服务",
	}, "\n")
	if err := runRoot(ctx, t, run, script); err != nil {
		fs.Remove(tmpBin)
		fs.Remove(tmpUnit)
		fs.Remove(tmpSocket)
		return err
	}
	t.Progress(80)
	for i := 0; i < 15; i++ {
		if v, _, err := probeAgent(ctx, n); err == nil {
			t.Log("agent %s 已就绪", v)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("agent 服务已启动，但接口没有响应；请在远程服务器上查看 journalctl -u sshdesktop-agent")
}

func (s *Service) uninstallAgent(ctx context.Context, t *task.Task, n *node.SSH) error {
	if _, err := n.Client(ctx); err != nil {
		return err
	}
	if _, panel, err := probeAgent(ctx, n); err == nil && panel {
		return errors.New("该服务器的 agent 接口由 sshDesktop 面板提供，不能在这里卸载")
	}
	run, err := rootRunner(ctx, n)
	if err != nil {
		return err
	}
	script := strings.Join([]string{
		"systemctl disable --now sshdesktop-agent.service sshdesktop-agent.socket >/dev/null 2>&1 || true",
		"rm -f " + agentBin + " " + agentUnit + " " + agentSocket,
		"systemctl daemon-reload",
		"rm -rf /var/lib/sshdesktop-agent /var/lib/private/sshdesktop-agent",
		cleanupRuntime,
		"echo 已卸载 sshdesktop-agent",
	}, "\n")
	return runRoot(ctx, t, run, script)
}
