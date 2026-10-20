package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

func TestParseLine(t *testing.T) {
	l := parseLine("*/5 * * * *   root  /usr/bin/foo  --x  'a b'", true)
	if l.Kind != "job" || l.User != "root" || l.Schedule != "*/5 * * * *" || l.Command != "/usr/bin/foo  --x  'a b'" || !l.Enabled || l.Next == 0 {
		t.Fatalf("%+v", l)
	}
	l = parseLine(offMark+"@daily /bin/backup.sh", false)
	if l.Kind != "job" || l.Enabled || l.Schedule != "@daily" || l.Command != "/bin/backup.sh" || l.Next != 0 {
		t.Fatalf("%+v", l)
	}
	for raw, kind := range map[string]string{
		"":                   "blank",
		"  # comment":        "comment",
		"MAILTO=root":        "env",
		"SHELL = /bin/bash":  "env",
		"* * * *":            "other",
		"61 * * * * cmd":     "other",
		"* * * * * root":     "other", // 系统 crontab 缺少命令
		"@reboot root /x.sh": "job",
	} {
		if got := parseLine(raw, true).Kind; got != kind {
			t.Errorf("%q：%s，期望 %s", raw, got, kind)
		}
	}
}

func TestRender(t *testing.T) {
	out := Render([]store.CronJob{
		{ID: 1, Name: "a\nb", Schedule: "0 3 * * *", Enabled: true},
		{ID: 2, Name: "off", Schedule: "* * * * *", Enabled: false},
	}, "/usr/local/bin/sshdesktop", "/etc/sshdesktop/config file.yaml")
	if !strings.Contains(out, "# 1 a b\n0 3 * * * root /usr/local/bin/sshdesktop -c '/etc/sshdesktop/config file.yaml' cron run 1 >/dev/null 2>&1\n") {
		t.Fatal(out)
	}
	if strings.Contains(out, "cron run 2") {
		t.Fatal("停用的任务不应写入")
	}
}

type testEnv struct {
	s    *Service
	root string
	r    *gin.Engine
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root := filepath.Join(dir, "root")
	os.MkdirAll(filepath.Join(root, "etc", "cron.d"), 0o755)
	os.MkdirAll(filepath.Join(root, "var", "spool", "cron", "crontabs"), 0o755)
	s := New(Deps{
		Store: st, Bin: "/usr/local/bin/sshdesktop", Config: "/etc/sshdesktop/config.yaml",
		Runner: &Runner{Store: st, DataDir: dir},
		Audit:  func(string, string) gin.HandlerFunc { return func(*gin.Context) {} },
	})
	s.SetPaths(filepath.Join(root, "etc", "cron.d", "sshdesktop"), root)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	ok := func(*gin.Context) {}
	s.Register(r.Group(""), ok, ok)
	return &testEnv{s: s, root: root, r: r}
}

func (e *testEnv) call(t *testing.T, method, path string, body any) (int, json.RawMessage) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	var resp struct {
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != 200 {
		return w.Code, json.RawMessage(resp.Message)
	}
	return w.Code, resp.Data
}

func TestJobsSync(t *testing.T) {
	e := newTestEnv(t)
	code, data := e.call(t, "POST", "/cron/jobs", store.CronJob{Name: "hello", Type: "shell", Schedule: "*/10  * * * *", Script: "echo hi", Enabled: true})
	if code != 200 {
		t.Fatalf("%d %s", code, data)
	}
	var job store.CronJob
	json.Unmarshal(data, &job)
	b, _ := os.ReadFile(e.s.cronFile)
	if !strings.Contains(string(b), "*/10 * * * * root /usr/local/bin/sshdesktop -c /etc/sshdesktop/config.yaml cron run 1") {
		t.Fatalf("cron 文件：%s", b)
	}
	if fi, _ := os.Stat(e.s.cronFile); fi.Mode().Perm() != 0o644 {
		t.Fatal("cron 文件权限应为 644")
	}
	for _, bad := range []store.CronJob{
		{Name: "x", Type: "shell", Schedule: "* * * *", Script: "x"},
		{Name: "x", Type: "shell", Schedule: "* * * * * %", Script: "x"},
		{Name: "x", Type: "url", Schedule: "@daily", URL: "ftp://x"},
		{Name: "x", Type: "cleanup", Schedule: "@daily", CleanDirs: "/var"},
		{Name: "x", Type: "backup", Schedule: "@daily"},
		{Name: "", Type: "shell", Schedule: "@daily", Script: "x"},
	} {
		if code, _ := e.call(t, "POST", "/cron/jobs", bad); code == 200 {
			t.Errorf("应拒绝 %+v", bad)
		}
	}
	e.call(t, "POST", "/cron/jobs/1/enable", map[string]bool{"enabled": false})
	if b, _ := os.ReadFile(e.s.cronFile); strings.Contains(string(b), "cron run 1") {
		t.Fatal("停用后应从 cron 文件移除")
	}
	// 手动执行
	code, data = e.call(t, "POST", "/cron/jobs/1/run", nil)
	if code != 200 {
		t.Fatal(string(data))
	}
	var run store.CronRun
	json.Unmarshal(data, &run)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, data = e.call(t, "GET", "/cron/runs/"+itoa(run.ID), nil)
		json.Unmarshal(data, &run)
		if run.Status != "running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if run.Status != "success" || strings.TrimSpace(run.Output) != "hi" {
		t.Fatalf("执行结果：%+v", run)
	}
	e.call(t, "DELETE", "/cron/jobs/1", nil)
	if b, _ := os.ReadFile(e.s.cronFile); strings.Contains(string(b), "cron run") {
		t.Fatal("删除后 cron 文件应没有任务")
	}
}

func itoa(n uint) string { b, _ := json.Marshal(n); return string(b) }

func TestRunnerTimeoutSkipCleanup(t *testing.T) {
	e := newTestEnv(t)
	r := e.s.d.Runner
	ctx := context.Background()
	run := r.Run(ctx, store.CronJob{ID: 7, Type: "shell", Script: "sleep 5", Timeout: 1}, "manual", nil)
	if run.Status != "failed" || !strings.Contains(run.Output, "超过 1 秒") || run.DurationMs > 4000 {
		t.Fatalf("超时：%+v", run)
	}
	run = r.Run(ctx, store.CronJob{ID: 8, Type: "shell", Script: "echo out; echo err >&2; exit 3"}, "manual", nil)
	if run.Status != "failed" || run.ExitCode != 3 || !strings.Contains(run.Output, "out") || !strings.Contains(run.Output, "err") {
		t.Fatalf("退出码：%+v", run)
	}
	// 同一任务正在执行时跳过
	done := make(chan store.CronRun)
	go func() { done <- r.Run(ctx, store.CronJob{ID: 9, Type: "shell", Script: "sleep 1"}, "cron", nil) }()
	time.Sleep(300 * time.Millisecond)
	if run := r.Run(ctx, store.CronJob{ID: 9, Type: "shell", Script: "true"}, "cron", nil); run.Status != "skipped" {
		t.Fatalf("应跳过：%+v", run)
	}
	if run := <-done; run.Status != "success" {
		t.Fatalf("%+v", run)
	}
	// 清理：只删匹配且足够旧的文件
	dir := filepath.Join(t.TempDir(), "logs")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	old := time.Now().AddDate(0, 0, -10)
	for _, n := range []string{"a.log", "sub/b.log.1", "keep.txt", "new.log"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
		if n != "new.log" {
			os.Chtimes(filepath.Join(dir, n), old, old)
		}
	}
	run = r.Run(ctx, store.CronJob{ID: 10, Type: "cleanup", CleanDirs: dir, CleanDays: 7}, "manual", nil)
	if run.Status != "success" || !strings.Contains(run.Output, "共删除 2 个文件") {
		t.Fatalf("清理：%+v", run)
	}
	for n, exist := range map[string]bool{"a.log": false, "sub/b.log.1": false, "keep.txt": true, "new.log": true} {
		if _, err := os.Stat(filepath.Join(dir, n)); (err == nil) != exist {
			t.Errorf("%s 存在=%v", n, err == nil)
		}
	}
	// 执行记录按 KeepRuns 保留
	for i := 0; i < 5; i++ {
		r.Run(ctx, store.CronJob{ID: 11, Type: "shell", Script: "true", KeepRuns: 3}, "manual", nil)
	}
	var n int64
	r.Store.DB.Model(&store.CronRun{}).Where("job_id = 11").Count(&n)
	if n != 3 {
		t.Fatalf("应保留 3 条记录，实际 %d", n)
	}
}

func TestSystemCrontab(t *testing.T) {
	e := newTestEnv(t)
	sys := filepath.Join(e.root, "etc", "crontab")
	os.WriteFile(sys, []byte("SHELL=/bin/sh\n# m h dom mon dow user command\n17 * * * * root cd / && run-parts --report /etc/cron.hourly\n"), 0o644)
	os.WriteFile(filepath.Join(e.root, "etc", "cron.d", "php"), []byte("09,39 * * * * root [ -x /usr/lib/php/sessionclean ] && /usr/lib/php/sessionclean\n"), 0o644)
	os.WriteFile(filepath.Join(e.root, "etc", "cron.d", "skip.dpkg-old"), []byte("* * * * * root x\n"), 0o644)
	os.WriteFile(filepath.Join(e.root, "var", "spool", "cron", "crontabs", "alice"), []byte("@daily /home/alice/b.sh\n"), 0o600)
	e.s.Sync()
	_, data := e.call(t, "GET", "/cron/system", nil)
	var files []crontabFile
	json.Unmarshal(data, &files)
	if len(files) != 3 {
		t.Fatalf("应列出 3 个文件：%s", data)
	}
	f := files[0]
	if f.Path != "/etc/crontab" || f.Lines[2].Kind != "job" || f.Lines[2].User != "root" {
		t.Fatalf("%+v", f)
	}
	if files[2].User != "alice" || files[2].System || files[2].Lines[0].Schedule != "@daily" {
		t.Fatalf("%+v", files[2])
	}
	// 停用、修改、追加、删除
	toggle := map[string]any{"path": "/etc/crontab", "hash": f.Hash, "no": 3, "action": "toggle"}
	code, data := e.call(t, "POST", "/cron/system/line", toggle)
	if code != 200 {
		t.Fatal(string(data))
	}
	json.Unmarshal(data, &f)
	if f.Lines[2].Enabled || !strings.HasPrefix(f.Lines[2].Raw, offMark) {
		t.Fatalf("停用：%+v", f.Lines[2])
	}
	if code, _ := e.call(t, "POST", "/cron/system/line", toggle); code != 409 {
		t.Fatal("旧的 hash 应冲突")
	}
	code, data = e.call(t, "POST", "/cron/system/line", map[string]any{"path": "/etc/crontab", "hash": f.Hash, "action": "add", "schedule": "0 4 * * *", "user": "root", "command": "/bin/true"})
	if code != 200 {
		t.Fatal(string(data))
	}
	json.Unmarshal(data, &f)
	code, data = e.call(t, "POST", "/cron/system/line", map[string]any{"path": "/etc/crontab", "hash": f.Hash, "no": 1, "action": "delete"})
	if code != 400 {
		t.Fatal("环境变量行不能按任务删除")
	}
	b, _ := os.ReadFile(sys)
	want := "SHELL=/bin/sh\n# m h dom mon dow user command\n" + offMark + "17 * * * * root cd / && run-parts --report /etc/cron.hourly\n0 4 * * * root /bin/true\n"
	if string(b) != want {
		t.Fatalf("文件内容：\n%s", b)
	}
	backups, _ := filepath.Glob(filepath.Join(e.s.d.Runner.DataDir, "takeover-backup", "cron", "etc_crontab.*"))
	if len(backups) != 1 {
		t.Fatal("修改前应备份原文件")
	}
	if code, _ := e.call(t, "PUT", "/cron/system/file", map[string]any{"path": "/etc/passwd", "content": "x"}); code != 404 {
		t.Fatal("只能修改 crontab 文件")
	}
	_, data = e.call(t, "GET", "/cron/system/file?path=/etc/cron.d/php", nil)
	var raw struct{ Content, Hash string }
	json.Unmarshal(data, &raw)
	code, _ = e.call(t, "PUT", "/cron/system/file", map[string]any{"path": "/etc/cron.d/php", "content": "# empty", "hash": raw.Hash})
	if b, _ := os.ReadFile(filepath.Join(e.root, "etc", "cron.d", "php")); code != 200 || string(b) != "# empty\n" {
		t.Fatalf("%d %q", code, b)
	}
}
