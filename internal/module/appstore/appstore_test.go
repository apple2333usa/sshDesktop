package appstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/store"
)

const sampleIndex = `{
  "additionalProperties": {"tags": [{"key": "Website", "name": "建站", "sort": 1, "locales": {"zh": "建站", "en": "Website"}}]},
  "apps": [
    {"id": "nginx", "name": "Nginx", "description": "web", "readMe": "# nginx", "icon": "https://x/logo.png",
     "additionalProperties": {"key": "nginx", "tags": ["Website"], "shortDescZh": "高性能 Web 服务器", "type": "website",
       "limit": 1, "architectures": ["amd64", "arm64", "amd64"]},
     "versions": [
       {"name": "1.27.0", "downloadUrl": "https://x/nginx/1.27.0.tar.gz", "additionalProperties": {"formFields": [
         {"envKey": "PANEL_APP_PORT_HTTP", "type": "number", "default": 80, "required": true, "rule": "paramPort",
          "label": {"zh": "HTTP 端口", "en": "HTTP port"}, "edit": true},
         {"envKey": "PASS", "type": "password", "random": true, "labelZh": "密码", "description": {"zh": "管理员密码"}}
       ]}},
       {"name": "1.26.0", "downloadUrl": "https://x/nginx/1.26.0.tar.gz"}
     ]},
    {"id": "wordpress", "name": "WordPress", "additionalProperties": {"key": "wordpress"},
     "versions": [{"name": "6.5", "downloadUrl": "https://x/wp.tar.gz", "additionalProperties": {"formFields": [
       {"envKey": "PANEL_DB_HOST", "type": "apps", "labelZh": "数据库服务"}]}}]},
    {"id": "nokey", "name": "NoKey", "additionalProperties": {}}
  ]
}`

func TestParseIndex(t *testing.T) {
	cat, err := parseIndex("1panel", []byte(sampleIndex))
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Apps) != 3 || len(cat.Tags) != 1 || cat.Tags[0].Name != "建站" {
		t.Fatalf("%+v", cat)
	}
	n := cat.Apps[0]
	if n.Key != "nginx" || n.Description != "高性能 Web 服务器" || len(n.Versions) != 2 || n.Unsupported != "" ||
		len(n.Architectures) != 2 || n.readme != "# nginx" || n.Limit != 1 {
		t.Fatalf("%+v", n)
	}
	f := n.versions[0].Fields
	if f[0].Label != "HTTP 端口" || f[1].Label != "密码" || f[1].Hint != "管理员密码" || !f[0].Edit {
		t.Fatalf("%+v", f)
	}
	if cat.Apps[1].Unsupported == "" {
		t.Fatal("依赖其他应用的应标记为暂不支持")
	}
	if cat.Apps[2].Key != "nokey" || cat.Apps[2].Unsupported == "" {
		t.Fatalf("没有 key 时用 id，且没有版本：%+v", cat.Apps[2])
	}
	views := withDefaults(f)
	if views[0].Value != "80" || len(views[1].Value) != 16 {
		t.Fatalf("%+v", views)
	}
}

// TestRealIndex 用真实的 1Panel 索引检查解析：SSHD_TEST_INDEX=/path/1panel.json
func TestRealIndex(t *testing.T) {
	p := os.Getenv("SSHD_TEST_INDEX")
	if p == "" {
		t.Skip("未设置 SSHD_TEST_INDEX")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := parseIndex("1panel", b)
	if err != nil {
		t.Fatal(err)
	}
	unsupported := 0
	for _, a := range cat.Apps {
		if a.Unsupported != "" {
			unsupported++
		}
	}
	t.Logf("%d 个应用，%d 个分类，%d 个暂不支持", len(cat.Apps), len(cat.Tags), unsupported)
	if len(cat.Apps) < 100 {
		t.Fatal("应用数量不对")
	}
}

func TestValidate(t *testing.T) {
	fields := []Field{
		{EnvKey: "PORT", Type: "number", Rule: "paramPort", Required: true, Label: "端口", Default: 8080},
		{EnvKey: "NAME", Type: "text", Rule: "paramCommon", Label: "名称"},
		{EnvKey: "MODE", Type: "select", Label: "模式", Default: "a", Values: []Option{{Value: "a"}, {Value: "b"}}},
		{EnvKey: "MULTI", Type: "select", Multiple: true, Label: "多选", Values: []Option{{Value: "x"}, {Value: "y"}}},
		{EnvKey: "PW", Type: "password", Rule: "paramComplexity", Label: "密码"},
		{EnvKey: "URL", Type: "text", Rule: "paramExtUrl", Label: "地址"},
		{EnvKey: "DOMAIN", Type: "text", Rule: "domain", Label: "域名"},
		{EnvKey: "RANGE", Type: "text", Rule: "paramPortRange", Label: "范围"},
	}
	out, err := validate(fields, map[string]string{"NAME": "my_app-1", "MULTI": "x,y", "PW": "abcdef", "URL": "https://a", "DOMAIN": "a.example.com", "RANGE": "30000-30010"})
	if err != nil {
		t.Fatal(err)
	}
	if out["PORT"] != "8080" || out["MODE"] != "a" || out["MULTI"] != "x,y" {
		t.Fatalf("%v", out)
	}
	bad := []map[string]string{
		{"PORT": ""}, {"PORT": "70000"}, {"PORT": "abc"}, {"NAME": "-x"}, {"MODE": "c"}, {"MULTI": "x,z"},
		{"PW": "a b c d e f"}, {"PW": "short"}, {"URL": "ftp://a"}, {"DOMAIN": "localhost"}, {"RANGE": "5-1"},
		{"NAME": "a\nb"},
	}
	for _, in := range bad {
		if _, err := validate(fields, in); err == nil {
			t.Errorf("应拒绝 %v", in)
		}
	}
}

func TestEnvRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("# 注释\nSECRET=keep-me\nPORT=1\n"), 0o600)
	vals := map[string]string{"PORT": "8080", "PW": `a'b"c$d\e`, "SP": "a b", "PLAIN": "x", "EMPTY": ""}
	if err := writeEnv(p, vals, []string{"PORT", "PW", "SP", "PLAIN", "EMPTY"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), "# 注释\nSECRET=keep-me\nPORT=8080\n") {
		t.Fatalf("应保留已有行并原地更新：\n%s", b)
	}
	got := readEnv(p)
	for k, v := range vals {
		if got[k] != v {
			t.Errorf("%s = %q，期望 %q", k, got[k], v)
		}
	}
	if got["SECRET"] != "keep-me" {
		t.Fatal("其他行应保留")
	}
	if envQuote("a b") != "'a b'" || envQuote("plain") != "plain" || envQuote(`it's $x`) != `"it's \$x"` {
		t.Fatal("引号规则不对")
	}
}

func TestBindHostIP(t *testing.T) {
	in := []byte(`services:
  web:
    image: nginx
    ports:
      - ${PANEL_APP_PORT_HTTP}:80
      - "127.0.0.1:${X}:81"
      - 8080:80
networks:
  1panel-network:
    external: true
`)
	out, changed, err := bindHostIP(in)
	if err != nil || !changed {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, `"${HOST_IP}:${PANEL_APP_PORT_HTTP}:80"`) || !strings.Contains(s, "127.0.0.1:${X}:81") || !strings.Contains(s, "8080:80") {
		t.Fatal(s)
	}
	if _, changed, _ := bindHostIP(out); changed {
		t.Fatal("不应重复修改")
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "apps", "demo")
	for _, v := range []string{"1.2.0", "1.10.0", "latest"} {
		os.MkdirAll(filepath.Join(app, v), 0o755)
		os.WriteFile(filepath.Join(app, v, "docker-compose.yml"), []byte("services: {}\n"), 0o644)
	}
	os.WriteFile(filepath.Join(app, "data.yml"), []byte(`name: Demo
title: 演示
additionalProperties:
  key: demo
  tags: [Tool]
  shortDescZh: 演示应用
  crossVersionUpdate: true
`), 0o644)
	os.WriteFile(filepath.Join(app, "1.10.0", "data.yml"), []byte(`additionalProperties:
  formFields:
    - envKey: PANEL_APP_PORT_HTTP
      labelZh: 端口
      type: number
      default: 8080
      rule: paramPort
`), 0o644)
	os.WriteFile(filepath.Join(app, "logo.png"), []byte("png"), 0o644)
	os.MkdirAll(filepath.Join(dir, "apps", "broken"), 0o755)
	cat, err := loadDir("local", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Apps) != 1 {
		t.Fatalf("%+v", cat.Apps)
	}
	a := cat.Apps[0]
	if a.Key != "demo" || a.Name != "Demo" || a.Description != "演示应用" || a.iconPath == "" || !a.CrossVersion {
		t.Fatalf("%+v", a)
	}
	if strings.Join(a.Versions, ",") != "latest,1.10.0,1.2.0" {
		t.Fatalf("版本顺序：%v", a.Versions)
	}
	v, _ := a.version("1.10.0")
	if len(v.Fields) != 1 || v.Fields[0].Label != "端口" {
		t.Fatalf("%+v", v.Fields)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.10.0", "1.9.9", 1}, {"1.2", "1.2.0", 0}, {"v2.0", "1.99", 1}, {"1.0.0", "1.0.1", -1}, {"latest", "9.9", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); (got > 0) != (c.want > 0) || (got < 0) != (c.want < 0) {
			t.Errorf("compare(%s,%s)=%d", c.a, c.b, got)
		}
	}
	if major("8.0.36") != "8" || major("v3.1") != "3" {
		t.Fatal("major")
	}
}

// 已有同步在进行时，手动同步等它结束再同步，而不是直接失败（否则刚添加的源不会被同步）。
func TestSyncWaitsForRunningSync(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	app := filepath.Join(dir, "apps", "demo", "1.0.0")
	os.MkdirAll(app, 0o755)
	os.WriteFile(filepath.Join(app, "docker-compose.yml"), []byte("services: {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "apps", "demo", "data.yml"), []byte("name: Demo\nadditionalProperties:\n  key: demo\n"), 0o644)
	st.SetSetting(sourcesKey, []Source{{ID: "1panel", Enabled: false}, {ID: "local", Name: "本机", Type: "dir", URL: dir, Enabled: true}})
	s := New(Deps{Store: st, DataDir: t.TempDir()})

	s.syncMu.Lock() // 模拟正在进行的自动同步
	var logs []string
	done := make(chan error, 1)
	go func() {
		done <- s.syncAll(context.Background(), func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) })
	}()
	select {
	case err := <-done:
		t.Fatalf("应当等待正在进行的同步，却返回了 %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	s.syncMu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("同步没有完成")
	}
	if s.catalogs["local"] == nil || len(s.catalogs["local"].Apps) != 1 {
		t.Fatalf("本机源没有同步：%v", s.catalogs)
	}
	if len(logs) == 0 || !strings.Contains(logs[0], "等它完成") {
		t.Fatalf("logs = %v", logs)
	}
}
