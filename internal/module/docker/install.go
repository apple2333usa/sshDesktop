package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
)

// 安装方式：script 为 Docker 官方安装脚本（get.docker.com，支持 --mirror），
// package 为发行版软件包（Debian / Ubuntu 用 docker.io，RHEL 系用 Docker 官方 yum 源）。
const installScript = `set -e
if [ "$SD_METHOD" = script ]; then
  if command -v curl >/dev/null 2>&1; then curl -fsSL https://get.docker.com -o /tmp/sshdesktop-get-docker.sh
  else wget -qO /tmp/sshdesktop-get-docker.sh https://get.docker.com; fi
  if [ -n "$SD_MIRROR" ]; then sh /tmp/sshdesktop-get-docker.sh --mirror "$SD_MIRROR"
  else sh /tmp/sshdesktop-get-docker.sh; fi
  rm -f /tmp/sshdesktop-get-docker.sh
elif command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y docker.io
  apt-get install -y docker-compose-v2 || apt-get install -y docker-compose-plugin || apt-get install -y docker-compose
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y dnf-plugins-core
  REPO=https://download.docker.com/linux/centos/docker-ce.repo
  if [ "$SD_MIRROR" = Aliyun ]; then REPO=https://mirrors.aliyun.com/docker-ce/linux/centos/docker-ce.repo; fi
  dnf config-manager --add-repo "$REPO" || dnf config-manager addrepo --from-repofile="$REPO"
  if [ "$SD_MIRROR" = Aliyun ]; then sed -i 's#download.docker.com#mirrors.aliyun.com/docker-ce#' /etc/yum.repos.d/docker-ce.repo; fi
  dnf install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
else
  echo "不支持的发行版：没有 apt-get 或 dnf" >&2
  exit 1
fi
systemctl enable --now docker
docker version
docker compose version || docker-compose version || true
`

func (s *Service) install(c *gin.Context) {
	var req struct {
		Method string `json:"method"` // script / package
		Mirror string `json:"mirror"` // 空 / Aliyun / AzureChinaCloud
	}
	if !api.Bind(c, &req) {
		return
	}
	if req.Method != "package" {
		req.Method = "script"
	}
	switch req.Mirror {
	case "", "Aliyun", "AzureChinaCloud":
	default:
		api.Fail(c, api.BadRequest("不支持的镜像源"))
		return
	}
	if err := s.d.Docker.Ping(c.Request.Context()); err == nil {
		api.Fail(c, api.BadRequest("Docker 已安装并在运行"))
		return
	}
	api.Target(c, req.Method+" "+req.Mirror)
	t := s.d.Tasks.Start("docker.install", "安装 Docker", api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		out := t.Lines()
		defer out.Flush()
		cmd := exec.CommandContext(ctx, "sh", "-c", installScript)
		cmd.Env = append(os.Environ(), "SD_METHOD="+req.Method, "SD_MIRROR="+req.Mirror, "LC_ALL=C")
		cmd.Stdout, cmd.Stderr = out, out
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
		cmd.WaitDelay = 10 * time.Second
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("安装失败：%w（详见任务日志）", err)
		}
		for i := 0; i < 20; i++ {
			if err := s.d.Docker.Ping(ctx); err == nil {
				t.Log("Docker 已就绪")
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("安装完成，但连不上 %s", strings.TrimSpace(s.d.Docker.Socket()))
	})
	api.OK(c, t)
}
