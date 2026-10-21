package website

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/nginx"
	"github.com/apple2333usa/sshdesktop/internal/pending"
	"github.com/apple2333usa/sshdesktop/internal/task"
)

// 面板安装的 OpenResty 容器使用主机网络（反向代理到 127.0.0.1 上的端口），
// 配置、证书、/var/www 以相同路径挂载进容器，所以站点配置中的路径在两种运行方式下一致。
const (
	openrestyVersion = "1.31.1.1"
	openrestyImage   = "openresty/openresty:" + openrestyVersion + "-alpine"
)

func mainConfContainer(rt nginx.Runtime, ipv6 bool) string {
	v6 := ""
	if ipv6 {
		v6 = "\n        listen [::]:80 default_server;"
	}
	return fmt.Sprintf(`# 由 sshDesktop 管理：面板安装的 OpenResty 的主配置。
worker_processes auto;
error_log %s warn;

events {
    worker_connections 4096;
}

http {
    include /usr/local/openresty/nginx/conf/mime.types;
    default_type application/octet-stream;
    sendfile on;
    tcp_nopush on;
    keepalive_timeout 65;
    server_tokens off;
    client_max_body_size 100m;
    gzip on;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml image/svg+xml;
    access_log off;

    # 不匹配任何站点的请求直接断开
    server {
        listen 80 default_server;%s
        server_name _;
        return 444;
    }

    include %s/*.conf;
}
`, nginx.Quote(filepath.Join(rt.LogDir, "error.log")), v6, rt.SiteDir)
}

func composeContainer(rt nginx.Runtime, dataDir string) string {
	return fmt.Sprintf(`# 由 sshDesktop 管理：网站使用的 OpenResty。
name: %[1]s
services:
  openresty:
    image: %[2]s
    container_name: %[1]s
    restart: unless-stopped
    network_mode: host
    # 默认 1024 个文件句柄不够 worker_connections 4096 用，Nginx 每次加载都会警告
    ulimits:
      nofile:
        soft: 65535
        hard: 65535
    command: ["openresty", "-c", "%[3]s", "-g", "daemon off;"]
    volumes:
      - %[4]s:%[4]s
      - %[5]s:%[5]s:ro
      - /var/www:/var/www
    labels:
      sshdesktop.role: website
`, nginx.ContainerName, openrestyImage, rt.Conf, rt.ComposeDir, filepath.Join(dataDir, "certs"))
}

func (s *Service) setupContainer(ctx context.Context, t *task.Task) error {
	if err := s.d.Docker.Ping(ctx); err != nil {
		return fmt.Errorf("Docker 不可用，请先在“Docker”中安装：%w", err)
	}
	rt := s.containerRuntime()
	for _, p := range []int{80, 443} {
		if who := PortOwner(p); who != "" && !strings.Contains(who, "openresty") && !strings.Contains(who, "nginx") {
			return fmt.Errorf("端口 %d 已被 %s 占用，请先停止它", p, who)
		} else if who != "" {
			if bin := s.findHost(); bin != "" {
				return fmt.Errorf("端口 %d 已被宿主机上的 Nginx 占用，请改为接管已有的 Nginx", p)
			}
		}
	}
	if err := s.ensureDirs(); err != nil {
		return err
	}
	for _, d := range []string{rt.SiteDir, filepath.Dir(rt.Conf), filepath.Join(s.d.DataDir, "certs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := pending.WriteFile(rt.Conf, []byte(mainConfContainer(rt, s.ipv6)), 0o644); err != nil {
		return err
	}
	compose := filepath.Join(rt.ComposeDir, "compose.yaml")
	if err := pending.WriteFile(compose, []byte(composeContainer(rt, s.d.DataDir)), 0o644); err != nil {
		return err
	}
	t.Log("拉取镜像 %s", openrestyImage)
	out := t.Lines()
	defer out.Flush()
	p := docker.Project{Name: nginx.ContainerName, Dir: rt.ComposeDir}
	if err := docker.Compose(ctx, p, out, "pull"); err != nil {
		if ok, _ := s.d.Docker.ImageExists(ctx, openrestyImage); !ok {
			return err
		}
		t.Log("拉取失败，使用本机已有的镜像：%v", err)
	}
	if err := docker.Compose(ctx, p, out, "up", "-d", "--remove-orphans"); err != nil {
		return err
	}
	// 等待容器启动
	deadline := time.Now().Add(30 * time.Second)
	for !rt.Running(ctx) {
		if time.Now().After(deadline) {
			return fmt.Errorf("OpenResty 容器没有启动，请在 Docker 中查看容器日志")
		}
		time.Sleep(time.Second)
	}
	return rt.Test(ctx)
}

func controlRuntime(ctx context.Context, rt nginx.Runtime, action string) error {
	var cmd *exec.Cmd
	if rt.Mode == nginx.ModeContainer {
		cmd = exec.CommandContext(ctx, "docker", action, rt.Container)
	} else {
		unit := "nginx"
		if rt.Flavor == "openresty" {
			unit = "openresty"
		}
		if action == "restart" {
			if err := rt.Test(ctx); err != nil {
				return err
			}
		}
		cmd = exec.CommandContext(ctx, "systemctl", action, unit)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s 失败：%s", action, strings.TrimSpace(string(out)))
	}
	return nil
}

var reSSProc = regexp.MustCompile(`users:\(\("([^"]+)"`)

// listenerProcess 用 ss 查找监听端口的进程名。
func listenerProcess(port int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ss", "-Hltnp", fmt.Sprintf("sport = :%d", port)).Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		if m := reSSProc.FindStringSubmatch(l); m != nil {
			return m[1]
		}
	}
	return ""
}
