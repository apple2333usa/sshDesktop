package website

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/apple2333usa/sshdesktop/internal/nginx"
	"github.com/apple2333usa/sshdesktop/internal/store"
)

// Paths 是生成站点配置所需的路径和 Nginx 版本信息。
type Paths struct {
	AcmeRoot string // HTTP-01 验证文件目录
	LogDir   string // 站点日志目录
	Cert     string // 证书与私钥（启用 HTTPS 时）
	Key      string
	Version  string // Nginx 版本，决定 http2 写法
	IPv6     bool   // 系统支持 IPv6 时同时监听 [::]
}

// HasIPv6 表示系统支持 IPv6（有的 VPS 关闭了 IPv6，这时 listen [::] 会让 Nginx 启动失败）。
func HasIPv6() bool {
	ln, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

func listen(b *strings.Builder, p Paths, port int, suffix string) {
	fmt.Fprintf(b, "    listen %d%s;\n", port, suffix)
	if p.IPv6 {
		fmt.Fprintf(b, "    listen [::]:%d%s;\n", port, suffix)
	}
}

var (
	reName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	reDomain = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// SplitDomains 拆分域名列表（空格、逗号、换行分隔），转为小写并去重。
func SplitDomains(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\r' || r == '\t' || r == '，' }) {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// ValidDomain 校验一个 server_name：域名、通配符域名或 IP。
func ValidDomain(d string) bool {
	if net.ParseIP(d) != nil || d == "localhost" {
		return true
	}
	return len(d) <= 253 && reDomain.MatchString(d)
}

// SafeName 由域名生成站点名（配置文件名）。
func SafeName(domain string) string {
	n := strings.NewReplacer("*", "wildcard", ":", "_").Replace(domain)
	n = regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(n, "_")
	n = strings.TrimLeft(n, "._-")
	if len(n) > 64 {
		n = n[:64]
	}
	if n == "" {
		n = "site"
	}
	return n
}

// Normalize 校验并补全站点设置。
func Normalize(w *store.Website) error {
	w.Name = strings.TrimSpace(w.Name)
	ds := SplitDomains(w.Domains)
	if len(ds) == 0 {
		return fmt.Errorf("至少填写一个域名")
	}
	for _, d := range ds {
		if !ValidDomain(d) {
			return fmt.Errorf("域名 %q 无效", d)
		}
	}
	w.Domains = strings.Join(ds, " ")
	if w.Name == "" {
		w.Name = SafeName(ds[0])
	}
	if !reName.MatchString(w.Name) {
		return fmt.Errorf("名称只能包含字母、数字、点、下划线和短横线，不超过 64 个字符")
	}
	if w.HTTPPort == 0 {
		w.HTTPPort = 80
	}
	if w.HTTPSPort == 0 {
		w.HTTPSPort = 443
	}
	for _, p := range []int{w.HTTPPort, w.HTTPSPort} {
		if p < 1 || p > 65535 {
			return fmt.Errorf("端口 %d 无效", p)
		}
	}
	if w.HTTPS && w.HTTPPort == w.HTTPSPort {
		return fmt.Errorf("HTTP 和 HTTPS 不能使用同一个端口")
	}
	if w.HTTPS && w.CertID == 0 {
		return fmt.Errorf("启用 HTTPS 时请选择证书")
	}
	if !w.HTTPS {
		w.ForceHTTPS, w.HSTS, w.HTTP2 = false, false, false
	}
	if !w.ForceHTTPS {
		w.HSTS = false
	}
	if strings.TrimSpace(w.Raw) != "" {
		return nil
	}
	switch w.Type {
	case "proxy":
		u, err := url.Parse(strings.TrimSpace(w.ProxyPass))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("后端地址请填写 http:// 或 https:// 开头的地址，例如 http://127.0.0.1:8080")
		}
		w.ProxyPass = strings.TrimSpace(w.ProxyPass)
		if strings.ContainsAny(w.ProxyPass, " ;{}\"'\n") || strings.ContainsAny(w.ProxyHost, " ;{}\"'\n") {
			return fmt.Errorf("后端地址或 Host 包含不允许的字符")
		}
	case "static":
		w.Root = strings.TrimSpace(w.Root)
		if w.Root == "" || !strings.HasPrefix(w.Root, "/") || strings.ContainsAny(w.Root, ";{}\"'\n") {
			return fmt.Errorf("网站目录请填写绝对路径")
		}
		w.Index = strings.Join(strings.Fields(w.Index), " ")
		if w.Index == "" {
			w.Index = "index.html index.htm"
		}
		if strings.ContainsAny(w.Index, ";{}\"'") {
			return fmt.Errorf("默认首页包含不允许的字符")
		}
	default:
		return fmt.Errorf("网站类型只能是 proxy（反向代理）或 static（静态网站）")
	}
	if ds, err := nginx.Parse("server {\n" + w.Custom + "\n}"); err != nil {
		return fmt.Errorf("自定义配置有语法错误：%v", err)
	} else if len(ds) != 1 {
		return fmt.Errorf("自定义配置的大括号没有配对")
	}
	return nil
}

// Render 生成站点的配置文件内容。Raw 不为空时原样使用。
func Render(w *store.Website, p Paths) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 由 sshDesktop 管理：%s。请在面板的“网站”中修改，手动修改会被覆盖。\n", w.Name)
	if strings.TrimSpace(w.Raw) != "" {
		b.WriteString(strings.TrimRight(w.Raw, "\n") + "\n")
		return b.String()
	}
	names := w.Domains
	logs := func(ind string) {
		if w.AccessLog {
			fmt.Fprintf(&b, "%saccess_log %s;\n", ind, nginx.Quote(p.LogDir+"/"+w.Name+".access.log"))
		} else {
			fmt.Fprintf(&b, "%saccess_log off;\n", ind)
		}
		fmt.Fprintf(&b, "%serror_log %s;\n", ind, nginx.Quote(p.LogDir+"/"+w.Name+".error.log"))
	}
	acme := func() {
		fmt.Fprintf(&b, "    location ^~ /.well-known/acme-challenge/ {\n        root %s;\n        default_type text/plain;\n    }\n", nginx.Quote(p.AcmeRoot))
	}
	body := func() {
		switch w.Type {
		case "proxy":
			host := w.ProxyHost
			if host == "" {
				host = "$host"
			}
			b.WriteString("    location / {\n")
			fmt.Fprintf(&b, "        proxy_pass %s;\n", w.ProxyPass)
			fmt.Fprintf(&b, "        proxy_set_header Host %s;\n", host)
			b.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n")
			b.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
			b.WriteString("        proxy_set_header X-Forwarded-Proto $scheme;\n")
			b.WriteString("        proxy_http_version 1.1;\n")
			if w.WebSocket {
				b.WriteString("        proxy_set_header Upgrade $http_upgrade;\n")
				b.WriteString("        proxy_set_header Connection $http_connection;\n")
				b.WriteString("        proxy_read_timeout 3600s;\n")
			}
			if strings.HasPrefix(w.ProxyPass, "https://") {
				b.WriteString("        proxy_ssl_server_name on;\n")
			}
			b.WriteString("    }\n")
		case "static":
			fmt.Fprintf(&b, "    root %s;\n", nginx.Quote(w.Root))
			fmt.Fprintf(&b, "    index %s;\n", w.Index)
			b.WriteString("    location / {\n        try_files $uri $uri/ =404;\n    }\n")
		}
		if c := strings.TrimSpace(w.Custom); c != "" {
			b.WriteString("    # 自定义配置\n")
			for _, l := range strings.Split(c, "\n") {
				b.WriteString("    " + strings.TrimRight(l, " \t\r") + "\n")
			}
		}
	}

	b.WriteString("server {\n")
	listen(&b, p, w.HTTPPort, "")
	fmt.Fprintf(&b, "    server_name %s;\n", names)
	logs("    ")
	acme()
	if w.HTTPS && w.ForceHTTPS {
		target := "https://$host$request_uri"
		if w.HTTPSPort != 443 {
			target = fmt.Sprintf("https://$host:%d$request_uri", w.HTTPSPort)
		}
		fmt.Fprintf(&b, "    location / {\n        return 301 %s;\n    }\n", target)
	} else {
		body()
	}
	b.WriteString("}\n")

	if w.HTTPS {
		b.WriteString("\nserver {\n")
		h2 := w.HTTP2 && nginx.AtLeast(p.Version, 1, 25, 1)
		suffix := ""
		if w.HTTP2 && !h2 {
			suffix = " http2"
		}
		listen(&b, p, w.HTTPSPort, " ssl"+suffix)
		if h2 {
			b.WriteString("    http2 on;\n")
		}
		fmt.Fprintf(&b, "    server_name %s;\n", names)
		logs("    ")
		fmt.Fprintf(&b, "    ssl_certificate %s;\n    ssl_certificate_key %s;\n", nginx.Quote(p.Cert), nginx.Quote(p.Key))
		b.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n")
		b.WriteString("    ssl_session_cache shared:sshdesktop_ssl:10m;\n    ssl_session_timeout 1d;\n")
		if w.HSTS {
			b.WriteString("    add_header Strict-Transport-Security \"max-age=31536000\" always;\n")
		}
		acme()
		body()
		b.WriteString("}\n")
	}
	return b.String()
}
