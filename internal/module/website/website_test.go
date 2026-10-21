package website

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/nginx"
	"github.com/apple2333usa/sshdesktop/internal/store"
)

func TestNormalize(t *testing.T) {
	w := store.Website{Type: "proxy", Domains: "A.test, www.a.test a.test", ProxyPass: " http://127.0.0.1:8080 "}
	if err := Normalize(&w); err != nil {
		t.Fatal(err)
	}
	if w.Domains != "a.test www.a.test" || w.Name != "a.test" || w.HTTPPort != 80 || w.HTTPSPort != 443 || w.ProxyPass != "http://127.0.0.1:8080" {
		t.Fatalf("%+v", w)
	}
	bad := []store.Website{
		{Type: "proxy", Domains: "", ProxyPass: "http://x"},
		{Type: "proxy", Domains: "a_b.test", ProxyPass: "http://x"},
		{Type: "proxy", Domains: "a..test", ProxyPass: "http://x"},
		{Type: "proxy", Domains: "a.test", ProxyPass: "127.0.0.1:8080"},
		{Type: "proxy", Domains: "a.test", ProxyPass: "http://x;evil"},
		{Type: "static", Domains: "a.test", Root: "relative"},
		{Type: "static", Domains: "a.test", Root: "/var/www/a", Index: "a;b"},
		{Type: "proxy", Domains: "a.test", ProxyPass: "http://x", HTTPS: true},
		{Type: "proxy", Domains: "a.test", ProxyPass: "http://x", HTTPS: true, CertID: 1, HTTPPort: 443},
		{Type: "proxy", Domains: "a.test", ProxyPass: "http://x", Custom: "} server { listen 81;"},
		{Type: "proxy", Domains: "a.test", ProxyPass: "http://x", Custom: "location / {"},
		{Type: "proxy", Name: "../x", Domains: "a.test", ProxyPass: "http://x"},
		{Type: "php", Domains: "a.test"},
	}
	for i, w := range bad {
		if err := Normalize(&w); err == nil {
			t.Errorf("第 %d 个应该校验失败：%+v", i, w)
		}
	}
	// 未启用 HTTPS 时强制跳转、HSTS、HTTP/2 自动关闭
	w = store.Website{Type: "static", Domains: "*.a.test", Root: "/var/www/a", ForceHTTPS: true, HSTS: true, HTTP2: true}
	if err := Normalize(&w); err != nil || w.ForceHTTPS || w.HSTS || w.HTTP2 || w.Index != "index.html index.htm" || w.Name != "wildcard.a.test" {
		t.Fatalf("%v %+v", err, w)
	}
}

func TestRender(t *testing.T) {
	w := store.Website{Name: "a", Type: "proxy", Domains: "a.test www.a.test", ProxyPass: "https://backend.test", WebSocket: true,
		HTTPPort: 80, HTTPSPort: 443, HTTPS: true, CertID: 1, ForceHTTPS: true, HSTS: true, HTTP2: true, AccessLog: true,
		Custom: "client_max_body_size 1g;"}
	p := Paths{AcmeRoot: "/var/www/sshdesktop/acme", LogDir: "/var/www/sshdesktop/logs", Cert: "/c.pem", Key: "/k.pem", Version: "1.24.0"}
	out := Render(&w, p)
	for _, want := range []string{
		"listen 80;", "listen 443 ssl http2;", "server_name a.test www.a.test;", "return 301 https://$host$request_uri;",
		"ssl_certificate /c.pem;", "Strict-Transport-Security", "proxy_pass https://backend.test;", "proxy_ssl_server_name on;",
		"proxy_set_header Upgrade $http_upgrade;", "client_max_body_size 1g;", "access_log /var/www/sshdesktop/logs/a.access.log;",
		"location ^~ /.well-known/acme-challenge/",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q：\n%s", want, out)
		}
	}
	if strings.Contains(out, "[::]") || strings.Contains(out, "http2 on;") {
		t.Errorf("没有 IPv6、Nginx 1.24 时不应出现 [::] 和 http2 on：\n%s", out)
	}
	if _, err := nginx.Parse(out); err != nil {
		t.Fatalf("生成的配置无法解析：%v\n%s", err, out)
	}
	p.IPv6, p.Version = true, "1.31.1.1"
	out = Render(&w, p)
	if !strings.Contains(out, "listen [::]:443 ssl;") || !strings.Contains(out, "http2 on;") {
		t.Errorf("新版本应使用 http2 on 并监听 IPv6：\n%s", out)
	}
	w.Raw = "server { listen 8080; }"
	if out := Render(&w, p); !strings.Contains(out, w.Raw) || strings.Contains(out, "proxy_pass") {
		t.Errorf("Raw 应原样写入：\n%s", out)
	}
}

func servers(t *testing.T, content string) []Server {
	ds, err := nginx.Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	var out []Server
	for _, d := range ds {
		if d.Name == "server" {
			out = append(out, analyze("/etc/nginx/sites-enabled/x", d))
		}
	}
	return out
}

func TestConvert(t *testing.T) {
	src := `server {
    listen 80;
    server_name a.test;
    return 301 https://$host$request_uri;
}
server {
    listen 443 ssl http2;
    server_name a.test;
    ssl_certificate /etc/letsencrypt/live/a.test/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/a.test/privkey.pem;
    include /etc/letsencrypt/options-ssl-nginx.conf;
    client_max_body_size 50m;
    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
`
	sv := servers(t, src)
	w, reason := convert("/etc/nginx/sites-enabled/x", sv, src)
	if w == nil {
		t.Fatalf("应可以接管：%s %+v", reason, sv)
	}
	if w.Type != "proxy" || w.ProxyPass != "http://127.0.0.1:3000" || !w.HTTPS || !w.ForceHTTPS || !w.WebSocket || w.Domains != "a.test" {
		t.Fatalf("%+v", w)
	}
	if !strings.Contains(w.Custom, "client_max_body_size 50m;") {
		t.Fatalf("未识别的简单指令应放到自定义配置：%q", w.Custom)
	}

	static := "server {\n listen 80;\n server_name b.test;\n root /var/www/b;\n index index.html;\n location / { try_files $uri $uri/ =404; }\n}\n"
	if w, reason := convert("x", servers(t, static), static); w == nil || w.Type != "static" || w.Root != "/var/www/b" {
		t.Fatalf("静态站点应可以接管：%s %+v", reason, w)
	}

	for name, src := range map[string]string{
		"php":     "server {\n listen 80;\n server_name c.test;\n location ~ \\.php$ { fastcgi_pass unix:/run/php.sock; }\n}\n",
		"default": "server {\n listen 80 default_server;\n server_name _;\n root /var/www/html;\n}\n",
		"three":   "server { listen 80; server_name a.test; root /a; }\nserver { listen 81; server_name a.test; root /a; }\nserver { listen 82; server_name a.test; root /a; }\n",
		"names":   "server { listen 80; server_name a.test; root /a; }\nserver { listen 443 ssl; server_name b.test; root /a; }\n",
	} {
		if w, _ := convert("x", servers(t, src), src); w != nil {
			t.Errorf("%s 不应自动接管：%+v", name, w)
		}
	}
}

// TestRenderNginx 用本机的 nginx -t 校验生成的配置（没有安装 nginx 时跳过）。
func TestRenderNginx(t *testing.T) {
	bin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("没有安装 nginx")
	}
	dir := t.TempDir()
	cert, key := selfSigned(t, dir)
	rt, err := nginx.DetectHost(t.Context(), bin)
	if err != nil {
		t.Skip(err)
	}
	p := Paths{AcmeRoot: dir, LogDir: dir, Cert: cert, Key: key, Version: rt.Version, IPv6: HasIPv6()}
	var confs []string
	for _, w := range []store.Website{
		{Name: "p", Type: "proxy", Domains: "p.test", ProxyPass: "http://127.0.0.1:3000", WebSocket: true, HTTPS: true, CertID: 1, ForceHTTPS: true, HSTS: true, HTTP2: true, AccessLog: true},
		{Name: "s", Type: "static", Domains: "s.test *.s.test", Root: dir, Custom: "location /api/ {\n    return 404;\n}"},
		{Name: "h", Type: "proxy", Domains: "h.test", ProxyPass: "https://example.com", ProxyHost: "example.com", HTTPS: true, CertID: 1, HTTP2: true},
	} {
		if err := Normalize(&w); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(dir, w.Name+".conf")
		os.WriteFile(f, []byte(Render(&w, p)), 0o644)
		confs = append(confs, f)
	}
	// 非 root 用户测试时用高端口
	main := "pid " + filepath.Join(dir, "nginx.pid") + ";\nerror_log " + filepath.Join(dir, "error.log") + ";\nevents {}\nhttp {\n"
	for _, f := range confs {
		b, _ := os.ReadFile(f)
		s := strings.NewReplacer("listen 80", "listen 18080", "listen [::]:80", "listen [::]:18080",
			"listen 443", "listen 18443", "listen [::]:443", "listen [::]:18443").Replace(string(b))
		os.WriteFile(f, []byte(s), 0o644)
		main += "include " + f + ";\n"
	}
	main += "}\n"
	conf := filepath.Join(dir, "nginx.conf")
	os.WriteFile(conf, []byte(main), 0o644)
	out, err := exec.Command(bin, "-t", "-p", dir, "-c", conf).CombinedOutput()
	if err != nil {
		t.Fatalf("nginx -t 失败：%s", out)
	}
}

func selfSigned(t *testing.T, dir string) (string, string) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "p.test"}, DNSNames: []string{"p.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	cert, key := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return cert, key
}
