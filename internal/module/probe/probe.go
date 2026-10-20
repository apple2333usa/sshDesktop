// Package probe 是服务监控：由面板定时探测 HTTP(S)、TCP 端口、Ping、SSL 证书到期，
// 结果保留 30 天，统计可用率和延迟；状态变化（异常、恢复）推送到通知中心。
package probe

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/realtime"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

type Probe = store.Probe

const (
	keepDays    = 30
	minInterval = 30
)

type Service struct {
	st    *store.Store
	hub   *realtime.Hub
	audit func(module, action string) gin.HandlerFunc

	mu      sync.Mutex
	ctx     context.Context
	workers map[uint]context.CancelFunc
	last    map[uint]Result
}

func New(st *store.Store, hub *realtime.Hub, audit func(string, string) gin.HandlerFunc) *Service {
	return &Service{st: st, hub: hub, audit: audit, workers: map[uint]context.CancelFunc{}, last: map[uint]Result{}}
}

// Run 启动所有已启用的探测，直到 ctx 结束。
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	var list []Probe
	s.st.DB.Where("enabled = ?", true).Find(&list)
	for _, p := range list {
		s.schedule(p)
	}
	<-ctx.Done()
}

// schedule 启动（或重启、停止）一个探测的定时器。
func (s *Service) schedule(p Probe) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.workers[p.ID]; cancel != nil {
		cancel()
		delete(s.workers, p.ID)
	}
	if s.ctx == nil || !p.Enabled {
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.workers[p.ID] = cancel
	go s.loop(ctx, p)
}

func (s *Service) unschedule(id uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel := s.workers[id]; cancel != nil {
		cancel()
		delete(s.workers, id)
	}
	delete(s.last, id)
}

func (s *Service) loop(ctx context.Context, p Probe) {
	interval := time.Duration(max(p.Interval, minInterval)) * time.Second
	// 错开启动时间，避免面板启动时所有探测同时发出
	delay := time.Duration(p.ID%10) * time.Second
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		s.runOnce(ctx, p)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Event 是状态变化通知（推送到 probe-events 频道）。
type Event struct {
	ProbeID uint   `json:"probeId"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Target  string `json:"target"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	TS      int64  `json:"ts"`
}

func (s *Service) runOnce(ctx context.Context, p Probe) {
	r := Check(ctx, p)
	if ctx.Err() != nil {
		return
	}
	ts := time.Now().Unix()
	if err := s.st.DB.Create(&store.ProbeResult{ProbeID: p.ID, TS: ts, OK: r.OK, LatencyMs: r.LatencyMs, Message: r.Message}).Error; err != nil {
		slog.Warn("保存探测结果失败", "probe", p.ID, "err", err)
	}
	s.mu.Lock()
	prev, had := s.last[p.ID]
	if !had {
		// 面板重启后以数据库里的上一次结果为准，避免重复通知
		var pr store.ProbeResult
		if s.st.DB.Where("probe_id = ? AND ts < ?", p.ID, ts).Order("ts desc").First(&pr).Error == nil {
			prev, had = Result{OK: pr.OK}, true
		}
	}
	s.last[p.ID] = r
	s.mu.Unlock()
	s.hub.Publish("probes", gin.H{"probeId": p.ID, "ts": ts, "result": r})
	if (had && prev.OK != r.OK) || (!had && !r.OK) {
		s.hub.Publish("probe-events", Event{ProbeID: p.ID, Name: p.Name, Type: p.Type, Target: p.Target, OK: r.OK, Message: r.Message, TS: ts})
	}
}

// Cleanup 删除 30 天前的探测结果。
func (s *Service) Cleanup() {
	s.st.DB.Where("ts < ?", time.Now().AddDate(0, 0, -keepDays).Unix()).Delete(&store.ProbeResult{})
}

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.audit
	r.GET("/probes", view, s.list)
	r.GET("/probes/:id/results", view, s.results)
	r.POST("/probes/test", manage, s.test)
	r.POST("/probes", manage, a("probe", "create"), s.create)
	r.PUT("/probes/:id", manage, a("probe", "update"), s.update)
	r.DELETE("/probes/:id", manage, a("probe", "delete"), s.remove)
}

type stats struct {
	Uptime24h  float64 `json:"uptime24h"` // 百分比，-1 表示没有数据
	Uptime7d   float64 `json:"uptime7d"`
	Uptime30d  float64 `json:"uptime30d"`
	AvgLatency float64 `json:"avgLatency24h"`
}

type view struct {
	Probe
	Last  *store.ProbeResult `json:"last"`
	Stats stats              `json:"stats"`
	// Recent 是最近 24 小时的结果，按时间分成 48 段（每段 30 分钟）：1 正常，0 异常，0~1 之间为部分异常，-1 无数据。
	Recent []float64 `json:"recent"`
}

func (s *Service) uptime(id uint, since int64) float64 {
	var r struct {
		Total int64
		Up    int64
	}
	s.st.DB.Model(&store.ProbeResult{}).Select("COUNT(*) AS total, COALESCE(SUM(CASE WHEN ok THEN 1 ELSE 0 END), 0) AS up").
		Where("probe_id = ? AND ts >= ?", id, since).Scan(&r)
	if r.Total == 0 {
		return -1
	}
	return float64(r.Up) / float64(r.Total) * 100
}

func (s *Service) describe(p Probe) view {
	now := time.Now().Unix()
	v := view{Probe: p}
	var last store.ProbeResult
	if s.st.DB.Where("probe_id = ?", p.ID).Order("ts desc").First(&last).Error == nil {
		v.Last = &last
	}
	v.Stats.Uptime24h = s.uptime(p.ID, now-86400)
	v.Stats.Uptime7d = s.uptime(p.ID, now-7*86400)
	v.Stats.Uptime30d = s.uptime(p.ID, now-30*86400)
	s.st.DB.Model(&store.ProbeResult{}).Select("COALESCE(AVG(latency_ms), 0)").
		Where("probe_id = ? AND ts >= ? AND ok", p.ID, now-86400).Scan(&v.Stats.AvgLatency)
	const buckets, span = 48, 1800
	var rows []store.ProbeResult
	s.st.DB.Select("ts", "ok").Where("probe_id = ? AND ts >= ?", p.ID, now-buckets*span).Find(&rows)
	up := make([]float64, buckets)
	cnt := make([]float64, buckets)
	for _, r := range rows {
		i := int((r.TS - (now - buckets*span)) / span)
		if i < 0 || i >= buckets {
			continue
		}
		cnt[i]++
		if r.OK {
			up[i]++
		}
	}
	v.Recent = make([]float64, buckets)
	for i := range v.Recent {
		if cnt[i] == 0 {
			v.Recent[i] = -1
		} else {
			v.Recent[i] = up[i] / cnt[i]
		}
	}
	return v
}

func (s *Service) list(c *gin.Context) {
	var list []Probe
	s.st.DB.Order("id").Find(&list)
	out := make([]view, 0, len(list))
	for _, p := range list {
		out = append(out, s.describe(p))
	}
	api.OK(c, out)
}

// results 返回时间范围内的结果；点数过多时按时间分桶，延迟取平均，有一次异常即视为异常。
func (s *Service) results(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	now := time.Now().Unix()
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	if to == 0 {
		to = now
	}
	if from == 0 {
		from = to - 86400
	}
	var rows []store.ProbeResult
	s.st.DB.Where("probe_id = ? AND ts >= ? AND ts <= ?", id, from, to).Order("ts").Find(&rows)
	const maxPoints = 1500
	if len(rows) > maxPoints {
		step := (to - from) / maxPoints
		var out []store.ProbeResult
		var cur *store.ProbeResult
		var latN float64
		for _, r := range rows {
			b := from + (r.TS-from)/step*step
			if cur == nil || cur.TS != b {
				if cur != nil {
					out = append(out, *cur)
				}
				cur = &store.ProbeResult{TS: b, OK: true}
				latN = 0
			}
			if r.OK {
				latN++
				cur.LatencyMs += (r.LatencyMs - cur.LatencyMs) / latN
			} else {
				cur.OK, cur.Message = false, r.Message
			}
		}
		if cur != nil {
			out = append(out, *cur)
		}
		rows = out
	}
	api.OK(c, rows)
}

type probeReq struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Target       string `json:"target"`
	Interval     int    `json:"interval"`
	Timeout      int    `json:"timeout"`
	Keyword      string `json:"keyword"`
	ExpectStatus string `json:"expectStatus"`
	SSLWarnDays  int    `json:"sslWarnDays"`
	Enabled      *bool  `json:"enabled"`
}

func (r *probeReq) probe() (Probe, error) {
	p := Probe{
		Name: strings.TrimSpace(r.Name), Type: r.Type, Target: strings.TrimSpace(r.Target),
		Interval: r.Interval, Timeout: r.Timeout, Keyword: r.Keyword, ExpectStatus: strings.TrimSpace(r.ExpectStatus),
		SSLWarnDays: r.SSLWarnDays, Enabled: r.Enabled == nil || *r.Enabled,
	}
	if p.Interval == 0 {
		p.Interval = 60
	}
	if p.Interval < minInterval || p.Interval > 86400 {
		return p, api.BadRequest(fmt.Sprintf("探测间隔应在 %d 秒到 1 天之间", minInterval))
	}
	if p.Timeout == 0 {
		p.Timeout = 10
	}
	if p.Timeout < 1 || p.Timeout > 60 {
		return p, api.BadRequest("超时应在 1 到 60 秒之间")
	}
	if p.Target == "" || len(p.Target) > 2048 {
		return p, api.BadRequest("请填写探测目标")
	}
	switch p.Type {
	case "http":
		u, err := url.Parse(p.Target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return p, api.BadRequest("HTTP 探测的目标应为 http:// 或 https:// 开头的网址")
		}
		if p.ExpectStatus != "" && !validStatus(p.ExpectStatus) {
			return p, api.BadRequest("期望状态码格式错误，例如 200-399 或 200,301")
		}
	case "tcp":
		if _, port, err := net.SplitHostPort(p.Target); err != nil || port == "" {
			return p, api.BadRequest("TCP 探测的目标应为 主机:端口，例如 example.com:443")
		}
	case "ping":
		if strings.ContainsAny(p.Target, " /:") && net.ParseIP(p.Target) == nil {
			return p, api.BadRequest("Ping 的目标应为域名或 IP")
		}
	case "ssl":
		if strings.Contains(p.Target, "/") {
			return p, api.BadRequest("SSL 探测的目标应为域名或 域名:端口")
		}
		if p.SSLWarnDays == 0 {
			p.SSLWarnDays = 14
		}
	default:
		return p, api.BadRequest("探测类型只能是 HTTP、TCP、Ping 或 SSL")
	}
	if p.Name == "" {
		p.Name = p.Target
	}
	if len([]rune(p.Name)) > 64 {
		return p, api.BadRequest("名称不能超过 64 个字符")
	}
	return p, nil
}

func validStatus(expect string) bool {
	for _, part := range strings.Split(expect, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		if _, err := strconv.Atoi(strings.TrimSpace(lo)); err != nil {
			return false
		}
		if isRange {
			if _, err := strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return false
			}
		}
	}
	return true
}

func (s *Service) test(c *gin.Context) {
	var req probeReq
	if !api.Bind(c, &req) {
		return
	}
	p, err := req.probe()
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, Check(c.Request.Context(), p))
}

func (s *Service) create(c *gin.Context) {
	var req probeReq
	if !api.Bind(c, &req) {
		return
	}
	p, err := req.probe()
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, p.Name+" "+p.Target)
	if err := s.st.DB.Create(&p).Error; err != nil {
		api.Fail(c, err)
		return
	}
	s.schedule(p)
	api.OK(c, s.describe(p))
}

func (s *Service) find(c *gin.Context) (*Probe, bool) {
	var p Probe
	if err := s.st.DB.First(&p, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("探测不存在"))
		return nil, false
	}
	return &p, true
}

func (s *Service) update(c *gin.Context) {
	old, ok := s.find(c)
	if !ok {
		return
	}
	var req probeReq
	if !api.Bind(c, &req) {
		return
	}
	p, err := req.probe()
	if err != nil {
		api.Fail(c, err)
		return
	}
	p.ID, p.CreatedAt = old.ID, old.CreatedAt
	api.Target(c, p.Name+" "+p.Target)
	if err := s.st.DB.Save(&p).Error; err != nil {
		api.Fail(c, err)
		return
	}
	s.unschedule(p.ID)
	s.schedule(p)
	api.OK(c, s.describe(p))
}

func (s *Service) remove(c *gin.Context) {
	p, ok := s.find(c)
	if !ok {
		return
	}
	api.Target(c, p.Name+" "+p.Target)
	s.unschedule(p.ID)
	s.st.DB.Where("probe_id = ?", p.ID).Delete(&store.ProbeResult{})
	s.st.DB.Delete(p)
	api.OK(c, nil)
}
