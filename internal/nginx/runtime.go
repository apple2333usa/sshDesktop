package nginx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 运行方式：宿主机上已有的 Nginx / OpenResty（host），或面板安装的 OpenResty 容器（container）。
const (
	ModeHost      = "host"
	ModeContainer = "container"
)

// ContainerName 是面板安装的 OpenResty 容器名（也是 Compose 项目名）。
const ContainerName = "sshdesktop-openresty"

// Runtime 描述网站使用的 Web 服务器。
type Runtime struct {
	Mode    string `json:"mode"`
	Binary  string `json:"binary"`  // host：nginx / openresty 可执行文件
	Flavor  string `json:"flavor"`  // nginx / openresty
	Version string `json:"version"` // 如 1.26.3
	// Conf 是主配置文件；SiteDir 是面板写入站点配置的目录（宿主机上的路径，容器中挂载为同一路径）
	Conf    string `json:"conf"`
	SiteDir string `json:"siteDir"`
	// LogDir 是面板站点的日志目录
	LogDir string `json:"logDir"`
	// Container 模式下的容器名与 Compose 目录
	Container  string `json:"container,omitempty"`
	ComposeDir string `json:"composeDir,omitempty"`
}

// hostCandidates 是查找宿主机 Nginx 的位置。
var hostCandidates = []string{
	"/usr/local/openresty/bin/openresty", "/usr/local/openresty/nginx/sbin/nginx",
	"/usr/sbin/nginx", "/usr/local/nginx/sbin/nginx", "/usr/bin/nginx",
}

// FindHost 返回宿主机上的 Nginx / OpenResty 可执行文件，没有时返回空。
func FindHost() string {
	for _, n := range []string{"openresty", "nginx"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	for _, p := range hostCandidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

var (
	reVersion  = regexp.MustCompile(`(nginx|openresty)/([0-9][0-9.]*)`)
	reConfPath = regexp.MustCompile(`--conf-path=(\S+)`)
)

// DetectHost 读取宿主机 Nginx 的版本和主配置文件位置（nginx -V）。
func DetectHost(ctx context.Context, bin string) (Runtime, error) {
	rt := Runtime{Mode: ModeHost, Binary: bin, Flavor: "nginx"}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-V").CombinedOutput()
	if err != nil {
		return rt, fmt.Errorf("%s -V 失败：%s", bin, strings.TrimSpace(string(out)))
	}
	if m := reVersion.FindStringSubmatch(string(out)); m != nil {
		rt.Flavor, rt.Version = m[1], m[2]
	}
	if strings.Contains(string(out), "openresty") {
		rt.Flavor = "openresty"
	}
	if m := reConfPath.FindStringSubmatch(string(out)); m != nil {
		rt.Conf = m[1]
	} else {
		// 没有 --conf-path 时使用编译默认的 <prefix>/conf/nginx.conf
		rt.Conf = filepath.Join(filepath.Dir(filepath.Dir(bin)), "conf", "nginx.conf")
		if rt.Flavor == "openresty" {
			rt.Conf = "/usr/local/openresty/nginx/conf/nginx.conf"
		}
	}
	rt.SiteDir = filepath.Join(filepath.Dir(rt.Conf), "conf.d", "sshdesktop")
	return rt, nil
}

// AtLeast 比较版本号（只比较数字部分）。
func AtLeast(v string, want ...int) bool {
	parts := strings.Split(v, ".")
	for i, w := range want {
		n := 0
		if i < len(parts) {
			fmt.Sscan(parts[i], &n)
		}
		if n != w {
			return n > w
		}
	}
	return true
}

// cmd 返回在宿主机或容器中执行 nginx 的命令。
func (rt Runtime) cmd(ctx context.Context, args ...string) *exec.Cmd {
	if rt.Mode == ModeContainer {
		a := append([]string{"exec", rt.Container, "openresty", "-c", rt.Conf}, args...)
		return exec.CommandContext(ctx, "docker", a...)
	}
	a := args
	if rt.Conf != "" {
		a = append([]string{"-c", rt.Conf}, args...)
	}
	return exec.CommandContext(ctx, rt.Binary, a...)
}

// Test 执行 nginx -t，失败时返回其输出。
func (rt Runtime) Test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := rt.cmd(ctx, "-t").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return &TestError{Output: msg}
	}
	return nil
}

// TestError 是 nginx -t 校验失败。
type TestError struct{ Output string }

func (e *TestError) Error() string { return "配置校验失败（nginx -t）：\n" + e.Output }

// Running 表示 Nginx 正在运行。
func (rt Runtime) Running(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if rt.Mode == ModeContainer {
		out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", rt.Container).Output()
		return err == nil && strings.TrimSpace(string(out)) == "true"
	}
	for _, unit := range []string{"nginx", "openresty"} {
		if out, _ := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output(); strings.TrimSpace(string(out)) == "active" {
			return true
		}
	}
	return exec.CommandContext(ctx, "pgrep", "-x", "nginx").Run() == nil || exec.CommandContext(ctx, "pgrep", "-f", "openresty").Run() == nil
}

// Reload 让 Nginx 重新加载配置；没有运行时启动它。
func (rt Runtime) Reload(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if rt.Mode == ModeContainer {
		if !rt.Running(ctx) {
			out, err := exec.CommandContext(ctx, "docker", "start", rt.Container).CombinedOutput()
			if err != nil {
				return fmt.Errorf("启动 OpenResty 容器失败：%s", strings.TrimSpace(string(out)))
			}
			return nil
		}
		if out, err := rt.cmd(ctx, "-s", "reload").CombinedOutput(); err != nil {
			return fmt.Errorf("重新加载失败：%s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	unit := rt.unit(ctx)
	if unit != "" {
		action := "reload"
		if out, _ := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output(); strings.TrimSpace(string(out)) != "active" {
			action = "start"
		}
		if out, err := exec.CommandContext(ctx, "systemctl", action, unit).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s %s 失败：%s", action, unit, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if !rt.Running(ctx) {
		if out, err := rt.cmd(ctx).CombinedOutput(); err != nil {
			return fmt.Errorf("启动 Nginx 失败：%s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	if out, err := rt.cmd(ctx, "-s", "reload").CombinedOutput(); err != nil {
		return fmt.Errorf("重新加载失败：%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// unit 返回管理 Nginx 的 systemd 单元，没有时为空。
func (rt Runtime) unit(ctx context.Context) string {
	for _, u := range []string{"openresty", "nginx"} {
		out, _ := exec.CommandContext(ctx, "systemctl", "show", "-p", "LoadState", "--value", u).Output()
		if strings.TrimSpace(string(out)) == "loaded" {
			if u == "openresty" && rt.Flavor != "openresty" {
				continue
			}
			return u
		}
	}
	return ""
}

// File 是 nginx -T 输出中的一个配置文件。
type File struct {
	Path    string `json:"path"`
	Content string `json:"-"`
}

// Dump 执行 nginx -T，返回生效的全部配置文件（按包含顺序）。
func (rt Runtime) Dump(ctx context.Context) ([]File, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	c := rt.cmd(ctx, "-T")
	c.Stdout, c.Stderr = &stdout, &stderr
	if err := c.Run(); err != nil {
		return nil, &TestError{Output: strings.TrimSpace(stderr.String())}
	}
	return splitDump(stdout.String()), nil
}

const dumpMarker = "# configuration file "

func splitDump(s string) []File {
	var out []File
	for _, part := range strings.Split(s, "\n"+dumpMarker) {
		part = strings.TrimPrefix(part, dumpMarker)
		nl := strings.IndexByte(part, '\n')
		if nl < 0 {
			continue
		}
		head := strings.TrimSpace(part[:nl])
		if !strings.HasSuffix(head, ":") {
			continue
		}
		out = append(out, File{Path: strings.TrimSuffix(head, ":"), Content: part[nl+1:]})
	}
	return out
}

// Included 表示 path 在生效的配置中。
func Included(files []File, path string) bool {
	for _, f := range files {
		if f.Path == path {
			return true
		}
	}
	return false
}

// ErrNoHTTP 表示主配置中没有 http 块。
var ErrNoHTTP = errors.New("主配置中没有 http 块")

// InsertInclude 在主配置 http 块末尾插入一行 include，返回新内容。
func InsertInclude(conf, line string) (string, error) {
	ds, err := Parse(conf)
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Name == "http" && d.IsBlock() {
			pos := d.BlockClose
			// 插到 } 所在行之前
			ls := strings.LastIndexByte(conf[:pos], '\n')
			if ls >= 0 && strings.TrimSpace(conf[ls+1:pos]) == "" {
				pos = ls + 1
				return conf[:pos] + "    " + line + "\n" + conf[pos:], nil
			}
			return conf[:pos] + "\n    " + line + "\n" + conf[pos:], nil
		}
	}
	return "", ErrNoHTTP
}
