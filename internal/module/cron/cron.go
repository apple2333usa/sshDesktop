package cron

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/cronexpr"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
)

// CronFile 是面板写入的系统 cron 文件。
const CronFile = "/etc/cron.d/sshdesktop"

type Deps struct {
	Store  *store.Store
	Tasks  *task.Manager
	Runner *Runner
	// Bin、Config 是写入 cron 文件的面板二进制和配置文件路径
	Bin    string
	Config string
	Audit  func(module, action string) gin.HandlerFunc
	// PlanExists 校验备份计划是否存在
	PlanExists func(id uint) bool
}

type Service struct {
	d Deps
	// 可在测试中替换
	cronFile   string
	systemRoot string
}

func New(d Deps) *Service { return &Service{d: d, cronFile: CronFile, systemRoot: "/"} }

// CronFilePath 返回 cron 文件路径。
func (s *Service) CronFilePath() string { return s.cronFile }

// SetPaths 仅用于测试：把 cron 文件和系统 crontab 的根目录指向临时目录。
func (s *Service) SetPaths(cronFile, systemRoot string) {
	s.cronFile, s.systemRoot = cronFile, systemRoot
}

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/cron")
	g.GET("/status", view, s.status)
	g.POST("/install-daemon", manage, a("cron", "install-daemon"), s.installDaemon)
	g.POST("/preview", view, s.preview)
	g.GET("/jobs", view, s.jobs)
	g.POST("/jobs", manage, a("cron", "create"), s.save)
	g.PUT("/jobs/:id", manage, a("cron", "update"), s.save)
	g.DELETE("/jobs/:id", manage, a("cron", "delete"), s.delete)
	g.POST("/jobs/:id/enable", manage, a("cron", "enable"), s.enable)
	g.POST("/jobs/:id/run", manage, a("cron", "run"), s.runNow)
	g.GET("/jobs/:id/runs", view, s.runs)
	g.GET("/runs/:id", view, s.run)

	g.GET("/system", view, s.systemList)
	g.GET("/system/file", view, s.systemFile)
	g.PUT("/system/file", manage, a("cron", "system-file"), s.systemFileSave)
	g.POST("/system/line", manage, a("cron", "system-line"), s.systemLine)
	g.GET("/timers", view, s.timers)
}

/* ---------- 系统 cron 文件 ---------- */

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '-' || r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) < 0 {
		return s
	}
	return node.ShellQuote(s)
}

// Render 生成 /etc/cron.d/sshdesktop 的内容（只包含启用的任务）。
func Render(jobs []store.CronJob, bin, config string) string {
	var b strings.Builder
	b.WriteString("# 由 sshDesktop 管理，请在面板的“计划任务”中修改，手动修改会被覆盖。\n")
	b.WriteString("SHELL=/bin/sh\nPATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n\n")
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		fmt.Fprintf(&b, "# %d %s\n", j.ID, strings.ReplaceAll(j.Name, "\n", " "))
		fmt.Fprintf(&b, "%s root %s -c %s cron run %d >/dev/null 2>&1\n", j.Schedule, shellQuote(bin), shellQuote(config), j.ID)
	}
	return b.String()
}

// Sync 按数据库重写 cron 文件。系统没有 /etc/cron.d 时返回错误（需要先安装 cron）。
func (s *Service) Sync() error {
	var jobs []store.CronJob
	s.d.Store.DB.Order("id").Find(&jobs)
	if _, err := os.Stat(s.cronFile); len(jobs) == 0 && os.IsNotExist(err) {
		return nil // 没有任务时不必创建文件
	}
	dir := filepath.Dir(s.cronFile)
	if _, err := os.Stat(dir); err != nil {
		return api.Err(503, api.CodeCronUnavailable, "系统没有 "+dir+"，请先安装 cron")
	}
	tmp := s.cronFile + ".tmp"
	// cron 要求 /etc/cron.d 下的文件属于 root、不能被其他人写入；文件名不能带点
	if err := os.WriteFile(tmp, []byte(Render(jobs, s.d.Bin, s.d.Config)), 0o644); err != nil {
		return err
	}
	os.Chmod(tmp, 0o644)
	if err := os.Rename(tmp, s.cronFile); err != nil {
		os.Remove(tmp)
		return err
	}
	// 让 cron 尽快读取（Debian 的 cron 按目录修改时间检测变化）
	now := time.Now()
	os.Chtimes(dir, now, now)
	return nil
}

// daemon 返回系统 cron 守护进程的状态。
func daemon(ctx context.Context) (installed, running bool, name string) {
	for _, n := range []string{"cron", "crond", "cronie"} {
		out, _ := exec.CommandContext(ctx, "systemctl", "is-active", n).Output()
		st := strings.TrimSpace(string(out))
		if st == "active" {
			return true, true, n
		}
		if st == "inactive" || st == "failed" {
			installed, name = true, n
		}
	}
	if !installed {
		for _, b := range []string{"cron", "crond"} {
			if _, err := exec.LookPath(b); err == nil {
				return true, false, b
			}
		}
	}
	return installed, false, name
}

func (s *Service) status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	installed, running, name := daemon(ctx)
	_, dirErr := os.Stat(filepath.Dir(s.cronFile))
	api.OK(c, gin.H{
		"installed": installed, "running": running, "daemon": name, "cronDir": dirErr == nil,
		"file": s.cronFile, "bin": s.d.Bin, "timezone": time.Now().Format("MST -07:00"),
	})
}

const daemonScript = `set -e
if command -v apt-get >/dev/null 2>&1; then
  DEBIAN_FRONTEND=noninteractive apt-get update
  DEBIAN_FRONTEND=noninteractive apt-get install -y cron
  systemctl enable --now cron
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y cronie
  systemctl enable --now crond
else
  echo "不支持的发行版：没有 apt-get 或 dnf" >&2; exit 1
fi`

func (s *Service) installDaemon(c *gin.Context) {
	t := s.d.Tasks.Start("cron.install", "安装 cron", api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		out := t.Lines()
		defer out.Flush()
		cmd := exec.CommandContext(ctx, "sh", "-c", daemonScript)
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("安装失败：%w", err)
		}
		return s.Sync()
	})
	api.OK(c, t)
}

func (s *Service) preview(c *gin.Context) {
	var req struct {
		Schedule string `json:"schedule"`
	}
	if !api.Bind(c, &req) {
		return
	}
	sch, err := cronexpr.Parse(req.Schedule)
	if err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	api.OK(c, gin.H{"next": nextRuns(sch, 5), "reboot": sch.Reboot})
}

func nextRuns(sch *cronexpr.Schedule, n int) []int64 {
	out := []int64{}
	t := time.Now()
	for i := 0; i < n; i++ {
		t = sch.Next(t)
		if t.IsZero() {
			break
		}
		out = append(out, t.Unix())
	}
	return out
}

/* ---------- 面板任务 ---------- */

type jobView struct {
	store.CronJob
	Next int64 `json:"next,omitempty"`
}

func (s *Service) jobs(c *gin.Context) {
	var list []store.CronJob
	s.d.Store.DB.Order("id").Find(&list)
	out := make([]jobView, 0, len(list))
	for _, j := range list {
		v := jobView{CronJob: j}
		if sch, err := cronexpr.Parse(j.Schedule); err == nil && j.Enabled {
			if t := sch.Next(time.Now()); !t.IsZero() {
				v.Next = t.Unix()
			}
		}
		out = append(out, v)
	}
	api.OK(c, out)
}

// Validate 校验任务配置（备份模块创建定时备份时也用它）。
func (s *Service) Validate(j *store.CronJob) error {
	j.Name, j.Schedule = strings.TrimSpace(j.Name), strings.Join(strings.Fields(j.Schedule), " ")
	if j.Name == "" || len([]rune(j.Name)) > 64 {
		return fmt.Errorf("名称不能为空，也不能超过 64 个字")
	}
	if strings.ContainsAny(j.Schedule, "\n\r%") {
		return fmt.Errorf("执行周期不能包含换行或 %%")
	}
	if _, err := cronexpr.Parse(j.Schedule); err != nil {
		return err
	}
	switch j.Type {
	case "shell":
		if strings.TrimSpace(j.Script) == "" {
			return fmt.Errorf("请填写脚本内容")
		}
	case "url":
		u, err := url.Parse(strings.TrimSpace(j.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("请填写以 http:// 或 https:// 开头的地址")
		}
		j.URL = u.String()
	case "cleanup":
		if _, err := CleanDirs(j.CleanDirs); err != nil {
			return err
		}
		if strings.ContainsAny(j.CleanPattern, "/\n") {
			return fmt.Errorf("文件名模式不能包含 /")
		}
		if j.CleanDays < 0 {
			j.CleanDays = 0
		}
	case "backup":
		if j.PlanID == 0 || (s.d.PlanExists != nil && !s.d.PlanExists(j.PlanID)) {
			return fmt.Errorf("请选择备份计划")
		}
	default:
		return fmt.Errorf("任务类型只能是 shell、backup、url 或 cleanup")
	}
	if j.Timeout < 0 {
		j.Timeout = 0
	}
	if j.KeepRuns <= 0 || j.KeepRuns > 1000 {
		j.KeepRuns = defKeepRuns
	}
	return nil
}

func (s *Service) save(c *gin.Context) {
	var req store.CronJob
	if !api.Bind(c, &req) {
		return
	}
	if err := s.Validate(&req); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	job := req
	if id := c.Param("id"); id != "" {
		var old store.CronJob
		if err := s.d.Store.DB.First(&old, "id = ?", id).Error; err != nil {
			api.Fail(c, api.NotFound("任务不存在"))
			return
		}
		job.ID, job.CreatedAt, job.LastRunAt, job.LastStatus = old.ID, old.CreatedAt, old.LastRunAt, old.LastStatus
	} else {
		job.ID, job.LastRunAt, job.LastStatus = 0, nil, ""
	}
	api.Target(c, job.Name)
	if err := s.d.Store.DB.Save(&job).Error; err != nil {
		api.Fail(c, err)
		return
	}
	if err := s.Sync(); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, job)
}

func (s *Service) job(c *gin.Context) (store.CronJob, bool) {
	var j store.CronJob
	if err := s.d.Store.DB.First(&j, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("任务不存在"))
		return j, false
	}
	api.Target(c, j.Name)
	return j, true
}

func (s *Service) delete(c *gin.Context) {
	j, ok := s.job(c)
	if !ok {
		return
	}
	s.d.Store.DB.Delete(&store.CronRun{}, "job_id = ?", j.ID)
	s.d.Store.DB.Delete(&j)
	if err := s.Sync(); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) enable(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !api.Bind(c, &req) {
		return
	}
	j, ok := s.job(c)
	if !ok {
		return
	}
	s.d.Store.DB.Model(&j).Update("enabled", req.Enabled)
	if err := s.Sync(); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

// runNow 立即在面板进程中执行一次，返回执行记录（执行在后台进行）。
func (s *Service) runNow(c *gin.Context) {
	j, ok := s.job(c)
	if !ok {
		return
	}
	started := make(chan store.CronRun, 1)
	go s.d.Runner.Run(context.Background(), j, "manual", func(r store.CronRun) { started <- r })
	api.OK(c, <-started)
}

func (s *Service) runs(c *gin.Context) {
	var list []store.CronRun
	s.d.Store.DB.Select("id", "job_id", "trigger", "status", "exit_code", "started_at", "finished_at", "duration_ms").
		Where("job_id = ?", c.Param("id")).Order("id desc").Limit(200).Find(&list)
	api.OK(c, list)
}

func (s *Service) run(c *gin.Context) {
	var r store.CronRun
	if err := s.d.Store.DB.First(&r, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("执行记录不存在"))
		return
	}
	api.OK(c, r)
}

// SaveBackupJob 为备份计划创建、更新或删除（schedule 为空）对应的定时任务，返回任务 ID。
func (s *Service) SaveBackupJob(planID uint, name, schedule string) (uint, error) {
	var j store.CronJob
	found := s.d.Store.DB.Where("type = ? AND plan_id = ?", "backup", planID).First(&j).Error == nil
	if strings.TrimSpace(schedule) == "" {
		if found {
			s.d.Store.DB.Delete(&store.CronRun{}, "job_id = ?", j.ID)
			s.d.Store.DB.Delete(&j)
		}
		return 0, s.Sync()
	}
	if !found {
		j = store.CronJob{Type: "backup", PlanID: planID, Enabled: true, Timeout: 0}
	}
	j.Name, j.Schedule = "备份："+name, schedule
	if err := s.Validate(&j); err != nil {
		return 0, err
	}
	if err := s.d.Store.DB.Save(&j).Error; err != nil {
		return 0, err
	}
	return j.ID, s.Sync()
}

// BackupSchedule 返回备份计划的定时周期（没有定时任务时为空）。
func (s *Service) BackupSchedule(planID uint) string {
	var j store.CronJob
	if s.d.Store.DB.Where("type = ? AND plan_id = ?", "backup", planID).First(&j).Error != nil {
		return ""
	}
	return j.Schedule
}
