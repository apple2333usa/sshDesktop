package store

import (
	"time"

	"github.com/apple2333usa/sshdesktop/internal/metric"
)

// 账号与权限

type User struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	Username     string     `gorm:"uniqueIndex;size:64;not null" json:"username"`
	PasswordHash string     `gorm:"not null" json:"-"`
	RoleID       uint       `gorm:"index;not null" json:"roleId"`
	Disabled     bool       `json:"disabled"`
	LastLoginAt  *time.Time `json:"lastLoginAt"`
	LastLoginIP  string     `gorm:"size:64" json:"lastLoginIp"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	Role         *Role      `gorm:"foreignKey:RoleID" json:"role,omitempty"`
}

type Role struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"uniqueIndex;size:64;not null" json:"name"`
	Description string `json:"description"`
	// IsAdmin 为内置管理员角色：拥有全部权限，不可修改或删除。
	IsAdmin     bool             `json:"isAdmin"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
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
	ID       uint   `gorm:"primaryKey"`
	IP       string `gorm:"index;size:64"`
	Username string `gorm:"index;size:64"`
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

// Metric 定义在 metric 包（独立 agent 也要用，不能依赖数据库）。
type Metric = metric.Metric

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
	UserID    uint   `gorm:"primaryKey"`
	State     string `gorm:"type:text"`
	UpdatedAt time.Time
}

type Setting struct {
	Key   string `gorm:"primaryKey;size:64"`
	Value string `gorm:"type:text"`
}

// 节点

// Node 是一台服务器。本机固定为 ID "local"、Kind "local"，启动时自动创建。
type Node struct {
	ID        string `gorm:"primaryKey;size:32" json:"id"`
	Kind      string `gorm:"size:16;not null" json:"kind"` // local / ssh
	Name      string `gorm:"size:64;not null" json:"name"`
	Host      string `gorm:"size:255" json:"host"`
	Port      int    `json:"port"`
	Username  string `gorm:"size:64" json:"username"`
	AuthType  string `gorm:"size:16" json:"authType"` // key / password
	Color     string `gorm:"size:16" json:"color"`
	SortOrder int    `json:"sortOrder"`
	// 按月流量：每月重置日（1-28）与额度（字节，0 表示不限），额度按 sum / rx / tx 计算
	TrafficResetDay  int       `json:"trafficResetDay"`
	TrafficQuota     uint64    `json:"trafficQuota"`
	TrafficQuotaMode string    `gorm:"size:8" json:"trafficQuotaMode"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// NodeCredential 是加密后的 SSH 凭据（私钥或密码，以及私钥口令）。
type NodeCredential struct {
	NodeID     string `gorm:"primaryKey;size:32"`
	Secret     []byte
	Passphrase []byte
}

// NodeHostKey 是用户确认过的主机密钥。
type NodeHostKey struct {
	NodeID      string    `gorm:"primaryKey;size:32" json:"nodeId"`
	KeyType     string    `gorm:"size:64" json:"keyType"`
	Key         string    `json:"-"` // base64 编码的公钥
	Fingerprint string    `gorm:"size:128" json:"fingerprint"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

// TrafficDaily 是每个节点、每块网卡每天的收发字节数。
type TrafficDaily struct {
	NodeID string `gorm:"primaryKey;size:32" json:"-"`
	Day    int    `gorm:"primaryKey" json:"day"` // YYYYMMDD
	Iface  string `gorm:"primaryKey;size:64" json:"iface"`
	Rx     uint64 `json:"rx"`
	Tx     uint64 `json:"tx"`
}

// 服务监控

type Probe struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Name         string    `gorm:"size:64;not null" json:"name"`
	Type         string    `gorm:"size:8;not null" json:"type"` // http / tcp / ping / ssl
	Target       string    `gorm:"not null" json:"target"`
	Interval     int       `json:"interval"` // 秒，最短 30
	Timeout      int       `json:"timeout"`  // 秒
	Keyword      string    `json:"keyword"`
	ExpectStatus string    `gorm:"size:64" json:"expectStatus"` // 例如 200-399
	SSLWarnDays  int       `json:"sslWarnDays"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type ProbeResult struct {
	ProbeID   uint    `gorm:"primaryKey" json:"-"`
	TS        int64   `gorm:"primaryKey" json:"ts"` // Unix 秒
	OK        bool    `json:"ok"`
	LatencyMs float64 `json:"latencyMs"`
	Message   string  `json:"message"`
}

// 批量命令

type Snippet struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Command   string    `gorm:"type:text;not null" json:"command"`
	CreatedAt time.Time `json:"createdAt"`
}

type BatchRun struct {
	ID         string     `gorm:"primaryKey;size:32" json:"id"`
	UserID     uint       `gorm:"index" json:"userId"`
	Username   string     `gorm:"size:64" json:"username"`
	Command    string     `gorm:"type:text" json:"command"`
	Nodes      string     `json:"nodes"` // 逗号分隔
	Timeout    int        `json:"timeout"`
	CreatedAt  time.Time  `gorm:"index" json:"createdAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

type BatchResult struct {
	RunID      string `gorm:"primaryKey;size:32" json:"runId"`
	NodeID     string `gorm:"primaryKey;size:32" json:"nodeId"`
	Status     string `gorm:"size:16" json:"status"` // running / done / failed / timeout / canceled
	ExitCode   int    `json:"exitCode"`
	Output     string `gorm:"type:text" json:"output"`
	Error      string `json:"error"`
	DurationMs int64  `json:"durationMs"`
	// Seq 只用于执行中的查询结果：Output 包含的推送片段数（见 batch.Event）
	Seq int `gorm:"-" json:"seq,omitempty"`
}

// Docker 与应用商店

// ComposeProject 是面板创建或登记的 Compose 项目（服务器上已有的项目从容器标签识别，不入库）。
type ComposeProject struct {
	Name      string    `gorm:"primaryKey;size:64" json:"name"`
	Dir       string    `json:"dir"`
	File      string    `json:"file"`    // 相对 Dir 的文件名，空则自动查找
	Managed   bool      `json:"managed"` // 目录由面板创建，删除项目时可一并删除
	CreatedAt time.Time `json:"createdAt"`
}

// AppInstalled 是应用商店安装的应用。Name 同时是 Compose 项目名和默认容器名。
type AppInstalled struct {
	Name      string    `gorm:"primaryKey;size:64" json:"name"`
	Source    string    `gorm:"size:64" json:"source"`
	Key       string    `gorm:"size:128;index" json:"key"`
	Title     string    `gorm:"size:128" json:"title"`
	Version   string    `gorm:"size:64" json:"version"`
	Dir       string    `json:"dir"`
	Params    string    `gorm:"type:text" json:"-"`    // JSON：表单参数
	Status    string    `gorm:"size:16" json:"status"` // installing / installed / failed / upgrading
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (AppInstalled) TableName() string { return "apps_installed" }

// 计划任务

type CronJob struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"size:64;not null" json:"name"`
	Type     string `gorm:"size:16;not null" json:"type"` // shell / backup / url / cleanup
	Schedule string `gorm:"size:64;not null" json:"schedule"`
	Enabled  bool   `json:"enabled"`
	Script   string `gorm:"type:text" json:"script"`
	URL      string `json:"url"`
	PlanID   uint   `gorm:"index" json:"planId"` // 备份计划
	// 清理日志：目录（每行一个）、文件名模式、保留天数
	CleanDirs    string     `gorm:"type:text" json:"cleanDirs"`
	CleanPattern string     `gorm:"size:128" json:"cleanPattern"`
	CleanDays    int        `json:"cleanDays"`
	Timeout      int        `json:"timeout"`  // 秒，0 表示不限
	KeepRuns     int        `json:"keepRuns"` // 保留的执行记录数
	LastRunAt    *time.Time `json:"lastRunAt"`
	LastStatus   string     `gorm:"size:16" json:"lastStatus"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type CronRun struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	JobID      uint       `gorm:"index" json:"jobId"`
	Trigger    string     `gorm:"size:16" json:"trigger"` // cron / manual
	Status     string     `gorm:"size:16" json:"status"`  // running / success / failed / skipped
	ExitCode   int        `json:"exitCode"`
	Output     string     `gorm:"type:text" json:"output,omitempty"`
	StartedAt  time.Time  `gorm:"index" json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	DurationMs int64      `json:"durationMs"`
}

// 备份

type BackupTarget struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Type      string    `gorm:"size:16;not null" json:"type"` // local / s3 / webdav / sftp
	Config    string    `gorm:"type:text" json:"-"`           // JSON：不含密钥的配置
	Secret    []byte    `json:"-"`                            // 加密的密钥（S3 Secret Key、WebDAV 密码）
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type BackupPlan struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Name      string `gorm:"size:64;not null" json:"name"`
	Type      string `gorm:"size:16;not null" json:"type"` // dir / app / panel
	Source    string `json:"source"`                       // 目录路径或应用名
	Excludes  string `gorm:"type:text" json:"excludes"`    // 每行一个排除模式
	TargetIDs string `json:"-"`                            // 逗号分隔
	Encrypt   bool   `json:"encrypt"`
	Password  []byte `json:"-"` // 加密保存的备份密码
	KeepCount int    `json:"keepCount"`
	KeepDays  int    `json:"keepDays"`
	StopApp   bool   `json:"stopApp"` // 备份应用前暂停容器
	// IncludeSecretKey 备份面板时包含主密钥（默认不包含，见设计文档 8.5）
	IncludeSecretKey bool      `json:"includeSecretKey"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type BackupRecord struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	PlanID     uint       `gorm:"index" json:"planId"`
	PlanName   string     `gorm:"size:64" json:"planName"`
	Type       string     `gorm:"size:16" json:"type"`
	Source     string     `json:"source"`
	TargetID   uint       `gorm:"index" json:"targetId"`
	TargetName string     `gorm:"size:64" json:"targetName"`
	Key        string     `json:"key"` // 存储中的对象路径
	Size       int64      `json:"size"`
	Encrypted  bool       `json:"encrypted"`
	Status     string     `gorm:"size:16" json:"status"`  // running / success / failed
	Trigger    string     `gorm:"size:16" json:"trigger"` // manual / cron / snapshot
	Error      string     `json:"error"`
	StartedAt  time.Time  `gorm:"index" json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
}

func allModels() []any {
	return []any{
		&User{}, &Role{}, &RolePermission{}, &Session{}, &LoginAttempt{}, &IPBan{},
		&AuditLog{}, &Metric1m{}, &Metric15m{}, &Metric1h{}, &Task{}, &TaskLog{},
		&DesktopState{}, &Setting{},
		&Node{}, &NodeCredential{}, &NodeHostKey{}, &TrafficDaily{},
		&Probe{}, &ProbeResult{}, &Snippet{}, &BatchRun{}, &BatchResult{},
		&ComposeProject{}, &AppInstalled{}, &CronJob{}, &CronRun{},
		&BackupTarget{}, &BackupPlan{}, &BackupRecord{},
	}
}
