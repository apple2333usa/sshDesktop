package website

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/nginx"
	"github.com/apple2333usa/sshdesktop/internal/pending"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

// 已有站点：宿主机 Nginx 中不是面板生成的 server 块。结构简单的可以“接管”为面板站点（转换成表单），
// 用到复杂指令的只能按原始配置编辑，并给出兼容性提醒。

// Server 是已有配置中的一个 server 块。
type Server struct {
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Names       []string `json:"names"`
	Listen      []string `json:"listen"`
	SSL         bool     `json:"ssl"`
	Root        string   `json:"root,omitempty"`
	ProxyPass   string   `json:"proxyPass,omitempty"`
	Redirect    string   `json:"redirect,omitempty"` // 整站跳转（return 301/302）
	Cert        string   `json:"cert,omitempty"`
	Key         string   `json:"key,omitempty"`
	Simple      bool     `json:"simple"`
	Reasons     []string `json:"reasons"` // 不能用表单管理的原因
	Certbot     bool     `json:"certbot"` // 使用 certbot 的证书
	DefaultSite bool     `json:"defaultSite"`

	d *nginx.Directive
}

// ExistingFile 是包含已有站点的配置文件。
type ExistingFile struct {
	Path    string   `json:"path"`
	Servers []Server `json:"servers"`
	// Takeover 不为空表示整个文件可以接管为一个面板站点；否则 Reason 说明原因
	Takeover *store.Website `json:"takeover,omitempty"`
	Reason   string         `json:"reason,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// 表单能表示的 server 级指令；其中 serverExtra 会原样放进“自定义配置”。
var (
	serverKnown = map[string]bool{
		"listen": true, "server_name": true, "root": true, "index": true, "location": true,
		"ssl_certificate": true, "ssl_certificate_key": true, "access_log": true, "error_log": true,
		"return": true, "http2": true, "ssl_protocols": true, "ssl_ciphers": true, "ssl_prefer_server_ciphers": true,
		"ssl_session_cache": true, "ssl_session_timeout": true, "ssl_session_tickets": true, "ssl_dhparam": true,
		"ssl_stapling": true, "ssl_stapling_verify": true, "include": true, "add_header": true,
	}
	serverExtra = map[string]bool{
		"client_max_body_size": true, "charset": true, "gzip": true, "gzip_types": true, "gzip_min_length": true,
		"gzip_comp_level": true, "gzip_vary": true, "error_page": true, "autoindex": true, "keepalive_timeout": true,
		"proxy_read_timeout": true, "proxy_connect_timeout": true, "proxy_send_timeout": true, "proxy_buffering": true,
	}
	locationKnown = map[string]bool{
		"proxy_pass": true, "proxy_set_header": true, "proxy_http_version": true, "try_files": true, "root": true,
		"index": true, "proxy_read_timeout": true, "proxy_redirect": true, "proxy_ssl_server_name": true, "return": true,
		"default_type": true,
	}
	// 证书相关的 include（certbot 的 options-ssl-nginx.conf 等），接管后由面板的 SSL 设置代替
	sslInclude = []string{"/etc/letsencrypt/options-ssl-nginx.conf", "snippets/ssl-params.conf"}
)

func analyze(file string, d *nginx.Directive) Server {
	sv := Server{File: file, Line: d.Line, d: d, Reasons: []string{}}
	addReason := func(r string) {
		for _, x := range sv.Reasons {
			if x == r {
				return
			}
		}
		sv.Reasons = append(sv.Reasons, r)
	}
	for _, c := range d.Block {
		switch c.Name {
		case "server_name":
			sv.Names = append(sv.Names, c.Args...)
		case "listen":
			sv.Listen = append(sv.Listen, strings.Join(c.Args, " "))
			for _, a := range c.Args {
				if a == "ssl" {
					sv.SSL = true
				}
				if a == "default_server" || a == "default" {
					sv.DefaultSite = true
				}
			}
		case "root":
			if len(c.Args) == 1 {
				sv.Root = c.Args[0]
			}
		case "ssl_certificate":
			if len(c.Args) == 1 {
				sv.Cert = c.Args[0]
			}
		case "ssl_certificate_key":
			if len(c.Args) == 1 {
				sv.Key = c.Args[0]
			}
		case "return":
			if len(c.Args) == 2 && (c.Args[0] == "301" || c.Args[0] == "302" || c.Args[0] == "308") {
				sv.Redirect = c.Args[1]
			} else {
				addReason("return " + strings.Join(c.Args, " "))
			}
		case "include":
			ok := false
			for _, s := range sslInclude {
				if len(c.Args) == 1 && strings.HasSuffix(c.Args[0], s) {
					ok = true
				}
			}
			if !ok {
				addReason("include " + strings.Join(c.Args, " "))
			}
		case "location":
			path := strings.Join(c.Args, " ")
			switch path {
			case "/", "= /":
			case "^~ /.well-known/acme-challenge/", "/.well-known/acme-challenge/", "~ /.well-known/acme-challenge", "^~ /.well-known/acme-challenge":
				continue
			default:
				addReason("location " + path)
				continue
			}
			for _, l := range c.Block {
				if !locationKnown[l.Name] {
					addReason("location " + path + " 中的 " + l.Name)
					continue
				}
				switch l.Name {
				case "proxy_pass":
					if len(l.Args) == 1 {
						sv.ProxyPass = l.Args[0]
					}
				case "root":
					if len(l.Args) == 1 {
						sv.Root = l.Args[0]
					}
				case "return":
					if len(l.Args) == 2 && strings.HasPrefix(l.Args[0], "30") {
						sv.Redirect = l.Args[1]
					} else {
						addReason("return " + strings.Join(l.Args, " "))
					}
				}
			}
		default:
			if !serverKnown[c.Name] && !serverExtra[c.Name] {
				addReason(c.Name)
			}
		}
	}
	if strings.Contains(sv.Cert, "/etc/letsencrypt/") {
		sv.Certbot = true
	}
	if sv.ProxyPass != "" && strings.Contains(sv.ProxyPass, "$") {
		addReason("proxy_pass 使用了变量")
	}
	if sv.ProxyPass != "" && !strings.HasPrefix(sv.ProxyPass, "http://") && !strings.HasPrefix(sv.ProxyPass, "https://") {
		addReason("proxy_pass 指向 upstream " + sv.ProxyPass)
	}
	sv.Simple = len(sv.Reasons) == 0
	return sv
}

func listenPort(listen string, def int) int {
	f := strings.Fields(listen)
	if len(f) == 0 {
		return def
	}
	a := f[0]
	if i := strings.LastIndex(a, ":"); i >= 0 {
		a = a[i+1:]
	}
	if n, err := strconv.Atoi(a); err == nil {
		return n
	}
	return def
}

// convert 尝试把一个文件中的 server 块转换成面板站点：一个 HTTP 块，加上可选的同名 HTTPS 块。
func convert(file string, servers []Server, content string) (*store.Website, string) {
	if len(servers) == 0 || len(servers) > 2 {
		return nil, "文件中有多个站点，只能按原始配置编辑"
	}
	for _, sv := range servers {
		if !sv.Simple {
			return nil, "用到了表单无法表示的配置：" + strings.Join(sv.Reasons, "、")
		}
		if sv.DefaultSite {
			return nil, "这是默认站点（default_server），保持原样"
		}
	}
	names := strings.Join(servers[0].Names, " ")
	if len(servers) == 2 && strings.Join(servers[1].Names, " ") != names {
		return nil, "两个 server 块的域名不一致"
	}
	for _, n := range servers[0].Names {
		if !ValidDomain(n) {
			return nil, "域名 " + n + " 无法用表单表示"
		}
	}
	if names == "" {
		return nil, "没有 server_name"
	}
	var plain, tls *Server
	for i := range servers {
		if servers[i].SSL {
			if tls != nil {
				return nil, "有两个 HTTPS 块"
			}
			tls = &servers[i]
		} else {
			if plain != nil {
				return nil, "有两个 HTTP 块"
			}
			plain = &servers[i]
		}
	}
	w := &store.Website{Domains: names, Enabled: true, AccessLog: true, HTTPPort: 80, HTTPSPort: 443}
	w.Name = SafeName(servers[0].Names[0])
	main := plain
	if tls != nil {
		w.HTTPS = true
		w.HTTPSPort = listenPort(tls.Listen[0], 443)
		main = tls
		if tls.Cert == "" || tls.Key == "" {
			return nil, "HTTPS 块没有证书"
		}
		for _, l := range tls.Listen {
			if strings.Contains(l, "http2") {
				w.HTTP2 = true
			}
		}
		if d := tls.d.Find("http2"); d != nil && len(d.Args) == 1 && d.Args[0] == "on" {
			w.HTTP2 = true
		}
		for _, h := range tls.d.All("add_header") {
			if len(h.Args) > 0 && strings.EqualFold(h.Args[0], "Strict-Transport-Security") {
				w.HSTS = true
			}
		}
	}
	if plain != nil {
		w.HTTPPort = listenPort(plain.Listen[0], 80)
		if tls != nil && plain.Redirect != "" && strings.HasPrefix(plain.Redirect, "https://") {
			w.ForceHTTPS = true
		}
	} else if tls == nil {
		return nil, "没有监听端口"
	}
	if main.Redirect != "" {
		return nil, "整站跳转到 " + main.Redirect + "，不是网站"
	}
	if plain != nil && tls != nil && !w.ForceHTTPS && (plain.ProxyPass != tls.ProxyPass || plain.Root != tls.Root) {
		return nil, "HTTP 和 HTTPS 的内容不同"
	}
	if !w.ForceHTTPS {
		w.HSTS = false
	}
	switch {
	case main.ProxyPass != "":
		w.Type, w.ProxyPass = "proxy", main.ProxyPass
		for _, loc := range main.d.All("location") {
			if strings.Join(loc.Args, " ") != "/" {
				continue
			}
			for _, h := range loc.All("proxy_set_header") {
				if len(h.Args) == 2 && strings.EqualFold(h.Args[0], "Host") && h.Args[1] != "$host" && h.Args[1] != "$http_host" {
					w.ProxyHost = h.Args[1]
				}
				if len(h.Args) == 2 && strings.EqualFold(h.Args[0], "Upgrade") {
					w.WebSocket = true
				}
			}
		}
	case main.Root != "":
		w.Type, w.Root = "static", main.Root
		if idx := main.d.Find("index"); idx != nil {
			w.Index = strings.Join(idx.Args, " ")
		}
	default:
		return nil, "既不是反向代理也没有网站目录"
	}
	if al := main.d.Find("access_log"); al != nil && len(al.Args) > 0 && al.Args[0] == "off" {
		w.AccessLog = false
	}
	// 表单之外的简单指令放进自定义配置
	var extra []string
	for _, c := range main.d.Block {
		if serverExtra[c.Name] {
			extra = append(extra, strings.TrimSpace(content[c.Start:c.End]))
		}
		if c.Name == "add_header" && !(len(c.Args) > 0 && strings.EqualFold(c.Args[0], "Strict-Transport-Security")) {
			extra = append(extra, strings.TrimSpace(content[c.Start:c.End]))
		}
	}
	w.Custom = strings.Join(extra, "\n")
	w.TakeoverFrom = file
	w.Remark = "接管自 " + file
	return w, ""
}

func hashOf(b []byte) string {
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:8])
}

// Existing 列出宿主机 Nginx 中不是面板生成的站点。
func (s *Service) Existing(ctx context.Context) ([]ExistingFile, error) {
	rt, err := s.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	if rt.Mode != nginx.ModeHost {
		return []ExistingFile{}, nil
	}
	files, err := rt.Dump(ctx)
	if err != nil {
		return nil, err
	}
	out := []ExistingFile{}
	for _, f := range files {
		if strings.HasPrefix(f.Path, rt.SiteDir+"/") || f.Path == s.stubPath(rt) {
			continue
		}
		// 从磁盘读取，保证偏移与文件一致
		b, err := os.ReadFile(f.Path)
		if err != nil {
			continue
		}
		ef := ExistingFile{Path: f.Path, Servers: []Server{}}
		ds, err := nginx.Parse(string(b))
		if err != nil {
			ef.Error = err.Error()
			out = append(out, ef)
			continue
		}
		nginx.Walk(ds, func(d *nginx.Directive, parents []*nginx.Directive) {
			if d.Name == "server" && d.IsBlock() && (len(parents) == 0 || parents[len(parents)-1].Name == "http") {
				ef.Servers = append(ef.Servers, analyze(f.Path, d))
			}
		})
		if len(ef.Servers) == 0 {
			continue
		}
		if f.Path == rt.Conf {
			ef.Reason = "站点写在主配置中，只能按原始配置编辑"
		} else if w, reason := convert(f.Path, ef.Servers, string(b)); w != nil {
			ef.Takeover = w
		} else {
			ef.Reason = reason
		}
		out = append(out, ef)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (s *Service) existing(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	list, err := s.Existing(ctx)
	if err != nil {
		api.Fail(c, asAPI(err))
		return
	}
	api.OK(c, list)
}

// allowedFile 校验路径是生效配置中的文件（只允许编辑 Nginx 实际加载的配置），返回 Runtime。
func (s *Service) allowedFile(ctx context.Context, path string) (nginx.Runtime, error) {
	rt, err := s.Runtime(ctx)
	if err != nil {
		return rt, err
	}
	if rt.Mode != nginx.ModeHost {
		return rt, api.BadRequest("只有接管的宿主机 Nginx 才有已有配置")
	}
	files, err := rt.Dump(ctx)
	if err != nil {
		return rt, asAPI(err)
	}
	if !nginx.Included(files, filepath.Clean(path)) {
		return rt, api.BadRequest("只能编辑 Nginx 正在使用的配置文件")
	}
	if strings.HasPrefix(path, rt.SiteDir+"/") {
		return rt, api.BadRequest("面板站点请在网站列表中修改")
	}
	return rt, nil
}

func (s *Service) existingFile(c *gin.Context) {
	path := c.Query("path")
	if _, err := s.allowedFile(c.Request.Context(), path); err != nil {
		api.Fail(c, err)
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, gin.H{"path": path, "content": string(b), "hash": hashOf(b)})
}

// existingSave 按原始文本保存已有配置文件：先备份原文件，nginx -t 不通过时还原。
func (s *Service) existingSave(c *gin.Context) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Hash    string `json:"hash"`
	}
	if !api.Bind(c, &req) {
		return
	}
	api.Target(c, req.Path)
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	rt, err := s.allowedFile(ctx, req.Path)
	if err != nil {
		api.Fail(c, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := os.ReadFile(req.Path)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if hashOf(cur) != req.Hash {
		api.Fail(c, api.Conflict(api.CodeFileModified, "文件已被修改，请刷新后再编辑"))
		return
	}
	if _, err := s.backupFile(req.Path); err != nil {
		api.Fail(c, err)
		return
	}
	if err := s.commit(ctx, rt, map[string][]byte{req.Path: []byte(req.Content)}); err != nil {
		api.Fail(c, asAPI(err))
		return
	}
	api.OK(c, gin.H{"hash": hashOf([]byte(req.Content))})
}

// takeover 把一个已有配置文件接管为面板站点：原文件移到 takeover-backup（可还原），由面板生成新配置。
func (s *Service) takeover(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}
	if !api.Bind(c, &req) {
		return
	}
	api.Target(c, req.Path)
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	if _, err := s.allowedFile(ctx, req.Path); err != nil {
		api.Fail(c, err)
		return
	}
	list, err := s.Existing(ctx)
	if err != nil {
		api.Fail(c, asAPI(err))
		return
	}
	var ef *ExistingFile
	for i := range list {
		if list[i].Path == req.Path {
			ef = &list[i]
		}
	}
	if ef == nil || ef.Takeover == nil {
		reason := "文件中没有站点"
		if ef != nil {
			reason = ef.Reason
		}
		api.Fail(c, api.BadRequest("不能接管："+reason))
		return
	}
	w := *ef.Takeover
	for _, sv := range ef.Servers {
		if sv.SSL {
			id, err := s.d.ImportCert(sv.Cert, sv.Key)
			if err != nil {
				api.Fail(c, api.BadRequest("登记证书失败："+err.Error()))
				return
			}
			w.CertID = id
		}
	}
	// 名称冲突时加序号
	base := w.Name
	for i := 2; ; i++ {
		var n int64
		s.d.Store.DB.Model(&store.Website{}).Where("name = ?", w.Name).Count(&n)
		if n == 0 {
			break
		}
		w.Name = fmt.Sprintf("%s-%d", base, i)
	}
	if err := s.doTakeover(ctx, &w); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, s.view(nil, w))
}

func (s *Service) doTakeover(ctx context.Context, w *store.Website) error {
	if err := Normalize(w); err != nil {
		return api.BadRequest(err.Error())
	}
	if err := s.conflict(w); err != nil {
		return api.BadRequest(err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, err := s.Runtime(ctx)
	if err != nil {
		return err
	}
	if err := s.ensureDirs(); err != nil {
		return err
	}
	path := w.TakeoverFrom
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	// 软链接（如 sites-enabled 指向 sites-available）只删除链接本身；普通文件移到备份目录
	bak := filepath.Join(s.takeoverDir(), path)
	if err := os.MkdirAll(filepath.Dir(bak), 0o700); err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		w.TakeoverBak = "link:" + target
	} else {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(bak, b, 0o600); err != nil {
			return err
		}
		w.TakeoverBak = bak
	}
	ch, err := s.siteChanges(rt, w, nil)
	if err != nil {
		return err
	}
	ch[path] = nil
	if err := s.commit(ctx, rt, ch); err != nil {
		if target, ok := strings.CutPrefix(w.TakeoverBak, "link:"); ok {
			os.Remove(path)
			os.Symlink(target, path)
		}
		return asAPI(err)
	}
	return s.d.Store.DB.Save(w).Error
}

// untakeover 还原接管前的原配置文件，并删除面板站点。
func (s *Service) untakeover(c *gin.Context) {
	w, ok := s.site(c)
	if !ok {
		return
	}
	if w.TakeoverFrom == "" || w.TakeoverBak == "" {
		api.Fail(c, api.BadRequest("这个网站不是接管来的"))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, err := s.Runtime(ctx)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if _, err := os.Lstat(w.TakeoverFrom); err == nil {
		api.Fail(c, api.BadRequest(w.TakeoverFrom+" 已经存在，不能还原"))
		return
	}
	ch, _ := s.siteChanges(rt, nil, &w)
	snap, _ := pending.Snap(w.TakeoverFrom)
	if target, ok := strings.CutPrefix(w.TakeoverBak, "link:"); ok {
		err = os.Symlink(target, w.TakeoverFrom)
	} else {
		var b []byte
		if b, err = os.ReadFile(w.TakeoverBak); err == nil {
			err = pending.WriteFile(w.TakeoverFrom, b, 0o644)
		}
	}
	if err != nil {
		api.Fail(c, err)
		return
	}
	if err := s.commit(ctx, rt, ch); err != nil {
		pending.Restore(snap)
		api.Fail(c, asAPI(err))
		return
	}
	s.d.Store.DB.Delete(&w)
	api.OK(c, nil)
}
