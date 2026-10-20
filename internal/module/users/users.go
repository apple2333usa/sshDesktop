// Package users 是账号与权限管理：账号、角色、在线会话、IP 封禁。
package users

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/auth"
	"github.com/apple2333usa/sshdesktop/internal/rbac"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Deps struct {
	Store   *store.Store
	Auth    *auth.Service
	Checker *rbac.Checker
	Audit   func(module, action string) gin.HandlerFunc
	// OnRevoke 在账号被停用、删除或改密码时调用，用于断开其实时连接和终端。
	OnRevoke func(userID uint, closeTerminals bool)
}

type Service struct{ d Deps }

func New(d Deps) *Service { return &Service{d: d} }

func (s *Service) Register(r *gin.RouterGroup, manage gin.HandlerFunc) {
	a := s.d.Audit
	r.GET("/permissions", manage, s.permissions)
	r.GET("/users", manage, s.listUsers)
	r.POST("/users", manage, a("users", "create"), s.createUser)
	r.PUT("/users/:id", manage, a("users", "update"), s.updateUser)
	r.POST("/users/:id/password", manage, a("users", "reset-password"), s.resetPassword)
	r.DELETE("/users/:id", manage, a("users", "delete"), s.deleteUser)
	r.GET("/roles", manage, s.listRoles)
	r.POST("/roles", manage, a("roles", "create"), s.createRole)
	r.PUT("/roles/:id", manage, a("roles", "update"), s.updateRole)
	r.DELETE("/roles/:id", manage, a("roles", "delete"), s.deleteRole)
	r.GET("/sessions", manage, s.listSessions)
	r.DELETE("/sessions/:id", manage, a("users", "kick-session"), s.kickSession)
	r.GET("/bans", manage, s.listBans)
	r.DELETE("/bans/:ip", manage, a("users", "unban"), s.unban)
}

func (s *Service) db() *gorm.DB { return s.d.Store.DB }

func idParam(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		api.Fail(c, api.BadRequest("ID 无效"))
		return 0, false
	}
	return uint(id), true
}

func (s *Service) permissions(c *gin.Context) { api.OK(c, rbac.Catalog) }

type userView struct {
	store.User
	RoleName string `json:"roleName"`
	IsAdmin  bool   `json:"isAdmin"`
}

func (s *Service) listUsers(c *gin.Context) {
	var list []store.User
	s.db().Preload("Role").Order("id").Find(&list)
	out := make([]userView, 0, len(list))
	for _, u := range list {
		v := userView{User: u}
		if u.Role != nil {
			v.RoleName, v.IsAdmin = u.Role.Name, u.Role.IsAdmin
		}
		v.Role = nil
		out = append(out, v)
	}
	api.OK(c, out)
}

func (s *Service) roleExists(id uint) bool {
	var n int64
	s.db().Model(&store.Role{}).Where("id = ?", id).Count(&n)
	return n > 0
}

func (s *Service) createUser(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		RoleID   uint   `json:"roleId"`
	}
	if !api.Bind(c, &req) {
		return
	}
	api.Target(c, req.Username)
	if !s.roleExists(req.RoleID) {
		api.Fail(c, api.BadRequest("角色不存在"))
		return
	}
	u, err := auth.CreateUser(s.db(), req.Username, req.Password, req.RoleID)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, u)
}

// adminCount 返回启用中的管理员账号数（排除 exceptID）。
func (s *Service) adminCount(exceptID uint) int64 {
	var n int64
	s.db().Model(&store.User{}).Joins("JOIN roles ON roles.id = users.role_id").
		Where("roles.is_admin = ? AND users.disabled = ? AND users.id <> ?", true, false, exceptID).Count(&n)
	return n
}

func lastAdmin() error {
	return api.Err(http.StatusBadRequest, api.CodeLastAdmin, "至少要保留一个启用中的管理员账号")
}

func (s *Service) updateUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		RoleID   *uint `json:"roleId"`
		Disabled *bool `json:"disabled"`
	}
	if !api.Bind(c, &req) {
		return
	}
	var u store.User
	if err := s.db().First(&u, id).Error; err != nil {
		api.Fail(c, api.NotFound("账号不存在"))
		return
	}
	api.Target(c, u.Username)
	updates := map[string]any{}
	losesAdmin := false
	if req.RoleID != nil && *req.RoleID != u.RoleID {
		if !s.roleExists(*req.RoleID) {
			api.Fail(c, api.BadRequest("角色不存在"))
			return
		}
		updates["role_id"] = *req.RoleID
		losesAdmin = s.d.Checker.IsAdmin(u.RoleID) && !s.d.Checker.IsAdmin(*req.RoleID)
	}
	if req.Disabled != nil && *req.Disabled != u.Disabled {
		updates["disabled"] = *req.Disabled
		if *req.Disabled {
			losesAdmin = losesAdmin || s.d.Checker.IsAdmin(u.RoleID)
			if u.ID == api.User(c).UserID {
				api.Fail(c, api.BadRequest("不能停用自己"))
				return
			}
		}
	}
	if losesAdmin && s.adminCount(u.ID) == 0 {
		api.Fail(c, lastAdmin())
		return
	}
	if len(updates) > 0 {
		if err := s.db().Model(&u).Updates(updates).Error; err != nil {
			api.Fail(c, err)
			return
		}
	}
	if req.Disabled != nil && *req.Disabled {
		s.d.Auth.RevokeUser(u.ID, 0)
		s.d.OnRevoke(u.ID, true)
	}
	api.OK(c, nil)
}

func (s *Service) resetPassword(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !api.Bind(c, &req) {
		return
	}
	var u store.User
	if err := s.db().First(&u, id).Error; err != nil {
		api.Fail(c, api.NotFound("账号不存在"))
		return
	}
	api.Target(c, u.Username)
	keep := uint(0)
	if u.ID == api.User(c).UserID {
		keep = api.User(c).SessionID
	}
	if err := s.d.Auth.SetPassword(u.ID, req.Password, keep); err != nil {
		api.Fail(c, err)
		return
	}
	if keep == 0 {
		s.d.OnRevoke(u.ID, false)
	}
	api.OK(c, nil)
}

func (s *Service) deleteUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var u store.User
	if err := s.db().First(&u, id).Error; err != nil {
		api.Fail(c, api.NotFound("账号不存在"))
		return
	}
	api.Target(c, u.Username)
	if u.ID == api.User(c).UserID {
		api.Fail(c, api.BadRequest("不能删除自己"))
		return
	}
	if s.d.Checker.IsAdmin(u.RoleID) && !u.Disabled && s.adminCount(u.ID) == 0 {
		api.Fail(c, lastAdmin())
		return
	}
	s.d.Auth.RevokeUser(u.ID, 0)
	s.db().Where("user_id = ?", u.ID).Delete(&store.DesktopState{})
	if err := s.db().Delete(&u).Error; err != nil {
		api.Fail(c, err)
		return
	}
	s.d.OnRevoke(u.ID, true)
	api.OK(c, nil)
}

type roleView struct {
	store.Role
	Permissions []string `json:"permissions"`
	UserCount   int64    `json:"userCount"`
}

func (s *Service) listRoles(c *gin.Context) {
	var roles []store.Role
	s.db().Preload("Permissions").Order("id").Find(&roles)
	out := make([]roleView, 0, len(roles))
	for _, r := range roles {
		v := roleView{Role: r, Permissions: s.d.Checker.List(r.ID)}
		v.Role.Permissions = nil
		s.db().Model(&store.User{}).Where("role_id = ?", r.ID).Count(&v.UserCount)
		out = append(out, v)
	}
	api.OK(c, out)
}

type roleReq struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func (r *roleReq) validate() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 64 {
		return api.BadRequest("角色名不能为空，最长 64 个字符")
	}
	for _, p := range r.Permissions {
		if !rbac.Valid(p) {
			return api.BadRequest("无效的权限点：" + p)
		}
	}
	return nil
}

func (s *Service) savePerms(tx *gorm.DB, roleID uint, perms []string) error {
	if err := tx.Where("role_id = ?", roleID).Delete(&store.RolePermission{}).Error; err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range perms {
		if seen[p] {
			continue
		}
		seen[p] = true
		if err := tx.Create(&store.RolePermission{RoleID: roleID, Permission: p}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) createRole(c *gin.Context) {
	var req roleReq
	if !api.Bind(c, &req) {
		return
	}
	if err := req.validate(); err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, req.Name)
	role := store.Role{Name: req.Name, Description: req.Description}
	err := s.db().Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&role).Error; err != nil {
			return api.Conflict(api.CodeConflict, "角色名已存在")
		}
		return s.savePerms(tx, role.ID, req.Permissions)
	})
	if err != nil {
		api.Fail(c, err)
		return
	}
	s.d.Checker.Invalidate()
	api.OK(c, role)
}

func (s *Service) updateRole(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req roleReq
	if !api.Bind(c, &req) {
		return
	}
	if err := req.validate(); err != nil {
		api.Fail(c, err)
		return
	}
	var role store.Role
	if err := s.db().First(&role, id).Error; err != nil {
		api.Fail(c, api.NotFound("角色不存在"))
		return
	}
	api.Target(c, role.Name)
	if role.IsAdmin {
		api.Fail(c, api.BadRequest("内置管理员角色不可修改"))
		return
	}
	err := s.db().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&role).Updates(map[string]any{"name": req.Name, "description": req.Description}).Error; err != nil {
			return api.Conflict(api.CodeConflict, "角色名已存在")
		}
		return s.savePerms(tx, role.ID, req.Permissions)
	})
	if err != nil {
		api.Fail(c, err)
		return
	}
	s.d.Checker.Invalidate()
	api.OK(c, nil)
}

func (s *Service) deleteRole(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var role store.Role
	if err := s.db().First(&role, id).Error; err != nil {
		api.Fail(c, api.NotFound("角色不存在"))
		return
	}
	api.Target(c, role.Name)
	if role.IsAdmin {
		api.Fail(c, api.BadRequest("内置管理员角色不可删除"))
		return
	}
	var n int64
	s.db().Model(&store.User{}).Where("role_id = ?", role.ID).Count(&n)
	if n > 0 {
		api.Fail(c, api.BadRequest("还有账号使用该角色，请先修改这些账号的角色"))
		return
	}
	err := s.db().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", role.ID).Delete(&store.RolePermission{}).Error; err != nil {
			return err
		}
		return tx.Delete(&role).Error
	})
	if err != nil {
		api.Fail(c, err)
		return
	}
	s.d.Checker.Invalidate()
	api.OK(c, nil)
}

type sessionView struct {
	store.Session
	Username string `json:"username"`
	Current  bool   `json:"current"`
}

func (s *Service) listSessions(c *gin.Context) {
	var list []store.Session
	s.db().Order("last_seen_at desc").Find(&list)
	names := map[uint]string{}
	var users []store.User
	s.db().Find(&users)
	for _, u := range users {
		names[u.ID] = u.Username
	}
	out := make([]sessionView, 0, len(list))
	for _, ss := range list {
		out = append(out, sessionView{Session: ss, Username: names[ss.UserID], Current: ss.ID == api.User(c).SessionID})
	}
	api.OK(c, out)
}

func (s *Service) kickSession(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var ss store.Session
	if err := s.db().First(&ss, id).Error; err != nil {
		api.Fail(c, api.NotFound("会话不存在"))
		return
	}
	api.Target(c, "会话 "+strconv.Itoa(int(id)))
	s.d.Auth.RevokeSession(id)
	api.OK(c, nil)
}

func (s *Service) listBans(c *gin.Context) {
	var list []store.IPBan
	s.db().Where("until > ?", time.Now()).Order("until desc").Find(&list)
	api.OK(c, list)
}

func (s *Service) unban(c *gin.Context) {
	ip := c.Param("ip")
	api.Target(c, ip)
	res := s.db().Where("ip = ?", ip).Delete(&store.IPBan{})
	if res.Error != nil {
		api.Fail(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		api.Fail(c, api.NotFound("该 IP 未被封禁"))
		return
	}
	api.OK(c, nil)
}
