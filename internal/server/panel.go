package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/config"
	"github.com/apple2333usa/sshdesktop/internal/pending"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

// 面板访问方式：仅本机（HTTP，经 SSH 隧道访问）或公网（域名 + HTTPS）。切换后必须在 60 秒内
// 从新地址确认，否则自动回到原来的方式，避免证书或网络问题导致面板失联。

const panelPendingKind = "panel"

// certLoader 按需读取面板 HTTPS 证书；证书文件更新（续期）后自动使用新证书，不需要重启面板。
type certLoader struct {
	mu      sync.Mutex
	cert    *tls.Certificate
	path    string
	keyPath string
	mtime   time.Time
	checked time.Time
}

func (l *certLoader) get(certPath, keyPath string) (*tls.Certificate, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cert != nil && l.path == certPath && l.keyPath == keyPath && time.Since(l.checked) < 10*time.Second {
		return l.cert, nil
	}
	fi, err := os.Stat(certPath)
	if err != nil {
		if l.cert != nil {
			return l.cert, nil
		}
		return nil, err
	}
	l.checked = time.Now()
	if l.cert != nil && l.path == certPath && l.keyPath == keyPath && fi.ModTime().Equal(l.mtime) {
		return l.cert, nil
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		if l.cert != nil {
			slog.Warn("读取新的面板证书失败，继续使用原证书", "err", err)
			return l.cert, nil
		}
		return nil, err
	}
	l.cert, l.path, l.keyPath, l.mtime = &c, certPath, keyPath, fi.ModTime()
	return l.cert, nil
}

// listen 按当前配置监听，替换正在运行的 HTTP 服务（旧服务在 2 秒后关闭，保证正在返回的响应能送达）。
func (s *Server) listen() error {
	s.httpMu.Lock()
	defer s.httpMu.Unlock()
	cfg := *s.cfg
	srv := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           s.engine,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if cfg.Public() {
		cert, key := cfg.HTTPS.Cert, cfg.HTTPS.Key
		if _, err := s.certs.get(cert, key); err != nil {
			return fmt.Errorf("读取面板证书失败：%w", err)
		}
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return s.certs.get(cert, key)
			},
		}
	}
	// 端口相同时（Linux 上 0.0.0.0 与 127.0.0.1 的同一端口也冲突）必须先关闭旧服务才能监听
	old := s.srv
	if old != nil && portOf(old.Addr) == portOf(srv.Addr) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		old.Shutdown(ctx)
		cancel()
		old = nil
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		if s.srv != nil && old == nil {
			// 旧服务已关闭：尽量恢复原来的监听
			s.srv = nil
		}
		return fmt.Errorf("监听 %s 失败：%w", srv.Addr, err)
	}
	s.srv = srv
	go func() {
		slog.Info("sshDesktop 已启动", "version", s.version, "listen", srv.Addr, "https", cfg.Public())
		var err error
		if srv.TLSConfig != nil {
			err = srv.ServeTLS(ln, "", "")
		} else {
			err = srv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case s.httpErr <- err:
			default:
			}
		}
	}()
	if old != nil {
		go func() {
			time.Sleep(2 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			old.Shutdown(ctx)
		}()
	}
	return nil
}

func portOf(addr string) string {
	_, p, _ := net.SplitHostPort(addr)
	return p
}

// panelCertID 返回面板 HTTPS 使用的证书 ID（证书模块用来标记和阻止删除）。
func (s *Server) panelCertID() uint {
	if s.cfg.Public() {
		return s.cfg.HTTPS.CertID
	}
	return 0
}

type accessReq struct {
	HTTPS  bool   `json:"https"`
	Domain string `json:"domain"`
	CertID uint   `json:"certId"`
	Port   int    `json:"port"`
	// OpenFirewall 在防火墙中放行面板端口
	OpenFirewall bool `json:"openFirewall"`
}

func (s *Server) accessStatus(c *gin.Context) {
	out := gin.H{
		"https": s.cfg.HTTPS.Enabled, "domain": s.cfg.Domain, "certId": s.cfg.HTTPS.CertID, "port": s.cfg.Port,
		"listen": s.cfg.ListenAddr(), "public": s.cfg.Public(), "certPath": s.cfg.HTTPS.Cert,
	}
	if p := s.pending.Get(panelPendingKind); p != nil {
		out["pending"] = p
	}
	api.OK(c, out)
}

// checkCert 校验证书能用于域名：证书与私钥匹配、包含该域名、在有效期内。
func checkCert(certPath, keyPath, domain string) error {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("读取证书失败：%w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return fmt.Errorf("证书不包含域名 %s", domain)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return errors.New("证书不在有效期内")
	}
	return nil
}

// accessChange 切换访问方式（二级确认），之后等待从新地址确认。
func (s *Server) accessChange(c *gin.Context) {
	var req accessReq
	if !api.Bind(c, &req) {
		return
	}
	next := *s.cfg
	if req.Port != 0 {
		next.Port = req.Port
	}
	if req.HTTPS {
		req.Domain = strings.ToLower(strings.TrimSpace(req.Domain))
		if req.Domain == "" || strings.ContainsAny(req.Domain, " /:") {
			api.Fail(c, api.BadRequest("请填写域名"))
			return
		}
		cert, key, err := s.certMod.Files(req.CertID)
		if err != nil {
			api.Fail(c, api.BadRequest(err.Error()))
			return
		}
		if err := checkCert(cert, key, req.Domain); err != nil {
			api.Fail(c, api.BadRequest(err.Error()))
			return
		}
		next.Domain = req.Domain
		next.HTTPS = config.HTTPS{Enabled: true, Cert: cert, Key: key, CertID: req.CertID}
	} else {
		// 域名只在公网 HTTPS 时使用，切回仅本机时一并清掉
		next.Domain, next.HTTPS = "", config.HTTPS{}
	}
	if err := next.Validate(); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	if next.ListenAddr() == s.cfg.ListenAddr() && next.Public() == s.cfg.Public() && next.HTTPS == s.cfg.HTTPS && next.Domain == s.cfg.Domain {
		api.Fail(c, api.BadRequest("访问方式没有变化"))
		return
	}
	target := "仅本机（http://127.0.0.1:" + fmt.Sprint(next.Port) + "）"
	if next.Public() {
		target = fmt.Sprintf("https://%s:%d", next.Domain, next.Port)
	}
	api.Target(c, target)
	if !s.confirm(c) {
		return
	}
	if req.OpenFirewall && next.Public() && s.firewall != nil {
		if err := s.firewall.EnsurePort(c.Request.Context(), next.Port); err != nil {
			slog.Warn("防火墙放行面板端口失败", "err", err)
		}
	}
	files, err := pending.Snap(s.cfg.Path())
	if err != nil {
		api.Fail(c, err)
		return
	}
	u := api.User(c)
	ch := &pending.Change{Kind: panelPendingKind, Title: "修改面板访问方式", UserID: u.UserID, Username: u.Username,
		Detail: []string{"新的访问地址：" + target}, Files: files}
	extra, _ := json.Marshal(gin.H{"url": accessURL(&next)})
	ch.Extra = extra
	// 先返回响应，再切换监听（切换后当前连接可能不再可用）
	res, err := s.pending.Start(ch, 60*time.Second, func() error {
		if err := next.Save(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		api.Fail(c, api.Err(409, api.CodePendingChange, err.Error()))
		return
	}
	*s.cfg = next
	// 新地址往往是另一个域名，浏览器在那里没有登录状态：用一次性令牌确认，确认后再登录
	tok := make([]byte, 24)
	rand.Read(tok)
	s.httpMu.Lock()
	s.accessToken = hex.EncodeToString(tok)
	confirmURL := accessURL(&next) + "?confirm-access=" + s.accessToken
	s.httpMu.Unlock()
	api.OK(c, gin.H{"pending": res, "url": accessURL(&next), "confirmUrl": confirmURL})
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := s.listen(); err != nil {
			slog.Error("切换面板访问方式失败，回到原来的方式", "err", err)
			s.pending.Revert(panelPendingKind, res.ID)
		}
	}()
}

func accessURL(c *config.Config) string {
	if c.Public() {
		if c.Port == 443 {
			return "https://" + c.Domain + "/"
		}
		return fmt.Sprintf("https://%s:%d/", c.Domain, c.Port)
	}
	return fmt.Sprintf("http://127.0.0.1:%d/", c.Port)
}

// accessConfirm 保留新的访问方式：必须从新地址发起，证明新地址可以访问。
func (s *Server) accessConfirm(c *gin.Context) {
	p := s.pending.Get(panelPendingKind)
	if p == nil {
		api.Fail(c, api.Err(404, api.CodePendingChange, pending.ErrNotFound.Error()))
		return
	}
	if s.cfg.Public() != (c.Request.TLS != nil) {
		api.Fail(c, api.BadRequest("请在新的访问地址确认"))
		return
	}
	if err := s.pending.Confirm(panelPendingKind, p.ID); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

// accessConfirmToken 用切换时返回的一次性令牌确认（不需要登录），同样必须从新地址发起。
func (s *Server) accessConfirmToken(c *gin.Context) {
	var req struct {
		Token string `json:"token"`
	}
	if !api.Bind(c, &req) {
		return
	}
	s.httpMu.Lock()
	tok := s.accessToken
	s.httpMu.Unlock()
	p := s.pending.Get(panelPendingKind)
	if p == nil || tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(req.Token)) != 1 {
		api.Fail(c, api.Err(404, api.CodePendingChange, "没有待确认的访问方式修改，可能已经超时回滚"))
		return
	}
	if s.cfg.Public() != (c.Request.TLS != nil) {
		api.Fail(c, api.BadRequest("请在新的访问地址确认"))
		return
	}
	if err := s.pending.Confirm(panelPendingKind, p.ID); err != nil {
		api.Fail(c, err)
		return
	}
	s.httpMu.Lock()
	s.accessToken = ""
	s.httpMu.Unlock()
	s.audit.Write(store.AuditLog{At: time.Now(), UserID: p.UserID, Username: p.Username, Node: "local", Module: "settings",
		Action: "panel-access-confirm", Target: accessURL(s.cfg), Success: true, IP: c.ClientIP()})
	api.OK(c, nil)
}

func (s *Server) accessRevert(c *gin.Context) {
	p := s.pending.Get(panelPendingKind)
	if p == nil {
		api.Fail(c, api.Err(404, api.CodePendingChange, pending.ErrNotFound.Error()))
		return
	}
	api.OK(c, nil)
	go s.pending.Revert(panelPendingKind, p.ID)
}

// panelRestore 在回滚后重新读取配置文件并恢复监听。
func (s *Server) panelRestore(_ *pending.Change) error {
	cfg, err := config.Load(s.cfg.Path())
	if err != nil {
		return err
	}
	*s.cfg = *cfg
	if s.engine == nil { // 启动时恢复：之后由 Run 监听
		return nil
	}
	time.Sleep(300 * time.Millisecond)
	return s.listen()
}

// notifyAll 向所有在线账号推送一条通知（通知中心显示）。
func (s *Server) notifyAll(level, title, body string) {
	s.hub.Publish("notify", gin.H{"level": level, "title": title, "body": body, "at": time.Now().UnixMilli()})
}

// pendingDone 记录自动回滚的审计日志并通知。
func (s *Server) pendingDone(c *pending.Change, result string, err error) {
	if result == "confirmed" && err == nil {
		return
	}
	detail := map[string]string{"timeout": "超时未确认，已自动回滚", "reverted": "已撤销", "recovered": "面板重启前未确认，已回滚", "confirmed": "确认后应用失败"}[result]
	if err != nil {
		detail += "；出错：" + err.Error()
	}
	s.audit.Write(store.AuditLog{At: time.Now(), UserID: c.UserID, Username: c.Username, Node: "local", Module: c.Kind,
		Action: "pending-" + result, Target: c.Title, Success: err == nil, Detail: detail})
	if result == "timeout" || result == "recovered" || err != nil {
		level := "warning"
		if err != nil {
			level = "error"
		}
		s.notifyAll(level, c.Title+"："+detail, strings.Join(c.Detail, "\n"))
	}
}

func (s *Server) registerPanel(v1 *gin.RouterGroup, settings gin.HandlerFunc) {
	a := s.audit.Action
	v1.GET("/panel/access", settings, s.accessStatus)
	v1.PUT("/panel/access", settings, a("settings", "panel-access"), s.accessChange)
	v1.POST("/panel/access/confirm", settings, a("settings", "panel-access-confirm"), s.accessConfirm)
	v1.POST("/panel/access/revert", settings, a("settings", "panel-access-revert"), s.accessRevert)
	v1.POST("/panel/access/confirm-token", s.accessConfirmToken)
	v1.GET("/pending", s.login, func(c *gin.Context) {
		out := []*pending.Change{}
		for _, k := range []string{panelPendingKind, "firewall", "sshd"} {
			if p := s.pending.Get(k); p != nil {
				out = append(out, p)
			}
		}
		api.OK(c, out)
	})
}
