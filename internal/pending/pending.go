// Package pending 实现“修改后限时确认，否则自动回滚”（防火墙、SSH 设置、面板访问方式）。
//
// 修改前把要改动的文件原样保存为快照并写入磁盘：到时未确认、用户选择撤销，
// 或者面板在等待期间重启，都会把文件还原并调用模块的恢复函数（重新加载服务）。
package pending

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// File 是一个文件修改前的状态；Missing 表示原本不存在（还原时删除）。
type File struct {
	Path    string      `json:"path"`
	Data    []byte      `json:"data,omitempty"`
	Mode    os.FileMode `json:"mode,omitempty"`
	Missing bool        `json:"missing,omitempty"`
}

// Snap 读取文件的当前状态。
func Snap(paths ...string) ([]File, error) {
	out := make([]File, 0, len(paths))
	for _, p := range paths {
		fi, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			out = append(out, File{Path: p, Missing: true})
			continue
		}
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: p, Data: b, Mode: fi.Mode().Perm()})
	}
	return out, nil
}

// Restore 把文件写回快照中的状态。
func Restore(files []File) error {
	var first error
	for _, f := range files {
		var err error
		if f.Missing {
			err = os.Remove(f.Path)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		} else {
			err = WriteFile(f.Path, f.Data, f.Mode)
		}
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// WriteFile 先写临时文件再改名，避免写到一半的配置被读取。
func WriteFile(path string, data []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".sshdesktop-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	os.Chmod(tmp, mode)
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Change 是一次等待确认的修改。
type Change struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	Detail   []string  `json:"detail"`
	UserID   uint      `json:"userId"`
	Username string    `json:"username"`
	Started  time.Time `json:"started"`
	Deadline time.Time `json:"deadline"`
	Files    []File    `json:"-"` // 不在接口中返回（可能含配置文件内容），保存到磁盘时单独写入
	// Extra 是模块自己的恢复信息（例如 SSH 改端口的阶段）
	Extra json.RawMessage `json:"extra,omitempty"`

	timer *time.Timer
}

// Handler 是模块提供的回调：Restore 在还原文件后执行（重新加载服务）；Commit 在确认后执行。
type Handler struct {
	Restore func(c *Change) error
	Commit  func(c *Change) error
	// Done 在每次结束时调用：result 为 confirmed / reverted / timeout / recovered，err 为回滚或提交失败的原因
	Done func(c *Change, result string, err error)
}

type Manager struct {
	dir string

	mu       sync.Mutex
	changes  map[string]*Change
	handlers map[string]Handler
}

var (
	ErrBusy     = errors.New("还有一项修改正在等待确认，请先确认或撤销")
	ErrNotFound = errors.New("没有等待确认的修改，可能已超时自动回滚")
)

// New 创建管理器，快照保存在 dir 下。
func New(dir string) *Manager {
	return &Manager{dir: dir, changes: map[string]*Change{}, handlers: map[string]Handler{}}
}

func (m *Manager) file(kind string) string { return filepath.Join(m.dir, kind+".json") }

// Handle 注册模块的回调，并还原面板上次退出时还没有确认的修改。
func (m *Manager) Handle(kind string, h Handler) {
	m.mu.Lock()
	m.handlers[kind] = h
	m.mu.Unlock()
	b, err := os.ReadFile(m.file(kind))
	if err != nil {
		return
	}
	var disk onDisk
	if json.Unmarshal(b, &disk) != nil {
		os.Remove(m.file(kind))
		return
	}
	c := disk.Change
	c.Files = disk.Files
	slog.Warn("面板重启前有未确认的修改，已自动回滚", "kind", kind, "title", c.Title)
	err = m.restore(&c, h)
	os.Remove(m.file(kind))
	if h.Done != nil {
		h.Done(&c, "recovered", err)
	}
}

// Start 开始一次等待确认的修改。调用前文件还没改；files 是修改前的快照，apply 执行修改。
// apply 失败时立即还原并返回错误。
func (m *Manager) Start(c *Change, timeout time.Duration, apply func() error) (*Change, error) {
	m.mu.Lock()
	if _, ok := m.changes[c.Kind]; ok {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	h := m.handlers[c.Kind]
	b := make([]byte, 8)
	rand.Read(b)
	c.ID = hex.EncodeToString(b)
	c.Started = time.Now()
	c.Deadline = c.Started.Add(timeout)
	if err := m.persist(c); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.changes[c.Kind] = c
	m.mu.Unlock()

	if err := apply(); err != nil {
		rerr := m.restore(c, h)
		m.drop(c)
		if rerr != nil {
			return nil, errors.Join(err, errors.New("还原失败："+rerr.Error()))
		}
		return nil, err
	}
	m.mu.Lock()
	c.timer = time.AfterFunc(timeout, func() { m.finish(c.Kind, c.ID, "timeout") })
	m.mu.Unlock()
	return c, nil
}

func (m *Manager) persist(c *Change) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(onDisk{Change: *c, Files: c.Files})
	if err != nil {
		return err
	}
	return WriteFile(m.file(c.Kind), b, 0o600)
}

// onDisk 是保存到磁盘的格式：Change 加上快照文件。
type onDisk struct {
	Change
	Files []File `json:"files"`
}

// Update 在等待期间更新恢复信息（例如追加快照文件）。
func (m *Manager) Update(c *Change) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.persist(c)
}

func (m *Manager) drop(c *Change) {
	m.mu.Lock()
	if cur, ok := m.changes[c.Kind]; ok && cur.ID == c.ID {
		delete(m.changes, c.Kind)
	}
	m.mu.Unlock()
	os.Remove(m.file(c.Kind))
}

func (m *Manager) restore(c *Change, h Handler) error {
	err := Restore(c.Files)
	if h.Restore != nil {
		if rerr := h.Restore(c); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}
	return err
}

// Get 返回某类正在等待确认的修改。
func (m *Manager) Get(kind string) *Change {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.changes[kind]
}

// Confirm 保留修改；Revert 立即撤销。
func (m *Manager) Confirm(kind, id string) error { return m.finish(kind, id, "confirmed") }
func (m *Manager) Revert(kind, id string) error  { return m.finish(kind, id, "reverted") }

func (m *Manager) finish(kind, id, result string) error {
	m.mu.Lock()
	c, ok := m.changes[kind]
	if !ok || (id != "" && c.ID != id) {
		m.mu.Unlock()
		return ErrNotFound
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	delete(m.changes, kind)
	h := m.handlers[kind]
	m.mu.Unlock()

	var err error
	if result == "confirmed" {
		if h.Commit != nil {
			err = h.Commit(c)
		}
	} else {
		err = m.restore(c, h)
		if err != nil {
			slog.Error("回滚失败", "kind", kind, "title", c.Title, "err", err)
		} else {
			slog.Warn("修改未确认，已回滚", "kind", kind, "title", c.Title, "result", result)
		}
	}
	os.Remove(m.file(kind))
	if h.Done != nil {
		h.Done(c, result, err)
	}
	return err
}
