// Package service 管理 systemd 系统服务：列表、启停、开机自启、日志。仅本机。
package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

type Unit struct {
	Name        string `json:"name"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
	UnitFile    string `json:"unitFile"` // enabled / disabled / static / masked …
}

type Service struct {
	local *node.Local
	audit func(module, action string) gin.HandlerFunc
}

func New(local *node.Local, audit func(module, action string) gin.HandlerFunc) *Service {
	return &Service{local: local, audit: audit}
}

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	r.GET("/services", view, s.list)
	r.GET("/services/:name", view, s.detail)
	r.GET("/services/:name/logs", view, s.logs)
	r.POST("/services/:name/:action", manage, s.audit("service", "control"), s.control)
}

var unitName = regexp.MustCompile(`^[A-Za-z0-9@._:\\-]+\.service$`)

func (s *Service) systemctl(ctx context.Context, args ...string) (string, error) {
	res, err := s.local.Exec(ctx, "systemctl", args...)
	if err != nil {
		return "", api.Err(500, api.CodeCommandFailed, "无法执行 systemctl："+err.Error())
	}
	if res.ExitCode != 0 && len(res.Stdout) == 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = fmt.Sprintf("systemctl 退出码 %d", res.ExitCode)
		}
		return "", api.Err(400, api.CodeCommandFailed, msg)
	}
	return string(res.Stdout), nil
}

// ParseUnits 解析 `systemctl list-units --plain --no-legend` 的输出。
func ParseUnits(out string) []Unit {
	var units []Unit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimLeft(line, "● *")
		f := strings.Fields(line)
		if len(f) < 4 || !strings.HasSuffix(f[0], ".service") {
			continue
		}
		units = append(units, Unit{Name: f[0], Load: f[1], Active: f[2], Sub: f[3], Description: strings.Join(f[4:], " ")})
	}
	return units
}

// ParseUnitFiles 解析 `systemctl list-unit-files --no-legend` 的输出。
func ParseUnitFiles(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.HasSuffix(f[0], ".service") {
			m[f[0]] = f[1]
		}
	}
	return m
}

func (s *Service) list(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	out, err := s.systemctl(ctx, "list-units", "--type=service", "--all", "--plain", "--no-legend", "--no-pager")
	if err != nil {
		api.Fail(c, err)
		return
	}
	units := ParseUnits(out)
	files, _ := s.systemctl(ctx, "list-unit-files", "--type=service", "--no-legend", "--no-pager")
	states := ParseUnitFiles(files)
	seen := map[string]bool{}
	for i := range units {
		units[i].UnitFile = states[units[i].Name]
		seen[units[i].Name] = true
	}
	// 已安装但未加载的服务（例如停用的）也列出来
	for name, st := range states {
		if !seen[name] && !strings.Contains(name, "@.") {
			units = append(units, Unit{Name: name, Load: "not-loaded", Active: "inactive", Sub: "dead", UnitFile: st})
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Name < units[j].Name })
	api.OK(c, units)
}

var showProps = []string{
	"Id", "Description", "LoadState", "ActiveState", "SubState", "UnitFileState", "FragmentPath",
	"MainPID", "ExecMainStartTimestamp", "ActiveEnterTimestamp", "MemoryCurrent", "CPUUsageNSec",
	"TasksCurrent", "Restart", "User", "Type",
}

func (s *Service) detail(c *gin.Context) {
	name := c.Param("name")
	if !unitName.MatchString(name) {
		api.Fail(c, api.BadRequest("服务名无效"))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	out, err := s.systemctl(ctx, "show", name, "--no-pager", "-p", strings.Join(showProps, ","))
	if err != nil {
		api.Fail(c, err)
		return
	}
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	unitFile, _ := s.systemctl(ctx, "cat", name, "--no-pager")
	api.OK(c, gin.H{"props": props, "unitFile": unitFile})
}

var actions = map[string][]string{
	"start": {"start"}, "stop": {"stop"}, "restart": {"restart"}, "reload": {"reload"},
	"enable": {"enable"}, "disable": {"disable"},
}

func (s *Service) control(c *gin.Context) {
	name, action := c.Param("name"), c.Param("action")
	if !unitName.MatchString(name) {
		api.Fail(c, api.BadRequest("服务名无效"))
		return
	}
	args, ok := actions[action]
	if !ok {
		api.Fail(c, api.BadRequest("不支持的操作"))
		return
	}
	api.Target(c, action+" "+name)
	if action == "stop" || action == "restart" {
		if name == "sshdesktop.service" {
			// 让响应先返回，再由 systemd 重启面板
			if action == "restart" {
				api.OK(c, gin.H{"output": "面板即将重启"})
				go func() {
					time.Sleep(500 * time.Millisecond)
					exec.Command("systemctl", "restart", name).Run()
				}()
				return
			}
			api.Fail(c, api.BadRequest("不能在面板中停止面板自身"))
			return
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	res, err := s.local.Exec(ctx, "systemctl", append(args, name)...)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if res.ExitCode != 0 {
		api.Fail(c, api.Err(400, api.CodeCommandFailed, strings.TrimSpace(string(res.Stderr))))
		return
	}
	api.OK(c, gin.H{"output": strings.TrimSpace(string(res.Stdout) + string(res.Stderr))})
}

// logs 通过 WebSocket 实时跟随 journalctl 输出（文本帧，每帧若干行）。
func (s *Service) logs(c *gin.Context) {
	name := c.Param("name")
	if !unitName.MatchString(name) {
		api.Fail(c, api.BadRequest("服务名无效"))
		return
	}
	lines, _ := strconv.Atoi(c.DefaultQuery("lines", "200"))
	if lines <= 0 || lines > 5000 {
		lines = 200
	}
	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()
	cmd := exec.CommandContext(ctx, "journalctl", "-u", name, "-n", strconv.Itoa(lines), "-f", "-o", "short-iso", "--no-pager")
	cmd.Env = []string{"LC_ALL=C.UTF-8", "SYSTEMD_COLORS=0"}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		msg, _ := json.Marshal([]string{"无法执行 journalctl：" + err.Error()})
		conn.Write(ctx, websocket.MessageText, msg)
		return
	}
	defer cmd.Wait()
	lineCh := make(chan string, 1024)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			lineCh <- sc.Text()
		}
		close(lineCh)
	}()
	// 合并 200 ms 内的行一起发送，减少帧数
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var batch []string
	for {
		select {
		case <-ctx.Done():
			return
		case l, ok := <-lineCh:
			if !ok {
				if len(batch) > 0 {
					b, _ := json.Marshal(batch)
					conn.Write(ctx, websocket.MessageText, b)
				}
				return
			}
			batch = append(batch, l)
		case <-tick.C:
			if len(batch) > 0 {
				b, _ := json.Marshal(batch)
				if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
					return
				}
				batch = batch[:0]
			}
		}
	}
}
