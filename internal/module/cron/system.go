package cron

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/cronexpr"
	"github.com/gin-gonic/gin"
)

// 系统已有的 crontab：/etc/crontab、/etc/cron.d/*（带用户字段），以及各用户的 crontab
// （Debian 的 /var/spool/cron/crontabs/<用户>，RHEL 的 /var/spool/cron/<用户>）。
// 面板停用一行时在行首加 offMark，启用时去掉；无法解析的行按原始文本编辑。

const offMark = "#sshdesktop-off# "

type crontabFile struct {
	Path     string `json:"path"`
	User     string `json:"user,omitempty"` // 用户 crontab 的所属用户
	System   bool   `json:"system"`         // 每行带用户字段
	Hash     string `json:"hash"`
	Lines    []line `json:"lines"`
	ReadOnly bool   `json:"readOnly"`
	Error    string `json:"error,omitempty"`
}

type line struct {
	No       int    `json:"no"` // 从 1 开始
	Kind     string `json:"kind"`
	Raw      string `json:"raw"`
	Enabled  bool   `json:"enabled"`
	Schedule string `json:"schedule,omitempty"`
	User     string `json:"user,omitempty"`
	Command  string `json:"command,omitempty"`
	Next     int64  `json:"next,omitempty"`
}

func hashOf(b []byte) string {
	h := sha1.Sum(b)
	return hex.EncodeToString(h[:8])
}

var envLine = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\s*=`)

// parseLine 解析一行：job（任务）、env（环境变量）、comment（注释）、blank、other（无法解析）。
func parseLine(raw string, system bool) line {
	l := line{Raw: raw, Kind: "other"}
	s := strings.TrimSpace(raw)
	enabled := true
	if strings.HasPrefix(raw, offMark) {
		s, enabled = strings.TrimSpace(strings.TrimPrefix(raw, offMark)), false
	}
	switch {
	case s == "":
		l.Kind = "blank"
		return l
	case strings.HasPrefix(s, "#"):
		l.Kind = "comment"
		return l
	case envLine.MatchString(s):
		l.Kind = "env"
		return l
	}
	f := strings.Fields(s)
	n := 5
	if strings.HasPrefix(f[0], "@") {
		n = 1
	}
	need := n + 1
	if system {
		need++
	}
	if len(f) < need {
		return l
	}
	sched := strings.Join(f[:n], " ")
	parsed, err := cronexpr.Parse(sched)
	if err != nil {
		return l
	}
	l.Kind, l.Enabled, l.Schedule = "job", enabled, sched
	rest := strings.TrimSpace(s)
	for i := 0; i < n; i++ { // 保留命令里原有的空白
		rest = strings.TrimSpace(strings.TrimPrefix(rest, f[i]))
	}
	if system {
		l.User = f[n]
		rest = strings.TrimSpace(strings.TrimPrefix(rest, f[n]))
	}
	l.Command = rest
	if enabled {
		if t := parsed.Next(time.Now()); !t.IsZero() {
			l.Next = t.Unix()
		}
	}
	return l
}

func (s *Service) path(p string) string { return filepath.Join(s.systemRoot, p) }

// crontabPaths 返回所有 crontab 文件（系统路径）及其属性。
func (s *Service) crontabPaths() []crontabFile {
	var out []crontabFile
	if _, err := os.Stat(s.path("/etc/crontab")); err == nil {
		out = append(out, crontabFile{Path: "/etc/crontab", System: true})
	}
	own := filepath.Base(s.cronFile)
	if entries, err := os.ReadDir(s.path("/etc/cron.d")); err == nil {
		for _, e := range entries {
			n := e.Name()
			// 与 cron 一致：跳过带点、以 ~ 结尾的文件；面板自己的文件在“面板任务”中管理
			if e.IsDir() || strings.ContainsAny(n, ".~") || n == own || strings.HasPrefix(n, "#") {
				continue
			}
			out = append(out, crontabFile{Path: "/etc/cron.d/" + n, System: true})
		}
	}
	for _, dir := range []string{"/var/spool/cron/crontabs", "/var/spool/cron"} {
		entries, err := os.ReadDir(s.path(dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasPrefix(e.Name(), "tmp.") {
				continue
			}
			out = append(out, crontabFile{Path: dir + "/" + e.Name(), User: e.Name()})
		}
		if len(out) > 0 && dir == "/var/spool/cron/crontabs" {
			break
		}
	}
	return out
}

func (s *Service) readCrontab(f crontabFile) crontabFile {
	b, err := os.ReadFile(s.path(f.Path))
	if err != nil {
		f.Error, f.ReadOnly = err.Error(), true
		f.Lines = []line{}
		return f
	}
	f.Hash = hashOf(b)
	text := strings.TrimSuffix(string(b), "\n")
	f.Lines = []line{}
	if text == "" {
		return f
	}
	for i, raw := range strings.Split(text, "\n") {
		l := parseLine(raw, f.System)
		l.No = i + 1
		f.Lines = append(f.Lines, l)
	}
	return f
}

func (s *Service) systemList(c *gin.Context) {
	out := []crontabFile{}
	for _, f := range s.crontabPaths() {
		out = append(out, s.readCrontab(f))
	}
	api.OK(c, out)
}

// lookup 只允许操作 crontabPaths 列出的文件。
func (s *Service) lookup(p string) (crontabFile, bool) {
	for _, f := range s.crontabPaths() {
		if f.Path == p {
			return f, true
		}
	}
	return crontabFile{}, false
}

func (s *Service) systemFile(c *gin.Context) {
	f, ok := s.lookup(c.Query("path"))
	if !ok {
		api.Fail(c, api.NotFound("crontab 文件不存在"))
		return
	}
	b, err := os.ReadFile(s.path(f.Path))
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, gin.H{"path": f.Path, "content": string(b), "hash": hashOf(b)})
}

// backupOnce 修改已有文件前，把原文件备份到 takeover-backup（每个文件每天一份）。
func (s *Service) backupOnce(f crontabFile, b []byte) {
	dir := filepath.Join(s.d.Runner.DataDir, "takeover-backup", "cron")
	os.MkdirAll(dir, 0o700)
	name := strings.ReplaceAll(strings.TrimPrefix(f.Path, "/"), "/", "_") + "." + time.Now().Format("20060102")
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		os.WriteFile(p, b, 0o600)
	}
}

// writeCrontab 写入新内容：用户 crontab 经 crontab -u 写入（会校验语法并通知 cron），系统文件原子替换。
func (s *Service) writeCrontab(ctx context.Context, f crontabFile, hash string, content []byte) error {
	old, err := os.ReadFile(s.path(f.Path))
	if err != nil {
		return err
	}
	if hash != hashOf(old) {
		return api.Conflict(api.CodeFileModified, "文件已被其他程序修改，请刷新后重试")
	}
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n') // cron 要求最后一行以换行结尾
	}
	s.backupOnce(f, old)
	if f.User != "" && s.systemRoot == "/" {
		if _, err := exec.LookPath("crontab"); err == nil {
			cmd := exec.CommandContext(ctx, "crontab", "-u", f.User, "-")
			cmd.Stdin = bytes.NewReader(content)
			if out, err := cmd.CombinedOutput(); err != nil {
				return api.BadRequest("crontab 拒绝了新内容：" + strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	p := s.path(f.Path)
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(p); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := filepath.Join(filepath.Dir(p), ".sshdesktop-"+filepath.Base(p))
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return err
	}
	os.Chmod(tmp, mode)
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	now := time.Now()
	os.Chtimes(filepath.Dir(p), now, now)
	return nil
}

func (s *Service) systemFileSave(c *gin.Context) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Hash    string `json:"hash"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.lookup(req.Path)
	if !ok {
		api.Fail(c, api.NotFound("crontab 文件不存在"))
		return
	}
	api.Target(c, f.Path)
	if err := s.writeCrontab(c.Request.Context(), f, req.Hash, []byte(req.Content)); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, s.readCrontab(f))
}

// systemLine 操作一行：toggle（启用 / 停用）、update（修改周期和命令）、delete、add（追加任务）。
func (s *Service) systemLine(c *gin.Context) {
	var req struct {
		Path     string `json:"path"`
		Hash     string `json:"hash"`
		No       int    `json:"no"`
		Action   string `json:"action"`
		Schedule string `json:"schedule"`
		User     string `json:"user"`
		Command  string `json:"command"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.lookup(req.Path)
	if !ok {
		api.Fail(c, api.NotFound("crontab 文件不存在"))
		return
	}
	f = s.readCrontab(f)
	if f.Error != "" {
		api.Fail(c, api.BadRequest(f.Error))
		return
	}
	lines := make([]string, 0, len(f.Lines))
	for _, l := range f.Lines {
		lines = append(lines, l.Raw)
	}
	build := func(enabled bool) (string, error) {
		sched := strings.Join(strings.Fields(req.Schedule), " ")
		if _, err := cronexpr.Parse(sched); err != nil {
			return "", err
		}
		cmd := strings.TrimSpace(req.Command)
		if cmd == "" || strings.ContainsAny(cmd, "\n\r") {
			return "", fmt.Errorf("命令不能为空，也不能换行")
		}
		out := sched + " "
		if f.System {
			u := strings.TrimSpace(req.User)
			if u == "" || strings.ContainsAny(u, " \t") {
				return "", fmt.Errorf("请填写执行用户")
			}
			out += u + " "
		}
		out += cmd
		if !enabled {
			out = offMark + out
		}
		return out, nil
	}
	if req.Action != "add" && (req.No < 1 || req.No > len(f.Lines) || f.Lines[req.No-1].Kind != "job") {
		api.Fail(c, api.BadRequest("这一行不是计划任务，请用原始文本方式编辑"))
		return
	}
	api.Target(c, fmt.Sprintf("%s 第 %d 行 %s", f.Path, req.No, req.Action))
	switch req.Action {
	case "toggle":
		raw := lines[req.No-1]
		if strings.HasPrefix(raw, offMark) {
			lines[req.No-1] = strings.TrimPrefix(raw, offMark)
		} else {
			lines[req.No-1] = offMark + raw
		}
	case "update":
		nl, err := build(f.Lines[req.No-1].Enabled)
		if err != nil {
			api.Fail(c, api.BadRequest(err.Error()))
			return
		}
		lines[req.No-1] = nl
	case "delete":
		lines = append(lines[:req.No-1], lines[req.No:]...)
	case "add":
		nl, err := build(true)
		if err != nil {
			api.Fail(c, api.BadRequest(err.Error()))
			return
		}
		lines = append(lines, nl)
	default:
		api.Fail(c, api.BadRequest("不支持的操作"))
		return
	}
	content := strings.Join(lines, "\n")
	if err := s.writeCrontab(c.Request.Context(), f, req.Hash, []byte(content)); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, s.readCrontab(f))
}

/* ---------- systemd timer（只读） ---------- */

type timer struct {
	Unit      string `json:"unit"`
	Activates string `json:"activates"`
	Next      int64  `json:"next"` // Unix 秒，0 表示无
	Last      int64  `json:"last"`
}

func (s *Service) timers(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "list-timers", "--all", "--output=json", "--no-pager").Output()
	if err != nil {
		api.OK(c, []timer{})
		return
	}
	var raw []struct {
		Unit      string `json:"unit"`
		Activates string `json:"activates"`
		Next      *int64 `json:"next"`
		Last      *int64 `json:"last"`
	}
	if json.Unmarshal(out, &raw) != nil {
		api.OK(c, []timer{})
		return
	}
	list := make([]timer, 0, len(raw))
	for _, r := range raw {
		t := timer{Unit: r.Unit, Activates: r.Activates}
		// systemd 以微秒给出时间
		if r.Next != nil && *r.Next > 0 {
			t.Next = *r.Next / 1e6
		}
		if r.Last != nil && *r.Last > 0 {
			t.Last = *r.Last / 1e6
		}
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Unit < list[j].Unit })
	api.OK(c, list)
}
