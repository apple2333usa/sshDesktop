package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/node/sshtest"
	"github.com/apple2333usa/sshdesktop/internal/rbac"
	"github.com/apple2333usa/sshdesktop/internal/store"
)

// addNode 走一遍添加向导：扫描指纹 → 用确认过的指纹和密码创建节点。
func (e *env) addNode(tok string, s *sshtest.Server) string {
	e.t.Helper()
	r := e.do("POST", "/api/v1/nodes/scan", map[string]any{"host": s.Host, "port": s.Port}, local, tok, nil)
	if r.Code != 0 {
		e.t.Fatalf("扫描指纹失败：%s", r.Msg)
	}
	var scan struct{ Key, Fingerprint string }
	json.Unmarshal(r.Data, &scan)
	if !strings.HasPrefix(scan.Fingerprint, "SHA256:") {
		e.t.Fatalf("指纹格式不对：%s", scan.Fingerprint)
	}
	req := map[string]any{
		"name": "测试节点", "host": s.Host, "port": s.Port, "username": sshtest.User,
		"authType": "password", "password": sshtest.Password, "hostKey": scan.Key,
	}
	r = e.do("POST", "/api/v1/nodes", req, local, tok, nil)
	if r.Code != 0 {
		e.t.Fatalf("添加节点失败：%s", r.Msg)
	}
	var v struct{ ID string }
	json.Unmarshal(r.Data, &v)
	e.t.Cleanup(func() {
		e.s.monitor.Stop()
		e.s.nodes.Remove(v.ID)
	})
	return v.ID
}

func TestNodeLifecycle(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice", "alicepass123", rbac.AdminRoleName)
	tok := e.login("alice", "alicepass123")
	srv := sshtest.Start(t)

	// 错误密码不能添加
	r := e.do("POST", "/api/v1/nodes/scan", map[string]any{"host": srv.Host, "port": srv.Port}, local, tok, nil)
	var scan struct{ Key string }
	json.Unmarshal(r.Data, &scan)
	bad := map[string]any{"host": srv.Host, "port": srv.Port, "username": sshtest.User, "authType": "password", "password": "wrong", "hostKey": scan.Key}
	if r := e.do("POST", "/api/v1/nodes", bad, local, tok, nil); r.Code == 0 {
		t.Fatal("错误密码不应添加成功")
	}
	// 没有确认指纹不能添加
	delete(bad, "hostKey")
	bad["password"] = sshtest.Password
	if r := e.do("POST", "/api/v1/nodes", bad, local, tok, nil); r.Code == 0 {
		t.Fatal("未确认指纹不应添加成功")
	}

	id := e.addNode(tok, srv)
	// 凭据加密存储
	var cred store.NodeCredential
	e.s.st.DB.First(&cred, "node_id = ?", id)
	if len(cred.Secret) == 0 || strings.Contains(string(cred.Secret), sshtest.Password) {
		t.Fatal("密码应加密存储")
	}
	r = e.do("GET", "/api/v1/nodes", nil, local, tok, nil)
	var list []struct {
		ID      string
		Kind    string
		HostKey *struct{ Fingerprint string }
		Status  node.Status
	}
	json.Unmarshal(r.Data, &list)
	if len(list) != 2 || list[0].ID != node.LocalID || list[1].ID != id || list[1].HostKey == nil {
		t.Fatalf("节点列表不对：%s", r.Data)
	}
	if strings.Contains(string(r.Data), sshtest.Password) {
		t.Fatal("节点列表不能包含凭据")
	}

	// 远程文件：经 SFTP 浏览
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "remote.txt"), []byte("hi"), 0o644)
	r = e.do("GET", "/api/v1/nodes/"+id+"/files/list?path="+dir, nil, local, tok, nil)
	if r.Code != 0 || !strings.Contains(string(r.Data), "remote.txt") {
		t.Fatalf("远程文件列表：%d %s", r.Code, r.Msg)
	}

	// 跨节点复制：本机 → 远程节点
	src, dst := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(src, "dir/sub"), 0o755)
	os.WriteFile(filepath.Join(src, "dir/sub/data.bin"), []byte(strings.Repeat("x", 300000)), 0o600)
	r = e.do("POST", "/api/v1/nodes/"+id+"/files/paste", map[string]any{
		"sources": []string{filepath.Join(src, "dir")}, "srcNode": "local", "dest": dst, "mode": "move",
	}, local, tok, nil)
	if r.Code != 0 {
		t.Fatalf("跨节点粘贴失败：%s", r.Msg)
	}
	if st := e.waitTask(tok, r.Data); st != "success" {
		t.Fatalf("跨节点移动任务：%s", st)
	}
	fi, err := os.Stat(filepath.Join(dst, "dir/sub/data.bin"))
	if err != nil || fi.Size() != 300000 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("跨节点复制结果不对：%v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(src, "dir")); !os.IsNotExist(err) {
		t.Fatal("移动后源文件应删除")
	}

	// 批量命令：本机和远程节点同时执行
	r = e.do("POST", "/api/v1/batch/runs", map[string]any{"nodes": []string{"local", id}, "command": "echo hi-$((40+2)); exit 3", "timeout": 20}, local, tok, nil)
	if r.Code != 0 {
		t.Fatalf("批量命令失败：%s", r.Msg)
	}
	var run struct{ ID string }
	json.Unmarshal(r.Data, &run)
	var detail struct {
		Running bool
		Results []store.BatchResult
	}
	for i := 0; i < 200; i++ {
		r = e.do("GET", "/api/v1/batch/runs/"+run.ID, nil, local, tok, nil)
		json.Unmarshal(r.Data, &detail)
		if !detail.Running {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(detail.Results) != 2 {
		t.Fatalf("批量命令结果数量不对：%s", r.Data)
	}
	for _, res := range detail.Results {
		if res.Status != "done" || res.ExitCode != 3 || !strings.Contains(res.Output, "hi-42") {
			t.Fatalf("批量命令结果不对：%+v", res)
		}
	}
	// 超时
	r = e.do("POST", "/api/v1/batch/runs", map[string]any{"nodes": []string{id}, "command": "sleep 20", "timeout": 1}, local, tok, nil)
	json.Unmarshal(r.Data, &run)
	for i := 0; i < 200; i++ {
		r = e.do("GET", "/api/v1/batch/runs/"+run.ID, nil, local, tok, nil)
		json.Unmarshal(r.Data, &detail)
		if !detail.Running {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if detail.Results[0].Status != "timeout" {
		t.Fatalf("应超时：%+v", detail.Results[0])
	}

	// 无 agent 时经 SSH 采集监控
	if !testing.Short() {
		deadline := time.Now().Add(25 * time.Second)
		ok := false
		for time.Now().Before(deadline) && !ok {
			r = e.do("GET", "/api/v1/monitor/overview", nil, local, tok, nil)
			var ov []struct {
				ID     string
				Source string
				Sample *struct{ MemTotal uint64 }
			}
			json.Unmarshal(r.Data, &ov)
			for _, o := range ov {
				if o.ID == id && o.Source == "ssh" && o.Sample != nil && o.Sample.MemTotal > 0 {
					ok = true
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("没有经 SSH 采集到监控数据：%s", r.Data)
		}
	}

	// 修改名称和颜色（不改连接参数时不需要重新验证）
	upd := map[string]any{"name": "改名", "host": srv.Host, "port": srv.Port, "username": sshtest.User, "authType": "password", "color": "#ff0000"}
	if r := e.do("PUT", "/api/v1/nodes/"+id, upd, local, tok, nil); r.Code != 0 {
		t.Fatalf("修改节点失败：%s", r.Msg)
	}

	// 删除节点是二级危险操作
	if r := e.do("DELETE", "/api/v1/nodes/"+id, nil, local, tok, nil); r.Code != api.CodeConfirmRequired {
		t.Fatalf("删除节点应要求密码确认，得到 %d", r.Code)
	}
	pw := map[string]string{"X-Confirm-Password": base64.StdEncoding.EncodeToString([]byte("alicepass123"))}
	if r := e.do("DELETE", "/api/v1/nodes/"+id, nil, local, tok, pw); r.Code != 0 {
		t.Fatalf("删除节点失败：%s", r.Msg)
	}
	if _, err := e.s.nodes.Get(id); err == nil {
		t.Fatal("删除后节点应从注册表移除")
	}
	var n int64
	e.s.st.DB.Model(&store.NodeCredential{}).Where("node_id = ?", id).Count(&n)
	if n != 0 {
		t.Fatal("删除节点应同时删除凭据")
	}
}

func TestNodePermissions(t *testing.T) {
	e := newEnv(t)
	e.createUser("bob", "bobpass12345", rbac.ReadonlyRoleName)
	tok := e.login("bob", "bobpass12345")
	if r := e.do("GET", "/api/v1/nodes", nil, local, tok, nil); r.Code != 0 {
		t.Fatal("已登录账号应能查看节点列表")
	}
	if r := e.do("POST", "/api/v1/nodes/scan", map[string]any{"host": "127.0.0.1", "port": 22}, local, tok, nil); r.Status != http.StatusForbidden {
		t.Fatalf("只读账号不能管理节点，得到 %d", r.Status)
	}
	if r := e.do("POST", "/api/v1/batch/runs", map[string]any{"nodes": []string{"local"}, "command": "id"}, local, tok, nil); r.Status != http.StatusForbidden {
		t.Fatalf("只读账号不能执行批量命令，得到 %d", r.Status)
	}
	if r := e.do("GET", "/api/v1/probes", nil, local, tok, nil); r.Code != 0 {
		t.Fatal("只读角色应能查看服务监控")
	}
	if r := e.do("DELETE", "/api/v1/nodes/local", nil, local, tok, nil); r.Status != http.StatusForbidden {
		t.Fatalf("只读账号不能删除节点，得到 %d", r.Status)
	}
}

func TestProbes(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice", "alicepass123", rbac.AdminRoleName)
	tok := e.login("alice", "alicepass123")
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/down" {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, "hello sshdesktop")
	}))
	defer web.Close()
	cases := []struct {
		req map[string]any
		ok  bool
	}{
		{map[string]any{"type": "http", "target": web.URL, "keyword": "sshdesktop"}, true},
		{map[string]any{"type": "http", "target": web.URL, "keyword": "missing"}, false},
		{map[string]any{"type": "http", "target": web.URL + "/down"}, false},
		{map[string]any{"type": "http", "target": web.URL + "/down", "expectStatus": "500-599"}, true},
		{map[string]any{"type": "tcp", "target": strings.TrimPrefix(web.URL, "http://")}, true},
		{map[string]any{"type": "tcp", "target": "127.0.0.1:1"}, false},
	}
	for _, c := range cases {
		r := e.do("POST", "/api/v1/probes/test", c.req, local, tok, nil)
		var res struct {
			OK      bool
			Message string
		}
		json.Unmarshal(r.Data, &res)
		if r.Code != 0 || res.OK != c.ok {
			t.Fatalf("%v：期望 %v，得到 %d %s %+v", c.req, c.ok, r.Code, r.Msg, res)
		}
	}
	if r := e.do("POST", "/api/v1/probes", map[string]any{"type": "http", "target": "ftp://x"}, local, tok, nil); r.Code == 0 {
		t.Fatal("非 http 网址应被拒绝")
	}
	if r := e.do("POST", "/api/v1/probes", map[string]any{"type": "tcp", "target": "example.com", "interval": 10}, local, tok, nil); r.Code == 0 {
		t.Fatal("间隔小于 30 秒应被拒绝")
	}
	r := e.do("POST", "/api/v1/probes", map[string]any{"name": "web", "type": "http", "target": web.URL, "interval": 30}, local, tok, nil)
	if r.Code != 0 {
		t.Fatalf("创建探测失败：%s", r.Msg)
	}
	r = e.do("GET", "/api/v1/probes", nil, local, tok, nil)
	if !strings.Contains(string(r.Data), `"name":"web"`) {
		t.Fatalf("探测列表：%s", r.Data)
	}
}
