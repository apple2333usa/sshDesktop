package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/node/sshtest"
	"github.com/apple2333usa/sshdesktop/internal/rbac"
	"github.com/apple2333usa/sshdesktop/internal/store"
)

// waitLong 轮询任务直到结束（Docker 操作需要更长时间）。
func (e *env) waitLong(tok string, data json.RawMessage, limit time.Duration) string {
	e.t.Helper()
	var tk struct {
		ID   string
		Task struct{ ID string }
	}
	json.Unmarshal(data, &tk)
	if tk.ID == "" {
		tk.ID = tk.Task.ID
	}
	if tk.ID == "" {
		e.t.Fatalf("没有返回任务：%s", data)
	}
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		r := e.do("GET", "/api/v1/tasks/"+tk.ID, nil, local, tok, nil)
		var d struct {
			Task struct{ Status, Error string }
			Logs []struct{ Line string }
		}
		json.Unmarshal(r.Data, &d)
		if d.Task.Status != "running" && d.Task.Status != "" {
			if d.Task.Status != "success" {
				var lines []string
				for _, l := range d.Logs {
					lines = append(lines, l.Line)
				}
				e.t.Logf("任务日志：\n%s", strings.Join(lines, "\n"))
				return d.Task.Status + ": " + d.Task.Error
			}
			return d.Task.Status
		}
		time.Sleep(200 * time.Millisecond)
	}
	e.t.Fatal("任务超时")
	return ""
}

func (e *env) ok(r resp, what string) json.RawMessage {
	e.t.Helper()
	if r.Code != 0 {
		e.t.Fatalf("%s：%d %s", what, r.Code, r.Msg)
	}
	return r.Data
}

func confirmHdr(pw string) map[string]string {
	return map[string]string{"X-Confirm-Password": base64.StdEncoding.EncodeToString([]byte(pw))}
}

// m3Env 准备管理员账号并把 cron 文件指向临时目录；没有 Docker 时 needDocker 跳过测试。
func m3Env(t *testing.T, needDocker bool) (*env, string) {
	e := newEnv(t)
	if needDocker {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e.s.docker.Ping(ctx) != nil || os.Geteuid() != 0 {
			t.Skip("需要 root 和正在运行的 Docker")
		}
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc", "cron.d"), 0o755)
	e.s.cron.SetPaths(filepath.Join(root, "etc", "cron.d", "sshdesktop"), root)
	e.createUser("ops1", "opspass123", rbac.AdminRoleName)
	return e, e.login("ops1", "opspass123")
}

const lb = "/api/v1/nodes/local"

func TestM3Permissions(t *testing.T) {
	e, _ := m3Env(t, false)
	e.createUser("ro", "readonly123", rbac.ReadonlyRoleName)
	ro := e.login("ro", "readonly123")
	for _, p := range []string{"/docker/status", "/cron/jobs", "/backup/plans", "/appstore/sources"} {
		if r := e.do("GET", lb+p, nil, local, ro, nil); r.Code != 0 && r.Status == 403 {
			t.Fatalf("只读角色应能查看 %s：%s", p, r.Msg)
		}
	}
	for _, c := range []struct{ m, p string }{
		{"POST", "/docker/containers"}, {"POST", "/cron/jobs"}, {"POST", "/backup/plans"}, {"POST", "/appstore/installed"},
	} {
		if r := e.do(c.m, lb+c.p, map[string]any{}, local, ro, nil); r.Status != 403 {
			t.Fatalf("只读角色不应能 %s %s：%d", c.m, c.p, r.Status)
		}
	}
	// 只对本机开放
	if r := e.do("GET", "/api/v1/nodes/other/docker/status", nil, local, ro, nil); r.Code == 0 {
		t.Fatal("远程节点不应提供 Docker 接口")
	}
}

func TestM3BackupSFTPAndCron(t *testing.T) {
	e, tok := m3Env(t, false)
	srv := sshtest.Start(t)
	nodeID := e.addNode(tok, srv)
	// 等节点连上
	for i := 0; i < 100; i++ {
		if n, ok := e.s.nodes.SSH(nodeID); ok && n.Status().State == "online" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644)
	remote := filepath.Join(t.TempDir(), "remote-backups")
	tgt := map[string]any{"name": "SFTP", "type": "sftp", "config": map[string]any{"nodeId": nodeID, "path": remote}}
	e.ok(e.do("POST", lb+"/backup/targets/test", tgt, local, tok, nil), "测试 SFTP 目标")
	var target store.BackupTarget
	json.Unmarshal(e.ok(e.do("POST", lb+"/backup/targets", tgt, local, tok, nil), "创建目标"), &target)
	plan := map[string]any{"name": "文档", "type": "dir", "source": src, "targetIds": []uint{target.ID}, "keepCount": 3, "schedule": "30 2 * * *"}
	var p store.BackupPlan
	json.Unmarshal(e.ok(e.do("POST", lb+"/backup/plans", plan, local, tok, nil), "创建计划"), &p)
	b, _ := os.ReadFile(e.s.cron.CronFilePath())
	if !strings.Contains(string(b), "30 2 * * * root") {
		t.Fatalf("定时备份应写入 cron 文件：%s", b)
	}
	if st := e.waitLong(tok, e.ok(e.do("POST", lb+"/backup/plans/"+itoa(p.ID)+"/run", nil, local, tok, nil), "执行备份"), 30*time.Second); st != "success" {
		t.Fatal(st)
	}
	var recs []store.BackupRecord
	json.Unmarshal(e.ok(e.do("GET", lb+"/backup/records?plan="+itoa(p.ID), nil, local, tok, nil), "备份记录"), &recs)
	if len(recs) != 1 || recs[0].Status != "success" {
		t.Fatalf("%+v", recs)
	}
	if m, _ := filepath.Glob(filepath.Join(remote, "plan-*", "dir_*.tar.gz")); len(m) != 1 {
		t.Fatal("备份文件应上传到 SFTP 目录")
	}
	var files []map[string]any
	json.Unmarshal(e.ok(e.do("GET", lb+"/backup/targets/"+itoa(target.ID)+"/files", nil, local, tok, nil), "目标文件"), &files)
	if len(files) != 1 || files[0]["recordId"] == nil {
		t.Fatalf("%v", files)
	}
	// 修改后恢复（需要密码确认）
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("changed"), 0o644)
	if r := e.do("POST", lb+"/backup/restore", map[string]any{"recordId": recs[0].ID}, local, tok, nil); r.Status != 428 {
		t.Fatalf("恢复应要求密码确认：%d", r.Status)
	}
	data := e.ok(e.do("POST", lb+"/backup/restore", map[string]any{"recordId": recs[0].ID}, local, tok, confirmHdr("opspass123")), "恢复")
	if st := e.waitLong(tok, data, 30*time.Second); st != "success" {
		t.Fatal(st)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "a.txt")); string(b) != "hello" {
		t.Fatalf("恢复后内容：%s", b)
	}
	// 目标被计划使用时不能删除；删除计划会同时删除定时任务
	if r := e.do("DELETE", lb+"/backup/targets/"+itoa(target.ID), nil, local, tok, nil); r.Code == 0 {
		t.Fatal("使用中的目标不能删除")
	}
	e.ok(e.do("DELETE", lb+"/backup/plans/"+itoa(p.ID), nil, local, tok, nil), "删除计划")
	if b, _ := os.ReadFile(e.s.cron.CronFilePath()); strings.Contains(string(b), "cron run") {
		t.Fatal("删除计划后定时任务应删除")
	}
	e.ok(e.do("DELETE", lb+"/backup/targets/"+itoa(target.ID), nil, local, tok, nil), "删除目标")
}

func TestM3DockerAppstoreBackup(t *testing.T) {
	e, tok := m3Env(t, true)
	cf := confirmHdr("opspass123")
	e.ok(e.do("GET", lb+"/docker/status", nil, local, tok, nil), "Docker 状态")
	cleanup := func(names ...string) {
		for _, n := range names {
			e.s.docker.ContainerRemove(context.Background(), n, true, true)
		}
	}

	// 容器
	cleanup("sshd-t-ct")
	defer cleanup("sshd-t-ct")
	data := e.ok(e.do("POST", lb+"/docker/containers", map[string]any{
		"name": "sshd-t-ct", "image": "alpine:3", "command": "sh -c 'echo hello-log; sleep 300'", "env": []string{"A=1"},
	}, local, tok, nil), "创建容器")
	if st := e.waitLong(tok, data, time.Minute); st != "success" {
		t.Fatal(st)
	}
	var cts []struct{ ID, Name, State string }
	json.Unmarshal(e.ok(e.do("GET", lb+"/docker/containers", nil, local, tok, nil), "容器列表"), &cts)
	var id string
	for _, c := range cts {
		if c.Name == "sshd-t-ct" {
			id = c.ID
			if c.State != "running" {
				t.Fatalf("容器状态 %s", c.State)
			}
		}
	}
	if id == "" {
		t.Fatalf("容器列表中没有新建的容器：%+v", cts)
	}
	e.ok(e.do("POST", lb+"/docker/containers/"+id+"/stop", nil, local, tok, nil), "停止容器")
	e.ok(e.do("DELETE", lb+"/docker/containers/"+id+"?force=1", nil, local, tok, nil), "删除容器")

	// 网络与存储卷
	var nw struct{ ID string }
	json.Unmarshal(e.ok(e.do("POST", lb+"/docker/networks", map[string]any{"name": "sshd-t-net"}, local, tok, nil), "创建网络"), &nw)
	e.ok(e.do("DELETE", lb+"/docker/networks/"+nw.ID, nil, local, tok, nil), "删除网络")
	e.ok(e.do("POST", lb+"/docker/volumes", map[string]any{"name": "sshd-t-vol"}, local, tok, nil), "创建存储卷")
	if r := e.do("DELETE", lb+"/docker/volumes/sshd-t-vol", nil, local, tok, nil); r.Status != 428 {
		t.Fatalf("删除存储卷应要求密码确认：%d", r.Status)
	}
	e.ok(e.do("DELETE", lb+"/docker/volumes/sshd-t-vol", nil, local, tok, cf), "删除存储卷")

	// Compose
	yml := "services:\n  app:\n    image: alpine:3\n    command: sleep 300\n"
	data = e.ok(e.do("POST", lb+"/docker/compose", map[string]any{"name": "sshd-t-compose", "mode": "yaml", "content": yml}, local, tok, nil), "创建 Compose")
	if st := e.waitLong(tok, data, time.Minute); st != "success" {
		t.Fatal(st)
	}
	var projects []struct{ Name, Status string }
	json.Unmarshal(e.ok(e.do("GET", lb+"/docker/compose", nil, local, tok, nil), "Compose 列表"), &projects)
	found := false
	for _, p := range projects {
		if p.Name == "sshd-t-compose" {
			found = p.Status == "running"
		}
	}
	if !found {
		t.Fatalf("Compose 项目应在运行：%+v", projects)
	}
	data = e.ok(e.do("DELETE", lb+"/docker/compose/sshd-t-compose?files=1", nil, local, tok, cf), "删除 Compose")
	if st := e.waitLong(tok, data, time.Minute); st != "success" {
		t.Fatal(st)
	}

	// 应用商店：本地目录源
	srcDir := t.TempDir()
	for _, v := range []string{"1.0.0", "1.1.0"} {
		vd := filepath.Join(srcDir, "demo", v)
		os.MkdirAll(vd, 0o755)
		os.WriteFile(filepath.Join(vd, "docker-compose.yml"), []byte(`services:
  demo:
    image: alpine:3
    container_name: ${CONTAINER_NAME}
    command: sh -c "echo $$GREETING-`+v+` > /data/greeting; sleep 300"
    environment:
      - GREETING=${GREETING}
    ports:
      - ${PANEL_APP_PORT_HTTP}:80
    volumes:
      - ./data:/data
    networks:
      - 1panel-network
networks:
  1panel-network:
    external: true
`), 0o644)
		os.WriteFile(filepath.Join(vd, "data.yml"), []byte(`additionalProperties:
  formFields:
    - envKey: PANEL_APP_PORT_HTTP
      labelZh: 端口
      type: number
      default: 18080
      required: true
      rule: paramPort
      edit: true
    - envKey: GREETING
      labelZh: 问候
      type: text
      default: hi
      edit: true
`), 0o644)
	}
	os.WriteFile(filepath.Join(srcDir, "demo", "data.yml"), []byte("name: Demo\nadditionalProperties:\n  key: demo\n  shortDescZh: 演示\n"), 0o644)
	e.ok(e.do("PUT", lb+"/appstore/sources", []map[string]any{
		{"id": "1panel", "enabled": false},
		{"id": "localsrc", "name": "本地", "type": "dir", "url": srcDir, "enabled": true},
	}, local, tok, nil), "保存应用源")
	if st := e.waitLong(tok, e.ok(e.do("POST", lb+"/appstore/sync", nil, local, tok, nil), "同步"), time.Minute); st != "success" {
		t.Fatal(st)
	}
	cleanup("demo-t")
	defer func() {
		cleanup("demo-t")
		e.s.docker.NetworkRemove(context.Background(), "1panel-network")
	}()
	data = e.ok(e.do("POST", lb+"/appstore/installed", map[string]any{
		"source": "localsrc", "key": "demo", "version": "1.0.0", "name": "demo-t",
		"params": map[string]string{"PANEL_APP_PORT_HTTP": "18091", "GREETING": "hello"}, "external": false,
	}, local, tok, nil), "安装应用")
	if st := e.waitLong(tok, data, 2*time.Minute); st != "success" {
		t.Fatal(st)
	}
	var row store.AppInstalled
	e.s.st.DB.First(&row, "name = ?", "demo-t")
	greeting := func() string {
		for i := 0; i < 50; i++ {
			if b, err := os.ReadFile(filepath.Join(row.Dir, "data", "greeting")); err == nil && len(b) > 0 {
				return strings.TrimSpace(string(b))
			}
			time.Sleep(100 * time.Millisecond)
		}
		return ""
	}
	if g := greeting(); g != "hello-1.0.0" {
		t.Fatalf("应用输出：%q", g)
	}
	if b, _ := os.ReadFile(filepath.Join(row.Dir, "docker-compose.yml")); !strings.Contains(string(b), "${HOST_IP}:${PANEL_APP_PORT_HTTP}:80") {
		t.Fatalf("仅本机访问时端口应绑定 HOST_IP：%s", b)
	}
	if r := e.do("POST", lb+"/appstore/installed", map[string]any{
		"source": "localsrc", "key": "demo", "version": "1.0.0", "name": "demo-t2",
		"params": map[string]string{"PANEL_APP_PORT_HTTP": "18091"},
	}, local, tok, nil); r.Code == 0 {
		t.Fatal("端口被占用时应拒绝安装")
	}
	os.Remove(filepath.Join(row.Dir, "data", "greeting"))
	data = e.ok(e.do("PUT", lb+"/appstore/installed/demo-t/params", map[string]any{"params": map[string]string{"GREETING": "bye"}}, local, tok, nil), "修改参数")
	if st := e.waitLong(tok, data, time.Minute); st != "success" {
		t.Fatal(st)
	}
	if g := greeting(); g != "bye-1.0.0" {
		t.Fatalf("修改参数后：%q", g)
	}
	os.Remove(filepath.Join(row.Dir, "data", "greeting"))
	data = e.ok(e.do("POST", lb+"/appstore/installed/demo-t/upgrade", map[string]any{"version": "1.1.0"}, local, tok, nil), "升级")
	if st := e.waitLong(tok, data, time.Minute); st != "success" {
		t.Fatal(st)
	}
	if g := greeting(); g != "bye-1.1.0" {
		t.Fatalf("升级后：%q", g)
	}

	// 备份应用 → 卸载（删除数据）→ 从备份恢复
	var target store.BackupTarget
	json.Unmarshal(e.ok(e.do("POST", lb+"/backup/targets", map[string]any{"name": "本地", "type": "local", "config": map[string]any{"path": t.TempDir()}}, local, tok, nil), "创建目标"), &target)
	var p store.BackupPlan
	json.Unmarshal(e.ok(e.do("POST", lb+"/backup/plans", map[string]any{
		"name": "演示应用", "type": "app", "source": "demo-t", "targetIds": []uint{target.ID}, "encrypt": true, "password": "backup-pass-1", "stopApp": true,
	}, local, tok, nil), "创建计划"), &p)
	if st := e.waitLong(tok, e.ok(e.do("POST", lb+"/backup/plans/"+itoa(p.ID)+"/run", nil, local, tok, nil), "备份应用"), 2*time.Minute); st != "success" {
		t.Fatal(st)
	}
	var recs []store.BackupRecord
	json.Unmarshal(e.ok(e.do("GET", lb+"/backup/records?plan="+itoa(p.ID), nil, local, tok, nil), "记录"), &recs)
	if len(recs) != 1 || !recs[0].Encrypted {
		t.Fatalf("%+v", recs)
	}
	data = e.ok(e.do("DELETE", lb+"/appstore/installed/demo-t?data=1", nil, local, tok, cf), "卸载")
	if st := e.waitLong(tok, data, time.Minute); st != "success" {
		t.Fatal(st)
	}
	if _, err := os.Stat(row.Dir); err == nil {
		t.Fatal("卸载并删除数据后目录应删除")
	}
	data = e.ok(e.do("POST", lb+"/backup/restore", map[string]any{"recordId": recs[0].ID}, local, tok, cf), "恢复应用")
	if st := e.waitLong(tok, data, 2*time.Minute); st != "success" {
		t.Fatal(st)
	}
	var apps []struct{ Name, State, Version string }
	json.Unmarshal(e.ok(e.do("GET", lb+"/appstore/installed", nil, local, tok, nil), "已安装"), &apps)
	if len(apps) != 1 || apps[0].Name != "demo-t" || apps[0].Version != "1.1.0" || apps[0].State != "running" {
		t.Fatalf("恢复后的应用：%+v", apps)
	}
	if b, _ := os.ReadFile(filepath.Join(row.Dir, ".env")); !strings.Contains(string(b), "GREETING=bye") {
		t.Fatalf("恢复后的参数：%s", b)
	}
	data = e.ok(e.do("DELETE", lb+"/appstore/installed/demo-t?data=1", nil, local, tok, cf), "卸载")
	if st := e.waitLong(tok, data, time.Minute); st != "success" {
		t.Fatal(st)
	}
}
