// Package docker 是 Docker 管理模块（仅本机）：容器、镜像、网络、存储卷、Compose 项目，
// 以及 Docker 未安装时的一键安装。服务器上已有的容器和 Compose 项目不论是否由面板创建都能管理。
package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

type Deps struct {
	Store   *store.Store
	Tasks   *task.Manager
	Docker  *docker.Client
	DataDir string
	Confirm func(c *gin.Context) bool
	Audit   func(module, action string) gin.HandlerFunc
	// AppOf 返回 Compose 项目对应的应用商店应用名（没有时为空），用于在列表中标注来源。
	AppOf func(project string) string
}

type Service struct {
	d Deps
}

func New(d Deps) *Service { return &Service{d: d} }

// Register 注册到 /api/v1/nodes/:node 下（调用方保证只对本机开放）。
func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/docker")
	g.GET("/status", view, s.status)
	g.POST("/install", manage, a("docker", "install"), s.install)
	g.POST("/service/:action", manage, a("docker", "service"), s.serviceControl)

	g.GET("/containers", view, s.containers)
	g.GET("/containers/stats", view, s.stats)
	g.GET("/containers/:id", view, s.containerInspect)
	g.GET("/containers/:id/logs", view, s.containerLogs)
	g.POST("/containers", manage, a("docker", "container-create"), s.containerCreate)
	g.POST("/containers/prune", manage, a("docker", "container-prune"), s.containerPrune)
	g.POST("/containers/:id/rename", manage, a("docker", "container-rename"), s.containerRename)
	g.POST("/containers/:id/:action", manage, a("docker", "container-control"), s.containerAction)
	g.DELETE("/containers/:id", manage, a("docker", "container-delete"), s.containerDelete)

	g.GET("/images", view, s.images)
	g.POST("/images/pull", manage, a("docker", "image-pull"), s.imagePull)
	g.POST("/images/prune", manage, a("docker", "image-prune"), s.imagePrune)
	g.DELETE("/images/:id", manage, a("docker", "image-delete"), s.imageDelete)

	g.GET("/networks", view, s.networks)
	g.POST("/networks", manage, a("docker", "network-create"), s.networkCreate)
	g.POST("/networks/prune", manage, a("docker", "network-prune"), s.networkPrune)
	g.DELETE("/networks/:id", manage, a("docker", "network-delete"), s.networkDelete)

	g.GET("/volumes", view, s.volumes)
	g.POST("/volumes", manage, a("docker", "volume-create"), s.volumeCreate)
	g.POST("/volumes/prune", manage, a("docker", "volume-prune"), s.volumePrune)
	g.DELETE("/volumes/:id", manage, a("docker", "volume-delete"), s.volumeDelete)

	g.GET("/compose", view, s.composeList)
	g.POST("/compose", manage, a("docker", "compose-create"), s.composeCreate)
	g.GET("/compose/:id", view, s.composeGet)
	g.GET("/compose/:id/logs", view, s.composeLogs)
	g.PUT("/compose/:id", manage, a("docker", "compose-edit"), s.composeSave)
	g.POST("/compose/:id/:action", manage, a("docker", "compose-control"), s.composeAction)
	g.DELETE("/compose/:id", manage, a("docker", "compose-delete"), s.composeDelete)
}

// fail 把 Docker 错误转换为接口错误。
func fail(c *gin.Context, err error) {
	var de *docker.Error
	switch {
	case errors.Is(err, docker.ErrUnavailable):
		api.Fail(c, api.Err(http.StatusServiceUnavailable, api.CodeDockerUnavailable, err.Error()))
	case errors.As(err, &de) && de.Status == http.StatusNotFound:
		api.Fail(c, api.NotFound(de.Message))
	case errors.As(err, &de) && de.Status == http.StatusConflict:
		api.Fail(c, api.Conflict(api.CodeConflict, de.Message))
	case errors.As(err, &de) && de.Status < 500:
		api.Fail(c, api.BadRequest(de.Message))
	default:
		api.Fail(c, api.Err(http.StatusInternalServerError, api.CodeCommandFailed, err.Error()))
	}
}

func timeout(c *gin.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), d)
}

/* ---------- 状态与安装 ---------- */

type statusView struct {
	Installed  bool         `json:"installed"`
	Running    bool         `json:"running"`
	Error      string       `json:"error,omitempty"`
	Version    string       `json:"version,omitempty"`
	APIVersion string       `json:"apiVersion,omitempty"`
	Compose    string       `json:"compose,omitempty"`
	Info       *docker.Info `json:"info,omitempty"`
	Socket     string       `json:"socket"`
}

func (s *Service) status(c *gin.Context) {
	ctx, cancel := timeout(c, 10*time.Second)
	defer cancel()
	v := statusView{Socket: s.d.Docker.Socket()}
	_, err := exec.LookPath("docker")
	v.Installed = err == nil
	ver, err := s.d.Docker.Version(ctx)
	if err != nil {
		v.Error = err.Error()
		api.OK(c, v)
		return
	}
	v.Installed, v.Running, v.Version, v.APIVersion = true, true, ver.Version, ver.APIVersion
	if info, err := s.d.Docker.Info(ctx); err == nil {
		v.Info = &info
	}
	if _, cv, err := docker.ComposeCommand(ctx); err == nil {
		v.Compose = cv
	}
	api.OK(c, v)
}

func (s *Service) serviceControl(c *gin.Context) {
	action := c.Param("action")
	if action != "start" && action != "restart" && action != "stop" {
		api.Fail(c, api.BadRequest("不支持的操作"))
		return
	}
	api.Target(c, "docker "+action)
	ctx, cancel := timeout(c, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", action, "docker").CombinedOutput()
	if err != nil {
		api.Fail(c, api.Err(http.StatusInternalServerError, api.CodeCommandFailed, strings.TrimSpace(string(out))+" "+err.Error()))
		return
	}
	api.OK(c, nil)
}

/* ---------- 容器 ---------- */

type portView struct {
	IP      string `json:"ip,omitempty"`
	Private int    `json:"private"`
	Public  int    `json:"public,omitempty"`
	Proto   string `json:"proto"`
}

type containerView struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Image    string            `json:"image"`
	ImageID  string            `json:"imageId"`
	Command  string            `json:"command"`
	Created  int64             `json:"created"`
	State    string            `json:"state"`
	Status   string            `json:"status"`
	Ports    []portView        `json:"ports"`
	Networks []string          `json:"networks"`
	IPs      []string          `json:"ips"`
	Mounts   []mountView       `json:"mounts"`
	Labels   map[string]string `json:"labels"`
	Project  string            `json:"project,omitempty"`
	Service  string            `json:"service,omitempty"`
	App      string            `json:"app,omitempty"`
}

type mountView struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

func toView(ct docker.Container, appOf func(string) string) containerView {
	v := containerView{
		ID: ct.ID, Name: ct.Name(), Image: ct.Image, ImageID: ct.ImageID, Command: ct.Command, Created: ct.Created,
		State: ct.State, Status: ct.Status, Labels: ct.Labels, Ports: []portView{}, Networks: []string{}, IPs: []string{},
		Mounts:  []mountView{},
		Project: ct.Labels[docker.LabelProject], Service: ct.Labels[docker.LabelService],
	}
	seen := map[string]bool{}
	for _, p := range ct.Ports {
		// IPv4 与 IPv6 各报一条，合并显示
		k := fmt.Sprintf("%d/%d/%s", p.PrivatePort, p.PublicPort, p.Type)
		if seen[k] {
			continue
		}
		seen[k] = true
		v.Ports = append(v.Ports, portView{IP: p.IP, Private: p.PrivatePort, Public: p.PublicPort, Proto: p.Type})
	}
	sort.Slice(v.Ports, func(i, j int) bool { return v.Ports[i].Private < v.Ports[j].Private })
	for name, n := range ct.NetworkSettings.Networks {
		v.Networks = append(v.Networks, name)
		if n.IPAddress != "" {
			v.IPs = append(v.IPs, n.IPAddress)
		}
	}
	sort.Strings(v.Networks)
	for _, m := range ct.Mounts {
		v.Mounts = append(v.Mounts, mountView{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	if v.Project != "" && appOf != nil {
		v.App = appOf(v.Project)
	}
	return v
}

func (s *Service) containers(c *gin.Context) {
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	list, err := s.d.Docker.Containers(ctx, true, nil)
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]containerView, 0, len(list))
	for _, ct := range list {
		out = append(out, toView(ct, s.d.AppOf))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	api.OK(c, out)
}

// stats 并发采样所有运行中容器的资源占用（每个约 1 秒，最多 8 个并发）。
func (s *Service) stats(c *gin.Context) {
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	list, err := s.d.Docker.Containers(ctx, false, nil)
	if err != nil {
		fail(c, err)
		return
	}
	out := map[string]docker.Stats{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, ct := range list {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st, err := s.d.Docker.ContainerStats(ctx, id)
			if err == nil {
				mu.Lock()
				out[id] = st
				mu.Unlock()
			}
		}(ct.ID)
	}
	wg.Wait()
	api.OK(c, out)
}

func (s *Service) containerInspect(c *gin.Context) {
	ctx, cancel := timeout(c, 10*time.Second)
	defer cancel()
	raw, err := s.d.Docker.ContainerInspect(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	api.OK(c, raw)
}

func (s *Service) containerAction(c *gin.Context) {
	id, action := c.Param("id"), c.Param("action")
	api.Target(c, action+" "+id)
	ctx, cancel := timeout(c, 90*time.Second)
	defer cancel()
	if err := s.d.Docker.ContainerAction(ctx, id, action); err != nil {
		fail(c, err)
		return
	}
	api.OK(c, nil)
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func (s *Service) containerRename(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
	}
	if !api.Bind(c, &req) {
		return
	}
	if !namePattern.MatchString(req.Name) {
		api.Fail(c, api.BadRequest("名称只能包含字母、数字、下划线、点和短横线"))
		return
	}
	api.Target(c, c.Param("id")+" → "+req.Name)
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	if err := s.d.Docker.ContainerRename(ctx, c.Param("id"), req.Name); err != nil {
		fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) containerDelete(c *gin.Context) {
	id := c.Param("id")
	api.Target(c, id)
	ctx, cancel := timeout(c, 90*time.Second)
	defer cancel()
	if err := s.d.Docker.ContainerRemove(ctx, id, c.Query("force") == "1", c.Query("volumes") == "1"); err != nil {
		fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) containerPrune(c *gin.Context) {
	ctx, cancel := timeout(c, 5*time.Minute)
	defer cancel()
	rep, err := s.d.Docker.ContainersPrune(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	api.Target(c, fmt.Sprintf("删除 %d 个已停止的容器", rep.Deleted))
	api.OK(c, rep)
}

// CreateForm 是创建容器的表单。
type CreateForm struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Command string `json:"command"` // 留空用镜像默认命令；按 shell 规则拆分参数
	Ports   []struct {
		HostIP    string `json:"hostIp"`
		Host      string `json:"host"` // 可为端口或范围，留空随机
		Container string `json:"container"`
		Proto     string `json:"proto"`
	} `json:"ports"`
	Env     []string `json:"env"` // KEY=VALUE
	Volumes []struct {
		Source string `json:"source"` // 宿主机路径或存储卷名
		Target string `json:"target"`
		RO     bool   `json:"ro"`
	} `json:"volumes"`
	Labels     map[string]string `json:"labels"`
	Restart    string            `json:"restart"` // no / always / unless-stopped / on-failure
	Network    string            `json:"network"`
	MemoryMB   int64             `json:"memoryMb"`
	CPUs       float64           `json:"cpus"`
	Privileged bool              `json:"privileged"`
	Tty        bool              `json:"tty"`
	Pull       string            `json:"pull"` // missing（默认）/ always
	Start      *bool             `json:"start"`
}

// Build 把表单转换为 Docker 的创建请求。
func (f *CreateForm) Build() (docker.CreateRequest, error) {
	req := docker.CreateRequest{Image: strings.TrimSpace(f.Image), Labels: f.Labels, Tty: f.Tty, OpenStdin: f.Tty}
	if req.Image == "" {
		return req, errors.New("请填写镜像")
	}
	if f.Name != "" && !namePattern.MatchString(f.Name) {
		return req, errors.New("容器名只能包含字母、数字、下划线、点和短横线")
	}
	if cmd := strings.TrimSpace(f.Command); cmd != "" {
		args, err := SplitArgs(cmd)
		if err != nil {
			return req, err
		}
		req.Cmd = args
	}
	for _, e := range f.Env {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if k, _, _ := strings.Cut(e, "="); k == "" || strings.ContainsAny(k, " \t") {
			return req, fmt.Errorf("环境变量格式应为 KEY=VALUE：%s", e)
		}
		req.Env = append(req.Env, e)
	}
	for _, p := range f.Ports {
		cp := strings.TrimSpace(p.Container)
		if cp == "" {
			continue
		}
		proto := p.Proto
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" && proto != "udp" {
			return req, fmt.Errorf("协议只能是 tcp 或 udp")
		}
		if n, err := strconv.Atoi(cp); err != nil || n < 1 || n > 65535 {
			return req, fmt.Errorf("容器端口无效：%s", cp)
		}
		if hp := strings.TrimSpace(p.Host); hp != "" {
			if n, err := strconv.Atoi(hp); err != nil || n < 1 || n > 65535 {
				return req, fmt.Errorf("宿主机端口无效：%s", hp)
			}
		}
		key := cp + "/" + proto
		if req.ExposedPorts == nil {
			req.ExposedPorts = map[string]struct{}{}
			req.HostConfig.PortBindings = map[string][]docker.PortBinding{}
		}
		req.ExposedPorts[key] = struct{}{}
		req.HostConfig.PortBindings[key] = append(req.HostConfig.PortBindings[key],
			docker.PortBinding{HostIP: strings.TrimSpace(p.HostIP), HostPort: strings.TrimSpace(p.Host)})
	}
	for _, v := range f.Volumes {
		src, dst := strings.TrimSpace(v.Source), strings.TrimSpace(v.Target)
		if src == "" && dst == "" {
			continue
		}
		if src == "" || !strings.HasPrefix(dst, "/") {
			return req, fmt.Errorf("挂载需要填写来源和容器内的绝对路径")
		}
		b := src + ":" + dst
		if v.RO {
			b += ":ro"
		}
		req.HostConfig.Binds = append(req.HostConfig.Binds, b)
	}
	switch f.Restart {
	case "", "no", "always", "unless-stopped", "on-failure":
		req.HostConfig.RestartPolicy.Name = f.Restart
	default:
		return req, errors.New("重启策略无效")
	}
	req.HostConfig.NetworkMode = strings.TrimSpace(f.Network)
	if f.MemoryMB > 0 {
		req.HostConfig.Memory = f.MemoryMB << 20
	}
	if f.CPUs > 0 {
		req.HostConfig.NanoCPUs = int64(f.CPUs * 1e9)
	}
	req.HostConfig.Privileged = f.Privileged
	return req, nil
}

// SplitArgs 按 shell 的引号规则拆分命令参数（不做变量展开）。
func SplitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inArg := false
	var quote rune
	esc := false
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && quote != '\'':
			esc, inArg = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inArg = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if inArg {
				out = append(out, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 || esc {
		return nil, errors.New("命令中的引号没有闭合")
	}
	if inArg {
		out = append(out, cur.String())
	}
	return out, nil
}

func (s *Service) containerCreate(c *gin.Context) {
	var f CreateForm
	if !api.Bind(c, &f) {
		return
	}
	req, err := f.Build()
	if err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	title := "创建容器 " + req.Image
	if f.Name != "" {
		title = "创建容器 " + f.Name
	}
	api.Target(c, f.Name+" ("+req.Image+")")
	t := s.d.Tasks.Start("docker.container-create", title, api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		exists, err := s.d.Docker.ImageExists(ctx, req.Image)
		if err != nil {
			return err
		}
		if !exists || f.Pull == "always" {
			if err := s.pull(ctx, t, req.Image, 0, 80); err != nil {
				return err
			}
		}
		id, err := s.d.Docker.ContainerCreate(ctx, f.Name, req)
		if err != nil {
			return err
		}
		t.Log("已创建容器 %s", id[:12])
		t.Progress(90)
		if f.Start == nil || *f.Start {
			if err := s.d.Docker.ContainerAction(ctx, id, "start"); err != nil {
				return fmt.Errorf("容器已创建，但启动失败：%w", err)
			}
			t.Log("已启动")
		}
		return nil
	})
	api.OK(c, t)
}

// pull 拉取镜像，把进度映射到任务进度 [from, to]，各层状态变化写入日志。
func (s *Service) pull(ctx context.Context, t *task.Task, ref string, from, to float64) error {
	return Pull(ctx, s.d.Docker, t, ref, from, to)
}

// Pull 拉取镜像并把进度写入任务（应用商店也用它）。
func Pull(ctx context.Context, cl *docker.Client, t *task.Task, ref string, from, to float64) error {
	t.Log("拉取镜像 %s", ref)
	type layer struct{ cur, total int64 }
	layers := map[string]*layer{}
	last := map[string]string{}
	err := cl.ImagePull(ctx, ref, func(p docker.PullProgress) {
		if p.ID != "" && p.ProgressDetail.Total > 0 {
			l := layers[p.ID]
			if l == nil {
				l = &layer{}
				layers[p.ID] = l
			}
			l.cur, l.total = p.ProgressDetail.Current, p.ProgressDetail.Total
			var cur, total int64
			for _, l := range layers {
				cur += l.cur
				total += l.total
			}
			if total > 0 {
				t.Progress(from + (to-from)*float64(cur)/float64(total))
			}
		}
		if p.Status == "" || p.Status == "Downloading" || p.Status == "Extracting" || p.Status == "Waiting" {
			return
		}
		if last[p.ID] == p.Status {
			return
		}
		last[p.ID] = p.Status
		if p.ID != "" {
			t.Log("%s: %s", p.ID, p.Status)
		} else {
			t.Log("%s", p.Status)
		}
	})
	if err != nil {
		return fmt.Errorf("拉取镜像 %s 失败：%w", ref, err)
	}
	t.Progress(to)
	return nil
}

// containerLogs 用 WebSocket 推送容器日志（JSON 字符串数组，每 200 ms 合并一次）。
func (s *Service) containerLogs(c *gin.Context) {
	id := c.Param("id")
	tail, _ := strconv.Atoi(c.DefaultQuery("tail", "500"))
	if tail <= 0 || tail > 20000 {
		tail = 500
	}
	tty, err := s.d.Docker.ContainerTTY(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	streamLines(c, func(ctx context.Context, w io.Writer) error {
		rc, err := s.d.Docker.ContainerLogs(ctx, id, docker.LogOptions{Follow: true, Tail: tail, Timestamps: c.Query("timestamps") == "1"})
		if err != nil {
			return err
		}
		defer rc.Close()
		if tty {
			_, err = io.Copy(w, rc)
			return err
		}
		return docker.Demux(rc, w)
	})
}

// streamLines 把 produce 写出的内容按行经 WebSocket 推送，直到客户端断开或 produce 结束。
func streamLines(c *gin.Context, produce func(ctx context.Context, w io.Writer) error) {
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
	pr, pw := io.Pipe()
	go func() {
		err := produce(ctx, pw)
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(pw, "\n[%s]\n", err.Error())
		}
		pw.Close()
	}()
	lineCh := make(chan string, 1024)
	go func() {
		defer close(lineCh)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			select {
			case lineCh <- strings.TrimRight(sc.Text(), "\r"):
			case <-ctx.Done():
				return
			}
		}
	}()
	defer pr.Close()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var batch []string
	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		b, _ := json.Marshal(batch)
		batch = batch[:0]
		return conn.Write(ctx, websocket.MessageText, b) == nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case l, ok := <-lineCh:
			if !ok {
				flush()
				conn.Close(websocket.StatusNormalClosure, "eof")
				return
			}
			batch = append(batch, l)
			if len(batch) >= 500 && !flush() {
				return
			}
		case <-tick.C:
			if !flush() {
				return
			}
		}
	}
}

/* ---------- 镜像 ---------- */

type imageView struct {
	ID         string   `json:"id"`
	Tags       []string `json:"tags"`
	Digests    []string `json:"digests"`
	Created    int64    `json:"created"`
	Size       int64    `json:"size"`
	Containers []string `json:"containers"`
}

func (s *Service) images(c *gin.Context) {
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	list, err := s.d.Docker.Images(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	cts, _ := s.d.Docker.Containers(ctx, true, nil)
	used := map[string][]string{}
	for _, ct := range cts {
		used[ct.ImageID] = append(used[ct.ImageID], ct.Name())
	}
	out := make([]imageView, 0, len(list))
	for _, im := range list {
		tags := []string{}
		for _, t := range im.RepoTags {
			if t != "<none>:<none>" {
				tags = append(tags, t)
			}
		}
		v := imageView{ID: im.ID, Tags: tags, Digests: im.RepoDigests, Created: im.Created, Size: im.Size, Containers: used[im.ID]}
		if v.Containers == nil {
			v.Containers = []string{}
		}
		if v.Digests == nil {
			v.Digests = []string{}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	api.OK(c, out)
}

var imageRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,255}$`)

func (s *Service) imagePull(c *gin.Context) {
	var req struct {
		Image string `json:"image"`
	}
	if !api.Bind(c, &req) {
		return
	}
	ref := strings.TrimSpace(req.Image)
	if !imageRef.MatchString(ref) {
		api.Fail(c, api.BadRequest("镜像名称无效"))
		return
	}
	api.Target(c, ref)
	t := s.d.Tasks.Start("docker.image-pull", "拉取镜像 "+ref, api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		return s.pull(ctx, t, ref, 0, 100)
	})
	api.OK(c, t)
}

func (s *Service) imageDelete(c *gin.Context) {
	id := c.Param("id")
	api.Target(c, id)
	ctx, cancel := timeout(c, 60*time.Second)
	defer cancel()
	if err := s.d.Docker.ImageRemove(ctx, id, c.Query("force") == "1"); err != nil {
		fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) imagePrune(c *gin.Context) {
	var req struct {
		All bool `json:"all"`
	}
	if !api.Bind(c, &req) {
		return
	}
	ctx, cancel := timeout(c, 10*time.Minute)
	defer cancel()
	rep, err := s.d.Docker.ImagesPrune(ctx, req.All)
	if err != nil {
		fail(c, err)
		return
	}
	api.Target(c, fmt.Sprintf("删除 %d 个镜像，释放 %d 字节", rep.Deleted, rep.SpaceReclaimed))
	api.OK(c, rep)
}

/* ---------- 网络 ---------- */

type networkView struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Internal   bool              `json:"internal"`
	Created    string            `json:"created"`
	Subnet     string            `json:"subnet"`
	Gateway    string            `json:"gateway"`
	Labels     map[string]string `json:"labels"`
	Containers []string          `json:"containers"`
	Builtin    bool              `json:"builtin"`
}

func (s *Service) networks(c *gin.Context) {
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	list, err := s.d.Docker.Networks(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	cts, _ := s.d.Docker.Containers(ctx, true, nil)
	used := map[string][]string{}
	for _, ct := range cts {
		for n := range ct.NetworkSettings.Networks {
			used[n] = append(used[n], ct.Name())
		}
	}
	out := make([]networkView, 0, len(list))
	for _, n := range list {
		v := networkView{ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal, Created: n.Created,
			Labels: n.Labels, Containers: used[n.Name], Builtin: n.Name == "bridge" || n.Name == "host" || n.Name == "none"}
		if len(n.IPAM.Config) > 0 {
			v.Subnet, v.Gateway = n.IPAM.Config[0].Subnet, n.IPAM.Config[0].Gateway
		}
		if v.Containers == nil {
			v.Containers = []string{}
		}
		sort.Strings(v.Containers)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	api.OK(c, out)
}

func (s *Service) networkCreate(c *gin.Context) {
	var req struct {
		Name     string `json:"name"`
		Driver   string `json:"driver"`
		Subnet   string `json:"subnet"`
		Gateway  string `json:"gateway"`
		Internal bool   `json:"internal"`
	}
	if !api.Bind(c, &req) {
		return
	}
	if !namePattern.MatchString(req.Name) {
		api.Fail(c, api.BadRequest("网络名只能包含字母、数字、下划线、点和短横线"))
		return
	}
	api.Target(c, req.Name)
	nc := docker.NetworkCreate{Name: req.Name, Driver: req.Driver, Internal: req.Internal}
	if nc.Driver == "" {
		nc.Driver = "bridge"
	}
	if req.Subnet != "" {
		cfg := map[string]string{"Subnet": strings.TrimSpace(req.Subnet)}
		if req.Gateway != "" {
			cfg["Gateway"] = strings.TrimSpace(req.Gateway)
		}
		nc.IPAM = &struct {
			Config []map[string]string `json:"Config"`
		}{Config: []map[string]string{cfg}}
	}
	ctx, cancel := timeout(c, 30*time.Second)
	defer cancel()
	id, err := s.d.Docker.NetworkCreate(ctx, nc)
	if err != nil {
		fail(c, err)
		return
	}
	api.OK(c, gin.H{"id": id})
}

func (s *Service) networkDelete(c *gin.Context) {
	api.Target(c, c.Param("id"))
	ctx, cancel := timeout(c, 30*time.Second)
	defer cancel()
	if err := s.d.Docker.NetworkRemove(ctx, c.Param("id")); err != nil {
		fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) networkPrune(c *gin.Context) {
	ctx, cancel := timeout(c, 2*time.Minute)
	defer cancel()
	rep, err := s.d.Docker.NetworksPrune(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	api.Target(c, fmt.Sprintf("删除 %d 个网络", rep.Deleted))
	api.OK(c, rep)
}

/* ---------- 存储卷 ---------- */

type volumeView struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	Created    string            `json:"created"`
	Labels     map[string]string `json:"labels"`
	Containers []string          `json:"containers"`
	Project    string            `json:"project,omitempty"`
}

func (s *Service) volumes(c *gin.Context) {
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	list, err := s.d.Docker.Volumes(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	cts, _ := s.d.Docker.Containers(ctx, true, nil)
	used := map[string][]string{}
	for _, ct := range cts {
		for _, m := range ct.Mounts {
			if m.Type == "volume" {
				used[m.Name] = append(used[m.Name], ct.Name())
			}
		}
	}
	out := make([]volumeView, 0, len(list))
	for _, v := range list {
		vv := volumeView{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Created: v.CreatedAt, Labels: v.Labels,
			Containers: used[v.Name], Project: v.Labels["com.docker.compose.project"]}
		if vv.Containers == nil {
			vv.Containers = []string{}
		}
		out = append(out, vv)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	api.OK(c, out)
}

func (s *Service) volumeCreate(c *gin.Context) {
	var req struct {
		Name   string `json:"name"`
		Driver string `json:"driver"`
	}
	if !api.Bind(c, &req) {
		return
	}
	if !namePattern.MatchString(req.Name) {
		api.Fail(c, api.BadRequest("存储卷名只能包含字母、数字、下划线、点和短横线"))
		return
	}
	api.Target(c, req.Name)
	ctx, cancel := timeout(c, 30*time.Second)
	defer cancel()
	v, err := s.d.Docker.VolumeCreate(ctx, req.Name, req.Driver, nil)
	if err != nil {
		fail(c, err)
		return
	}
	api.OK(c, v)
}

// volumeDelete 删除存储卷（数据随之删除，二级确认）。
func (s *Service) volumeDelete(c *gin.Context) {
	api.Target(c, c.Param("id"))
	if !s.d.Confirm(c) {
		return
	}
	ctx, cancel := timeout(c, 60*time.Second)
	defer cancel()
	if err := s.d.Docker.VolumeRemove(ctx, c.Param("id"), c.Query("force") == "1"); err != nil {
		fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) volumePrune(c *gin.Context) {
	api.Target(c, "清理未使用的存储卷")
	if !s.d.Confirm(c) {
		return
	}
	ctx, cancel := timeout(c, 10*time.Minute)
	defer cancel()
	rep, err := s.d.Docker.VolumesPrune(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	api.Target(c, fmt.Sprintf("删除 %d 个存储卷，释放 %d 字节", rep.Deleted, rep.SpaceReclaimed))
	api.OK(c, rep)
}
