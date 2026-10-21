// Package sshd 是 SSH 设置（仅本机）：端口、密码登录、root 登录、公钥登录、授权密钥和登录日志。
// 修改写入面板管理的独立文件 sshd_config.d/00-sshdesktop.conf（OpenSSH 取第一次出现的值，所以它排在最前面），
// 先用 sshd -t 校验，再进入 60 秒确认，到时未确认自动回滚。改端口时先同时监听新旧端口，确认后再关闭旧端口。
package sshd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/pending"
	"github.com/gin-gonic/gin"
)

const (
	pendingKind = "sshd"
	offMark     = "#sshdesktop-off# "
	managedName = "00-sshdesktop.conf"
)

// ConfirmTimeout 是修改后等待确认的时间。
var ConfirmTimeout = 60 * time.Second

// Firewall 是防火墙模块提供的能力。
type Firewall interface {
	EnsurePort(ctx context.Context, port int) error
}

type Deps struct {
	Pending  *pending.Manager
	Confirm  func(c *gin.Context) bool
	Audit    func(module, action string) gin.HandlerFunc
	Firewall Firewall
	Done     func(c *pending.Change, result string, err error)
	DataDir  string
}

type Service struct {
	d  Deps
	mu sync.Mutex

	// 测试中替换
	mainConf string
	dropDir  string
	sshdBin  func() string
	reloadFn func(ctx context.Context) error
	passwd   string

	portsMu   sync.Mutex
	ports     []int
	portsTime time.Time
}

func New(d Deps) *Service {
	s := &Service{d: d, mainConf: "/etc/ssh/sshd_config", dropDir: "/etc/ssh/sshd_config.d", passwd: "/etc/passwd"}
	s.sshdBin = func() string {
		for _, p := range []string{"/usr/sbin/sshd", "/usr/bin/sshd"} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		p, _ := exec.LookPath("sshd")
		return p
	}
	s.reloadFn = reloadService
	d.Pending.Handle(pendingKind, pending.Handler{Restore: s.restore, Commit: s.commit, Done: d.Done})
	return s
}

// SetTesting 仅用于测试：配置文件位置、sshd 可执行文件、重新加载函数和 /etc/passwd。
func (s *Service) SetTesting(mainConf, dropDir string, bin func() string, reload func(ctx context.Context) error, passwd string) {
	s.mainConf, s.dropDir, s.sshdBin, s.reloadFn, s.passwd = mainConf, dropDir, bin, reload, passwd
}

func (s *Service) managed() string { return filepath.Join(s.dropDir, managedName) }

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/sshd")
	g.GET("", view, s.status)
	g.PUT("/config", manage, a("sshd", "config"), s.change)
	g.POST("/pending/confirm", manage, a("sshd", "confirm"), s.pendingConfirm)
	g.POST("/pending/revert", manage, a("sshd", "revert"), s.pendingRevert)
	g.GET("/keys", view, s.keys)
	g.POST("/keys", manage, a("sshd", "key-add"), s.keyAdd)
	g.POST("/keys/delete", manage, a("sshd", "key-delete"), s.keyDelete)
	g.GET("/logins", view, s.logins)
}

/* ---------- 读取配置 ---------- */

// Effective 是 sshd -T 给出的生效配置。
type Effective struct {
	Ports           []int    `json:"ports"`
	ListenAddresses []string `json:"listenAddresses"`
	PasswordAuth    bool     `json:"passwordAuth"`
	PermitRootLogin string   `json:"permitRootLogin"`
	PubkeyAuth      bool     `json:"pubkeyAuth"`
	KbdInteractive  bool     `json:"kbdInteractive"`
}

func (s *Service) sshdArgs(args ...string) []string {
	return append([]string{"-f", s.mainConf}, args...)
}

// effective 执行 sshd -T 读取生效配置。
func (s *Service) effective(ctx context.Context) (Effective, error) {
	var e Effective
	bin := s.sshdBin()
	if bin == "" {
		return e, errors.New("没有安装 OpenSSH 服务端（sshd）")
	}
	os.MkdirAll("/run/sshd", 0o755) // sshd -T 要求权限分离目录存在
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, s.sshdArgs("-T")...).CombinedOutput()
	if err != nil {
		return e, fmt.Errorf("sshd -T 失败：%s", strings.TrimSpace(string(out)))
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		switch k {
		case "port":
			if n, err := strconv.Atoi(v); err == nil {
				e.Ports = append(e.Ports, n)
			}
		case "listenaddress":
			e.ListenAddresses = append(e.ListenAddresses, v)
		case "passwordauthentication":
			e.PasswordAuth = v == "yes"
		case "permitrootlogin":
			e.PermitRootLogin = v
		case "pubkeyauthentication":
			e.PubkeyAuth = v == "yes"
		case "kbdinteractiveauthentication":
			e.KbdInteractive = v == "yes"
		}
	}
	if e.PermitRootLogin == "without-password" {
		e.PermitRootLogin = "prohibit-password"
	}
	return e, nil
}

// Ports 返回 SSH 正在使用的端口（防火墙防锁死用），30 秒缓存。
func (s *Service) Ports() []int {
	s.portsMu.Lock()
	defer s.portsMu.Unlock()
	if s.ports != nil && time.Since(s.portsTime) < 30*time.Second {
		return s.ports
	}
	e, err := s.effective(context.Background())
	if err != nil || len(e.Ports) == 0 {
		return []int{22}
	}
	s.ports, s.portsTime = e.Ports, time.Now()
	return s.ports
}

func (s *Service) invalidatePorts() {
	s.portsMu.Lock()
	s.ports = nil
	s.portsMu.Unlock()
}

// configFiles 返回主配置和 sshd_config.d 下的配置文件（不含面板自己的文件）。
func (s *Service) configFiles() []string {
	files := []string{s.mainConf}
	entries, _ := os.ReadDir(s.dropDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".conf") && e.Name() != managedName {
			files = append(files, filepath.Join(s.dropDir, e.Name()))
		}
	}
	return files
}

var reInclude = regexp.MustCompile(`(?im)^\s*Include\s+(\S+)`)

// included 表示主配置包含 sshd_config.d/*.conf。
func (s *Service) included() bool {
	b, err := os.ReadFile(s.mainConf)
	if err != nil {
		return false
	}
	for _, m := range reInclude.FindAllStringSubmatch(string(b), -1) {
		p := m[1]
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(s.mainConf), p)
		}
		if p == filepath.Join(s.dropDir, "*.conf") {
			return true
		}
	}
	return false
}

type statusView struct {
	Installed bool       `json:"installed"`
	Version   string     `json:"version"`
	Service   string     `json:"service"`
	Active    bool       `json:"active"`
	Socket    bool       `json:"socket"`
	Effective *Effective `json:"effective,omitempty"`
	Error     string     `json:"error,omitempty"`
	Files     []string   `json:"files"`
	Managed   string     `json:"managed"`
	Included  bool       `json:"included"`
	Notes     []string   `json:"notes"`
}

// Status 返回 SSH 服务和配置状态（环境体检也用它）。
func (s *Service) Status(ctx context.Context) statusView {
	v := statusView{Files: s.configFiles(), Notes: []string{}}
	bin := s.sshdBin()
	v.Installed = bin != ""
	if !v.Installed {
		v.Error = "没有安装 OpenSSH 服务端"
		return v
	}
	if out, _ := exec.CommandContext(ctx, bin, "-V").CombinedOutput(); len(out) > 0 {
		v.Version = strings.TrimSpace(strings.SplitN(string(out), ",", 2)[0])
	}
	v.Service = serviceName(ctx)
	v.Active = isActive(ctx, v.Service) || isActive(ctx, "ssh.socket")
	v.Socket = isActive(ctx, "ssh.socket")
	if e, err := s.effective(ctx); err == nil {
		v.Effective = &e
	} else {
		v.Error = err.Error()
	}
	if b, err := os.ReadFile(s.managed()); err == nil {
		v.Managed = string(b)
	}
	v.Included = s.included()
	if !v.Included {
		v.Notes = append(v.Notes, "主配置没有包含 sshd_config.d/*.conf，第一次修改时面板会在主配置开头加上 Include（修改前备份）")
	}
	if len(v.Files) > 1 {
		v.Notes = append(v.Notes, "配置分散在多个文件中，面板的修改写入独立的 "+s.managed()+"，它排在最前面，优先生效")
	}
	return v
}

func (s *Service) status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	out := gin.H{"status": s.Status(ctx)}
	if p := s.d.Pending.Get(pendingKind); p != nil {
		out["pending"] = p
	}
	api.OK(c, out)
}

func isActive(ctx context.Context, unit string) bool {
	out, _ := exec.CommandContext(ctx, "systemctl", "is-active", unit).Output()
	return strings.TrimSpace(string(out)) == "active"
}

// serviceName 返回 SSH 的 systemd 服务名：Debian / Ubuntu 为 ssh，RHEL 系为 sshd。
func serviceName(ctx context.Context) string {
	for _, u := range []string{"ssh", "sshd"} {
		out, _ := exec.CommandContext(ctx, "systemctl", "show", "-p", "LoadState", "--value", u+".service").Output()
		if strings.TrimSpace(string(out)) == "loaded" {
			return u
		}
	}
	return "ssh"
}

// reloadService 让 sshd 重新读取配置。使用 socket 激活时（Ubuntu 22.10 起）端口在 ssh.socket 中，要重新生成并重启它。
func reloadService(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		// 没有 systemd（容器等）：向 sshd 主进程发送 SIGHUP 重新加载
		b, err := os.ReadFile("/run/sshd.pid")
		if err != nil {
			return errors.New("没有 systemd，也找不到 /run/sshd.pid，无法重新加载 sshd")
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		if pid <= 1 {
			return errors.New("/run/sshd.pid 内容无效")
		}
		return syscall.Kill(pid, syscall.SIGHUP)
	}
	if isActive(ctx, "ssh.socket") {
		exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
		if out, err := exec.CommandContext(ctx, "systemctl", "restart", "ssh.socket").CombinedOutput(); err != nil {
			return fmt.Errorf("重启 ssh.socket 失败：%s", strings.TrimSpace(string(out)))
		}
		return nil
	}
	svc := serviceName(ctx)
	if out, err := exec.CommandContext(ctx, "systemctl", "reload-or-restart", svc).CombinedOutput(); err != nil {
		return fmt.Errorf("重新加载 %s 失败：%s", svc, strings.TrimSpace(string(out)))
	}
	return nil
}

/* ---------- 修改 ---------- */

// Settings 是面板管理的 SSH 设置。
type Settings struct {
	Port            int    `json:"port"`
	PasswordAuth    bool   `json:"passwordAuth"`
	PermitRootLogin string `json:"permitRootLogin"` // yes / prohibit-password / no
	PubkeyAuth      bool   `json:"pubkeyAuth"`
}

type extra struct {
	OldPorts []int    `json:"oldPorts"`
	Next     Settings `json:"next"`
}

// render 生成面板管理的配置文件。ports 为本次要监听的全部端口。
func render(set Settings, ports []int) string {
	var b strings.Builder
	b.WriteString("# 由 sshDesktop 管理，请在面板的“SSH 设置”中修改，手动修改会被覆盖。\n")
	b.WriteString("# 这个文件排在 sshd_config.d 的最前面：OpenSSH 对同一选项使用第一次出现的值。\n")
	for _, p := range ports {
		fmt.Fprintf(&b, "Port %d\n", p)
	}
	yn := func(v bool) string {
		if v {
			return "yes"
		}
		return "no"
	}
	fmt.Fprintf(&b, "PasswordAuthentication %s\n", yn(set.PasswordAuth))
	if !set.PasswordAuth {
		b.WriteString("KbdInteractiveAuthentication no\n")
	}
	fmt.Fprintf(&b, "PermitRootLogin %s\n", set.PermitRootLogin)
	fmt.Fprintf(&b, "PubkeyAuthentication %s\n", yn(set.PubkeyAuth))
	return b.String()
}

var (
	rePort  = regexp.MustCompile(`(?i)^\s*Port\s+\d+`)
	reMatch = regexp.MustCompile(`(?i)^\s*Match\s`)
)

// commentPorts 注释掉其他文件中的 Port（Port 会叠加，不注释就会继续监听旧端口）。只处理 Match 块之前的部分。
func commentPorts(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	changed := false
	for i, l := range lines {
		if reMatch.MatchString(l) {
			break
		}
		if rePort.MatchString(l) {
			lines[i] = offMark + l
			changed = true
		}
	}
	return strings.Join(lines, "\n"), changed
}

func (s *Service) test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.sshdBin(), s.sshdArgs("-t")...).CombinedOutput()
	if err != nil {
		return api.Err(400, api.CodeSSHDTest, "配置校验失败（sshd -t）："+strings.TrimSpace(string(out)))
	}
	return nil
}

func (s *Service) change(c *gin.Context) {
	var set Settings
	if !api.Bind(c, &set) {
		return
	}
	if set.Port < 1 || set.Port > 65535 {
		api.Fail(c, api.BadRequest("端口无效"))
		return
	}
	switch set.PermitRootLogin {
	case "yes", "prohibit-password", "no":
	default:
		api.Fail(c, api.BadRequest("root 登录只能是 yes、prohibit-password 或 no"))
		return
	}
	if !set.PasswordAuth && !set.PubkeyAuth {
		api.Fail(c, api.BadRequest("密码登录和公钥登录不能都关闭"))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	cur, err := s.effective(ctx)
	if err != nil {
		api.Fail(c, err)
		return
	}
	var detail []string
	portChanged := len(cur.Ports) != 1 || cur.Ports[0] != set.Port
	if portChanged {
		detail = append(detail, fmt.Sprintf("SSH 端口：%s → %d（确认前新旧端口同时可用，请用新端口测试登录）", joinInts(cur.Ports), set.Port))
	}
	if cur.PasswordAuth != set.PasswordAuth {
		detail = append(detail, "密码登录："+onOff(set.PasswordAuth))
	}
	if cur.PermitRootLogin != set.PermitRootLogin {
		detail = append(detail, "root 登录："+set.PermitRootLogin)
	}
	if cur.PubkeyAuth != set.PubkeyAuth {
		detail = append(detail, "公钥登录："+onOff(set.PubkeyAuth))
	}
	if len(detail) == 0 {
		api.Fail(c, api.BadRequest("设置没有变化"))
		return
	}
	api.Target(c, strings.Join(detail, "；"))
	if !s.d.Confirm(c) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	paths := append(s.configFiles(), s.managed())
	files, err := pending.Snap(paths...)
	if err != nil {
		api.Fail(c, err)
		return
	}
	ports := []int{set.Port}
	if portChanged {
		ports = append(append([]int{}, cur.Ports...), set.Port)
	}
	u := api.User(c)
	ex, _ := json.Marshal(extra{OldPorts: cur.Ports, Next: set})
	ch := &pending.Change{Kind: pendingKind, Title: "修改 SSH 设置", Detail: detail, UserID: u.UserID, Username: u.Username, Files: files, Extra: ex}
	res, err := s.d.Pending.Start(ch, ConfirmTimeout, func() error {
		if err := s.write(set, ports); err != nil {
			return err
		}
		if err := s.test(ctx); err != nil {
			return err
		}
		if portChanged && s.d.Firewall != nil {
			if err := s.d.Firewall.EnsurePort(ctx, set.Port); err != nil {
				return fmt.Errorf("防火墙放行新端口失败：%w", err)
			}
		}
		defer s.invalidatePorts()
		return s.reloadFn(ctx)
	})
	if err != nil {
		if errors.Is(err, pending.ErrBusy) {
			api.Fail(c, api.Err(409, api.CodePendingChange, err.Error()))
			return
		}
		var ae *api.Error
		if errors.As(err, &ae) {
			api.Fail(c, ae)
			return
		}
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	api.OK(c, gin.H{"pending": res})
}

// write 写入面板管理的配置文件，注释掉其他文件中的 Port，主配置没有 Include 时在开头加上。
func (s *Service) write(set Settings, ports []int) error {
	if !s.included() {
		b, err := os.ReadFile(s.mainConf)
		if err != nil {
			return err
		}
		if err := s.backup(s.mainConf, b); err != nil {
			return err
		}
		head := "# 由 sshDesktop 添加：读取 sshd_config.d 中的配置\nInclude " + filepath.Join(s.dropDir, "*.conf") + "\n\n"
		if err := pending.WriteFile(s.mainConf, append([]byte(head), b...), 0o644); err != nil {
			return err
		}
	}
	// 面板的文件中总有 Port，其他文件中的 Port 要注释掉，否则会叠加监听
	for _, f := range s.configFiles() {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		next, changed := commentPorts(string(b))
		if !changed {
			continue
		}
		if err := s.backup(f, b); err != nil {
			return err
		}
		fi, _ := os.Stat(f)
		if err := pending.WriteFile(f, []byte(next), fi.Mode().Perm()); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(s.dropDir, 0o755); err != nil {
		return err
	}
	return pending.WriteFile(s.managed(), []byte(render(set, ports)), 0o644)
}

// backup 把第一次修改前的文件原样复制到 takeover-backup/ssh/（每个文件每天一份）。
func (s *Service) backup(path string, b []byte) error {
	if s.d.DataDir == "" {
		return nil
	}
	dst := filepath.Join(s.d.DataDir, "takeover-backup", "ssh", time.Now().Format("20060102"), path)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

// commit 在确认后关闭旧端口：只保留新端口。
func (s *Service) commit(c *pending.Change) error {
	var ex extra
	json.Unmarshal(c.Extra, &ex)
	if len(ex.OldPorts) == 1 && ex.OldPorts[0] == ex.Next.Port {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	prev, _ := os.ReadFile(s.managed())
	if err := pending.WriteFile(s.managed(), []byte(render(ex.Next, []int{ex.Next.Port})), 0o644); err != nil {
		return err
	}
	if err := s.test(ctx); err != nil {
		pending.WriteFile(s.managed(), prev, 0o644)
		return err
	}
	defer s.invalidatePorts()
	return s.reloadFn(ctx)
}

func (s *Service) restore(*pending.Change) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	defer s.invalidatePorts()
	return s.reloadFn(ctx)
}

func (s *Service) pendingConfirm(c *gin.Context) {
	var req struct {
		ID string `json:"id"`
	}
	c.ShouldBindJSON(&req)
	if err := s.d.Pending.Confirm(pendingKind, req.ID); err != nil {
		if errors.Is(err, pending.ErrNotFound) {
			api.Fail(c, api.Err(404, api.CodePendingChange, err.Error()))
			return
		}
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) pendingRevert(c *gin.Context) {
	var req struct {
		ID string `json:"id"`
	}
	c.ShouldBindJSON(&req)
	if err := s.d.Pending.Revert(pendingKind, req.ID); err != nil {
		if errors.Is(err, pending.ErrNotFound) {
			api.Fail(c, api.Err(404, api.CodePendingChange, err.Error()))
			return
		}
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	sort.Strings(s)
	return strings.Join(s, "、")
}

func onOff(v bool) string {
	if v {
		return "开启"
	}
	return "关闭"
}
