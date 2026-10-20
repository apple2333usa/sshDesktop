// Package audit 记录操作审计日志。审计日志在面板中只能查询和导出，不能删除。
package audit

import (
	"log/slog"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

type Recorder struct{ st *store.Store }

func New(st *store.Store) *Recorder { return &Recorder{st: st} }

func (r *Recorder) Write(e store.AuditLog) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if err := r.st.DB.Create(&e).Error; err != nil {
		slog.Error("写入审计日志失败", "err", err)
	}
}

// FromContext 用请求上下文填充用户、IP、节点等字段。
func FromContext(c *gin.Context, module, action string) store.AuditLog {
	e := store.AuditLog{At: time.Now(), IP: api.IP(c), Module: module, Action: action, Node: api.Node(c)}
	if u := api.User(c); u != nil {
		e.UserID, e.Username = u.UserID, u.Username
	}
	if e.Node == "" {
		e.Node = c.Param("node")
	}
	return e
}

// Action 返回一个中间件：handler 执行完后记录审计日志，结果按响应状态判断。
func (r *Recorder) Action(module, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		e := FromContext(c, module, action)
		e.Target = api.AuditTarget(c)
		e.Success = c.Writer.Status() < 400
		e.Detail = api.AuditErr(c)
		e.DurationMs = time.Since(start).Milliseconds()
		r.Write(e)
	}
}

// Cleanup 按保留天数清理审计日志（只由后台定时任务调用）。
func (r *Recorder) Cleanup() {
	days := r.st.Runtime().AuditRetentionDays
	r.st.DB.Where("at < ?", time.Now().AddDate(0, 0, -days)).Delete(&store.AuditLog{})
}
