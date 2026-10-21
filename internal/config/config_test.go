package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsListenLocalOnly(t *testing.T) {
	c := Default()
	if got := c.ListenAddr(); got != "127.0.0.1:33333" {
		t.Fatalf("默认应只监听本机，得到 %s", got)
	}
	c.Domain = "panel.example.com"
	c.HTTPS = HTTPS{Enabled: true, Cert: "/c", Key: "/k"}
	if got := c.ListenAddr(); got != "0.0.0.0:33333" {
		t.Fatalf("配置 HTTPS 后应监听所有地址，得到 %s", got)
	}
}

func TestSetSaveLoad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set("port", "70000"); err == nil {
		t.Fatal("无效端口应报错")
	}
	if err := c.Set("https", "maybe"); err == nil {
		t.Fatal("https 只接受布尔值")
	}
	for k, v := range map[string]string{"port": "8443", "domain": "a.example.com", "https.cert": "/c.pem", "https.key": "/k.pem", "https": "true"} {
		if err := c.Set(k, v); err != nil {
			t.Fatal(k, err)
		}
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Public() || c2.Port != 8443 {
		t.Fatalf("保存后读取不一致：%+v", c2)
	}
	if v, _ := c2.Get("https.cert"); v != "/c.pem" {
		t.Fatalf("Get 返回 %q", v)
	}
	c2.Unset("https")
	if c2.Public() {
		t.Fatal("unset https 后应回到仅本机访问")
	}
}

func TestValidateHTTPSNeedsDomainAndCert(t *testing.T) {
	c := Default()
	c.HTTPS.Enabled = true
	if c.Validate() == nil {
		t.Fatal("启用 HTTPS 但没有域名应报错")
	}
	c.Domain = "x.example.com"
	if c.Validate() == nil {
		t.Fatal("启用 HTTPS 但没有证书应报错")
	}
}

func TestSaveKeepsComments(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(p, []byte(`# sshDesktop 配置。修改后运行 systemctl restart sshdesktop 生效。
# 也可以用命令修改：sshdesktop config set port 33333
port: 33333 # 面板端口
dataDir: /var/lib/sshdesktop
logDir: /var/log/sshdesktop
logLevel: info
`), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Set("port", "34567")
	c.Set("domain", "panel.example.com")
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	got := string(b)
	for _, want := range []string{"# sshDesktop 配置。", "# 也可以用命令修改", "port: 34567 # 面板端口", "domain: panel.example.com", "https:\n  enabled: false"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q：\n%s", want, got)
		}
	}
	// 去掉的键从文件中删除，注释仍在
	c.Unset("domain")
	c.Save()
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "domain") || !strings.Contains(string(b), "# sshDesktop 配置。") {
		t.Fatalf("unset 后：\n%s", b)
	}
	c2, err := Load(p)
	if err != nil || c2.Port != 34567 {
		t.Fatalf("重新读取：%v %+v", err, c2)
	}
}
