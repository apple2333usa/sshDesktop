package server

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/auth"
	"github.com/apple2333usa/sshdesktop/internal/config"
	"github.com/apple2333usa/sshdesktop/internal/rbac"
	"github.com/apple2333usa/sshdesktop/internal/store"
)

type env struct {
	t   *testing.T
	s   *Server
	cfg *config.Config
}

type resp struct {
	Status int
	Code   int             `json:"code"`
	Msg    string          `json:"message"`
	Data   json.RawMessage `json:"data"`
	Header http.Header
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.LogDir = ""
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	web := fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}
	s, err := New(cfg, st, "test", web)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t: t, s: s, cfg: cfg}
}

// do 发送请求；remote 为来源地址，cookie 为会话令牌。
func (e *env) do(method, path string, body any, remote, cookie string, hdr map[string]string) resp {
	e.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = remote
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-Requested-With", "sshdesktop")
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	e.s.engine.ServeHTTP(w, req)
	r := resp{Status: w.Code, Header: w.Header()}
	json.Unmarshal(w.Body.Bytes(), &r)
	return r
}

const local = "127.0.0.1:50000"

func (e *env) createUser(name, pw, roleName string) {
	e.t.Helper()
	var role store.Role
	if err := e.s.st.DB.Where("name = ?", roleName).First(&role).Error; err != nil {
		e.t.Fatal(err)
	}
	if _, err := auth.CreateUser(e.s.st.DB, name, pw, role.ID); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) login(name, pw string) string {
	e.t.Helper()
	r := e.do("POST", "/api/v1/auth/login", map[string]string{"username": name, "password": pw}, local, "", nil)
	if r.Code != 0 {
		e.t.Fatalf("登录失败：%d %s", r.Code, r.Msg)
	}
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == cookieName {
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				e.t.Fatalf("会话 Cookie 属性不正确：%+v", c)
			}
			return c.Value
		}
	}
	e.t.Fatal("没有返回会话 Cookie")
	return ""
}

func TestInitialAdminCreated(t *testing.T) {
	e := newEnv(t)
	b, err := os.ReadFile(filepath.Join(e.cfg.DataDir, "initial-admin.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "admin") {
		t.Fatalf("initial-admin.txt 内容不正确：%s", b)
	}
}

func TestAccessGuardRejectsRemoteWithoutHTTPS(t *testing.T) {
	e := newEnv(t)
	if r := e.do("GET", "/api/v1/health", nil, "203.0.113.5:1234", "", nil); r.Status != http.StatusForbidden {
		t.Fatalf("未配置 HTTPS 时远程访问应被拒绝，得到 %d", r.Status)
	}
	if r := e.do("GET", "/api/v1/health", nil, local, "", nil); r.Status != 200 {
		t.Fatalf("本机访问应允许，得到 %d", r.Status)
	}
}

func TestPublicModeChecksHost(t *testing.T) {
	e := newEnv(t)
	e.cfg.Domain = "panel.example.com"
	e.cfg.HTTPS.Enabled = true
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Host = "evil.example.com"
	w := httptest.NewRecorder()
	e.s.engine.ServeHTTP(w, req)
	if w.Code != http.StatusMisdirectedRequest {
		t.Fatalf("Host 不匹配应返回 421，得到 %d", w.Code)
	}
	req.Host = "panel.example.com"
	w = httptest.NewRecorder()
	e.s.engine.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("Host 匹配应允许，得到 %d", w.Code)
	}
}

func TestLoginAndCSRF(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice", "alicepass123", rbac.AdminRoleName)

	r := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "wrong-password"}, local, "", nil)
	if r.Code != api.CodeBadCredentials {
		t.Fatalf("错误密码应返回 %d，得到 %d", api.CodeBadCredentials, r.Code)
	}
	// 本机登录失败后按用户名延迟
	r = e.do("POST", "/api/v1/auth/login", map[string]string{"username": "alice", "password": "alicepass123"}, local, "", nil)
	if r.Code != api.CodeRetryLater {
		t.Fatalf("失败后立即重试应被限速，得到 %d", r.Code)
	}
	time.Sleep(1100 * time.Millisecond)
	tok := e.login("alice", "alicepass123")

	if r := e.do("GET", "/api/v1/auth/me", nil, local, tok, nil); r.Code != 0 {
		t.Fatalf("/auth/me 失败：%d %s", r.Code, r.Msg)
	}
	if r := e.do("GET", "/api/v1/auth/me", nil, local, "", nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("未登录应返回 401，得到 %d", r.Status)
	}
	// 写请求缺少 X-Requested-With 时拒绝
	r = e.do("PUT", "/api/v1/desktop/state", map[string]any{"a": 1}, local, tok, map[string]string{"X-Requested-With": ""})
	if r.Status != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 头应返回 403，得到 %d", r.Status)
	}
	if r := e.do("PUT", "/api/v1/desktop/state", map[string]any{"a": 1}, local, tok, nil); r.Code != 0 {
		t.Fatalf("保存桌面状态失败：%d %s", r.Code, r.Msg)
	}
	// 注销后令牌失效
	e.do("POST", "/api/v1/auth/logout", nil, local, tok, nil)
	if r := e.do("GET", "/api/v1/auth/me", nil, local, tok, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("注销后令牌应失效，得到 %d", r.Status)
	}
}

func TestRemoteIPBan(t *testing.T) {
	e := newEnv(t)
	e.cfg.Domain = "panel.example.com"
	e.cfg.HTTPS.Enabled = true
	e.createUser("bob", "bobpass1234", rbac.AdminRoleName)
	try := func(pw string) resp {
		req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"username":"bob","password":"`+pw+`"}`))
		req.RemoteAddr = "203.0.113.9:4000"
		req.Host = "panel.example.com"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Requested-With", "sshdesktop")
		w := httptest.NewRecorder()
		e.s.engine.ServeHTTP(w, req)
		var r resp
		json.Unmarshal(w.Body.Bytes(), &r)
		return r
	}
	for i := 0; i < 5; i++ {
		try("bad")
	}
	if r := try("bobpass1234"); r.Code != api.CodeIPBanned {
		t.Fatalf("连续失败后应封禁 IP，得到 %d %s", r.Code, r.Msg)
	}
}

func TestPermissions(t *testing.T) {
	e := newEnv(t)
	e.createUser("viewer", "viewerpass1", rbac.ReadonlyRoleName)
	tok := e.login("viewer", "viewerpass1")
	dir := t.TempDir()
	if r := e.do("GET", "/api/v1/nodes/local/files/list?path="+dir, nil, local, tok, nil); r.Code != 0 {
		t.Fatalf("只读角色应能浏览文件：%d %s", r.Code, r.Msg)
	}
	if r := e.do("POST", "/api/v1/nodes/local/files/mkdir", map[string]string{"path": dir + "/x"}, local, tok, nil); r.Status != http.StatusForbidden {
		t.Fatalf("只读角色不应能创建目录，得到 %d", r.Status)
	}
	if r := e.do("GET", "/api/v1/users", nil, local, tok, nil); r.Status != http.StatusForbidden {
		t.Fatalf("非管理员不应能管理账号，得到 %d", r.Status)
	}
	if r := e.do("POST", "/api/v1/nodes/local/terminals", map[string]any{}, local, tok, nil); r.Status != http.StatusForbidden {
		t.Fatalf("只读角色不应能打开终端，得到 %d", r.Status)
	}
	if r := e.do("GET", "/api/v1/nodes/other/files/list?path=/", nil, local, tok, nil); r.Code != api.CodeNotFound {
		t.Fatalf("不存在的节点应返回 404，得到 %d", r.Code)
	}
}

func TestFilesWorkflow(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice", "alicepass123", rbac.AdminRoleName)
	tok := e.login("alice", "alicepass123")
	dir := t.TempDir()
	base := "/api/v1/nodes/local/files"

	if r := e.do("POST", base+"/create", map[string]string{"path": dir + "/a.txt"}, local, tok, nil); r.Code != 0 {
		t.Fatalf("新建文件失败：%s", r.Msg)
	}
	r := e.do("GET", base+"/content?path="+dir+"/a.txt", nil, local, tok, nil)
	var content struct{ Version string }
	json.Unmarshal(r.Data, &content)
	r = e.do("PUT", base+"/content", map[string]any{"path": dir + "/a.txt", "content": "hello", "version": content.Version}, local, tok, nil)
	if r.Code != 0 {
		t.Fatalf("保存失败：%s", r.Msg)
	}
	// 用旧版本号再次保存应提示冲突
	r = e.do("PUT", base+"/content", map[string]any{"path": dir + "/a.txt", "content": "again", "version": content.Version}, local, tok, nil)
	if r.Code != api.CodeFileModified {
		t.Fatalf("版本冲突应返回 %d，得到 %d", api.CodeFileModified, r.Code)
	}
	if b, _ := os.ReadFile(dir + "/a.txt"); string(b) != "hello" {
		t.Fatalf("文件内容不正确：%q", b)
	}

	// 永久删除需要二级确认
	os.WriteFile(dir+"/b.txt", []byte("b"), 0o644)
	del := map[string]any{"paths": []string{dir + "/b.txt"}, "permanent": true}
	if r := e.do("POST", base+"/delete", del, local, tok, nil); r.Code != api.CodeConfirmRequired {
		t.Fatalf("缺少确认密码应返回 %d，得到 %d", api.CodeConfirmRequired, r.Code)
	}
	bad := map[string]string{"X-Confirm-Password": base64.StdEncoding.EncodeToString([]byte("nope"))}
	if r := e.do("POST", base+"/delete", del, local, tok, bad); r.Code != api.CodeConfirmFailed {
		t.Fatalf("确认密码错误应返回 %d，得到 %d", api.CodeConfirmFailed, r.Code)
	}
	good := map[string]string{"X-Confirm-Password": base64.StdEncoding.EncodeToString([]byte("alicepass123"))}
	if r := e.do("POST", base+"/delete", del, local, tok, good); r.Code != 0 {
		t.Fatalf("永久删除失败：%s", r.Msg)
	}
	if _, err := os.Stat(dir + "/b.txt"); !os.IsNotExist(err) {
		t.Fatal("文件应已被删除")
	}

	// 路径必须是绝对路径
	if r := e.do("GET", base+"/list?path=relative/dir", nil, local, tok, nil); r.Code == 0 {
		t.Fatal("相对路径应被拒绝")
	}

	// 审计日志记录了写操作
	var n int64
	e.s.st.DB.Model(&store.AuditLog{}).Where("module = ? AND action = ?", "files", "edit").Count(&n)
	if n == 0 {
		t.Fatal("编辑文件应写入审计日志")
	}
}

// waitTask 轮询任务直到结束。
func (e *env) waitTask(tok string, data json.RawMessage) string {
	e.t.Helper()
	var tk struct{ ID string }
	json.Unmarshal(data, &tk)
	for i := 0; i < 100; i++ {
		r := e.do("GET", "/api/v1/tasks/"+tk.ID, nil, local, tok, nil)
		var d struct {
			Task struct {
				Status string
				Error  string
			}
		}
		json.Unmarshal(r.Data, &d)
		if d.Task.Status != "running" && d.Task.Status != "" {
			if d.Task.Error != "" {
				return d.Task.Status + ": " + d.Task.Error
			}
			return d.Task.Status
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.t.Fatal("任务超时")
	return ""
}

func TestArchiveRoundTripAndZipSlip(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice", "alicepass123", rbac.AdminRoleName)
	tok := e.login("alice", "alicepass123")
	dir := t.TempDir()
	base := "/api/v1/nodes/local/files"
	os.MkdirAll(dir+"/src/sub", 0o755)
	os.WriteFile(dir+"/src/sub/x.txt", []byte("xyz"), 0o644)

	for _, ext := range []string{"zip", "tar.gz", "tar.xz"} {
		dest := dir + "/out." + ext
		r := e.do("POST", base+"/compress", map[string]any{"paths": []string{dir + "/src"}, "dest": dest}, local, tok, nil)
		if r.Code != 0 {
			t.Fatalf("压缩 %s 失败：%s", ext, r.Msg)
		}
		if st := e.waitTask(tok, r.Data); st != "success" {
			t.Fatalf("压缩 %s 任务失败：%s", ext, st)
		}
		target := dir + "/x-" + ext
		r = e.do("POST", base+"/extract", map[string]any{"path": dest, "dest": target, "overwrite": false}, local, tok, nil)
		if r.Code != 0 {
			t.Fatalf("解压 %s 失败：%s", ext, r.Msg)
		}
		if st := e.waitTask(tok, r.Data); st != "success" {
			t.Fatalf("解压 %s 任务失败：%s", ext, st)
		}
		if b, err := os.ReadFile(target + "/src/sub/x.txt"); err != nil || string(b) != "xyz" {
			t.Fatalf("%s 解压结果不正确：%v %q", ext, err, b)
		}
	}

	// 构造包含 ../ 的恶意压缩包
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../../evil.txt")
	w.Write([]byte("evil"))
	w, _ = zw.Create("ok.txt")
	w.Write([]byte("ok"))
	zw.Close()
	os.WriteFile(dir+"/evil.zip", buf.Bytes(), 0o644)
	r := e.do("POST", base+"/extract", map[string]any{"path": dir + "/evil.zip", "dest": dir + "/safe/inner", "overwrite": false}, local, tok, nil)
	if r.Code != 0 {
		t.Fatalf("解压失败：%s", r.Msg)
	}
	e.waitTask(tok, r.Data)
	if _, err := os.Stat(dir + "/evil.txt"); err == nil {
		t.Fatal("zip slip：文件被写到了目标目录之外")
	}
	if _, err := os.Stat(dir + "/safe/inner/ok.txt"); err != nil {
		t.Fatal("正常条目应被解压")
	}
}

func TestTrashRestore(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice", "alicepass123", rbac.AdminRoleName)
	tok := e.login("alice", "alicepass123")
	dir := t.TempDir()
	base := "/api/v1/nodes/local/files"
	os.WriteFile(dir+"/t.txt", []byte("t"), 0o644)

	r := e.do("POST", base+"/delete", map[string]any{"paths": []string{dir + "/t.txt"}}, local, tok, nil)
	if r.Code == api.CodeTrashUnavailable {
		t.Skip("当前环境无法在挂载点根目录创建回收站：" + r.Msg)
	}
	if r.Code != 0 {
		t.Fatalf("移入回收站失败：%s", r.Msg)
	}
	if _, err := os.Stat(dir + "/t.txt"); !os.IsNotExist(err) {
		t.Fatal("文件应已移入回收站")
	}
	r = e.do("GET", base+"/trash", nil, local, tok, nil)
	var items []struct{ ID, Root, Original string }
	json.Unmarshal(r.Data, &items)
	var refs []map[string]string
	for _, it := range items {
		if it.Original == dir+"/t.txt" {
			refs = append(refs, map[string]string{"root": it.Root, "id": it.ID})
		}
	}
	if len(refs) != 1 {
		t.Fatalf("回收站中应有该文件，得到 %+v", items)
	}
	if r := e.do("POST", base+"/trash/restore", map[string]any{"items": refs}, local, tok, nil); r.Code != 0 {
		t.Fatalf("还原失败：%s", r.Msg)
	}
	if b, err := os.ReadFile(dir + "/t.txt"); err != nil || string(b) != "t" {
		t.Fatal("文件应已还原")
	}
}

func TestLastAdminProtected(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice", "alicepass123", rbac.AdminRoleName)
	tok := e.login("alice", "alicepass123")
	var admin store.User
	e.s.st.DB.Where("username = ?", "admin").First(&admin)
	// 删除初始 admin 后，alice 是唯一管理员，不能把自己降级
	if r := e.do("DELETE", "/api/v1/users/"+itoa(admin.ID), nil, local, tok, nil); r.Code != 0 {
		t.Fatalf("删除 admin 失败：%s", r.Msg)
	}
	var ops store.Role
	e.s.st.DB.Where("name = ?", rbac.OpsRoleName).First(&ops)
	var alice store.User
	e.s.st.DB.Where("username = ?", "alice").First(&alice)
	r := e.do("PUT", "/api/v1/users/"+itoa(alice.ID), map[string]any{"roleId": ops.ID}, local, tok, nil)
	if r.Code != api.CodeLastAdmin {
		t.Fatalf("降级最后一个管理员应返回 %d，得到 %d %s", api.CodeLastAdmin, r.Code, r.Msg)
	}
}

func itoa(n uint) string {
	b, _ := json.Marshal(n)
	return string(b)
}
