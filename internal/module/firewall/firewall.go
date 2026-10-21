// Package firewall 是防火墙管理（仅本机）：识别并管理 ufw 或 firewalld 中已有的全部规则；
// 两者都没有、只有手写 iptables / nftables 规则时只读展示，并给出接管建议。
// 可能挡住现有连接的修改（删除规则、添加拒绝规则、启用防火墙）进入 60 秒确认，到时未确认自动回滚；放行端口和防火墙未启用时的修改直接生效。
// SSH 端口（以及对外开放时的面板端口）始终保持放行。
package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/pending"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
)

const pendingKind = "firewall"

// ConfirmTimeout 是修改后等待确认的时间。
var ConfirmTimeout = 60 * time.Second

// Rule 是一条规则。Port 为空表示所有端口；Source 为空表示任意来源。
type Rule struct {
	ID       string `json:"id"`
	Action   string `json:"action"` // allow / deny / reject / limit
	Port     string `json:"port"`   // 80、8000-9000 或空
	Proto    string `json:"proto"`  // tcp / udp / 空（两者）
	Source   string `json:"source"`
	Family   string `json:"family"` // ipv4 / ipv6 / 空（两者）
	Service  string `json:"service,omitempty"`
	Comment  string `json:"comment,omitempty"`
	Raw      string `json:"raw"`
	Editable bool   `json:"editable"`
	// Direction 为 out 的规则只读展示
	Direction string `json:"direction,omitempty"`
}

// Status 是防火墙的状态。
type Status struct {
	Backend   string   `json:"backend"` // ufw / firewalld / none
	Active    bool     `json:"active"`
	Version   string   `json:"version,omitempty"`
	Zone      string   `json:"zone,omitempty"`
	Default   string   `json:"default,omitempty"` // 入站默认策略
	Rules     []Rule   `json:"rules"`
	Raw       string   `json:"raw,omitempty"`       // none：iptables / nft 规则原文
	RawTool   string   `json:"rawTool,omitempty"`   // iptables / nftables
	Installed []string `json:"installed,omitempty"` // 已安装但未启用的工具
	Managers  []string `json:"managers,omitempty"`  // 规则中发现的其他管理者：1Panel / fail2ban
	Notes     []string `json:"notes,omitempty"`
}

// backend 是 ufw 或 firewalld。
type backend interface {
	name() string
	status(ctx context.Context) (Status, error)
	add(ctx context.Context, r Rule) error
	remove(ctx context.Context, r Rule) error
	enable(ctx context.Context, on bool) error
	files(ctx context.Context) []string
	// reload 在还原快照文件后让规则生效；active 是修改前的启用状态
	reload(ctx context.Context, active bool) error
}

type Deps struct {
	Pending *pending.Manager
	Tasks   *task.Manager
	Confirm func(c *gin.Context) bool
	Audit   func(module, action string) gin.HandlerFunc
	// ProtectedPorts 返回必须保持放行的端口（端口 → 用途）
	ProtectedPorts func() map[int]string
	Done           func(c *pending.Change, result string, err error)
}

type Service struct {
	d  Deps
	mu sync.Mutex
	// 测试中替换
	detect func(ctx context.Context) backend
}

func New(d Deps) *Service {
	s := &Service{d: d, detect: detect}
	d.Pending.Handle(pendingKind, pending.Handler{Restore: s.restore, Done: d.Done})
	return s
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return string(out), fmt.Errorf("%s %s 失败：%s", name, strings.Join(args, " "), msg)
	}
	return string(out), nil
}

func has(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// detect 选择后端：正在运行的 firewalld 优先，其次 ufw（启用或未启用），再次未运行的 firewalld。
func detect(ctx context.Context) backend {
	fwd := has("firewall-cmd")
	if fwd && firewalldRunning(ctx) {
		return &firewalld{}
	}
	if has("ufw") {
		return &ufw{}
	}
	if fwd {
		return &firewalld{}
	}
	return nil
}

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/firewall")
	g.GET("", view, s.status)
	g.POST("/rules", manage, a("firewall", "add-rule"), s.addRule)
	g.POST("/rules/delete", manage, a("firewall", "delete-rule"), s.deleteRule)
	g.POST("/enable", manage, a("firewall", "enable"), s.enable)
	g.POST("/install", manage, a("firewall", "install"), s.install)
	g.GET("/pending", view, s.pendingGet)
	g.POST("/pending/confirm", manage, a("firewall", "confirm"), s.pendingConfirm)
	g.POST("/pending/revert", manage, a("firewall", "revert"), s.pendingRevert)
}

// Status 返回防火墙状态（环境体检也用它）。
func (s *Service) Status(ctx context.Context) Status {
	b := s.detect(ctx)
	if b == nil {
		st := Status{Backend: "none", Rules: []Rule{}}
		st.RawTool, st.Raw = rawRules(ctx)
		st.Managers = managers(st.Raw)
		st.Notes = noneNotes(st.Raw, st.Managers)
		return st
	}
	st, err := b.status(ctx)
	if err != nil {
		st = Status{Backend: b.name(), Rules: []Rule{}, Notes: []string{err.Error()}}
	}
	if st.Rules == nil {
		st.Rules = []Rule{}
	}
	tool, raw := rawRules(ctx)
	if !st.Active {
		st.RawTool, st.Raw = tool, raw
		if strings.TrimSpace(st.Raw) != "" {
			st.Notes = append(st.Notes, "防火墙未启用，下面是系统中现有的 "+st.RawTool+" 规则（只读）。启用后由 "+b.name()+" 管理。")
		}
	}
	st.Managers = managers(raw)
	if slices.Contains(st.Managers, "1Panel") {
		st.Notes = append(st.Notes, "检测到 1Panel 的规则链（1PANEL_*）。1Panel 也会管理 "+b.name()+"，请只在一个面板中修改防火墙规则，避免互相覆盖。")
	}
	return st
}

// managers 从规则原文中识别其他管理防火墙的程序。
func managers(raw string) []string {
	var out []string
	if strings.Contains(raw, "1PANEL") {
		out = append(out, "1Panel")
	}
	if strings.Contains(raw, "f2b-") {
		out = append(out, "fail2ban")
	}
	return out
}

// rawRules 读取 iptables / nftables 规则原文（只读展示）。
func rawRules(ctx context.Context) (string, string) {
	if has("iptables-save") {
		if out, err := run(ctx, "iptables-save"); err == nil && strings.Contains(out, "-A ") {
			return "iptables", out
		}
	}
	if has("nft") {
		if out, err := run(ctx, "nft", "list", "ruleset"); err == nil && strings.TrimSpace(out) != "" {
			return "nftables", out
		}
	}
	return "", ""
}

func noneNotes(raw string, mgrs []string) []string {
	notes := []string{"没有安装 ufw 或 firewalld，面板无法管理防火墙。"}
	if slices.Contains(mgrs, "1Panel") {
		// 另一个面板在直接管理 iptables，再装 ufw 会让两套规则互相覆盖
		notes = append(notes,
			"检测到 1Panel 的规则链（1PANEL_*），防火墙正由 1Panel 管理（下方只读展示）。不建议在这里安装 ufw 或 firewalld：两个面板会各自改写规则，可能互相覆盖甚至断开 SSH。请继续在 1Panel 中管理防火墙。")
		if slices.Contains(mgrs, "fail2ban") {
			notes = append(notes, "f2b-* 规则链是 fail2ban 的封禁规则，由 fail2ban 自动维护。")
		}
		return notes
	}
	if slices.Contains(mgrs, "fail2ban") {
		notes = append(notes, "f2b-* 规则链是 fail2ban 的封禁规则，由 fail2ban 自动维护，与 ufw / firewalld 可以共存。")
	}
	if strings.TrimSpace(raw) != "" {
		notes = append(notes,
			"系统中有手写的 iptables / nftables 规则（下方只读展示，例如 Oracle Cloud 默认镜像）。直接改写这些规则容易与云平台的默认规则冲突，面板不做修改。",
			"建议：Debian / Ubuntu 安装 ufw，RHEL 系安装 firewalld，再由面板接管。安装后先在面板中放行需要的端口，再启用防火墙；启用时面板会自动放行 SSH 端口。",
			"迁移步骤：① 记下现有规则中放行的端口；② 安装 ufw 或 firewalld；③ 在面板中逐条添加放行规则；④ 启用防火墙并在 60 秒内确认；⑤ 确认无误后删除旧的规则文件（如 /etc/iptables/rules.v4），避免重启后两套规则同时生效。")
	}
	return notes
}

func (s *Service) status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	st := s.Status(ctx)
	out := gin.H{"status": st, "protected": s.d.ProtectedPorts()}
	if p := s.d.Pending.Get(pendingKind); p != nil {
		out["pending"] = p
	}
	api.OK(c, out)
}

/* ---------- 校验与防锁死 ---------- */

func normalize(r *Rule) error {
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	switch r.Action {
	case "allow", "deny", "reject":
	default:
		return errors.New("动作只能是 allow、deny 或 reject")
	}
	r.Proto = strings.ToLower(strings.TrimSpace(r.Proto))
	if r.Proto != "" && r.Proto != "tcp" && r.Proto != "udp" {
		return errors.New("协议只能是 tcp、udp 或留空（两者）")
	}
	r.Port = strings.ReplaceAll(strings.TrimSpace(r.Port), ":", "-")
	if r.Port != "" {
		lo, hi, err := portRange(r.Port)
		if err != nil || lo < 1 || hi > 65535 || lo > hi {
			return fmt.Errorf("端口 %q 无效，填写如 80 或 8000-9000", r.Port)
		}
	}
	r.Source = strings.TrimSpace(r.Source)
	r.Family = ""
	if r.Source != "" {
		ip := net.ParseIP(r.Source)
		if ip == nil {
			var n *net.IPNet
			var err error
			ip, n, err = net.ParseCIDR(r.Source)
			if err != nil {
				return fmt.Errorf("来源 %q 不是有效的 IP 或网段", r.Source)
			}
			r.Source = n.String()
		}
		r.Family = "ipv4"
		if ip.To4() == nil {
			r.Family = "ipv6"
		}
	}
	if r.Port == "" && r.Source == "" {
		return errors.New("端口和来源至少填写一个")
	}
	if r.Port == "" && r.Proto != "" {
		r.Proto = ""
	}
	if strings.ContainsAny(r.Comment, "\n'\"") {
		return errors.New("备注不能包含引号或换行")
	}
	return nil
}

func portRange(p string) (int, int, error) {
	if p == "" {
		return 1, 65535, nil
	}
	a, b, ok := strings.Cut(p, "-")
	lo, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, err
	}
	hi := lo
	if ok {
		if hi, err = strconv.Atoi(b); err != nil {
			return 0, 0, err
		}
	}
	return lo, hi, nil
}

// servicePorts 是常见服务名（firewalld 服务、ufw 应用）对应的端口。
var servicePorts = map[string]string{"ssh": "22", "OpenSSH": "22", "http": "80", "https": "443", "Nginx Full": "80,443", "Nginx HTTP": "80", "Nginx HTTPS": "443"}

func covers(r Rule, port int) bool {
	if r.Port == "" && r.Service != "" {
		for _, p := range strings.Split(servicePorts[r.Service], ",") {
			if p == strconv.Itoa(port) {
				return true
			}
		}
		return false
	}
	lo, hi, err := portRange(r.Port)
	return err == nil && port >= lo && port <= hi && (r.Proto == "" || r.Proto == "tcp")
}

// openFor 表示规则列表中有放行端口 port（任意来源、TCP）的规则。
func openFor(rules []Rule, port int) bool {
	for _, r := range rules {
		if r.Action == "allow" && r.Source == "" && covers(r, port) {
			return true
		}
	}
	return false
}

// guardDelete 拒绝删除唯一放行受保护端口的规则。
func (s *Service) guardDelete(st Status, target Rule) error {
	if !st.Active || target.Action != "allow" || target.Source != "" {
		return nil
	}
	rest := []Rule{}
	for _, r := range st.Rules {
		if r.ID != target.ID {
			rest = append(rest, r)
		}
	}
	for port, what := range s.d.ProtectedPorts() {
		if covers(target, port) && !openFor(rest, port) {
			return fmt.Errorf("这条规则放行了%s端口 %d，删除后会失联。请先添加其他放行规则", what, port)
		}
	}
	return nil
}

// guardAdd 拒绝对受保护端口添加“任意来源拒绝”的规则。
func (s *Service) guardAdd(r Rule) error {
	if r.Action == "allow" || r.Source != "" {
		return nil
	}
	for port, what := range s.d.ProtectedPorts() {
		if r.Port == "" || covers(r, port) {
			return fmt.Errorf("不能拒绝所有来源访问%s端口 %d，否则会失联", what, port)
		}
	}
	return nil
}

/* ---------- 修改（60 秒确认） ---------- */

type extra struct {
	Backend string `json:"backend"`
	Active  bool   `json:"active"`
}

// change 执行一次修改。可能导致失联的修改（删除规则、添加拒绝规则、启用或停用）在 60 秒内确认，否则回滚；
// safe 返回 true 时（防火墙未启用，或只是放行）直接生效。
func (s *Service) change(c *gin.Context, title string, detail []string, safe func(Status) bool, apply func(ctx context.Context, b backend, st Status) error) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.detect(ctx)
	if b == nil {
		api.Fail(c, api.Err(409, api.CodeFirewallMissing, "没有安装 ufw 或 firewalld"))
		return
	}
	st, err := b.status(ctx)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if safe != nil && safe(st) && s.d.Pending.Get(pendingKind) == nil {
		if err := apply(ctx, b, st); err != nil {
			api.Fail(c, api.BadRequest(err.Error()))
			return
		}
		api.OK(c, gin.H{"pending": nil})
		return
	}
	files, err := pending.Snap(b.files(ctx)...)
	if err != nil {
		api.Fail(c, err)
		return
	}
	u := api.User(c)
	ex, _ := json.Marshal(extra{Backend: b.name(), Active: st.Active})
	ch := &pending.Change{Kind: pendingKind, Title: title, Detail: detail, UserID: u.UserID, Username: u.Username, Files: files, Extra: ex}
	res, err := s.d.Pending.Start(ch, ConfirmTimeout, func() error { return apply(ctx, b, st) })
	if err != nil {
		if errors.Is(err, pending.ErrBusy) {
			api.Fail(c, api.Err(409, api.CodePendingChange, err.Error()))
			return
		}
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	api.OK(c, gin.H{"pending": res})
}

func (s *Service) restore(c *pending.Change) error {
	var ex extra
	json.Unmarshal(c.Extra, &ex)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var b backend
	switch ex.Backend {
	case "ufw":
		b = &ufw{}
	case "firewalld":
		b = &firewalld{}
	default:
		return nil
	}
	return b.reload(ctx, ex.Active)
}

func describe(r Rule) string {
	act := map[string]string{"allow": "放行", "deny": "拒绝", "reject": "拒绝并回应", "limit": "限速"}[r.Action]
	what := "所有端口"
	if r.Port != "" {
		what = "端口 " + r.Port
		if r.Proto != "" {
			what += "/" + r.Proto
		}
	}
	if r.Service != "" {
		what = "服务 " + r.Service
	}
	src := "任意来源"
	if r.Source != "" {
		src = r.Source
	}
	return fmt.Sprintf("%s %s（来源：%s）", act, what, src)
}

func (s *Service) addRule(c *gin.Context) {
	var r Rule
	if !api.Bind(c, &r) {
		return
	}
	if err := normalize(&r); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	if err := s.guardAdd(r); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	api.Target(c, describe(r))
	safe := func(st Status) bool { return !st.Active || r.Action == "allow" }
	s.change(c, "添加防火墙规则", []string{describe(r)}, safe, func(ctx context.Context, b backend, st Status) error {
		return b.add(ctx, r)
	})
}

func (s *Service) deleteRule(c *gin.Context) {
	var req struct {
		ID string `json:"id"`
	}
	if !api.Bind(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	st := s.Status(ctx)
	var target *Rule
	for i := range st.Rules {
		if st.Rules[i].ID == req.ID {
			target = &st.Rules[i]
		}
	}
	if target == nil {
		api.Fail(c, api.NotFound("规则不存在，请刷新"))
		return
	}
	if err := s.guardDelete(st, *target); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	r := *target
	api.Target(c, r.Raw)
	s.change(c, "删除防火墙规则", []string{describe(r), r.Raw}, func(st Status) bool { return !st.Active }, func(ctx context.Context, b backend, st Status) error {
		return b.remove(ctx, r)
	})
}

// ensureProtected 在启用前放行受保护的端口。
func (s *Service) ensureProtected(ctx context.Context, b backend, st Status) error {
	ports := s.d.ProtectedPorts()
	keys := make([]int, 0, len(ports))
	for p := range ports {
		keys = append(keys, p)
	}
	sort.Ints(keys)
	for _, p := range keys {
		if !openFor(st.Rules, p) {
			if err := b.add(ctx, Rule{Action: "allow", Port: strconv.Itoa(p), Proto: "tcp", Comment: "sshdesktop " + ports[p]}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) enable(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !api.Bind(c, &req) {
		return
	}
	title := "停用防火墙"
	detail := []string{"所有端口将不再受防火墙限制"}
	if req.Enabled {
		title = "启用防火墙"
		detail = []string{"未放行的端口将无法从外部访问"}
		for p, what := range s.d.ProtectedPorts() {
			detail = append(detail, fmt.Sprintf("自动放行%s端口 %d", what, p))
		}
	}
	api.Target(c, title)
	if !s.d.Confirm(c) {
		return
	}
	// 停用不会导致失联，直接生效
	s.change(c, title, detail, func(Status) bool { return !req.Enabled }, func(ctx context.Context, b backend, st Status) error {
		if req.Enabled {
			if err := s.ensureProtected(ctx, b, st); err != nil {
				return err
			}
		}
		return b.enable(ctx, req.Enabled)
	})
}

// EnsurePort 放行一个 TCP 端口（面板对外开放、SSH 改端口时调用）。防火墙未启用或没有安装时什么也不做。
func (s *Service) EnsurePort(ctx context.Context, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.detect(ctx)
	if b == nil {
		return nil
	}
	st, err := b.status(ctx)
	if err != nil || !st.Active || openFor(st.Rules, port) {
		return err
	}
	return b.add(ctx, Rule{Action: "allow", Port: strconv.Itoa(port), Proto: "tcp", Comment: "sshdesktop"})
}

func (s *Service) pendingGet(c *gin.Context) {
	api.OK(c, s.d.Pending.Get(pendingKind))
}

func (s *Service) pendingConfirm(c *gin.Context) {
	var req struct {
		ID string `json:"id"`
	}
	c.ShouldBindJSON(&req)
	if err := s.d.Pending.Confirm(pendingKind, req.ID); err != nil {
		api.Fail(c, api.Err(404, api.CodePendingChange, err.Error()))
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

const installScript = `set -e
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y ufw
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y firewalld
  echo "firewalld 已安装但没有启动，请在面板中添加放行规则后再启用"
  systemctl disable --now firewalld || true
else
  echo "不支持的发行版：没有 apt-get 或 dnf" >&2; exit 1
fi`

// install 安装 ufw（Debian / Ubuntu）或 firewalld（RHEL 系），安装后不启用。
func (s *Service) install(c *gin.Context) {
	t := s.d.Tasks.Start("firewall.install", "安装防火墙", api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		out := t.Lines()
		defer out.Flush()
		cmd := exec.CommandContext(ctx, "sh", "-c", installScript)
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("安装失败：%w", err)
		}
		return nil
	})
	api.OK(c, t)
}
