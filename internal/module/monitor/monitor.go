// Package monitor 提供监控概览：本机与远程节点的实时指标推送、历史曲线和多节点总览。
// 本机由内置采集模块采集；远程节点优先经 SSH 通道读取 agent（或对方面板自带的 agent 接口），
// 没有 agent 时在 SSH 连接上运行采集脚本（见 remote.go）。
package monitor

import (
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/metric"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/realtime"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

// 流量统计保留约 13 个月，足够按月对比。
const trafficKeepDays = 400

type Service struct {
	st    *store.Store
	col   *agent.Collector
	hub   *realtime.Hub
	nodes *node.Registry
	// bc 把本机实时样本分发给面板自带的 agent 接口（其他面板经 SSH 读取）。
	bc      *agent.Broadcaster
	traffic *agent.Traffic

	mu      sync.Mutex
	remotes map[string]*remote
}

func New(st *store.Store, col *agent.Collector, hub *realtime.Hub, nodes *node.Registry) *Service {
	s := &Service{
		st: st, col: col, hub: hub, nodes: nodes,
		bc: agent.NewBroadcaster(), traffic: agent.NewTraffic(),
		remotes: map[string]*remote{},
	}
	s.loadTraffic()
	col.Traffic = s.traffic
	col.OnSample = func(sm agent.Sample) {
		s.bc.Publish(sm)
		s.publish(node.LocalID, sm)
	}
	col.OnMinute = func(m metric.Metric) {
		s.saveMinute(node.LocalID, m)
		s.saveTraffic(node.LocalID, s.traffic.TakeDirty())
	}
	return s
}

func (s *Service) publish(id string, sm agent.Sample) {
	ch := "metrics:" + id
	if s.hub.HasSubscribers(ch) {
		s.hub.Publish(ch, sm)
	}
}

func (s *Service) loadTraffic() {
	var rows []store.TrafficDaily
	s.st.DB.Where("node_id = ? AND day >= ?", node.LocalID, agent.DayOf(time.Now().AddDate(0, 0, -trafficKeepDays))).Find(&rows)
	list := make([]agent.TrafficRow, 0, len(rows))
	for _, r := range rows {
		list = append(list, agent.TrafficRow{Day: r.Day, Iface: r.Iface, Rx: r.Rx, Tx: r.Tx})
	}
	s.traffic.Load(list)
}

func (s *Service) saveTraffic(id string, rows []agent.TrafficRow) {
	if len(rows) == 0 {
		return
	}
	list := make([]store.TrafficDaily, 0, len(rows))
	for _, r := range rows {
		list = append(list, store.TrafficDaily{NodeID: id, Day: r.Day, Iface: r.Iface, Rx: r.Rx, Tx: r.Tx})
	}
	if err := s.st.DB.Clauses(clause.OnConflict{UpdateAll: true}).CreateInBatches(&list, 200).Error; err != nil {
		slog.Warn("保存流量统计失败", "node", id, "err", err)
	}
}

func (s *Service) saveMinute(id string, m store.Metric) {
	m.NodeID = id
	db := s.st.DB.Clauses(clause.OnConflict{UpdateAll: true})
	if err := db.Create(&store.Metric1m{Metric: m}).Error; err != nil {
		slog.Warn("保存分钟指标失败", "node", id, "err", err)
		return
	}
	end := m.TS + 60
	if end%900 == 0 {
		s.rollup(id, "metrics_1m", end-900, end, func(a store.Metric) { db.Create(&store.Metric15m{Metric: a}) })
	}
	if end%3600 == 0 {
		s.rollup(id, "metrics_15m", end-3600, end, func(a store.Metric) { db.Create(&store.Metric1h{Metric: a}) })
	}
}

func (s *Service) rollup(nodeID, table string, from, to int64, save func(store.Metric)) {
	var rows []store.Metric
	s.st.DB.Table(table).Where("node_id = ? AND ts >= ? AND ts < ?", nodeID, from, to).Order("ts").Find(&rows)
	if len(rows) == 0 {
		return
	}
	a := agent.Average(rows)
	a.NodeID, a.TS = nodeID, from
	save(a)
}

// rollupRange 补算 [from, to) 内全部 15 分钟级和小时级数据（用于补齐离线期间从 agent 取回的历史）。
func (s *Service) rollupRange(nodeID string, from, to int64) {
	db := s.st.DB.Clauses(clause.OnConflict{UpdateAll: true})
	for t := from - from%900; t+900 <= to; t += 900 {
		s.rollup(nodeID, "metrics_1m", t, t+900, func(a store.Metric) { db.Create(&store.Metric15m{Metric: a}) })
	}
	for t := from - from%3600; t+3600 <= to; t += 3600 {
		s.rollup(nodeID, "metrics_15m", t, t+3600, func(a store.Metric) { db.Create(&store.Metric1h{Metric: a}) })
	}
}

// Cleanup 按保留期清理：分钟级 7 天，15 分钟级 90 天，小时级 2 年；流量统计约 13 个月。
func (s *Service) Cleanup() {
	now := time.Now()
	s.st.DB.Where("ts < ?", now.AddDate(0, 0, -7).Unix()).Delete(&store.Metric1m{})
	s.st.DB.Where("ts < ?", now.AddDate(0, 0, -90).Unix()).Delete(&store.Metric15m{})
	s.st.DB.Where("ts < ?", now.AddDate(-2, 0, 0).Unix()).Delete(&store.Metric1h{})
	s.st.DB.Where("day < ?", agent.DayOf(now.AddDate(0, 0, -trafficKeepDays))).Delete(&store.TrafficDaily{})
	s.traffic.Prune(now, trafficKeepDays)
}

/* ---------- 远程节点 ---------- */

// Attach 为远程节点启动（或重新启动）监控采集。
func (s *Service) Attach(n *node.SSH) {
	r := newRemote(s, n)
	s.mu.Lock()
	old := s.remotes[n.ID()]
	s.remotes[n.ID()] = r
	s.mu.Unlock()
	if old != nil {
		old.stop()
	}
	go r.run()
}

// Detach 停止远程节点的采集（节点被删除）。
func (s *Service) Detach(id string) {
	s.mu.Lock()
	old := s.remotes[id]
	delete(s.remotes, id)
	s.mu.Unlock()
	if old != nil {
		old.stop()
	}
}

// Restart 重新探测监控来源（例如刚安装或卸载了 agent）。
func (s *Service) Restart(id string) {
	if n, ok := s.nodes.SSH(id); ok {
		s.Attach(n)
	}
}

// Stop 停止全部远程采集（面板退出）。
func (s *Service) Stop() {
	s.mu.Lock()
	list := s.remotes
	s.remotes = map[string]*remote{}
	s.mu.Unlock()
	for _, r := range list {
		r.stop()
	}
}

func (s *Service) remote(id string) *remote {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remotes[id]
}

/* ---------- 面板自带的 agent 接口 ---------- */

type provider struct{ s *Service }

func (p provider) Info() agent.Info                         { return p.s.col.Info() }
func (p provider) Subscribe() (<-chan agent.Sample, func()) { return p.s.bc.Subscribe() }
func (p provider) Traffic(from, to int) []agent.TrafficRow  { return p.s.traffic.Rows(from, to) }
func (p provider) History(from, to int64) []metric.Metric {
	var rows []store.Metric
	p.s.st.DB.Table("metrics_1m").Where("node_id = ? AND ts >= ? AND ts <= ?", node.LocalID, from, to).Order("ts").Find(&rows)
	return rows
}

// Provider 返回面板自带 agent 接口的数据来源。
func (s *Service) Provider() agent.Provider { return provider{s} }

/* ---------- 接口 ---------- */

func (s *Service) Register(r *gin.RouterGroup, view gin.HandlerFunc) {
	r.GET("/monitor/current", view, s.current)
	r.GET("/monitor/realtime", view, s.realtime)
	r.GET("/monitor/history", view, s.history)
}

// RegisterGlobal 注册不属于单个节点的接口。
func (s *Service) RegisterGlobal(r *gin.RouterGroup, view gin.HandlerFunc) {
	r.GET("/monitor/overview", view, s.overview)
}

// Overview 是多节点总览中的一张卡片。
type Overview struct {
	ID string `json:"id"`
	// Source：local / agent / panel / ssh；空表示还没取到数据
	Source       string        `json:"source"`
	AgentVersion string        `json:"agentVersion,omitempty"`
	Info         *agent.Info   `json:"info"`
	Sample       *agent.Sample `json:"sample"`
	Error        string        `json:"error,omitempty"`
}

func (s *Service) describe(id string) Overview {
	if id == node.LocalID {
		info := s.col.Info()
		o := Overview{ID: id, Source: "local", Info: &info}
		if sm, ok := s.col.Latest(); ok {
			o.Sample = &sm
		}
		return o
	}
	r := s.remote(id)
	if r == nil {
		return Overview{ID: id}
	}
	return r.overview()
}

// SourceOf 返回节点的监控来源（local / agent / panel / ssh，未知为空）。
func (s *Service) SourceOf(id string) string { return s.describe(id).Source }

func (s *Service) overview(c *gin.Context) {
	out := []Overview{s.describe(node.LocalID)}
	for _, n := range s.nodes.Remote() {
		out = append(out, s.describe(n.ID()))
	}
	api.OK(c, out)
}

func (s *Service) current(c *gin.Context) {
	o := s.describe(c.Param("node"))
	api.OK(c, gin.H{"info": o.Info, "sample": o.Sample, "source": o.Source, "agentVersion": o.AgentVersion, "error": o.Error})
}

func (s *Service) realtime(c *gin.Context) {
	n, _ := strconv.Atoi(c.DefaultQuery("seconds", "300"))
	id := c.Param("node")
	if id == node.LocalID {
		api.OK(c, s.col.Recent(n))
		return
	}
	r := s.remote(id)
	if r == nil {
		api.OK(c, []agent.Sample{})
		return
	}
	api.OK(c, r.recent(n))
}

func (s *Service) history(c *gin.Context) {
	now := time.Now().Unix()
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	if to == 0 {
		to = now
	}
	if from == 0 {
		from = to - 3600
	}
	if from >= to {
		api.Fail(c, api.BadRequest("时间范围无效"))
		return
	}
	span := to - from
	tiers := []string{"metrics_1m", "metrics_15m", "metrics_1h"}
	start := 0
	switch {
	case span > 31*86400:
		start = 2
	case span > 26*3600:
		start = 1
	}
	var rows []store.Metric
	var step int64
	// 粗粒度数据还没积累起来时（刚安装），退回到更细的粒度
	for i := start; i >= 0; i-- {
		rows = rows[:0]
		s.st.DB.Table(tiers[i]).Where("node_id = ? AND ts >= ? AND ts <= ?", c.Param("node"), from, to).Order("ts").Find(&rows)
		step = []int64{60, 900, 3600}[i]
		if len(rows) > 0 {
			break
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, api.Response{Data: gin.H{"step": step, "points": rows}})
}
