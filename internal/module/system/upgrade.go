package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/gin-gonic/gin"
)

// 面板内一键升级：在独立的 systemd 单元里执行 `sshdesktop upgrade`（与命令行相同：下载 → 校验 → 备份 → 替换 → 重启 →
// 健康检查，失败自动回滚）。不能在面板进程里直接执行，否则重启面板时 systemd 会连同子进程一起结束。

const (
	upgradeUnit = "sshdesktop-upgrade"
	releaseAPI  = "https://api.github.com/repos/apple2333usa/sshDesktop/releases/latest"
	releasePage = "https://github.com/apple2333usa/sshDesktop/releases"
)

var reTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// Release 是 GitHub 上的最新版本。
type Release struct {
	Tag       string    `json:"tag"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Notes     string    `json:"notes"`
	Published time.Time `json:"published"`
}

type releaseCache struct {
	mu      sync.Mutex
	rel     *Release
	err     string
	checked time.Time
}

// ReleaseURL 仅用于测试：替换查询最新版本的地址。
var ReleaseURL = releaseAPI

func (s *Service) latest(ctx context.Context, refresh bool) (*Release, string, time.Time) {
	rc := &s.release
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if !refresh && !rc.checked.IsZero() && time.Since(rc.checked) < 6*time.Hour {
		return rc.rel, rc.err, rc.checked
	}
	rel, err := fetchLatest(ctx)
	rc.checked = time.Now()
	if err != nil {
		rc.err = "查询最新版本失败：" + err.Error()
	} else {
		rc.rel, rc.err = rel, ""
	}
	return rc.rel, rc.err, rc.checked
}

// ReleasePageURL 仅用于测试：GitHub 网页上的最新版本地址（会重定向到 /releases/tag/vX.Y.Z）。
var ReleasePageURL = releasePage + "/latest"

// fetchLatest 先查 GitHub 接口（带更新说明）；接口不可用（例如未登录时每小时 60 次的限额用完）时，
// 改从网页的重定向地址取版本号。
func fetchLatest(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rel, err := fetchLatestAPI(ctx)
	if err == nil {
		return rel, nil
	}
	tag, err2 := latestFromRedirect(ctx)
	if err2 != nil {
		return nil, fmt.Errorf("%v；%v", err, err2)
	}
	return &Release{Tag: tag, URL: strings.TrimSuffix(ReleasePageURL, "/latest") + "/tag/" + tag}, nil
}

// LatestTag 返回最新版本号（命令行升级也用它）。
func LatestTag(ctx context.Context) (string, error) {
	rel, err := fetchLatest(ctx)
	if err != nil {
		return "", err
	}
	return rel.Tag, nil
}

func latestFromRedirect(ctx context.Context) (string, error) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, ReleasePageURL, nil)
	req.Header.Set("User-Agent", "sshdesktop")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	tag := loc[strings.LastIndex(loc, "/")+1:]
	if !reTag.MatchString(tag) {
		return "", fmt.Errorf("没有从 %s 取到版本号（HTTP %d）", ReleasePageURL, resp.StatusCode)
	}
	return tag, nil
}

func fetchLatestAPI(ctx context.Context) (*Release, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ReleaseURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sshdesktop")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var r struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		HTMLURL     string    `json:"html_url"`
		Body        string    `json:"body"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, err
	}
	if !reTag.MatchString(r.TagName) {
		return nil, fmt.Errorf("版本号格式不对：%q", r.TagName)
	}
	return &Release{Tag: r.TagName, Name: r.Name, URL: r.HTMLURL, Notes: r.Body, Published: r.PublishedAt}, nil
}

// Newer 判断版本 a 是否比 b 新（都是 x.y.z，可带 v 前缀）。
func Newer(a, b string) bool {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func splitVersion(v string) [3]int {
	var out [3]int
	for i, p := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
		n, _ := strconv.Atoi(strings.TrimFunc(p, func(r rune) bool { return r < '0' || r > '9' }))
		out[i] = n
	}
	return out
}

// upgradeSupported 返回不能在面板中升级的原因；空字符串表示可以。
func (s *Service) upgradeSupported() string {
	if os.Getenv("INVOCATION_ID") == "" {
		return "面板不是由 systemd 运行的，请在服务器上执行 sshdesktop upgrade"
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return "没有 systemd-run，请在服务器上执行 sshdesktop upgrade"
	}
	return ""
}

func (s *Service) upgradeLog() string { return filepath.Join(s.d.Config.DataDir, "upgrade.log") }

func upgradeRunning(ctx context.Context) bool {
	out, _ := exec.CommandContext(ctx, "systemctl", "is-active", upgradeUnit).Output()
	st := strings.TrimSpace(string(out))
	return st == "active" || st == "activating"
}

func (s *Service) upgradeStatus(c *gin.Context) {
	rel, errMsg, checked := s.latest(c.Request.Context(), c.Query("refresh") == "1")
	out := gin.H{
		"current": s.d.Version, "latest": rel, "error": errMsg, "checkedAt": checked, "releases": releasePage,
		"unsupported": s.upgradeSupported(), "running": upgradeRunning(c.Request.Context()),
	}
	if rel != nil {
		out["available"] = Newer(rel.Tag, s.d.Version)
	}
	if b, err := os.ReadFile(s.upgradeLog()); err == nil {
		if len(b) > 64<<10 {
			b = b[len(b)-64<<10:]
		}
		out["log"] = string(b)
	}
	api.OK(c, out)
}

func (s *Service) upgradeStart(c *gin.Context) {
	var req struct {
		Version string `json:"version"`
		Mirror  string `json:"mirror"`
	}
	if !api.Bind(c, &req) {
		return
	}
	req.Mirror = strings.TrimRight(strings.TrimSpace(req.Mirror), "/")
	if !reTag.MatchString(req.Version) {
		api.Fail(c, api.BadRequest("版本号应为 vX.Y.Z"))
		return
	}
	if req.Mirror != "" && !strings.HasPrefix(req.Mirror, "https://") {
		api.Fail(c, api.BadRequest("下载加速前缀应以 https:// 开头"))
		return
	}
	api.Target(c, s.d.Version+" → "+req.Version)
	if why := s.upgradeSupported(); why != "" {
		api.Fail(c, api.BadRequest(why))
		return
	}
	if !s.d.Confirm(c) {
		return
	}
	if upgradeRunning(c.Request.Context()) {
		api.Fail(c, api.Conflict(api.CodeConflict, "已经有一次升级正在进行"))
		return
	}
	if err := s.startUpgrade(c.Request.Context(), req.Version, req.Mirror); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	api.OK(c, nil)
}

func (s *Service) startUpgrade(ctx context.Context, version, mirror string) error {
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := s.upgradeLog()
	header := fmt.Sprintf("%s 开始升级：%s → %s\n", time.Now().Format("2006-01-02 15:04:05"), s.d.Version, version)
	if err := os.WriteFile(logPath, []byte(header), 0o600); err != nil {
		return err
	}
	// 上一次失败的单元会留下记录，先清掉，否则同名单元无法再次启动
	exec.CommandContext(ctx, "systemctl", "reset-failed", upgradeUnit).Run()
	args := []string{
		"--unit=" + upgradeUnit, "--collect", "--quiet",
		"--property=StandardOutput=append:" + logPath, "--property=StandardError=append:" + logPath,
		"--", bin, "-c", s.d.Config.Path(), "upgrade", "--version", version,
	}
	if mirror != "" {
		args = append(args, "--mirror", mirror)
	}
	out, err := exec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New("启动升级失败：" + msg)
	}
	return nil
}
