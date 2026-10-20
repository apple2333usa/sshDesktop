// Package rbac 定义功能权限点、内置角色，以及按角色查询权限。
package rbac

import (
	"errors"
	"sync"

	"github.com/apple2333usa/sshdesktop/internal/store"
	"gorm.io/gorm"
)

// Perm 描述一个权限点。RootEquivalent 表示拥有它等同于拥有 root 权限，界面需提示。
type Perm struct {
	Key            string `json:"key"`
	Module         string `json:"module"`
	RootEquivalent bool   `json:"rootEquivalent"`
	AdminOnly      bool   `json:"adminOnly"`
}

const (
	MonitorView    = "monitor:view"
	FilesView      = "files:view"
	FilesManage    = "files:manage"
	TerminalUse    = "terminal:use"
	ProcessView    = "process:view"
	ProcessManage  = "process:manage"
	AuditView      = "audit:view"
	SystemPower    = "system:power"
	UsersManage    = "users:manage"
	SettingsManage = "settings:manage"
	NodesManage    = "nodes:manage"
	BatchRun       = "batch:run"
	ProbeView      = "probe:view"
	ProbeManage    = "probe:manage"
)

// Catalog 是当前版本支持的全部权限点，顺序即界面展示顺序。
var Catalog = []Perm{
	{Key: MonitorView, Module: "monitor"},
	{Key: FilesView, Module: "files"},
	{Key: FilesManage, Module: "files", RootEquivalent: true},
	{Key: TerminalUse, Module: "terminal", RootEquivalent: true},
	{Key: ProcessView, Module: "process"},
	{Key: ProcessManage, Module: "process"},
	{Key: NodesManage, Module: "nodes", RootEquivalent: true},
	{Key: BatchRun, Module: "batch", RootEquivalent: true},
	{Key: ProbeView, Module: "probe"},
	{Key: ProbeManage, Module: "probe"},
	{Key: AuditView, Module: "audit"},
	{Key: SystemPower, Module: "system"},
	{Key: UsersManage, Module: "users", AdminOnly: true},
	{Key: SettingsManage, Module: "settings", AdminOnly: true},
}

func Valid(key string) bool {
	for _, p := range Catalog {
		if p.Key == key {
			return !p.AdminOnly
		}
	}
	return false
}

const (
	AdminRoleName    = "管理员"
	OpsRoleName      = "运维"
	ReadonlyRoleName = "只读"
)

// Seed 创建内置角色（只在不存在时创建，不覆盖用户的修改）。
func Seed(db *gorm.DB) error {
	seeds := []struct {
		role  store.Role
		perms []string
	}{
		{store.Role{Name: AdminRoleName, IsAdmin: true, Description: "全部权限，不可修改"}, nil},
		{store.Role{Name: OpsRoleName, Description: "日常运维：监控、文件、终端、进程与服务"},
			[]string{MonitorView, FilesView, FilesManage, TerminalUse, ProcessView, ProcessManage, AuditView, BatchRun, ProbeView, ProbeManage}},
		{store.Role{Name: ReadonlyRoleName, Description: "只能查看监控、文件和进程"},
			[]string{MonitorView, FilesView, ProcessView, ProbeView}},
	}
	for _, s := range seeds {
		var n int64
		db.Model(&store.Role{}).Where("name = ?", s.role.Name).Count(&n)
		if n > 0 {
			continue
		}
		r := s.role
		if err := db.Create(&r).Error; err != nil {
			return err
		}
		for _, p := range s.perms {
			db.Create(&store.RolePermission{RoleID: r.ID, Permission: p})
		}
	}
	return nil
}

func AdminRole(db *gorm.DB) (*store.Role, error) {
	var r store.Role
	err := db.Where("is_admin = ?", true).First(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("管理员角色不存在")
	}
	return &r, err
}

// Checker 缓存角色权限，角色修改后调用 Invalidate。
type Checker struct {
	db    *gorm.DB
	mu    sync.RWMutex
	cache map[uint]map[string]bool
	admin map[uint]bool
}

func NewChecker(db *gorm.DB) *Checker {
	return &Checker{db: db, cache: map[uint]map[string]bool{}, admin: map[uint]bool{}}
}

func (c *Checker) Invalidate() {
	c.mu.Lock()
	c.cache = map[uint]map[string]bool{}
	c.admin = map[uint]bool{}
	c.mu.Unlock()
}

func (c *Checker) load(roleID uint) (map[string]bool, bool) {
	c.mu.RLock()
	perms, ok := c.cache[roleID]
	isAdmin := c.admin[roleID]
	c.mu.RUnlock()
	if ok {
		return perms, isAdmin
	}
	var role store.Role
	perms = map[string]bool{}
	if err := c.db.Preload("Permissions").First(&role, roleID).Error; err == nil {
		isAdmin = role.IsAdmin
		for _, p := range role.Permissions {
			perms[p.Permission] = true
		}
	}
	c.mu.Lock()
	c.cache[roleID] = perms
	c.admin[roleID] = isAdmin
	c.mu.Unlock()
	return perms, isAdmin
}

func (c *Checker) Has(roleID uint, perm string) bool {
	perms, isAdmin := c.load(roleID)
	return isAdmin || perms[perm]
}

func (c *Checker) IsAdmin(roleID uint) bool {
	_, isAdmin := c.load(roleID)
	return isAdmin
}

// List 返回角色拥有的权限点；管理员返回全部。
func (c *Checker) List(roleID uint) []string {
	perms, isAdmin := c.load(roleID)
	out := []string{}
	for _, p := range Catalog {
		if isAdmin || perms[p.Key] {
			out = append(out, p.Key)
		}
	}
	return out
}
