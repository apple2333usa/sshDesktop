package config

import (
	"path/filepath"
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
