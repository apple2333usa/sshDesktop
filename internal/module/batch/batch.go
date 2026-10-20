// Package batch 是多服务器批量命令：选择多个节点并发执行同一段命令（默认并发 5，可设超时），
// 按节点实时推送输出和退出码；执行记录保存在数据库并进入审计日志。另提供共享的常用命令片段。
package batch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/realtime"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

const (
	maxOutput   = 1 << 20 // 每个节点保存的输出上限
	keepDays    = 90
	maxCommand  = 64 << 10
	defTimeout  = 60
	maxTimeout  = 3600
	defParallel = 5
)

type Service struct {
	st    *store.Store
	nodes *node.Registry
	hub   *realtime.Hub
	audit func(module, action string) gin.HandlerFunc

	mu      sync.Mutex
	running map[string]context.CancelFunc
	// live 是执行中各节点的输出（运行 ID → 节点 ID），查询时用它补上还没写入数据库的输出
	live map[string]map[string]*output
}

func New(st *store.Store, nodes *node.Registry, hub *realtime.Hub, audit func(string, string) gin.HandlerFunc) *Service {
	// 上次面板退出时仍在运行的记录标记为中断
	st.DB.Model(&store.BatchResult{}).Where("status = ?", "running").
		Updates(map[string]any{"status": "failed", "error": "面板重启，执行中断"})
	return &Service{st: st, nodes: nodes, hub: hub, audit: audit, running: map[string]context.CancelFunc{}, live: map[string]map[string]*output{}}
}

func (s *Service) Register(r *gin.RouterGroup, run gin.HandlerFunc) {
	a := s.audit
	r.GET("/batch/runs", run, s.list)
	r.GET("/batch/runs/:id", run, s.get)
	r.POST("/batch/runs", run, a("batch", "run"), s.start)
	r.POST("/batch/runs/:id/cancel", run, a("batch", "cancel"), s.cancel)
	r.GET("/batch/snippets", run, s.snippets)
	r.POST("/batch/snippets", run, a("batch", "snippet-create"), s.saveSnippet)
	r.PUT("/batch/snippets/:id", run, a("batch", "snippet-update"), s.saveSnippet)
	r.DELETE("/batch/snippets/:id", run, a("batch", "snippet-delete"), s.deleteSnippet)
}

// Event 是推送到 batch:<runId> 频道的消息。输出片段带序号 Seq（每个节点从 1 递增），
// 客户端用它和查询结果中的 seq 对齐：订阅之前已推送的片段从查询结果里取，重复的丢弃，缺失时重新查询。
type Event struct {
	RunID      string `json:"runId"`
	NodeID     string `json:"nodeId"`
	Status     string `json:"status,omitempty"`
	Output     string `json:"output,omitempty"` // 新增的输出片段
	Seq        int    `json:"seq,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	Finished   bool   `json:"finished,omitempty"` // 整次执行结束
}

type runView struct {
	store.BatchRun
	Results []store.BatchResult `json:"results"`
	Running bool                `json:"running"`
}

func (s *Service) isRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.running[id]
	return ok
}

func (s *Service) list(c *gin.Context) {
	var runs []store.BatchRun
	s.st.DB.Order("created_at desc").Limit(50).Find(&runs)
	out := make([]runView, 0, len(runs))
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	var results []store.BatchResult
	if len(ids) > 0 {
		s.st.DB.Select("run_id", "node_id", "status", "exit_code", "error", "duration_ms").Where("run_id IN ?", ids).Find(&results)
	}
	byRun := map[string][]store.BatchResult{}
	for _, r := range results {
		byRun[r.RunID] = append(byRun[r.RunID], r)
	}
	for _, r := range runs {
		out = append(out, runView{BatchRun: r, Results: byRun[r.ID], Running: s.isRunning(r.ID)})
	}
	api.OK(c, out)
}

func (s *Service) get(c *gin.Context) {
	var run store.BatchRun
	if err := s.st.DB.First(&run, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("执行记录不存在"))
		return
	}
	var results []store.BatchResult
	s.st.DB.Where("run_id = ?", run.ID).Find(&results)
	s.mu.Lock()
	live := s.live[run.ID]
	s.mu.Unlock()
	for i := range results {
		if o := live[results[i].NodeID]; o != nil {
			results[i].Output, results[i].Seq = o.snapshot()
		}
	}
	api.OK(c, runView{BatchRun: run, Results: results, Running: s.isRunning(run.ID)})
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) start(c *gin.Context) {
	var req struct {
		Nodes       []string `json:"nodes"`
		Command     string   `json:"command"`
		Timeout     int      `json:"timeout"`
		Concurrency int      `json:"concurrency"`
	}
	if !api.Bind(c, &req) {
		return
	}
	cmd := strings.TrimSpace(req.Command)
	if cmd == "" || len(cmd) > maxCommand {
		api.Fail(c, api.BadRequest("命令不能为空，也不能超过 64 KB"))
		return
	}
	seen := map[string]bool{}
	var ids []string
	for _, id := range req.Nodes {
		if seen[id] {
			continue
		}
		if _, err := s.nodes.Get(id); err != nil {
			api.Fail(c, api.BadRequest("节点不存在："+id))
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		api.Fail(c, api.BadRequest("请至少选择一个节点"))
		return
	}
	if req.Timeout <= 0 {
		req.Timeout = defTimeout
	}
	req.Timeout = min(req.Timeout, maxTimeout)
	if req.Concurrency <= 0 {
		req.Concurrency = defParallel
	}
	req.Concurrency = min(req.Concurrency, 20)
	u := api.User(c)
	run := store.BatchRun{
		ID: newID(), UserID: u.UserID, Username: u.Username, Command: req.Command,
		Nodes: strings.Join(ids, ","), Timeout: req.Timeout, CreatedAt: time.Now(),
	}
	api.Target(c, fmt.Sprintf("%s 在 %s 执行：%s", run.ID, run.Nodes, cmd))
	results := make([]store.BatchResult, len(ids))
	for i, id := range ids {
		results[i] = store.BatchResult{RunID: run.ID, NodeID: id, Status: "pending"}
	}
	if err := s.st.DB.Create(&run).Error; err != nil {
		api.Fail(c, err)
		return
	}
	s.st.DB.Create(&results)
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.running[run.ID] = cancel
	s.live[run.ID] = map[string]*output{}
	s.mu.Unlock()
	go s.execute(ctx, cancel, run, ids, req.Concurrency)
	api.OK(c, runView{BatchRun: run, Results: results, Running: true})
}

func (s *Service) publish(e Event) { s.hub.Publish("batch:"+e.RunID, e) }

func (s *Service) execute(ctx context.Context, cancel context.CancelFunc, run store.BatchRun, ids []string, parallel int) {
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.running, run.ID)
		delete(s.live, run.ID)
		s.mu.Unlock()
		now := time.Now()
		s.st.DB.Model(&store.BatchRun{}).Where("id = ?", run.ID).Update("finished_at", &now)
		s.publish(Event{RunID: run.ID, Finished: true})
	}()
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				s.finishNode(run.ID, id, "canceled", -1, "已取消", 0, "")
				return
			}
			defer func() { <-sem }()
			s.runNode(ctx, run, id)
		}(id)
	}
	wg.Wait()
}

// output 收集一个节点的输出：保存前 1 MB，并每 200 ms 推送一次新增部分。
type output struct {
	mu   sync.Mutex
	all  []byte
	sent int // 已推送的字节数
	seq  int // 已推送的片段数
	cut  bool
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if room := maxOutput - len(o.all); room > 0 {
		chunk := p
		if len(chunk) > room {
			chunk, o.cut = chunk[:room], true
		}
		o.all = append(o.all, chunk...)
	} else {
		o.cut = true
	}
	return len(p), nil
}

// take 取出还没推送的部分和它的序号。
func (o *output) take() (string, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.sent == len(o.all) {
		return "", o.seq
	}
	s := string(o.all[o.sent:])
	o.sent = len(o.all)
	o.seq++
	return s, o.seq
}

// snapshot 返回已推送的输出和片段数。
func (o *output) snapshot() (string, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.all[:o.sent]), o.seq
}

func (o *output) text() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := string(o.all)
	if o.cut {
		s += "\n…（输出超过 1 MB，已截断）"
	}
	return s
}

func (s *Service) runNode(ctx context.Context, run store.BatchRun, id string) {
	start := time.Now()
	s.st.DB.Model(&store.BatchResult{}).Where("run_id = ? AND node_id = ?", run.ID, id).Update("status", "running")
	s.publish(Event{RunID: run.ID, NodeID: id, Status: "running"})
	n, err := s.nodes.Get(id)
	if err != nil {
		s.finishNode(run.ID, id, "failed", -1, err.Error(), 0, "")
		return
	}
	out := &output{}
	s.mu.Lock()
	if m := s.live[run.ID]; m != nil {
		m[id] = out
	}
	s.mu.Unlock()
	tctx, cancel := context.WithTimeout(ctx, time.Duration(run.Timeout)*time.Second)
	defer cancel()
	stopPush := make(chan struct{})
	pushed := make(chan struct{})
	go func() {
		defer close(pushed)
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stopPush:
				return
			case <-t.C:
				if chunk, seq := out.take(); chunk != "" {
					s.publish(Event{RunID: run.ID, NodeID: id, Output: chunk, Seq: seq})
				}
			}
		}
	}()
	code, err := n.RunScript(tctx, run.Command, out)
	close(stopPush)
	<-pushed
	if chunk, seq := out.take(); chunk != "" {
		s.publish(Event{RunID: run.ID, NodeID: id, Output: chunk, Seq: seq})
	}
	dur := time.Since(start).Milliseconds()
	switch {
	case errors.Is(tctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		s.finishNode(run.ID, id, "timeout", -1, fmt.Sprintf("超过 %d 秒未结束，已终止", run.Timeout), dur, out.text())
	case ctx.Err() != nil:
		s.finishNode(run.ID, id, "canceled", -1, "已取消", dur, out.text())
	case err != nil:
		s.finishNode(run.ID, id, "failed", -1, err.Error(), dur, out.text())
	default:
		s.finishNode(run.ID, id, "done", code, "", dur, out.text())
	}
}

func (s *Service) finishNode(runID, id, status string, code int, msg string, dur int64, text string) {
	s.st.DB.Model(&store.BatchResult{}).Where("run_id = ? AND node_id = ?", runID, id).Updates(map[string]any{
		"status": status, "exit_code": code, "error": msg, "duration_ms": dur, "output": text,
	})
	s.publish(Event{RunID: runID, NodeID: id, Status: status, ExitCode: &code, Error: msg, DurationMs: dur})
}

func (s *Service) cancel(c *gin.Context) {
	id := c.Param("id")
	api.Target(c, id)
	s.mu.Lock()
	cancel := s.running[id]
	s.mu.Unlock()
	if cancel == nil {
		api.Fail(c, api.BadRequest("该批量命令已结束"))
		return
	}
	cancel()
	api.OK(c, nil)
}

// Cleanup 删除超过 90 天的执行记录。
func (s *Service) Cleanup() {
	cut := time.Now().AddDate(0, 0, -keepDays)
	var ids []string
	s.st.DB.Model(&store.BatchRun{}).Where("created_at < ?", cut).Pluck("id", &ids)
	if len(ids) > 0 {
		s.st.DB.Where("run_id IN ?", ids).Delete(&store.BatchResult{})
		s.st.DB.Where("id IN ?", ids).Delete(&store.BatchRun{})
	}
}

/* ---------- 命令片段 ---------- */

func (s *Service) snippets(c *gin.Context) {
	var list []store.Snippet
	s.st.DB.Order("name").Find(&list)
	api.OK(c, list)
}

func (s *Service) saveSnippet(c *gin.Context) {
	var req struct {
		Name    string `json:"name"`
		Command string `json:"command"`
	}
	if !api.Bind(c, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 64 || strings.TrimSpace(req.Command) == "" || len(req.Command) > maxCommand {
		api.Fail(c, api.BadRequest("名称和命令不能为空"))
		return
	}
	api.Target(c, req.Name)
	sn := store.Snippet{Name: req.Name, Command: req.Command}
	if id := c.Param("id"); id != "" {
		if err := s.st.DB.First(&sn, "id = ?", id).Error; err != nil {
			api.Fail(c, api.NotFound("片段不存在"))
			return
		}
		sn.Name, sn.Command = req.Name, req.Command
	}
	if err := s.st.DB.Save(&sn).Error; err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, sn)
}

func (s *Service) deleteSnippet(c *gin.Context) {
	api.Target(c, c.Param("id"))
	s.st.DB.Delete(&store.Snippet{}, "id = ?", c.Param("id"))
	api.OK(c, nil)
}
