// Package monitor 提供监控概览：实时指标推送与历史曲线。
package monitor

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/realtime"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

type Service struct {
	st  *store.Store
	col *agent.Collector
	hub *realtime.Hub
}

func New(st *store.Store, col *agent.Collector, hub *realtime.Hub) *Service {
	s := &Service{st: st, col: col, hub: hub}
	col.OnSample = func(sm agent.Sample) {
		if hub.HasSubscribers("metrics:local") {
			hub.Publish("metrics:local", sm)
		}
	}
	col.OnMinute = s.saveMinute
	return s
}

func (s *Service) saveMinute(m store.Metric) {
	m.NodeID = node.LocalID
	db := s.st.DB.Clauses(clause.OnConflict{UpdateAll: true})
	if err := db.Create(&store.Metric1m{Metric: m}).Error; err != nil {
		slog.Warn("保存分钟指标失败", "err", err)
		return
	}
	end := m.TS + 60
	if end%900 == 0 {
		s.rollup(m.NodeID, "metrics_1m", end-900, end, func(a store.Metric) { db.Create(&store.Metric15m{Metric: a}) })
	}
	if end%3600 == 0 {
		s.rollup(m.NodeID, "metrics_15m", end-3600, end, func(a store.Metric) { db.Create(&store.Metric1h{Metric: a}) })
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

// Cleanup 按保留期清理：分钟级 7 天，15 分钟级 90 天，小时级 2 年。
func (s *Service) Cleanup() {
	now := time.Now()
	s.st.DB.Where("ts < ?", now.AddDate(0, 0, -7).Unix()).Delete(&store.Metric1m{})
	s.st.DB.Where("ts < ?", now.AddDate(0, 0, -90).Unix()).Delete(&store.Metric15m{})
	s.st.DB.Where("ts < ?", now.AddDate(-2, 0, 0).Unix()).Delete(&store.Metric1h{})
}

func (s *Service) Register(r *gin.RouterGroup, view gin.HandlerFunc) {
	r.GET("/monitor/current", view, s.current)
	r.GET("/monitor/realtime", view, s.realtime)
	r.GET("/monitor/history", view, s.history)
}

func (s *Service) current(c *gin.Context) {
	latest, _ := s.col.Latest()
	api.OK(c, gin.H{"info": s.col.Info(), "sample": latest})
}

func (s *Service) realtime(c *gin.Context) {
	n, _ := strconv.Atoi(c.DefaultQuery("seconds", "300"))
	api.OK(c, s.col.Recent(n))
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
