package sshd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/pending"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
)

/* ---------- 授权密钥 ---------- */

type sysUser struct {
	Name string `json:"name"`
	UID  int    `json:"uid"`
	GID  int    `json:"gid"`
	Home string `json:"home"`
}

// loginUsers 返回可以登录的账号：root 和 UID ≥ 1000、登录 shell 不是 nologin / false 的账号。
func (s *Service) loginUsers() []sysUser {
	b, err := os.ReadFile(s.passwd)
	if err != nil {
		return nil
	}
	var out []sysUser
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		gid, _ := strconv.Atoi(f[3])
		shell := f[6]
		if strings.HasSuffix(shell, "nologin") || strings.HasSuffix(shell, "/false") || shell == "" {
			continue
		}
		if uid == 0 || (uid >= 1000 && uid < 60000) {
			out = append(out, sysUser{Name: f[0], UID: uid, GID: gid, Home: f[5]})
		}
	}
	return out
}

func (s *Service) user(name string) (sysUser, bool) {
	for _, u := range s.loginUsers() {
		if u.Name == name {
			return u, true
		}
	}
	return sysUser{}, false
}

// Key 是 authorized_keys 中的一行。
type Key struct {
	Line        int    `json:"line"` // 从 1 开始
	Type        string `json:"type"`
	Comment     string `json:"comment"`
	Fingerprint string `json:"fingerprint"`
	Options     string `json:"options,omitempty"`
	Error       string `json:"error,omitempty"`
}

func parseKeys(content string) []Key {
	out := []Key{}
	for i, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		k := Key{Line: i + 1}
		pk, comment, opts, _, err := ssh.ParseAuthorizedKey([]byte(t))
		if err != nil {
			k.Error = "无法解析"
			k.Comment = t
			if len(k.Comment) > 60 {
				k.Comment = k.Comment[:60] + "…"
			}
		} else {
			k.Type, k.Comment, k.Fingerprint, k.Options = pk.Type(), comment, ssh.FingerprintSHA256(pk), strings.Join(opts, ",")
		}
		out = append(out, k)
	}
	return out
}

func keyPath(u sysUser) string { return filepath.Join(u.Home, ".ssh", "authorized_keys") }

func (s *Service) keys(c *gin.Context) {
	users := s.loginUsers()
	name := c.DefaultQuery("user", "root")
	u, ok := s.user(name)
	if !ok {
		api.OK(c, gin.H{"users": users, "keys": []Key{}})
		return
	}
	p := keyPath(u)
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		api.Fail(c, err)
		return
	}
	api.OK(c, gin.H{"users": users, "user": u.Name, "path": p, "keys": parseKeys(string(b))})
}

// writeKeys 写入 authorized_keys，保证 .ssh 目录和文件的属主与权限正确（sshd 的 StrictModes 会检查）。
func writeKeys(u sysUser, content string) error {
	dir := filepath.Join(u.Home, ".ssh")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		os.Chown(dir, u.UID, u.GID)
	}
	p := keyPath(u)
	if err := pending.WriteFile(p, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Chown(p, u.UID, u.GID)
}

func (s *Service) keyAdd(c *gin.Context) {
	var req struct {
		User string `json:"user"`
		Key  string `json:"key"`
	}
	if !api.Bind(c, &req) {
		return
	}
	u, ok := s.user(req.User)
	if !ok {
		api.Fail(c, api.BadRequest("账号不存在或不能登录"))
		return
	}
	line := strings.TrimSpace(req.Key)
	if strings.Contains(line, "\n") {
		api.Fail(c, api.BadRequest("一次只能添加一个公钥"))
		return
	}
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		api.Fail(c, api.BadRequest("公钥格式不正确，请粘贴 .pub 文件的内容"))
		return
	}
	fp := ssh.FingerprintSHA256(pk)
	api.Target(c, fmt.Sprintf("%s：%s %s", u.Name, fp, comment))
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := os.ReadFile(keyPath(u))
	for _, k := range parseKeys(string(b)) {
		if k.Fingerprint == fp {
			api.Fail(c, api.BadRequest("这个公钥已经存在"))
			return
		}
	}
	content := string(b)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += line + "\n"
	if err := writeKeys(u, content); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) keyDelete(c *gin.Context) {
	var req struct {
		User        string `json:"user"`
		Line        int    `json:"line"`
		Fingerprint string `json:"fingerprint"`
	}
	if !api.Bind(c, &req) {
		return
	}
	u, ok := s.user(req.User)
	if !ok {
		api.Fail(c, api.BadRequest("账号不存在或不能登录"))
		return
	}
	api.Target(c, fmt.Sprintf("%s：%s", u.Name, req.Fingerprint))
	if !s.d.Confirm(c) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(keyPath(u))
	if err != nil {
		api.Fail(c, err)
		return
	}
	lines := strings.Split(string(b), "\n")
	found := false
	for _, k := range parseKeys(string(b)) {
		if k.Line == req.Line && (k.Fingerprint == req.Fingerprint || (k.Error != "" && req.Fingerprint == "")) {
			lines = append(lines[:k.Line-1], lines[k.Line:]...)
			found = true
			break
		}
	}
	if !found {
		api.Fail(c, api.Conflict(api.CodeFileModified, "文件已被修改，请刷新后再删除"))
		return
	}
	if err := writeKeys(u, strings.Join(lines, "\n")); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

/* ---------- 登录日志 ---------- */

// Login 是一条 SSH 登录记录。
type Login struct {
	Time    string `json:"time"`
	Success bool   `json:"success"`
	User    string `json:"user"`
	IP      string `json:"ip"`
	Port    string `json:"port"`
	Method  string `json:"method"`
	Invalid bool   `json:"invalid"` // 不存在的用户
}

var (
	reAccepted = regexp.MustCompile(`Accepted (\S+) for (\S+) from (\S+) port (\d+)`)
	reFailed   = regexp.MustCompile(`Failed (\S+) for (invalid user )?(\S*) from (\S+) port (\d+)`)
	reInvalid  = regexp.MustCompile(`Invalid user (\S*) from (\S+) port (\d+)`)
	reISOTime  = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)`)
	reSyslogTm = regexp.MustCompile(`^([A-Z][a-z]{2}\s+\d+ \d\d:\d\d:\d\d)`)
)

// ParseLogins 从 sshd 日志中提取登录记录（新的在前）。
func ParseLogins(lines []string) []Login {
	var out []Login
	for _, l := range lines {
		var lg Login
		switch {
		case reAccepted.MatchString(l):
			m := reAccepted.FindStringSubmatch(l)
			lg = Login{Success: true, Method: m[1], User: m[2], IP: m[3], Port: m[4]}
		case reFailed.MatchString(l):
			m := reFailed.FindStringSubmatch(l)
			lg = Login{Method: m[1], Invalid: m[2] != "", User: m[3], IP: m[4], Port: m[5]}
		case reInvalid.MatchString(l):
			// “Failed password for invalid user” 已记录过同一次尝试；只有公钥尝试的不存在用户在这里记录
			continue
		default:
			continue
		}
		if m := reISOTime.FindStringSubmatch(l); m != nil {
			lg.Time = m[1]
		} else if m := reSyslogTm.FindStringSubmatch(l); m != nil {
			lg.Time = m[1]
		}
		out = append(out, lg)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// readAuthLog 读取 sshd 日志：优先 journalctl，其次 /var/log/auth.log 或 /var/log/secure。
func readAuthLog(ctx context.Context, n int) ([]string, string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := exec.LookPath("journalctl"); err == nil {
		out, err := exec.CommandContext(ctx, "journalctl", "--no-pager", "-o", "short-iso", "-n", strconv.Itoa(n),
			"-t", "sshd", "-t", "sshd-session").Output()
		if err == nil && strings.Contains(string(out), "sshd") {
			return strings.Split(strings.TrimSpace(string(out)), "\n"), "journalctl"
		}
	}
	for _, p := range []string{"/var/log/auth.log", "/var/log/secure"} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		var lines []string
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "sshd") {
				lines = append(lines, sc.Text())
				if len(lines) > n {
					lines = lines[1:]
				}
			}
		}
		f.Close()
		return lines, p
	}
	return nil, ""
}

type ipStat struct {
	IP     string `json:"ip"`
	Failed int    `json:"failed"`
	Users  int    `json:"users"`
}

func (s *Service) logins(c *gin.Context) {
	n, _ := strconv.Atoi(c.DefaultQuery("lines", "5000"))
	if n <= 0 || n > 50000 {
		n = 5000
	}
	lines, source := readAuthLog(c.Request.Context(), n)
	list := ParseLogins(lines)
	stats := map[string]*ipStat{}
	users := map[string]map[string]bool{}
	ok, fail := 0, 0
	for _, l := range list {
		if l.Success {
			ok++
			continue
		}
		fail++
		st := stats[l.IP]
		if st == nil {
			st = &ipStat{IP: l.IP}
			stats[l.IP] = st
			users[l.IP] = map[string]bool{}
		}
		st.Failed++
		users[l.IP][l.User] = true
	}
	top := []ipStat{}
	for ip, st := range stats {
		st.Users = len(users[ip])
		top = append(top, *st)
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Failed > top[j].Failed })
	if len(top) > 20 {
		top = top[:20]
	}
	if len(list) > 1000 {
		list = list[:1000]
	}
	if list == nil {
		list = []Login{}
	}
	api.OK(c, gin.H{"source": source, "items": list, "success": ok, "failed": fail, "topFailed": top})
}
