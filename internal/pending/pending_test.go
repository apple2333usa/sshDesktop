package pending

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu      sync.Mutex
	results []string
	restore int
	commit  int
}

func (r *recorder) handler() Handler {
	return Handler{
		Restore: func(*Change) error { r.mu.Lock(); r.restore++; r.mu.Unlock(); return nil },
		Commit:  func(*Change) error { r.mu.Lock(); r.commit++; r.mu.Unlock(); return nil },
		Done: func(_ *Change, result string, _ error) {
			r.mu.Lock()
			r.results = append(r.results, result)
			r.mu.Unlock()
		},
	}
}

func (r *recorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.results) == 0 {
		return ""
	}
	return r.results[len(r.results)-1]
}

func setup(t *testing.T) (dir, conf, missing string) {
	dir = t.TempDir()
	conf = filepath.Join(dir, "a.conf")
	missing = filepath.Join(dir, "new.conf")
	if err := os.WriteFile(conf, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	return
}

func start(t *testing.T, m *Manager, conf, missing string, timeout time.Duration) *Change {
	files, err := Snap(conf, missing)
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Start(&Change{Kind: "x", Title: "测试", Files: files}, timeout, func() error {
		if err := WriteFile(conf, []byte("new"), 0o640); err != nil {
			return err
		}
		return WriteFile(missing, []byte("created"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "<missing>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConfirmAndRevert(t *testing.T) {
	dir, conf, missing := setup(t)
	m := New(filepath.Join(dir, "pending"))
	rec := &recorder{}
	m.Handle("x", rec.handler())

	c := start(t, m, conf, missing, time.Minute)
	if m.Get("x") == nil || read(t, conf) != "new" {
		t.Fatal("修改没有生效")
	}
	if _, err := m.Start(&Change{Kind: "x"}, time.Minute, func() error { return nil }); err != ErrBusy {
		t.Fatalf("同类修改进行中应返回 ErrBusy，得到 %v", err)
	}
	if err := m.Confirm("x", "wrong"); err != ErrNotFound {
		t.Fatalf("错误的 id 应返回 ErrNotFound，得到 %v", err)
	}
	if err := m.Confirm("x", c.ID); err != nil {
		t.Fatal(err)
	}
	if read(t, conf) != "new" || rec.commit != 1 || rec.last() != "confirmed" || m.Get("x") != nil {
		t.Fatalf("确认后状态不对：%+v", rec)
	}
	if _, err := os.Stat(filepath.Join(dir, "pending", "x.json")); !os.IsNotExist(err) {
		t.Fatal("确认后应删除磁盘上的记录")
	}

	c = start(t, m, conf, missing, time.Minute)
	if err := m.Revert("x", c.ID); err != nil {
		t.Fatal(err)
	}
	if read(t, conf) != "new" || read(t, missing) != "created" {
		// 第二次修改前的快照就是第一次修改后的内容
		t.Fatalf("撤销后内容不对：%q %q", read(t, conf), read(t, missing))
	}
	if rec.restore != 1 || rec.last() != "reverted" {
		t.Fatalf("%+v", rec)
	}
}

func TestTimeoutRestoresFiles(t *testing.T) {
	dir, conf, missing := setup(t)
	m := New(filepath.Join(dir, "pending"))
	rec := &recorder{}
	m.Handle("x", rec.handler())
	start(t, m, conf, missing, 50*time.Millisecond)
	deadline := time.Now().Add(3 * time.Second)
	for rec.last() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if rec.last() != "timeout" {
		t.Fatalf("应超时回滚：%+v", rec)
	}
	if read(t, conf) != "old" || read(t, missing) != "<missing>" {
		t.Fatalf("超时后应还原：%q %q", read(t, conf), read(t, missing))
	}
	fi, _ := os.Stat(conf)
	if fi.Mode().Perm() != 0o640 {
		t.Fatalf("权限应还原为 0640，得到 %v", fi.Mode())
	}
}

func TestRecoverAfterRestart(t *testing.T) {
	dir, conf, missing := setup(t)
	m := New(filepath.Join(dir, "pending"))
	m.Handle("x", Handler{})
	start(t, m, conf, missing, time.Hour)

	// 模拟面板重启：新的 Manager 注册处理函数时回滚遗留的修改
	m2 := New(filepath.Join(dir, "pending"))
	rec := &recorder{}
	m2.Handle("x", rec.handler())
	if rec.last() != "recovered" || rec.restore != 1 {
		t.Fatalf("%+v", rec)
	}
	if read(t, conf) != "old" || read(t, missing) != "<missing>" {
		t.Fatal("重启后应还原")
	}
	if m2.Get("x") != nil {
		t.Fatal("回滚后不应再有待确认的修改")
	}
}

func TestApplyErrorRestores(t *testing.T) {
	dir, conf, _ := setup(t)
	m := New(filepath.Join(dir, "pending"))
	rec := &recorder{}
	m.Handle("x", rec.handler())
	files, _ := Snap(conf)
	_, err := m.Start(&Change{Kind: "x", Files: files}, time.Minute, func() error {
		os.WriteFile(conf, []byte("broken"), 0o640)
		return os.ErrInvalid
	})
	if err == nil {
		t.Fatal("apply 失败应返回错误")
	}
	if read(t, conf) != "old" || m.Get("x") != nil || rec.restore != 1 {
		t.Fatalf("apply 失败应立即还原：%q %+v", read(t, conf), rec)
	}
}
