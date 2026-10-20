package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// 节点连接状态。
const (
	StateConnecting = "connecting"
	StateOnline     = "online"
	StateOffline    = "offline"
	// StateHostKeyMismatch：主机密钥与确认过的不一致，拒绝连接，直到用户重新确认。
	StateHostKeyMismatch = "hostkey-mismatch"
	StateAuthFailed      = "auth-failed"
)

type Status struct {
	State     string  `json:"state"`
	Error     string  `json:"error,omitempty"`
	Since     int64   `json:"since"` // 毫秒
	LatencyMs float64 `json:"latencyMs"`
}

var (
	ErrOffline = errors.New("节点未连接")
	errScanned = errors.New("已取得主机密钥")
)

// HostKeyMismatchError 表示服务器的主机密钥变了。
type HostKeyMismatchError struct{ Got ssh.PublicKey }

func (e *HostKeyMismatchError) Error() string {
	return "主机密钥与之前确认的不一致（现在是 " + ssh.FingerprintSHA256(e.Got) + "），可能是服务器重装，也可能是中间人攻击，已拒绝连接"
}

// SSHConfig 是连接一个远程节点所需的全部信息（凭据已解密）。
type SSHConfig struct {
	ID       string
	Host     string
	Port     int
	User     string
	Key      []byte // PEM 私钥；为空时用密码
	KeyPass  []byte
	Password string
	HostKey  ssh.PublicKey
	// Err 不为空表示配置不可用（例如凭据无法解密），节点不会尝试连接。
	Err string
}

func (c SSHConfig) addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

func (c SSHConfig) auth() ([]ssh.AuthMethod, error) {
	if len(c.Key) > 0 {
		var signer ssh.Signer
		var err error
		if len(c.KeyPass) > 0 {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(c.Key, c.KeyPass)
		} else {
			signer, err = ssh.ParsePrivateKey(c.Key)
		}
		if err != nil {
			var pe *ssh.PassphraseMissingError
			if errors.As(err, &pe) {
				return nil, errors.New("私钥有口令保护，请填写私钥口令")
			}
			return nil, fmt.Errorf("私钥无效: %w", err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	}
	pw := c.Password
	return []ssh.AuthMethod{
		ssh.Password(pw),
		ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range ans {
				ans[i] = pw
			}
			return ans, nil
		}),
	}, nil
}

// hostKeyAlgorithms 让协商结果固定为确认过的密钥类型，否则服务器有多种密钥时可能拿到另一种。
func hostKeyAlgorithms(k ssh.PublicKey) []string {
	if k == nil {
		return nil
	}
	if k.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{k.Type()}
}

func dial(ctx context.Context, cfg SSHConfig, auth []ssh.AuthMethod) (*ssh.Client, error) {
	cc := &ssh.ClientConfig{
		User:              cfg.User,
		Auth:              auth,
		Timeout:           15 * time.Second,
		HostKeyAlgorithms: hostKeyAlgorithms(cfg.HostKey),
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if cfg.HostKey == nil {
				return errors.New("主机密钥未确认")
			}
			if !bytes.Equal(key.Marshal(), cfg.HostKey.Marshal()) {
				return &HostKeyMismatchError{Got: key}
			}
			return nil
		},
	}
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", cfg.addr())
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, cfg.addr(), cc)
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return ssh.NewClient(c, chans, reqs), nil
}

// ScanHostKey 连接服务器取得主机密钥（不认证），用于添加节点时让用户确认指纹。
func ScanHostKey(ctx context.Context, host string, port int) (ssh.PublicKey, error) {
	var got ssh.PublicKey
	cc := &ssh.ClientConfig{
		User: "sshdesktop",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errScanned
		},
		Timeout: 15 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	_, _, _, err = ssh.NewClientConn(conn, addr, cc)
	if got != nil {
		return got, nil
	}
	if err == nil {
		err = errors.New("没有收到主机密钥")
	}
	return nil, err
}

// TestConnection 用给定配置连接一次并执行 true，用于添加节点前验证凭据。
func TestConnection(ctx context.Context, cfg SSHConfig) error {
	auth, err := cfg.auth()
	if err != nil {
		return err
	}
	c, err := dial(ctx, cfg, auth)
	if err != nil {
		return friendlyDialError(err)
	}
	defer c.Close()
	s, err := c.NewSession()
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Run("true")
}

func friendlyDialError(err error) error {
	var hk *HostKeyMismatchError
	switch {
	case errors.As(err, &hk):
		return err
	case strings.Contains(err.Error(), "unable to authenticate"):
		return errors.New("认证失败：用户名、私钥或密码不正确")
	case strings.Contains(err.Error(), "connection refused"):
		return errors.New("连接被拒绝：请检查地址和 SSH 端口")
	case strings.Contains(err.Error(), "i/o timeout"), errors.Is(err, context.DeadlineExceeded):
		return errors.New("连接超时：请检查地址、端口和防火墙")
	case strings.Contains(err.Error(), "no such host"):
		return errors.New("无法解析主机名")
	}
	return err
}

// SSH 是通过 SSH 管理的远程节点：维持一条长连接，终端、SFTP、命令、agent 通道都复用它。
type SSH struct {
	cfg      SSHConfig
	auth     []ssh.AuthMethod
	onStatus func(id string, st Status)

	mu     sync.Mutex
	client *ssh.Client
	sftp   *sftp.Client
	home   string
	status Status
	ready  chan struct{} // 每次状态变化时关闭并替换，用于等待连接
	wake   chan struct{}
	sem    chan struct{}
	tsem   chan struct{}
	fs     *vfs.SFTP
	ctx    context.Context
	cancel context.CancelFunc
}

// 每个节点同时最多 32 个 SSH 通道、8 个 SFTP 传输，避免拖垮远程 sshd。
const (
	maxChannels  = 32
	maxTransfers = 8
)

// NewSSH 创建节点并在后台开始连接。onStatus 在状态变化时调用。
func NewSSH(cfg SSHConfig, onStatus func(id string, st Status)) *SSH {
	ctx, cancel := context.WithCancel(context.Background())
	n := &SSH{
		cfg: cfg, onStatus: onStatus,
		status: Status{State: StateConnecting, Since: time.Now().UnixMilli()},
		ready:  make(chan struct{}), wake: make(chan struct{}, 1),
		sem: make(chan struct{}, maxChannels), tsem: make(chan struct{}, maxTransfers), ctx: ctx, cancel: cancel,
	}
	n.fs = n.newFS()
	if cfg.Err != "" {
		n.status = Status{State: StateAuthFailed, Error: cfg.Err, Since: time.Now().UnixMilli()}
		return n
	}
	auth, err := cfg.auth()
	if err != nil {
		n.status = Status{State: StateAuthFailed, Error: err.Error(), Since: time.Now().UnixMilli()}
		return n
	}
	n.auth = auth
	go n.loop()
	return n
}

func (n *SSH) ID() string { return n.cfg.ID }
func (n *SSH) Kind() Kind { return KindSSH }

func (n *SSH) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status
}

func (n *SSH) setStatus(st Status) {
	n.mu.Lock()
	changed := st.State != n.status.State || st.Error != n.status.Error
	if changed {
		st.Since = time.Now().UnixMilli()
	} else {
		st.Since = n.status.Since
	}
	n.status = st
	close(n.ready)
	n.ready = make(chan struct{})
	cb := n.onStatus
	n.mu.Unlock()
	if changed && cb != nil {
		cb(n.cfg.ID, st)
	}
}

// Close 断开连接并停止重连。
func (n *SSH) Close() {
	n.cancel()
	n.mu.Lock()
	c, s := n.client, n.sftp
	n.client, n.sftp = nil, nil
	n.mu.Unlock()
	if s != nil {
		s.Close()
	}
	if c != nil {
		c.Close()
	}
}

// Reconnect 立即重试连接（例如用户点了“重新连接”）。
func (n *SSH) Reconnect() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

func (n *SSH) loop() {
	backoff := time.Second
	for n.ctx.Err() == nil {
		n.setStatus(Status{State: StateConnecting})
		ctx, cancel := context.WithTimeout(n.ctx, 30*time.Second)
		c, err := dial(ctx, n.cfg, n.auth)
		cancel()
		if err != nil {
			wait := backoff
			st := Status{State: StateOffline, Error: friendlyDialError(err).Error()}
			var hk *HostKeyMismatchError
			if errors.As(err, &hk) {
				st.State, wait = StateHostKeyMismatch, 10*time.Minute
			} else if strings.Contains(err.Error(), "unable to authenticate") {
				st.State, wait = StateAuthFailed, 10*time.Minute
			}
			n.setStatus(st)
			select {
			case <-n.ctx.Done():
				return
			case <-n.wake:
			case <-time.After(wait):
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		n.mu.Lock()
		n.client = c
		n.mu.Unlock()
		n.setStatus(Status{State: StateOnline, LatencyMs: n.ping(c)})
		n.keepalive(c)
		n.mu.Lock()
		if n.client == c {
			n.client = nil
		}
		s := n.sftp
		n.sftp = nil
		n.mu.Unlock()
		if s != nil {
			s.Close()
		}
		c.Close()
		if n.ctx.Err() == nil {
			n.setStatus(Status{State: StateOffline, Error: "连接已断开，正在重连"})
		}
	}
}

func (n *SSH) ping(c *ssh.Client) float64 {
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return -1
		}
		return float64(time.Since(start).Microseconds()) / 1000
	case <-time.After(15 * time.Second):
		return -1
	}
}

// keepalive 每 15 秒探测一次，连续失败或连接关闭时返回。
func (n *SSH) keepalive(c *ssh.Client) {
	closed := make(chan struct{})
	go func() { c.Wait(); close(closed) }()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	fails := 0
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-closed:
			return
		case <-n.wake: // 已连接时忽略重连请求
		case <-t.C:
			lat := n.ping(c)
			if lat < 0 {
				fails++
				if fails >= 2 {
					return
				}
				continue
			}
			fails = 0
			n.mu.Lock()
			n.status.LatencyMs = lat
			n.mu.Unlock()
		}
	}
}

// Client 返回当前连接；未连接时最多等待 ctx 或 10 秒。
func (n *SSH) Client(ctx context.Context) (*ssh.Client, error) {
	deadline := time.After(10 * time.Second)
	for {
		n.mu.Lock()
		c, st, ready := n.client, n.status, n.ready
		n.mu.Unlock()
		if c != nil {
			return c, nil
		}
		if n.auth == nil { // 配置不可用，不会连接
			return nil, fmt.Errorf("%w：%s", ErrOffline, st.Error)
		}
		if st.State != StateConnecting && st.State != "" {
			n.Reconnect()
		}
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-n.ctx.Done():
			return nil, ErrOffline
		case <-deadline:
			n.mu.Lock()
			msg := n.status.Error
			n.mu.Unlock()
			if msg == "" {
				return nil, ErrOffline
			}
			return nil, fmt.Errorf("%w：%s", ErrOffline, msg)
		}
	}
}

func (n *SSH) acquire(ctx context.Context) error {
	select {
	case n.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
		return errors.New("该节点同时打开的通道太多，请稍后再试")
	}
}

func (n *SSH) release() { <-n.sem }

// AcquireTransfer 占用一个 SFTP 传输名额（上传、下载、复制等大块数据传输），返回释放函数。
func (n *SSH) AcquireTransfer(ctx context.Context) (func(), error) {
	select {
	case n.tsem <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-n.tsem }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (n *SSH) session(ctx context.Context) (*ssh.Session, error) {
	c, err := n.Client(ctx)
	if err != nil {
		return nil, err
	}
	if err := n.acquire(ctx); err != nil {
		return nil, err
	}
	s, err := c.NewSession()
	if err != nil {
		n.release()
		return nil, err
	}
	return s, nil
}

// ShellQuote 把参数转成 POSIX shell 的单引号字面量。
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,+@%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func commandLine(name string, args []string) string {
	parts := []string{"LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=", ShellQuote(name)}
	for _, a := range args {
		parts = append(parts, ShellQuote(a))
	}
	return strings.Join(parts, " ")
}

// run 在会话里执行命令行，ctx 取消时关闭会话。
func (n *SSH) run(ctx context.Context, line string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	s, err := n.session(ctx)
	if err != nil {
		return -1, err
	}
	defer n.release()
	defer s.Close()
	s.Stdin, s.Stdout, s.Stderr = stdin, stdout, stderr
	if err := s.Start(line); err != nil {
		return -1, err
	}
	done := make(chan error, 1)
	go func() { done <- s.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		s.Signal(ssh.SIGKILL)
		s.Close()
		<-done
		return -1, ctx.Err()
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus(), nil
	}
	var missing *ssh.ExitMissingError
	if errors.As(err, &missing) {
		return -1, errors.New("连接中断，命令没有返回退出码")
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// Exec 以参数数组执行命令（逐个参数加引号，不做 shell 展开）。
func (n *SSH) Exec(ctx context.Context, name string, args ...string) (Result, error) {
	var stdout, stderr limitedBuffer
	stdout.max, stderr.max = 32<<20, 4<<20
	code, err := n.run(ctx, commandLine(name, args), nil, &stdout, &stderr)
	return Result{Stdout: stdout.b, Stderr: stderr.b, ExitCode: code}, err
}

// ExecInput 与 Exec 相同，但把 stdin 传给命令（用于 sudo -S 等）。
func (n *SSH) ExecInput(ctx context.Context, stdin io.Reader, name string, args ...string) (Result, error) {
	var stdout, stderr limitedBuffer
	stdout.max, stderr.max = 32<<20, 4<<20
	code, err := n.run(ctx, commandLine(name, args), stdin, &stdout, &stderr)
	return Result{Stdout: stdout.b, Stderr: stderr.b, ExitCode: code}, err
}

// RunScript 用登录用户的 shell 执行一段脚本，输出（含 stderr）实时写入 out。
func (n *SSH) RunScript(ctx context.Context, script string, out io.Writer) (int, error) {
	w := &syncWriter{w: out} // stdout 和 stderr 由两个 goroutine 同时写入
	return n.run(ctx, script, nil, w, w)
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// Start 启动一个长时间运行的命令行（例如采集循环），返回其输出流；ctx 结束时停止。
func (n *SSH) Start(ctx context.Context, line string) (io.Reader, func() error, error) {
	s, err := n.session(ctx)
	if err != nil {
		return nil, nil, err
	}
	out, err := s.StdoutPipe()
	if err != nil {
		s.Close()
		n.release()
		return nil, nil, err
	}
	if err := s.Start(line); err != nil {
		s.Close()
		n.release()
		return nil, nil, err
	}
	var once sync.Once
	stop := func() error {
		once.Do(func() {
			s.Signal(ssh.SIGTERM)
			s.Close()
			n.release()
		})
		return nil
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-n.ctx.Done():
		}
		stop()
	}()
	return out, stop, nil
}

// DialUnix 通过 SSH 的 direct-streamlocal 通道连接远程的 Unix socket（agent 接口）。
func (n *SSH) DialUnix(ctx context.Context, path string) (net.Conn, error) {
	c, err := n.Client(ctx)
	if err != nil {
		return nil, err
	}
	return c.Dial("unix", path)
}

// AgentClient 返回经 SSH 通道访问远程 agent 接口（/run/sshdesktop/agent.sock）的客户端。
func (n *SSH) AgentClient() *agent.Client {
	return agent.NewClient(func(ctx context.Context) (net.Conn, error) { return n.DialUnix(ctx, agent.SocketPath) })
}

type sshPTY struct {
	n    *SSH
	s    *ssh.Session
	in   io.WriteCloser
	out  *io.PipeReader
	once sync.Once
}

func (p *sshPTY) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *sshPTY) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *sshPTY) Resize(cols, rows uint16) error {
	return p.s.WindowChange(int(rows), int(cols))
}

func (p *sshPTY) Wait() (int, error) {
	err := p.s.Wait()
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func (p *sshPTY) Close() error {
	p.once.Do(func() {
		p.s.Close()
		p.out.Close()
		p.n.release()
	})
	return nil
}

func (n *SSH) Shell(ctx context.Context, opt ShellOptions) (PTY, error) {
	s, err := n.session(ctx)
	if err != nil {
		return nil, err
	}
	cols, rows := opt.Size.Cols, opt.Size.Rows
	if cols == 0 || rows == 0 {
		cols, rows = 120, 32
	}
	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 115200, ssh.TTY_OP_OSPEED: 115200}
	fail := func(err error) (PTY, error) {
		s.Close()
		n.release()
		return nil, err
	}
	if err := s.RequestPty("xterm-256color", int(rows), int(cols), modes); err != nil {
		return fail(err)
	}
	in, err := s.StdinPipe()
	if err != nil {
		return fail(err)
	}
	pr, pw := io.Pipe()
	s.Stdout, s.Stderr = pw, pw
	if opt.Dir != "" {
		err = s.Start("cd " + ShellQuote(opt.Dir) + ` 2>/dev/null; exec "${SHELL:-/bin/sh}" -l`)
	} else {
		err = s.Shell()
	}
	if err != nil {
		return fail(err)
	}
	p := &sshPTY{n: n, s: s, in: in, out: pr}
	go func() {
		s.Wait()
		pw.Close()
	}()
	return p, nil
}

// SFTP 返回当前连接上的 SFTP 客户端（按需创建，断线后重建）。
func (n *SSH) SFTP(ctx context.Context) (*sftp.Client, error) {
	c, err := n.Client(ctx)
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.sftp != nil && n.client == c {
		return n.sftp, nil
	}
	sc, err := sftp.NewClient(c, sftp.UseConcurrentWrites(true), sftp.MaxConcurrentRequestsPerFile(16))
	if err != nil {
		return nil, fmt.Errorf("SFTP 不可用：%w", err)
	}
	n.sftp = sc
	if n.home == "" {
		if wd, err := sc.Getwd(); err == nil {
			n.home = wd
		}
	}
	return sc, nil
}

// FS 返回节点的文件系统（同一节点始终是同一个实例，便于按节点缓存）。
func (n *SSH) FS() vfs.FS { return n.fs }

func (n *SSH) newFS() *vfs.SFTP {
	return &vfs.SFTP{
		Client: func() (*sftp.Client, error) {
			ctx, cancel := context.WithTimeout(n.ctx, 15*time.Second)
			defer cancel()
			return n.SFTP(ctx)
		},
		Home: func() string {
			n.mu.Lock()
			defer n.mu.Unlock()
			if n.home == "" {
				return "/root"
			}
			return n.home
		},
		Mounts: n.mounts,
	}
}

// mounts 用 df 取得远程的挂载点与用量，过滤掉虚拟文件系统。
func (n *SSH) mounts() []vfs.MountUsage {
	ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
	defer cancel()
	res, err := n.Exec(ctx, "df", "-PTk")
	if err != nil || res.ExitCode != 0 && len(res.Stdout) == 0 {
		return nil
	}
	return ParseDF(string(res.Stdout))
}

// ParseDF 解析 df -PTk 的输出。
func ParseDF(out string) []vfs.MountUsage {
	var list []vfs.MountUsage
	seen := map[string]bool{}
	for i, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 7 {
			continue
		}
		point := strings.Join(f[6:], " ")
		if seen[point] || vfs.HiddenMount(f[1], point) {
			continue
		}
		seen[point] = true
		total, _ := strconv.ParseUint(f[2], 10, 64)
		used, _ := strconv.ParseUint(f[3], 10, 64)
		free, _ := strconv.ParseUint(f[4], 10, 64)
		list = append(list, vfs.MountUsage{
			Mount: vfs.Mount{Point: point, Device: f[0], FSType: f[1]},
			Total: total * 1024, Used: used * 1024, Free: free * 1024,
		})
	}
	return list
}

// Addr 返回 host:port。
func (n *SSH) Addr() string { return n.cfg.addr() }

// Host 返回主机地址。
func (n *SSH) Host() string { return n.cfg.Host }

// User 返回登录用户名。
func (n *SSH) User() string { return n.cfg.User }

// Password 返回密码认证时的密码（用于 sudo -S），私钥认证时为空。
func (n *SSH) Password() string { return n.cfg.Password }
