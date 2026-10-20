package store

import "time"

// 账号与权限

type User struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Username      string    `gorm:"uniqueIndex;size:64;not null" json:"username"`
	PasswordHash  string    `gorm:"not null" json:"-"`
	RoleID        uint      `gorm:"index;not null" json:"roleId"`
	Disabled      bool      `json:"disabled"`
	LastLoginAt   *time.Time `json:"lastLoginAt"`
	LastLoginIP   string    `gorm:"size:64" json:"lastLoginIp"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Role          *Role     `gorm:"foreignKey:RoleID" json:"role,omitempty"`
}

type Role struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:64;not null" json:"name"`
	Description string    `json:"description"`
	// IsAdmin 为内置管理员角色：拥有全部权限，不可修改或删除。
	IsAdmin     bool      `json:"isAdmin"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Permissions []RolePermission `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE" json:"-"`
}

type RolePermission struct {
	RoleID     uint   `gorm:"primaryKey"`
	Permission string `gorm:"primaryKey;size:64"`
}

type Session struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	TokenHash  string    `gorm:"uniqueIndex;size:64;not null" json:"-"`
	UserID     uint      `gorm:"index;not null" json:"userId"`
	IP         string    `gorm:"size:64" json:"ip"`
	UserAgent  string    `json:"userAgent"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `gorm:"index" json:"lastSeenAt"`
}

type LoginAttempt struct {
	ID       uint      `gorm:"primaryKey"`
	IP       string    `gorm:"index;size:64"`
	Username string    `gorm:"index;size:64"`
	Success  bool
	At       time.Time `gorm:"index"`
}

type IPBan struct {
	IP        string    `gorm:"primaryKey;size:64" json:"ip"`
	Reason    string    `json:"reason"`
	Until     time.Time `json:"until"`
	CreatedAt time.Time `json:"createdAt"`
}

// 审计

type AuditLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	At         time.Time `gorm:"index" json:"at"`
	UserID     uint      `gorm:"index" json:"userId"`
	Username   string    `gorm:"index;size:64" json:"username"`
	IP         string    `gorm:"size:64" json:"ip"`
	Node       string    `gorm:"size:64" json:"node"`
	Module     string    `gorm:"index;size:32" json:"module"`
	Action     string    `gorm:"size:64" json:"action"`
	Target     string    `json:"target"`
	Success    bool      `json:"success"`
	Detail     string    `json:"detail"`
	DurationMs int64     `json:"durationMs"`
}

// 监控历史

type Metric struct {
	NodeID       string  `gorm:"primaryKey;size:64" json:"-"`
	TS           int64   `gorm:"primaryKey" json:"ts"` // Unix 秒
	CPU          float64 `json:"cpu"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
	MemUsed      uint64  `json:"memUsed"`
	MemTotal     uint64  `json:"memTotal"`
	SwapUsed     uint64  `json:"swapUsed"`
	SwapTotal    uint64  `json:"swapTotal"`
	DiskUsed     uint64  `json:"diskUsed"`
	DiskTotal    uint64  `json:"diskTotal"`
	DiskReadBps  float64 `json:"diskReadBps"`
	DiskWriteBps float64 `json:"diskWriteBps"`
	NetRxBps     float64 `json:"netRxBps"`
	NetTxBps     float64 `json:"netTxBps"`
	TCPConns     float64 `json:"tcpConns"`
	Procs        float64 `json:"procs"`
}

type Metric1m struct{ Metric }
type Metric15m struct{ Metric }
type Metric1h struct{ Metric }

func (Metric1m) TableName() string  { return "metrics_1m" }
func (Metric15m) TableName() string { return "metrics_15m" }
func (Metric1h) TableName() string  { return "metrics_1h" }

// 后台任务

type Task struct {
	ID         string     `gorm:"primaryKey;size:32" json:"id"`
	Kind       string     `gorm:"size:32" json:"kind"`
	Title      string     `json:"title"`
	UserID     uint       `gorm:"index" json:"userId"`
	Node       string     `gorm:"size:64" json:"node"`
	Status     string     `gorm:"size:16;index" json:"status"` // running / success / failed / canceled
	Progress   float64    `json:"progress"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `gorm:"index" json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

type TaskLog struct {
	ID     uint      `gorm:"primaryKey" json:"id"`
	TaskID string    `gorm:"index;size:32" json:"taskId"`
	At     time.Time `json:"at"`
	Line   string    `json:"line"`
}

// 桌面状态与设置

type DesktopState struct {
	UserID    uint      `gorm:"primaryKey"`
	State     string    `gorm:"type:text"`
	UpdatedAt time.Time
}

type Setting struct {
	Key   string `gorm:"primaryKey;size:64"`
	Value string `gorm:"type:text"`
}

func allModels() []any {
	return []any{
		&User{}, &Role{}, &RolePermission{}, &Session{}, &LoginAttempt{}, &IPBan{},
		&AuditLog{}, &Metric1m{}, &Metric15m{}, &Metric1h{}, &Task{}, &TaskLog{},
		&DesktopState{}, &Setting{},
	}
}
