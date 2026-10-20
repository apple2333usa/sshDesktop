package server

import (
	"encoding/base64"
	"net"
	"net/http"
	"strings"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/audit"
	"github.com/gin-gonic/gin"
)

const cookieName = "sd_session"

func isLoopback(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsLoopback()
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// clientIP 解析真实来源 IP。只有开启“受信任代理”且请求来自本机时才采信 X-Forwarded-For。
func (s *Server) clientIP(r *http.Request) string {
	ip := remoteIP(r)
	if isLoopback(ip) && s.st.Runtime().TrustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if v := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(v) != nil {
				return v
			}
		}
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(v) != nil {
			return v
		}
	}
	return ip
}

// accessGuard 实现访问控制：未配置域名和 HTTPS 时只接受本机连接；配置后校验 Host。
func (s *Server) accessGuard(c *gin.Context) {
	if !s.cfg.Public() {
		if !isLoopback(remoteIP(c.Request)) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	} else {
		host := c.Request.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !strings.EqualFold(host, s.cfg.Domain) {
			c.AbortWithStatus(http.StatusMisdirectedRequest)
			return
		}
	}
	api.SetIP(c, s.clientIP(c.Request))
	c.Next()
}

func securityHeaders(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self' 'wasm-unsafe-eval'",
		// Naive UI 以 CSS-in-JS 方式注入样式，需要内联样式
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"media-src 'self' blob:",
		"font-src 'self' data:",
		"connect-src 'self' ws: wss:",
		"worker-src 'self' blob:",
		"frame-src 'self' blob:",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; "))
	c.Next()
}

// csrfGuard：写请求必须带 X-Requested-With 自定义请求头（跨站表单无法设置自定义头）。
func csrfGuard(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		if c.GetHeader("X-Requested-With") == "" {
			api.Fail(c, api.Forbidden("缺少 X-Requested-With 请求头"))
			return
		}
	}
	c.Next()
}

// authenticate 校验会话 Cookie，失败时输出 401 并返回 false。
func (s *Server) authenticate(c *gin.Context) bool {
	if api.User(c) != nil {
		return true
	}
	token, _ := c.Cookie(cookieName)
	sess, u, err := s.auth.Authenticate(token)
	if err != nil {
		api.Fail(c, api.Err(http.StatusUnauthorized, api.CodeUnauthorized, "未登录或会话已过期"))
		return false
	}
	api.SetUser(c, &api.Principal{UserID: u.ID, Username: u.Username, RoleID: u.RoleID, SessionID: sess.ID})
	return true
}

// login 只要求已登录。
func (s *Server) login(c *gin.Context) {
	if s.authenticate(c) {
		c.Next()
	}
}

// perm 返回“已登录且拥有权限点”的校验中间件。
func (s *Server) perm(p string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.authenticate(c) {
			return
		}
		if !s.checker.Has(api.User(c).RoleID, p) {
			api.Fail(c, api.Forbidden("没有权限："+p))
			return
		}
		c.Next()
	}
}

func (s *Server) isAdmin(c *gin.Context) bool {
	u := api.User(c)
	return u != nil && s.checker.IsAdmin(u.RoleID)
}

// confirm 校验二级危险操作的密码确认：请求头 X-Confirm-Password 为 base64 编码的当前账号密码。
func (s *Server) confirm(c *gin.Context) bool {
	raw := c.GetHeader("X-Confirm-Password")
	if raw == "" {
		api.Fail(c, api.Err(http.StatusPreconditionRequired, api.CodeConfirmRequired, "该操作需要输入当前账号密码确认"))
		return false
	}
	pw, err := base64.StdEncoding.DecodeString(raw)
	u := api.User(c)
	ok := err == nil && s.auth.CheckPassword(u.UserID, string(pw))
	e := audit.FromContext(c, "security", "confirm")
	e.Target, e.Success = api.AuditTarget(c), ok
	if !ok {
		e.Detail = "密码错误"
	}
	s.audit.Write(e)
	if !ok {
		api.Fail(c, api.Err(http.StatusForbidden, api.CodeConfirmFailed, "密码错误，操作已取消"))
		return false
	}
	return true
}

// nodeGuard 校验 :node 参数。
func (s *Server) nodeGuard(c *gin.Context) {
	if _, err := s.nodes.Get(c.Param("node")); err != nil {
		api.Fail(c, api.NotFound("节点不存在"))
		return
	}
	api.SetNode(c, c.Param("node"))
	c.Next()
}
