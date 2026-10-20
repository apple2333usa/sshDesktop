package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/metric"
)

// SocketPath 是 agent 接口的 Unix socket。面板自带同样的接口，所以装了面板就不需要独立 agent。
const SocketPath = "/run/sshdesktop/agent.sock"

// AgentInfo 是 /v1/info 的返回值。
type AgentInfo struct {
	Info
	AgentVersion string `json:"agentVersion"`
	// Panel 为 true 表示对方装的是面板（自带 agent 接口）。
	Panel bool `json:"panel"`
}

// Provider 是 agent 接口背后的数据来源。
type Provider interface {
	Info() Info
	Subscribe() (<-chan Sample, func())
	History(from, to int64) []metric.Metric
	Traffic(from, to int) []TrafficRow
}

// Broadcaster 把实时样本分发给多个订阅者，订阅者处理不过来时丢弃。
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan Sample]struct{}
}

func NewBroadcaster() *Broadcaster { return &Broadcaster{subs: map[chan Sample]struct{}{}} }

func (b *Broadcaster) Subscribe() (<-chan Sample, func()) {
	ch := make(chan Sample, 16)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
		})
	}
}

func (b *Broadcaster) Publish(s Sample) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- s:
		default:
		}
	}
}

// Serve 在 Unix socket 上提供 agent 接口，直到 ctx 结束。接口全部只读，socket 权限 0666。
func Serve(ctx context.Context, path string, p Provider, version string, panel bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return errors.New(path + " 已被其他进程占用（可能是独立 agent 还在运行）")
	}
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	os.Chmod(path, 0o666)
	srv := &http.Server{Handler: Handler(p, version, panel), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
		os.Remove(path)
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// Handler 返回 agent 接口的 HTTP 处理器。
func Handler(p Provider, version string, panel bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, AgentInfo{Info: p.Info(), AgentVersion: version, Panel: panel})
	})
	mux.HandleFunc("GET /v1/metrics/stream", func(w http.ResponseWriter, r *http.Request) {
		ch, cancel := p.Subscribe()
		defer cancel()
		w.Header().Set("Content-Type", "application/x-ndjson")
		fl, _ := w.(http.Flusher)
		enc := json.NewEncoder(w)
		for {
			select {
			case <-r.Context().Done():
				return
			case s := <-ch:
				if enc.Encode(s) != nil {
					return
				}
				if fl != nil {
					fl.Flush()
				}
			}
		}
	})
	mux.HandleFunc("GET /v1/metrics/history", func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		to, _ := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
		if to == 0 {
			to = time.Now().Unix()
		}
		writeJSON(w, p.History(from, to))
	})
	mux.HandleFunc("GET /v1/traffic", func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.Atoi(r.URL.Query().Get("from"))
		to, _ := strconv.Atoi(r.URL.Query().Get("to"))
		if to == 0 {
			to = DayOf(time.Now())
		}
		writeJSON(w, p.Traffic(from, to))
	})
	mux.HandleFunc("GET /v1/processes/top", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		writeJSON(w, TopProcs(n))
	})
	return mux
}

// Client 通过任意连接方式（通常是 SSH 的 Unix socket 转发）访问 agent 接口。
type Client struct{ hc *http.Client }

func NewClient(dial func(ctx context.Context) (net.Conn, error)) *Client {
	tr := &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		DisableKeepAlives: true, // 每个请求一个 SSH 通道，断线重连后不会拿到失效的连接
	}
	return &Client{hc: &http.Client{Transport: tr}}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent"+path, nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("agent 返回 " + resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

func (c *Client) Info(ctx context.Context) (AgentInfo, error) {
	var in AgentInfo
	err := c.get(ctx, "/v1/info", &in)
	return in, err
}

func (c *Client) History(ctx context.Context, from, to int64) ([]metric.Metric, error) {
	var out []metric.Metric
	err := c.get(ctx, "/v1/metrics/history?from="+strconv.FormatInt(from, 10)+"&to="+strconv.FormatInt(to, 10), &out)
	return out, err
}

func (c *Client) Traffic(ctx context.Context, from, to int) ([]TrafficRow, error) {
	var out []TrafficRow
	err := c.get(ctx, "/v1/traffic?from="+strconv.Itoa(from)+"&to="+strconv.Itoa(to), &out)
	return out, err
}

func (c *Client) Top(ctx context.Context, n int) ([]Proc, error) {
	var out []Proc
	err := c.get(ctx, "/v1/processes/top?n="+strconv.Itoa(n), &out)
	return out, err
}

// Stream 持续读取实时样本，直到 ctx 结束或连接断开。
func (c *Client) Stream(ctx context.Context, fn func(Sample)) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://agent/v1/metrics/stream", nil)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("agent 返回 " + resp.Status)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var s Sample
		if json.Unmarshal(sc.Bytes(), &s) == nil {
			fn(s)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}
