package server

import (
	"net/http"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/audit"
	"github.com/apple2333usa/sshdesktop/internal/rbac"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

func (s *Server) setCookie(c *gin.Context, token string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: cookieName, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: s.cfg.Public(), SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) handleLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !api.Bind(c, &req) {
		return
	}
	ip := api.IP(c)
	token, sess, u, err := s.auth.Login(req.Username, req.Password, ip, c.Request.UserAgent(), isLoopback(ip))
	e := audit.FromContext(c, "auth", "login")
	e.Username, e.Target = req.Username, req.Username
	if err != nil {
		e.Detail = err.Error()
		s.audit.Write(e)
		api.Fail(c, err)
		return
	}
	e.UserID, e.Success = u.ID, true
	s.audit.Write(e)
	// Cookie 本身保留 30 天，空闲超时由服务端控制
	s.setCookie(c, token, 30*24*3600)
	api.SetUser(c, &api.Principal{UserID: u.ID, Username: u.Username, RoleID: u.RoleID, SessionID: sess.ID})
	s.handleMe(c)
}

func (s *Server) handleLogout(c *gin.Context) {
	if token, err := c.Cookie(cookieName); err == nil {
		if sess, u, err := s.auth.Authenticate(token); err == nil {
			api.SetUser(c, &api.Principal{UserID: u.ID, Username: u.Username, RoleID: u.RoleID, SessionID: sess.ID})
			e := audit.FromContext(c, "auth", "logout")
			e.Success = true
			s.audit.Write(e)
		}
		s.auth.Logout(token)
	}
	s.setCookie(c, "", -1)
	api.OK(c, nil)
}

func (s *Server) handleMe(c *gin.Context) {
	p := api.User(c)
	var u store.User
	if err := s.st.DB.Preload("Role").First(&u, p.UserID).Error; err != nil {
		api.Fail(c, api.Err(http.StatusUnauthorized, api.CodeUnauthorized, "账号不存在"))
		return
	}
	roleName := ""
	if u.Role != nil {
		roleName = u.Role.Name
	}
	api.OK(c, gin.H{
		"user": gin.H{
			"id": u.ID, "username": u.Username, "roleId": u.RoleID, "roleName": roleName,
			"isAdmin": s.checker.IsAdmin(u.RoleID), "lastLoginAt": u.LastLoginAt, "lastLoginIp": u.LastLoginIP,
		},
		"permissions":       s.checker.List(u.RoleID),
		"catalog":           rbac.Catalog,
		"defaultAppearance": s.st.Runtime().DefaultAppearance,
		"version":           s.version,
	})
}

func (s *Server) handleChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if !api.Bind(c, &req) {
		return
	}
	p := api.User(c)
	api.Target(c, p.Username)
	if !s.auth.CheckPassword(p.UserID, req.OldPassword) {
		api.Fail(c, api.Err(http.StatusBadRequest, api.CodeBadCredentials, "当前密码错误"))
		return
	}
	// 修改密码会注销该账号的其他会话，保留当前会话
	if err := s.auth.SetPassword(p.UserID, req.NewPassword, p.SessionID); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}
