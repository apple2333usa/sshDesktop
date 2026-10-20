// Package traffic 是按月流量统计：每个节点按自己的重置日划分计费周期，统计各网卡上行、下行与合计，
// 可设置额度并显示已用百分比。数据来自本机采集模块和远程 agent 的持久化计数（见 monitor 模块）。
package traffic

import (
	"sort"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

type Service struct {
	st *store.Store
	// Source 返回节点的监控来源（local / agent / panel / ssh），只有前三种有流量统计。
	Source func(id string) string
}

func New(st *store.Store, source func(string) string) *Service { return &Service{st: st, Source: source} }

func (s *Service) Register(v1 *gin.RouterGroup, n *gin.RouterGroup, view, manage gin.HandlerFunc, audit func(string, string) gin.HandlerFunc) {
	v1.GET("/traffic/summary", view, s.summary)
	n.GET("/traffic", view, s.detail)
	n.PUT("/traffic/settings", manage, audit("traffic", "settings"), s.settings)
}

// Cycle 返回 t 所在计费周期的起止日期（含起始日，不含结束日）。resetDay 为每月重置日（1-28）。
func Cycle(t time.Time, resetDay int) (start, end time.Time) {
	if resetDay < 1 || resetDay > 28 {
		resetDay = 1
	}
	y, m, d := t.Date()
	start = time.Date(y, m, resetDay, 0, 0, 0, 0, t.Location())
	if d < resetDay {
		start = start.AddDate(0, -1, 0)
	}
	return start, start.AddDate(0, 1, 0)
}

type Usage struct {
	NodeID    string  `json:"nodeId"`
	Start     int     `json:"start"` // YYYYMMDD
	End       int     `json:"end"`   // YYYYMMDD（含）
	Rx        uint64  `json:"rx"`
	Tx        uint64  `json:"tx"`
	Quota     uint64  `json:"quota"`
	QuotaMode string  `json:"quotaMode"` // sum / rx / tx
	Used      uint64  `json:"used"`      // 按额度方式计算的用量
	Percent   float64 `json:"percent"`
	ResetDay  int     `json:"resetDay"`
	Supported bool    `json:"supported"`
}

func (s *Service) supported(id string) bool {
	if id == node.LocalID {
		return true
	}
	switch s.Source(id) {
	case "agent", "panel":
		return true
	}
	// 曾经有过 agent 的节点保留历史数据
	var n int64
	s.st.DB.Model(&store.TrafficDaily{}).Where("node_id = ?", id).Limit(1).Count(&n)
	return n > 0
}

func (s *Service) usage(row store.Node, start, end time.Time) Usage {
	u := Usage{
		NodeID: row.ID, Start: agent.DayOf(start), End: agent.DayOf(end.AddDate(0, 0, -1)),
		Quota: row.TrafficQuota, QuotaMode: row.TrafficQuotaMode, ResetDay: row.TrafficResetDay,
		Supported: s.supported(row.ID),
	}
	if u.QuotaMode == "" {
		u.QuotaMode = "sum"
	}
	if u.ResetDay == 0 {
		u.ResetDay = 1
	}
	var sum struct{ Rx, Tx uint64 }
	s.st.DB.Model(&store.TrafficDaily{}).Select("COALESCE(SUM(rx), 0) AS rx, COALESCE(SUM(tx), 0) AS tx").
		Where("node_id = ? AND day >= ? AND day <= ?", row.ID, u.Start, u.End).Scan(&sum)
	u.Rx, u.Tx = sum.Rx, sum.Tx
	switch u.QuotaMode {
	case "rx":
		u.Used = u.Rx
	case "tx":
		u.Used = u.Tx
	default:
		u.Used = u.Rx + u.Tx
	}
	if u.Quota > 0 {
		u.Percent = float64(u.Used) / float64(u.Quota) * 100
	}
	return u
}

func (s *Service) summary(c *gin.Context) {
	var rows []store.Node
	s.st.DB.Order("sort_order").Find(&rows)
	now := time.Now()
	out := make([]Usage, 0, len(rows))
	for _, r := range rows {
		start, end := Cycle(now, r.TrafficResetDay)
		out = append(out, s.usage(r, start, end))
	}
	api.OK(c, out)
}

type dayRow struct {
	Day int    `json:"day"`
	Rx  uint64 `json:"rx"`
	Tx  uint64 `json:"tx"`
}

type ifaceRow struct {
	Iface string `json:"iface"`
	Rx    uint64 `json:"rx"`
	Tx    uint64 `json:"tx"`
}

// detail 返回某个计费周期（cycle=0 为本期，1 为上期……）的每日用量、各网卡合计，以及最近 12 期的总量。
func (s *Service) detail(c *gin.Context) {
	var row store.Node
	if err := s.st.DB.First(&row, "id = ?", c.Param("node")).Error; err != nil {
		api.Fail(c, api.NotFound("节点不存在"))
		return
	}
	var q struct {
		Cycle int `form:"cycle"`
	}
	c.ShouldBindQuery(&q)
	if q.Cycle < 0 || q.Cycle > 12 {
		q.Cycle = 0
	}
	start, end := Cycle(time.Now(), row.TrafficResetDay)
	start, end = start.AddDate(0, -q.Cycle, 0), end.AddDate(0, -q.Cycle, 0)
	u := s.usage(row, start, end)

	var raw []store.TrafficDaily
	s.st.DB.Where("node_id = ? AND day >= ? AND day <= ?", row.ID, u.Start, u.End).Find(&raw)
	days := map[int]*dayRow{}
	ifaces := map[string]*ifaceRow{}
	for _, r := range raw {
		d := days[r.Day]
		if d == nil {
			d = &dayRow{Day: r.Day}
			days[r.Day] = d
		}
		d.Rx += r.Rx
		d.Tx += r.Tx
		f := ifaces[r.Iface]
		if f == nil {
			f = &ifaceRow{Iface: r.Iface}
			ifaces[r.Iface] = f
		}
		f.Rx += r.Rx
		f.Tx += r.Tx
	}
	// 周期内每天都给出一行，没有数据的为 0
	dayList := []dayRow{}
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		k := agent.DayOf(d)
		if r := days[k]; r != nil {
			dayList = append(dayList, *r)
		} else {
			dayList = append(dayList, dayRow{Day: k})
		}
	}
	ifList := []ifaceRow{}
	for _, f := range ifaces {
		ifList = append(ifList, *f)
	}
	sort.Slice(ifList, func(i, j int) bool { return ifList[i].Rx+ifList[i].Tx > ifList[j].Rx+ifList[j].Tx })

	cs, ce := Cycle(time.Now(), row.TrafficResetDay)
	cycles := []Usage{}
	for i := 0; i < 12; i++ {
		cycles = append(cycles, s.usage(row, cs.AddDate(0, -i, 0), ce.AddDate(0, -i, 0)))
	}
	api.OK(c, gin.H{"usage": u, "days": dayList, "ifaces": ifList, "cycles": cycles})
}

func (s *Service) settings(c *gin.Context) {
	var row store.Node
	if err := s.st.DB.First(&row, "id = ?", c.Param("node")).Error; err != nil {
		api.Fail(c, api.NotFound("节点不存在"))
		return
	}
	var req struct {
		ResetDay  int    `json:"resetDay"`
		Quota     uint64 `json:"quota"`
		QuotaMode string `json:"quotaMode"`
	}
	if !api.Bind(c, &req) {
		return
	}
	if req.ResetDay < 1 || req.ResetDay > 28 {
		api.Fail(c, api.BadRequest("重置日只能是 1 到 28"))
		return
	}
	if req.QuotaMode == "" {
		req.QuotaMode = "sum"
	}
	if req.QuotaMode != "sum" && req.QuotaMode != "rx" && req.QuotaMode != "tx" {
		api.Fail(c, api.BadRequest("额度计算方式无效"))
		return
	}
	api.Target(c, row.Name)
	s.st.DB.Model(&row).Updates(map[string]any{
		"traffic_reset_day": req.ResetDay, "traffic_quota": req.Quota, "traffic_quota_mode": req.QuotaMode,
	})
	api.OK(c, nil)
}
