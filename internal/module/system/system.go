// Package system 提供主机信息、电源操作、面板设置、审计日志查询和后台任务查询。
package system

import (
	"encoding/csv"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/config"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Deps struct {
	Store     *store.Store
	Config    *config.Config
	Collector *agent.Collector
	Tasks     *task.Manager
	Version   string
	Confirm   func(c *gin.Context) bool
	Audit     func(module, action string) gin.HandlerFunc
	IsAdmin   func(c *gin.Context) bool
}

type Service struct {
	d       Deps
	release releaseCache
}

func New(d Deps) *Service { return &Service{d: d} }

type Guards struct {
	Login, Power, AuditView, Settings gin.HandlerFunc
}

func (s *Service) Register(r *gin.RouterGroup, g Guards) {
	r.GET("/system/info", g.Login, s.info)
	r.POST("/system/power/:action", g.Power, s.d.Audit("system", "power"), s.power)
	r.GET("/settings", g.Login, s.getSettings)
	r.PUT("/settings", g.Settings, s.d.Audit("settings", "update"), s.putSettings)
	r.GET("/system/upgrade", g.Settings, s.upgradeStatus)
	r.POST("/system/upgrade", g.Settings, s.d.Audit("settings", "upgrade"), s.upgradeStart)
	r.GET("/audit", g.AuditView, s.auditList)
	r.GET("/audit/export", g.AuditView, s.auditExport)
	r.GET("/tasks", g.Login, s.tasks)
	r.GET("/tasks/:id", g.Login, s.taskDetail)
	r.POST("/tasks/:id/cancel", g.Login, s.taskCancel)
}

func (s *Service) info(c *gin.Context) {
	var addrs []string
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			if i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagUp == 0 {
				continue
			}
			as, _ := i.Addrs()
			for _, a := range as {
				if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
					addrs = append(addrs, ipn.IP.String())
				}
			}
		}
	}
	latest, _ := s.d.Collector.Latest()
	api.OK(c, gin.H{
		"host":    s.d.Collector.Info(),
		"uptime":  latest.Uptime,
		"addrs":   addrs,
		"version": s.d.Version,
		"panel": gin.H{
			"port": s.d.Config.Port, "domain": s.d.Config.Domain, "https": s.d.Config.HTTPS.Enabled,
			"listen": s.d.Config.ListenAddr(), "dataDir": s.d.Config.DataDir,
		},
		"time": time.Now().UnixMilli(),
	})
}

// power 重启或关机（二级危险操作）。响应返回后再执行，保证浏览器能收到结果。
func (s *Service) power(c *gin.Context) {
	action := c.Param("action")
	if action != "reboot" && action != "poweroff" {
		api.Fail(c, api.BadRequest("不支持的操作"))
		return
	}
	api.Target(c, action)
	if !s.d.Confirm(c) {
		return
	}
	api.OK(c, nil)
	go func() {
		time.Sleep(time.Second)
		exec.Command("systemctl", action).Run()
	}()
}

func (s *Service) getSettings(c *gin.Context) {
	rt := s.d.Store.Runtime()
	if !s.d.IsAdmin(c) {
		// 非管理员只需要知道默认外观
		api.OK(c, gin.H{"defaultAppearance": rt.DefaultAppearance})
		return
	}
	api.OK(c, rt)
}

func (s *Service) putSettings(c *gin.Context) {
	var rt store.Runtime
	if !api.Bind(c, &rt) {
		return
	}
	if err := s.d.Store.SetRuntime(rt); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, s.d.Store.Runtime())
}

func (s *Service) auditQuery(c *gin.Context) *gorm.DB {
	q := s.d.Store.DB.Model(&store.AuditLog{})
	if v := c.Query("user"); v != "" {
		q = q.Where("username = ?", v)
	}
	if v := c.Query("module"); v != "" {
		q = q.Where("module = ?", v)
	}
	if v := c.Query("result"); v == "ok" {
		q = q.Where("success = ?", true)
	} else if v == "fail" {
		q = q.Where("success = ?", false)
	}
	if v, err := strconv.ParseInt(c.Query("from"), 10, 64); err == nil && v > 0 {
		q = q.Where("at >= ?", time.UnixMilli(v))
	}
	if v, err := strconv.ParseInt(c.Query("to"), 10, 64); err == nil && v > 0 {
		q = q.Where("at <= ?", time.UnixMilli(v))
	}
	if v := strings.TrimSpace(c.Query("q")); v != "" {
		like := "%" + v + "%"
		q = q.Where("target LIKE ? OR action LIKE ? OR ip LIKE ? OR detail LIKE ?", like, like, like, like)
	}
	return q
}

func (s *Service) auditList(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 50
	}
	var total int64
	s.auditQuery(c).Count(&total)
	var list []store.AuditLog
	s.auditQuery(c).Order("id desc").Offset((page - 1) * size).Limit(size).Find(&list)
	var modules []string
	s.d.Store.DB.Model(&store.AuditLog{}).Distinct("module").Pluck("module", &modules)
	api.OK(c, gin.H{"total": total, "items": list, "modules": modules})
}

func (s *Service) auditExport(c *gin.Context) {
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-%s.csv"`, time.Now().Format("20060102-150405")))
	c.Writer.Write([]byte("\xEF\xBB\xBF")) // BOM，方便 Excel 识别 UTF-8
	w := csv.NewWriter(c.Writer)
	w.Write([]string{"时间", "用户", "来源 IP", "节点", "模块", "操作", "对象", "结果", "详情", "耗时(ms)"})
	const batch = 1000
	var lastID uint = ^uint(0) >> 1
	for {
		var list []store.AuditLog
		s.auditQuery(c).Where("id < ?", lastID).Order("id desc").Limit(batch).Find(&list)
		for _, e := range list {
			result := "成功"
			if !e.Success {
				result = "失败"
			}
			w.Write([]string{e.At.Local().Format("2006-01-02 15:04:05"), e.Username, e.IP, e.Node, e.Module, e.Action,
				e.Target, result, e.Detail, strconv.FormatInt(e.DurationMs, 10)})
			lastID = e.ID
		}
		w.Flush()
		if len(list) < batch {
			return
		}
	}
}

func (s *Service) tasks(c *gin.Context) {
	api.OK(c, s.d.Tasks.Recent(api.User(c).UserID, 50))
}

func (s *Service) ownTask(c *gin.Context) (*store.Task, []store.TaskLog, bool) {
	rec, logs, err := s.d.Tasks.Get(c.Param("id"))
	if err != nil || (rec.UserID != api.User(c).UserID && !s.d.IsAdmin(c)) {
		api.Fail(c, api.NotFound("任务不存在"))
		return nil, nil, false
	}
	return rec, logs, true
}

func (s *Service) taskDetail(c *gin.Context) {
	rec, logs, ok := s.ownTask(c)
	if !ok {
		return
	}
	api.OK(c, gin.H{"task": rec, "logs": logs})
}

func (s *Service) taskCancel(c *gin.Context) {
	rec, _, ok := s.ownTask(c)
	if !ok {
		return
	}
	if !s.d.Tasks.Cancel(rec.ID) {
		api.Fail(c, api.BadRequest("任务已结束"))
		return
	}
	api.OK(c, nil)
}
