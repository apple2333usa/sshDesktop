// Package task 是后台任务系统：耗时操作立即返回任务 ID，进度和日志通过 WebSocket 推送。
package task

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/realtime"
	"github.com/apple2333usa/sshdesktop/internal/store"
)

const keepDays = 30

type Manager struct {
	st  *store.Store
	hub *realtime.Hub

	mu      sync.Mutex
	running map[string]*Task
}

// Task 是运行中的任务句柄，任务函数通过它汇报进度和日志。
type Task struct {
	m      *Manager
	rec    store.Task
	cancel context.CancelFunc
	mu     sync.Mutex
	lastPush time.Time
}

type Event struct {
	Task store.Task `json:"task"`
	Log  string     `json:"log,omitempty"`
}

func NewManager(st *store.Store, hub *realtime.Hub) *Manager {
	m := &Manager{st: st, hub: hub, running: map[string]*Task{}}
	// 上次进程退出时仍在运行的任务标记为失败
	st.DB.Model(&store.Task{}).Where("status = ?", "running").
		Updates(map[string]any{"status": "failed", "error": "面板重启，任务中断"})
	return m
}

var ErrCanceled = errors.New("任务已取消")

// Start 创建并在后台运行任务。
func (m *Manager) Start(kind, title string, userID uint, node string, fn func(ctx context.Context, t *Task) error) *store.Task {
	b := make([]byte, 8)
	rand.Read(b)
	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{m: m, cancel: cancel, rec: store.Task{
		ID: hex.EncodeToString(b), Kind: kind, Title: title, UserID: userID, Node: node,
		Status: "running", CreatedAt: time.Now(),
	}}
	m.st.DB.Create(&t.rec)
	m.mu.Lock()
	m.running[t.rec.ID] = t
	m.mu.Unlock()
	rec := t.rec
	go func() {
		defer cancel()
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("任务异常：%v", r)
					slog.Error("任务 panic", "task", t.rec.ID, "panic", r)
				}
			}()
			err = fn(ctx, t)
		}()
		t.finish(ctx, err)
		m.mu.Lock()
		delete(m.running, t.rec.ID)
		m.mu.Unlock()
	}()
	return &rec
}

func (t *Task) ID() string { return t.rec.ID }

// Progress 设置进度（0~100），推送频率限制为每 300 ms 一次。
func (t *Task) Progress(p float64) {
	t.mu.Lock()
	t.rec.Progress = p
	push := time.Since(t.lastPush) > 300*time.Millisecond
	if push {
		t.lastPush = time.Now()
	}
	rec := t.rec
	t.mu.Unlock()
	if push {
		t.m.hub.Publish("task:"+rec.ID, Event{Task: rec})
		t.m.hub.PublishUser(rec.UserID, "tasks", Event{Task: rec})
	}
}

func (t *Task) Log(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	t.m.st.DB.Create(&store.TaskLog{TaskID: t.rec.ID, At: time.Now(), Line: line})
	t.mu.Lock()
	rec := t.rec
	t.mu.Unlock()
	t.m.hub.Publish("task:"+rec.ID, Event{Task: rec, Log: line})
}

func (t *Task) finish(ctx context.Context, err error) {
	now := time.Now()
	t.mu.Lock()
	t.rec.FinishedAt = &now
	switch {
	case err == nil:
		t.rec.Status, t.rec.Progress = "success", 100
	case ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, ErrCanceled):
		t.rec.Status, t.rec.Error = "canceled", "已取消"
	default:
		t.rec.Status, t.rec.Error = "failed", err.Error()
	}
	rec := t.rec
	t.mu.Unlock()
	t.m.st.DB.Save(&rec)
	t.m.hub.Publish("task:"+rec.ID, Event{Task: rec})
	t.m.hub.PublishUser(rec.UserID, "tasks", Event{Task: rec})
}

func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	t := m.running[id]
	m.mu.Unlock()
	if t == nil {
		return false
	}
	t.cancel()
	return true
}

func (m *Manager) Get(id string) (*store.Task, []store.TaskLog, error) {
	var rec store.Task
	if err := m.st.DB.First(&rec, "id = ?", id).Error; err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	if t := m.running[id]; t != nil {
		t.mu.Lock()
		rec = t.rec
		t.mu.Unlock()
	}
	m.mu.Unlock()
	var logs []store.TaskLog
	m.st.DB.Where("task_id = ?", id).Order("id").Limit(2000).Find(&logs)
	return &rec, logs, nil
}

// Recent 返回某账号最近的任务（userID 为 0 时返回全部）。
func (m *Manager) Recent(userID uint, limit int) []store.Task {
	var out []store.Task
	q := m.st.DB.Order("created_at desc").Limit(limit)
	if userID != 0 {
		q = q.Where("user_id = ?", userID)
	}
	q.Find(&out)
	m.mu.Lock()
	for i := range out {
		if t := m.running[out[i].ID]; t != nil {
			t.mu.Lock()
			out[i] = t.rec
			t.mu.Unlock()
		}
	}
	m.mu.Unlock()
	return out
}

func (m *Manager) Cleanup() {
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	var ids []string
	m.st.DB.Model(&store.Task{}).Where("created_at < ? AND status <> ?", cutoff, "running").Pluck("id", &ids)
	if len(ids) > 0 {
		m.st.DB.Where("task_id IN ?", ids).Delete(&store.TaskLog{})
		m.st.DB.Where("id IN ?", ids).Delete(&store.Task{})
	}
}
