// Package docker 是 Docker Engine API 的精简客户端：经 Unix socket 直接调用 HTTP 接口，
// 只覆盖面板用到的容器、镜像、网络、存储卷和系统接口。Compose 走 docker compose 命令（见 compose.go）。
package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultSocket 是 dockerd 的默认 socket；环境变量 DOCKER_HOST=unix://… 可覆盖。
const DefaultSocket = "/var/run/docker.sock"

type Client struct {
	sock string
	hc   *http.Client
}

// New 创建客户端；sock 为空时取 DOCKER_HOST 或默认 socket。
func New(sock string) *Client {
	if sock == "" {
		sock = DefaultSocket
		if h := os.Getenv("DOCKER_HOST"); strings.HasPrefix(h, "unix://") {
			sock = strings.TrimPrefix(h, "unix://")
		}
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
		MaxIdleConns:    8,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Client{sock: sock, hc: &http.Client{Transport: tr}}
}

func (c *Client) Socket() string { return c.sock }

// Error 是 dockerd 返回的错误。
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// IsNotFound 判断是否为“对象不存在”。
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// ErrUnavailable 表示连不上 dockerd（未安装或未运行）。
var ErrUnavailable = errors.New("无法连接 Docker，请确认 Docker 已安装并正在运行")

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var op *net.OpError
		if errors.As(err, &op) {
			return nil, ErrUnavailable
		}
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var m struct {
			Message string `json:"message"`
		}
		msg := strings.TrimSpace(string(b))
		if json.Unmarshal(b, &m) == nil && m.Message != "" {
			msg = m.Message
		}
		if msg == "" {
			msg = fmt.Sprintf("Docker 返回 HTTP %d", resp.StatusCode)
		}
		return nil, &Error{Status: resp.StatusCode, Message: msg}
	}
	return resp, nil
}

// call 发送请求，out 不为 nil 时解码 JSON 响应。
func (c *Client) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := c.do(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func filters(m map[string][]string) string {
	b, _ := json.Marshal(m)
	return string(b)
}

/* ---------- 系统 ---------- */

func (c *Client) Ping(ctx context.Context) error {
	return c.call(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

type Version struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
	KernelVersion string `json:"KernelVersion"`
}

func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	err := c.call(ctx, http.MethodGet, "/version", nil, nil, &v)
	return v, err
}

type Info struct {
	Containers        int    `json:"Containers"`
	ContainersRunning int    `json:"ContainersRunning"`
	ContainersPaused  int    `json:"ContainersPaused"`
	ContainersStopped int    `json:"ContainersStopped"`
	Images            int    `json:"Images"`
	Driver            string `json:"Driver"`
	DockerRootDir     string `json:"DockerRootDir"`
	ServerVersion     string `json:"ServerVersion"`
	OperatingSystem   string `json:"OperatingSystem"`
	CgroupVersion     string `json:"CgroupVersion"`
	NCPU              int    `json:"NCPU"`
	MemTotal          int64  `json:"MemTotal"`
	RegistryConfig    struct {
		Mirrors []string `json:"Mirrors"`
	} `json:"RegistryConfig"`
}

func (c *Client) Info(ctx context.Context) (Info, error) {
	var v Info
	err := c.call(ctx, http.MethodGet, "/info", nil, nil, &v)
	return v, err
}

/* ---------- 容器 ---------- */

type Port struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

type MountPoint struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type Container struct {
	ID              string            `json:"Id"`
	Names           []string          `json:"Names"`
	Image           string            `json:"Image"`
	ImageID         string            `json:"ImageID"`
	Command         string            `json:"Command"`
	Created         int64             `json:"Created"`
	State           string            `json:"State"`
	Status          string            `json:"Status"`
	Ports           []Port            `json:"Ports"`
	Labels          map[string]string `json:"Labels"`
	Mounts          []MountPoint      `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Name 返回去掉前导斜杠的容器名。
func (ct Container) Name() string {
	if len(ct.Names) == 0 {
		return ct.ID[:min(12, len(ct.ID))]
	}
	return strings.TrimPrefix(ct.Names[0], "/")
}

func (c *Client) Containers(ctx context.Context, all bool, f map[string][]string) ([]Container, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	if len(f) > 0 {
		q.Set("filters", filters(f))
	}
	var out []Container
	err := c.call(ctx, http.MethodGet, "/containers/json", q, nil, &out)
	return out, err
}

// ContainerInspect 返回原始的 inspect JSON。
func (c *Client) ContainerInspect(ctx context.Context, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, nil, &raw)
	return raw, err
}

// ContainerTTY 返回容器是否分配了 TTY（决定日志是否多路复用）。
func (c *Client) ContainerTTY(ctx context.Context, id string) (bool, error) {
	raw, err := c.ContainerInspect(ctx, id)
	if err != nil {
		return false, err
	}
	var v struct {
		Config struct {
			Tty bool `json:"Tty"`
		} `json:"Config"`
	}
	json.Unmarshal(raw, &v)
	return v.Config.Tty, nil
}

// CreateRequest 是 POST /containers/create 的请求体（只列出用到的字段）。
type CreateRequest struct {
	Image        string              `json:"Image"`
	Cmd          []string            `json:"Cmd,omitempty"`
	Entrypoint   []string            `json:"Entrypoint,omitempty"`
	Env          []string            `json:"Env,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	WorkingDir   string              `json:"WorkingDir,omitempty"`
	Tty          bool                `json:"Tty,omitempty"`
	OpenStdin    bool                `json:"OpenStdin,omitempty"`
	HostConfig   HostConfig          `json:"HostConfig"`
}

type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type HostConfig struct {
	Binds         []string                 `json:"Binds,omitempty"`
	PortBindings  map[string][]PortBinding `json:"PortBindings,omitempty"`
	RestartPolicy struct {
		Name string `json:"Name,omitempty"`
	} `json:"RestartPolicy"`
	NetworkMode string `json:"NetworkMode,omitempty"`
	Memory      int64  `json:"Memory,omitempty"`
	NanoCPUs    int64  `json:"NanoCpus,omitempty"`
	Privileged  bool   `json:"Privileged,omitempty"`
}

func (c *Client) ContainerCreate(ctx context.Context, name string, req CreateRequest) (string, error) {
	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	var out struct {
		ID string `json:"Id"`
	}
	err := c.call(ctx, http.MethodPost, "/containers/create", q, req, &out)
	return out.ID, err
}

// ContainerAction 执行 start / stop / restart / kill / pause / unpause。
func (c *Client) ContainerAction(ctx context.Context, id, action string) error {
	switch action {
	case "start", "stop", "restart", "kill", "pause", "unpause":
	default:
		return fmt.Errorf("不支持的操作：%s", action)
	}
	err := c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/"+action, nil, nil, nil)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotModified {
		return nil // 已经是目标状态
	}
	return err
}

func (c *Client) ContainerRemove(ctx context.Context, id string, force, volumes bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	if volumes {
		q.Set("v", "1")
	}
	return c.call(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), q, nil, nil)
}

func (c *Client) ContainerRename(ctx context.Context, id, name string) error {
	return c.call(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/rename", url.Values{"name": {name}}, nil, nil)
}

// NetworkConnect 把容器接入网络。
func (c *Client) NetworkConnect(ctx context.Context, network, container string) error {
	return c.call(ctx, http.MethodPost, "/networks/"+url.PathEscape(network)+"/connect", nil,
		map[string]string{"Container": container}, nil)
}

// LogOptions 控制容器日志。
type LogOptions struct {
	Follow     bool
	Tail       int
	Timestamps bool
	Since      int64
}

// ContainerLogs 返回日志流；tty 为 false 时是多路复用格式，用 Demux 拆分。
func (c *Client) ContainerLogs(ctx context.Context, id string, o LogOptions) (io.ReadCloser, error) {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}}
	if o.Follow {
		q.Set("follow", "1")
	}
	if o.Tail > 0 {
		q.Set("tail", fmt.Sprint(o.Tail))
	}
	if o.Timestamps {
		q.Set("timestamps", "1")
	}
	if o.Since > 0 {
		q.Set("since", fmt.Sprint(o.Since))
	}
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Demux 把多路复用的日志流（8 字节头：流类型、3 字节保留、4 字节长度）合并写入 w。
func Demux(r io.Reader, w io.Writer) error {
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := int64(binary.BigEndian.Uint32(hdr[4:]))
		if _, err := io.CopyN(w, r, n); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// Stats 是一次资源占用采样。
type Stats struct {
	CPUPercent float64 `json:"cpuPercent"`
	MemUsage   uint64  `json:"memUsage"`
	MemLimit   uint64  `json:"memLimit"`
	NetRx      uint64  `json:"netRx"`
	NetTx      uint64  `json:"netTx"`
	BlockRead  uint64  `json:"blockRead"`
	BlockWrite uint64  `json:"blockWrite"`
	PIDs       uint64  `json:"pids"`
}

type rawStats struct {
	CPUStats    cpuStats `json:"cpu_stats"`
	PreCPUStats cpuStats `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IOServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage  uint64   `json:"total_usage"`
		PercpuUsage []uint64 `json:"percpu_usage"`
	} `json:"cpu_usage"`
	SystemUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs  int    `json:"online_cpus"`
}

// ContainerStats 采样一次（dockerd 会等待约 1 秒以计算 CPU 占用）。
func (c *Client) ContainerStats(ctx context.Context, id string) (Stats, error) {
	var r rawStats
	if err := c.call(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/stats", url.Values{"stream": {"0"}}, nil, &r); err != nil {
		return Stats{}, err
	}
	return r.summary(), nil
}

func (r rawStats) summary() Stats {
	var s Stats
	cpuDelta := float64(r.CPUStats.CPUUsage.TotalUsage) - float64(r.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(r.CPUStats.SystemUsage) - float64(r.PreCPUStats.SystemUsage)
	ncpu := r.CPUStats.OnlineCPUs
	if ncpu == 0 {
		ncpu = len(r.CPUStats.CPUUsage.PercpuUsage)
	}
	if cpuDelta > 0 && sysDelta > 0 && ncpu > 0 {
		s.CPUPercent = cpuDelta / sysDelta * float64(ncpu) * 100
	}
	// 与 docker stats 一致：扣除页缓存（cgroup v2 为 inactive_file，v1 为 total_inactive_file）
	s.MemUsage = r.MemoryStats.Usage
	for _, k := range []string{"inactive_file", "total_inactive_file"} {
		if v, ok := r.MemoryStats.Stats[k]; ok && v < s.MemUsage {
			s.MemUsage -= v
			break
		}
	}
	s.MemLimit = r.MemoryStats.Limit
	for _, n := range r.Networks {
		s.NetRx += n.RxBytes
		s.NetTx += n.TxBytes
	}
	for _, b := range r.BlkioStats.IOServiceBytesRecursive {
		switch strings.ToLower(b.Op) {
		case "read":
			s.BlockRead += b.Value
		case "write":
			s.BlockWrite += b.Value
		}
	}
	s.PIDs = r.PidsStats.Current
	return s
}

// PruneReport 是清理接口的结果。
type PruneReport struct {
	Deleted        int    `json:"deleted"`
	SpaceReclaimed uint64 `json:"spaceReclaimed"`
}

func (c *Client) prune(ctx context.Context, path string, f map[string][]string, key string) (PruneReport, error) {
	q := url.Values{}
	if len(f) > 0 {
		q.Set("filters", filters(f))
	}
	var raw map[string]json.RawMessage
	if err := c.call(ctx, http.MethodPost, path, q, nil, &raw); err != nil {
		return PruneReport{}, err
	}
	var rep PruneReport
	var list []json.RawMessage
	json.Unmarshal(raw[key], &list)
	rep.Deleted = len(list)
	json.Unmarshal(raw["SpaceReclaimed"], &rep.SpaceReclaimed)
	return rep, nil
}

func (c *Client) ContainersPrune(ctx context.Context) (PruneReport, error) {
	return c.prune(ctx, "/containers/prune", nil, "ContainersDeleted")
}

/* ---------- 镜像 ---------- */

type Image struct {
	ID          string   `json:"Id"`
	RepoTags    []string `json:"RepoTags"`
	RepoDigests []string `json:"RepoDigests"`
	Created     int64    `json:"Created"`
	Size        int64    `json:"Size"`
}

func (c *Client) Images(ctx context.Context) ([]Image, error) {
	var out []Image
	err := c.call(ctx, http.MethodGet, "/images/json", nil, nil, &out)
	return out, err
}

func (c *Client) ImageInspect(ctx context.Context, ref string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := c.call(ctx, http.MethodGet, "/images/"+ref+"/json", nil, nil, &raw)
	return raw, err
}

func (c *Client) ImageRemove(ctx context.Context, id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	return c.call(ctx, http.MethodDelete, "/images/"+id, q, nil, nil)
}

// ImagesPrune 清理镜像：all 为 false 时只清理悬空镜像，为 true 时清理所有未被容器使用的镜像。
func (c *Client) ImagesPrune(ctx context.Context, all bool) (PruneReport, error) {
	f := map[string][]string{"dangling": {"true"}}
	if all {
		f["dangling"] = []string{"false"}
	}
	return c.prune(ctx, "/images/prune", f, "ImagesDeleted")
}

// PullProgress 是拉取镜像时的一条进度消息。
type PullProgress struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Error          string `json:"error"`
	ProgressDetail struct {
		Current int64 `json:"current"`
		Total   int64 `json:"total"`
	} `json:"progressDetail"`
}

// SplitImage 把镜像引用拆成仓库和标签（没写标签时为 latest）。
func SplitImage(ref string) (repo, tag string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.LastIndex(ref, ":"); i > slash {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// ImagePull 拉取镜像，每条进度消息回调 fn。
func (c *Client) ImagePull(ctx context.Context, ref string, fn func(PullProgress)) error {
	repo, tag := SplitImage(ref)
	resp, err := c.do(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {repo}, "tag": {tag}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var p PullProgress
		if json.Unmarshal(sc.Bytes(), &p) != nil {
			continue
		}
		if p.Error != "" {
			return errors.New(p.Error)
		}
		if fn != nil {
			fn(p)
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}

// ImageExists 判断本地是否已有该镜像。
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	_, err := c.ImageInspect(ctx, ref)
	if IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

/* ---------- 网络 ---------- */

type Network struct {
	ID       string            `json:"Id"`
	Name     string            `json:"Name"`
	Driver   string            `json:"Driver"`
	Scope    string            `json:"Scope"`
	Internal bool              `json:"Internal"`
	Created  string            `json:"Created"`
	Labels   map[string]string `json:"Labels"`
	IPAM     struct {
		Config []struct {
			Subnet  string `json:"Subnet"`
			Gateway string `json:"Gateway"`
		} `json:"Config"`
	} `json:"IPAM"`
}

func (c *Client) Networks(ctx context.Context) ([]Network, error) {
	var out []Network
	err := c.call(ctx, http.MethodGet, "/networks", nil, nil, &out)
	return out, err
}

type NetworkCreate struct {
	Name     string `json:"Name"`
	Driver   string `json:"Driver,omitempty"`
	Internal bool   `json:"Internal,omitempty"`
	IPAM     *struct {
		Config []map[string]string `json:"Config"`
	} `json:"IPAM,omitempty"`
	Labels map[string]string `json:"Labels,omitempty"`
}

func (c *Client) NetworkCreate(ctx context.Context, req NetworkCreate) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	err := c.call(ctx, http.MethodPost, "/networks/create", nil, req, &out)
	return out.ID, err
}

func (c *Client) NetworkRemove(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/networks/"+url.PathEscape(id), nil, nil, nil)
}

func (c *Client) NetworksPrune(ctx context.Context) (PruneReport, error) {
	return c.prune(ctx, "/networks/prune", nil, "NetworksDeleted")
}

// EnsureNetwork 确保存在指定名称的 bridge 网络（应用商店的应用使用 1panel-network）。
func (c *Client) EnsureNetwork(ctx context.Context, name string) error {
	list, err := c.Networks(ctx)
	if err != nil {
		return err
	}
	for _, n := range list {
		if n.Name == name {
			return nil
		}
	}
	_, err = c.NetworkCreate(ctx, NetworkCreate{Name: name, Driver: "bridge"})
	return err
}

/* ---------- 存储卷 ---------- */

type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Labels     map[string]string `json:"Labels"`
	Scope      string            `json:"Scope"`
}

func (c *Client) Volumes(ctx context.Context) ([]Volume, error) {
	var out struct {
		Volumes []Volume `json:"Volumes"`
	}
	err := c.call(ctx, http.MethodGet, "/volumes", nil, nil, &out)
	return out.Volumes, err
}

func (c *Client) VolumeCreate(ctx context.Context, name, driver string, labels map[string]string) (Volume, error) {
	var v Volume
	body := map[string]any{"Name": name}
	if driver != "" {
		body["Driver"] = driver
	}
	if len(labels) > 0 {
		body["Labels"] = labels
	}
	err := c.call(ctx, http.MethodPost, "/volumes/create", nil, body, &v)
	return v, err
}

func (c *Client) VolumeRemove(ctx context.Context, name string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	return c.call(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), q, nil, nil)
}

// VolumesPrune 清理未被使用的存储卷（包括有名字的卷）。
func (c *Client) VolumesPrune(ctx context.Context) (PruneReport, error) {
	return c.prune(ctx, "/volumes/prune", map[string][]string{"all": {"true"}}, "VolumesDeleted")
}
