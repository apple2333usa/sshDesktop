// Package server 组装面板：HTTP 服务、路由、中间件、后台定时任务。
package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/audit"
	"github.com/apple2333usa/sshdesktop/internal/auth"
	"github.com/apple2333usa/sshdesktop/internal/backup"
	"github.com/apple2333usa/sshdesktop/internal/config"
	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/module/appstore"
	backupmod "github.com/apple2333usa/sshdesktop/internal/module/backup"
	"github.com/apple2333usa/sshdesktop/internal/module/batch"
	"github.com/apple2333usa/sshdesktop/internal/module/cron"
	"github.com/apple2333usa/sshdesktop/internal/module/desktop"
	dockermod "github.com/apple2333usa/sshdesktop/internal/module/docker"
	"github.com/apple2333usa/sshdesktop/internal/module/files"
	"github.com/apple2333usa/sshdesktop/internal/module/monitor"
	"github.com/apple2333usa/sshdesktop/internal/module/nodes"
	"github.com/apple2333usa/sshdesktop/internal/module/probe"
	"github.com/apple2333usa/sshdesktop/internal/module/process"
	"github.com/apple2333usa/sshdesktop/internal/module/service"
	"github.com/apple2333usa/sshdesktop/internal/module/system"
	"github.com/apple2333usa/sshdesktop/internal/module/terminal"
	"github.com/apple2333usa/sshdesktop/internal/module/traffic"
	"github.com/apple2333usa/sshdesktop/internal/module/users"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/rbac"
	"github.com/apple2333usa/sshdesktop/internal/realtime"
	"github.com/apple2333usa/sshdesktop/internal/secret"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/gin-gonic/gin"
)

type Server struct {
	cfg     *config.Config
	st      *store.Store
	version string
	web     fs.FS

	auth     *auth.Service
	checker  *rbac.Checker
	audit    *audit.Recorder
	hub      *realtime.Hub
	tasks    *task.Manager
	nodes    *node.Registry
	col      *agent.Collector
	monitor  *monitor.Service
	terminal *terminal.Service
	nodeMgr  *nodes.Service
	batch    *batch.Service
	probe    *probe.Service
	box      *secret.Box
	docker   *docker.Client
	apps     *appstore.Service
	cron     *cron.Service
	backup   *backup.Engine

	engine *gin.Engine
}

func New(cfg *config.Config, st *store.Store, version string, web fs.FS) (*Server, error) {
	if err := rbac.Seed(st.DB); err != nil {
		return nil, err
	}
	s := &Server{
		cfg: cfg, st: st, version: version, web: web,
		auth:    auth.New(st),
		checker: rbac.NewChecker(st.DB),
		audit:   audit.New(st),
		hub:     realtime.NewHub(),
		nodes:   node.NewRegistry(),
		col:     agent.NewCollector(),
	}
	s.tasks = task.NewManager(st, s.hub)
	s.monitor = monitor.New(st, s.col, s.hub, s.nodes)
	s.terminal = terminal.New(s.nodes, st, s.audit)
	s.batch = batch.New(st, s.nodes, s.hub, s.audit.Action)
	s.probe = probe.New(st, s.hub, s.audit.Action)
	s.hub.Authorize = s.authorizeChannel
	if err := ensureSecretKey(cfg.DataDir); err != nil {
		return nil, err
	}
	box, err := secret.Load(filepath.Join(cfg.DataDir, "secret.key"))
	if err != nil {
		return nil, err
	}
	s.nodeMgr = nodes.New(nodes.Deps{
		Store: st, Nodes: s.nodes, Hub: s.hub, Tasks: s.tasks, Box: box,
		Confirm: s.confirm, Audit: s.audit.Action, Version: version,
		CacheDir: filepath.Join(cfg.DataDir, "agent-cache"),
	})
	s.nodeMgr.OnAdded = s.monitor.Attach
	s.nodeMgr.OnRemoved = s.monitor.Detach
	s.nodeMgr.OnAgentChanged = s.monitor.Restart
	s.box = box
	s.docker = docker.New("")
	s.apps = appstore.New(appstore.Deps{
		Store: st, Tasks: s.tasks, Docker: s.docker, DataDir: cfg.DataDir, Confirm: s.confirm, Audit: s.audit.Action,
	})
	s.backup = &backup.Engine{
		Store: st, Box: box, DataDir: cfg.DataDir, ConfigPath: cfg.Path(), Version: version,
		Docker: s.docker, Remote: s.remoteFS,
	}
	s.backup.Cleanup()
	bin, _ := os.Executable()
	s.cron = cron.New(cron.Deps{
		Store: st, Tasks: s.tasks, Bin: bin, Config: cfg.Path(), Audit: s.audit.Action,
		Runner: &cron.Runner{Store: st, DataDir: cfg.DataDir, Backup: s.backup.RunPlan},
		PlanExists: func(id uint) bool {
			var n int64
			st.DB.Model(&store.BackupPlan{}).Where("id = ?", id).Count(&n)
			return n > 0
		},
	})
	s.terminal.AllowContainer = func(c *gin.Context) bool {
		if !s.checker.Has(api.User(c).RoleID, rbac.DockerManage) {
			api.Fail(c, api.Forbidden("没有权限："+rbac.DockerManage))
			return false
		}
		return true
	}
	if err := s.ensureAdmin(); err != nil {
		return nil, err
	}
	if err := s.nodeMgr.Load(); err != nil {
		return nil, err
	}
	s.engine = s.routes()
	return s, nil
}

// remoteFS 返回 SFTP 备份目标所在节点的文件系统。
func (s *Server) remoteFS(ctx context.Context, id string) (vfs.FS, func(), error) {
	n, ok := s.nodes.SSH(id)
	if !ok {
		return nil, nil, errors.New("SFTP 备份目标所在的服务器不存在")
	}
	if st := n.Status(); st.State != node.StateOnline {
		return nil, nil, fmt.Errorf("SFTP 备份目标所在的服务器未连接：%s", st.Error)
	}
	return n.FS(), func() {}, nil
}

// restartPanel 在 systemd 下重启面板（面板数据恢复后需要重启）。
func restartPanel() {
	slog.Warn("面板数据已恢复，正在重启面板")
	err := exec.Command("systemctl", "--no-block", "restart", "sshdesktop").Run()
	// 重启任务排队后 systemd 会立即停止服务，同一 cgroup 里还没退出的 systemctl 也会被 SIGTERM 结束，这不是失败
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGTERM {
			return
		}
	}
	if err != nil {
		slog.Error("重启面板失败，请手动执行 systemctl restart sshdesktop", "err", err)
	}
}

// ensureSecretKey 首次启动时生成加密主密钥，用于加密远程节点凭据。
func ensureSecretKey(dir string) error {
	p := filepath.Join(dir, "secret.key")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	return os.WriteFile(p, key, 0o600)
}

// ensureAdmin：一个账号都没有时创建 admin 并把随机密码写入日志和 initial-admin.txt。
func (s *Server) ensureAdmin() error {
	var n int64
	s.st.DB.Model(&store.User{}).Count(&n)
	if n > 0 {
		return nil
	}
	role, err := rbac.AdminRole(s.st.DB)
	if err != nil {
		return err
	}
	pw := auth.RandomPassword(16)
	if _, err := auth.CreateUser(s.st.DB, "admin", pw, role.ID); err != nil {
		return err
	}
	p := filepath.Join(s.cfg.DataDir, "initial-admin.txt")
	os.WriteFile(p, []byte(fmt.Sprintf("用户名: admin\n密码: %s\n登录后请立即修改密码，并删除本文件。\n", pw)), 0o600)
	slog.Warn("没有任何账号，已创建初始管理员", "username", "admin", "password", pw, "file", p)
	return nil
}

func (s *Server) authorizeChannel(p *api.Principal, ch string) bool {
	prefix, rest := realtime.ChannelNode(ch)
	switch prefix {
	case "tasks": // 账号自己的任务进度，本来就会推送
		return true
	case "metrics":
		_, err := s.nodes.Get(rest)
		return err == nil && s.checker.Has(p.RoleID, rbac.MonitorView)
	case "task":
		rec, _, err := s.tasks.Get(rest)
		return err == nil && (rec.UserID == p.UserID || s.checker.IsAdmin(p.RoleID))
	case "node-status":
		return true
	case "batch":
		return s.checker.Has(p.RoleID, rbac.BatchRun)
	case "probes", "probe-events":
		return s.checker.Has(p.RoleID, rbac.ProbeView)
	}
	return false
}

func (s *Server) routes() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		slog.Error("panic", "path", c.Request.URL.Path, "err", err)
		api.Fail(c, api.Err(http.StatusInternalServerError, api.CodeInternal, "服务器内部错误"))
	}))
	e.Use(s.accessGuard, securityHeaders)
	e.MaxMultipartMemory = 8 << 20

	v1 := e.Group("/api/v1", csrfGuard, func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	a := s.audit.Action

	v1.GET("/health", func(c *gin.Context) { api.OK(c, gin.H{"status": "ok", "version": s.version}) })
	v1.POST("/auth/login", s.handleLogin)
	v1.POST("/auth/logout", s.handleLogout)
	v1.GET("/auth/me", s.login, s.handleMe)
	v1.POST("/auth/password", s.login, a("auth", "change-password"), s.handleChangePassword)
	v1.GET("/ws", s.login, s.hub.Handle)

	desktop.New(s.st).Register(v1, s.login)
	system.New(system.Deps{
		Store: s.st, Config: s.cfg, Collector: s.col, Tasks: s.tasks, Version: s.version,
		Confirm: s.confirm, Audit: a, IsAdmin: s.isAdmin,
	}).Register(v1, system.Guards{
		Login: s.login, Power: s.perm(rbac.SystemPower), AuditView: s.perm(rbac.AuditView),
		Settings: s.perm(rbac.SettingsManage),
	})
	users.New(users.Deps{
		Store: s.st, Auth: s.auth, Checker: s.checker, Audit: a,
		OnRevoke: func(uid uint, closeTerminals bool) {
			s.hub.DisconnectUser(uid)
			if closeTerminals {
				s.terminal.CloseUser(uid)
			}
		},
	}).Register(v1, s.perm(rbac.UsersManage))

	s.nodeMgr.Register(v1, nodes.Guards{Login: s.login, Manage: s.perm(rbac.NodesManage)})
	s.monitor.RegisterGlobal(v1, s.perm(rbac.MonitorView))
	s.batch.Register(v1, s.perm(rbac.BatchRun))
	s.probe.Register(v1, s.perm(rbac.ProbeView), s.perm(rbac.ProbeManage))

	n := v1.Group("/nodes/:node", s.nodeGuard)
	s.monitor.Register(n, s.perm(rbac.MonitorView))
	traffic.New(s.st, s.monitor.SourceOf).Register(v1, n, s.perm(rbac.MonitorView), s.perm(rbac.NodesManage), a)
	files.New(files.Deps{Nodes: s.nodes, Tasks: s.tasks, Confirm: s.confirm, Audit: a}).
		Register(n, s.perm(rbac.FilesView), s.perm(rbac.FilesManage))
	s.terminal.Register(n, s.perm(rbac.TerminalUse))
	// 进程与系统服务只对本机开放
	local := n.Group("", func(c *gin.Context) {
		if c.Param("node") != node.LocalID {
			api.Fail(c, api.BadRequest("该功能只对本机开放"))
			return
		}
		c.Next()
	})
	process.New(a).Register(local, s.perm(rbac.ProcessView), s.perm(rbac.ProcessManage))
	service.New(s.nodes.Local(), a).Register(local, s.perm(rbac.ProcessView), s.perm(rbac.ProcessManage))
	dockermod.New(dockermod.Deps{
		Store: s.st, Tasks: s.tasks, Docker: s.docker, DataDir: s.cfg.DataDir, Confirm: s.confirm, Audit: a, AppOf: s.apps.AppOf,
	}).Register(local, s.perm(rbac.DockerView), s.perm(rbac.DockerManage))
	s.apps.Register(local, s.perm(rbac.AppstoreView), s.perm(rbac.AppstoreManage))
	s.cron.Register(local, s.perm(rbac.CronView), s.perm(rbac.CronManage))
	var restart func()
	if os.Getenv("INVOCATION_ID") != "" { // 由 systemd 启动
		restart = restartPanel
	}
	backupmod.New(backupmod.Deps{
		Store: s.st, Tasks: s.tasks, Engine: s.backup, Cron: s.cron, Confirm: s.confirm, Audit: a, Restart: restart,
	}).Register(local, s.perm(rbac.BackupView), s.perm(rbac.BackupManage))

	e.NoRoute(s.static)
	return e
}

// static 提供内嵌的前端资源；未知路径回退到 index.html（单页应用路由）。
func (s *Server) static(c *gin.Context) {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		api.Fail(c, api.NotFound("接口不存在"))
		return
	}
	p := strings.TrimPrefix(c.Request.URL.Path, "/")
	if strings.HasPrefix(p, "api/") {
		api.Fail(c, api.NotFound("接口不存在"))
		return
	}
	if p != "" {
		if fi, err := fs.Stat(s.web, p); err == nil && !fi.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			c.FileFromFS(p, http.FS(s.web))
			return
		}
	}
	b, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		c.String(http.StatusOK, "sshDesktop %s：前端尚未构建（cd web && npm run build）", s.version)
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", b)
}

// Run 启动 HTTP 服务和后台任务，直到 ctx 结束。
func (s *Server) Run(ctx context.Context) error {
	go s.col.Run(ctx)
	go s.background(ctx)
	go s.probe.Run(ctx)
	go s.apps.Load()
	if err := s.cron.Sync(); err != nil {
		slog.Info("计划任务未写入系统 cron", "err", err)
	}
	defer s.monitor.Stop()
	if sock := s.agentSocket(); sock != "" {
		go func() {
			if err := agent.Serve(ctx, sock, s.monitor.Provider(), s.version, true); err != nil {
				slog.Warn("面板自带的 agent 接口未启动，其他面板将无法读取本机监控", "socket", sock, "err", err)
			}
		}()
	}
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr(),
		Handler:           s.engine,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("sshDesktop 已启动", "version", s.version, "listen", srv.Addr, "https", s.cfg.Public())
		var err error
		if s.cfg.Public() {
			err = srv.ListenAndServeTLS(s.cfg.HTTPS.Cert, s.cfg.HTTPS.Key)
		} else {
			err = srv.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// agentSocket 返回面板自带 agent 接口的 socket 路径；配置为 "-" 时不提供。
func (s *Server) agentSocket() string {
	switch s.cfg.AgentSocket {
	case "":
		return agent.SocketPath
	case "-":
		return ""
	}
	return s.cfg.AgentSocket
}

func (s *Server) background(ctx context.Context) {
	minute := time.NewTicker(time.Minute)
	defer minute.Stop()
	hourly := func() {
		s.auth.Cleanup()
		s.audit.Cleanup()
		s.tasks.Cleanup()
		s.monitor.Cleanup()
		s.batch.Cleanup()
		s.probe.Cleanup()
		days := s.st.Runtime().TrashRetentionDays
		files.CleanupTrash(s.nodes.Local().FS(), days)
		for _, n := range s.nodes.Remote() {
			if n.Status().State == node.StateOnline {
				go files.CleanupTrash(n.FS(), days)
			}
		}
	}
	hourly()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-minute.C:
			s.terminal.Reap()
			n++
			if n%60 == 0 {
				hourly()
			}
		}
	}
}
