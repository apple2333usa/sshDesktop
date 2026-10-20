// Package cron 是计划任务模块（仅本机）。面板的任务写入 /etc/cron.d/sshdesktop，
// 由系统 cron 调用 `sshdesktop cron run <id>` 执行（面板停止时照常运行），执行记录写入数据库；
// 服务器上已有的 crontab 全部列出，可启停、编辑和删除，systemd timer 只读展示。
package cron

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/store"
)

const (
	maxOutput   = 256 << 10
	defKeepRuns = 30
)

// BackupFunc 执行一个备份计划（由备份模块提供），日志写入 log。
type BackupFunc func(ctx context.Context, planID uint, trigger string, log io.Writer) error

// Runner 执行计划任务并记录结果；面板进程和 `sshdesktop cron run` 共用。
type Runner struct {
	Store   *store.Store
	DataDir string
	Backup  BackupFunc
}

// output 收集输出，超过上限后截断。
type output struct {
	b   bytes.Buffer
	cut bool
}

func (o *output) Write(p []byte) (int, error) {
	if room := maxOutput - o.b.Len(); room > 0 {
		if len(p) > room {
			o.b.Write(p[:room])
			o.cut = true
		} else {
			o.b.Write(p)
		}
	} else {
		o.cut = true
	}
	return len(p), nil
}

func (o *output) String() string {
	s := o.b.String()
	if o.cut {
		s += "\n…（输出超过 256 KB，已截断）"
	}
	return s
}

// Run 执行任务。onStart 在执行记录创建后调用（面板手动执行时用它立即返回记录 ID）。
func (r *Runner) Run(ctx context.Context, job store.CronJob, trigger string, onStart func(store.CronRun)) store.CronRun {
	run := store.CronRun{JobID: job.ID, Trigger: trigger, Status: "running", StartedAt: time.Now()}
	r.Store.DB.Create(&run)
	if onStart != nil {
		onStart(run)
	}
	out := &output{}
	code, err := 0, error(nil)
	unlock, locked := r.lock(job.ID)
	if !locked {
		run.Status, code = "skipped", -1
		fmt.Fprintln(out, "上一次执行还没有结束，本次跳过")
	} else {
		defer unlock()
		if job.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(job.Timeout)*time.Second)
			defer cancel()
		}
		code, err = r.execute(ctx, job, out)
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			run.Status, code = "failed", -1
			fmt.Fprintf(out, "\n超过 %d 秒未结束，已终止\n", job.Timeout)
		case err != nil:
			run.Status, code = "failed", -1
			fmt.Fprintf(out, "\n%s\n", err)
		case code != 0:
			run.Status = "failed"
		default:
			run.Status = "success"
		}
	}
	now := time.Now()
	run.ExitCode, run.Output, run.FinishedAt = code, out.String(), &now
	run.DurationMs = now.Sub(run.StartedAt).Milliseconds()
	r.Store.DB.Save(&run)
	r.Store.DB.Model(&store.CronJob{}).Where("id = ?", job.ID).
		Updates(map[string]any{"last_run_at": run.StartedAt, "last_status": run.Status})
	r.prune(job)
	return run
}

// lock 用文件锁防止同一任务重叠执行（面板和 cron 进程之间也有效）。
func (r *Runner) lock(id uint) (func(), bool) {
	dir := filepath.Join(r.DataDir, "cron-locks")
	os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%d.lock", id)), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true
}

func (r *Runner) prune(job store.CronJob) {
	keep := job.KeepRuns
	if keep <= 0 {
		keep = defKeepRuns
	}
	var ids []uint
	r.Store.DB.Model(&store.CronRun{}).Where("job_id = ?", job.ID).Order("id desc").Offset(keep).Pluck("id", &ids)
	if len(ids) > 0 {
		r.Store.DB.Delete(&store.CronRun{}, ids)
	}
}

func (r *Runner) execute(ctx context.Context, job store.CronJob, out io.Writer) (int, error) {
	switch job.Type {
	case "shell":
		return runShell(ctx, job.Script, out)
	case "url":
		return runURL(ctx, job.URL, out)
	case "cleanup":
		return 0, runCleanup(job, out)
	case "backup":
		if r.Backup == nil {
			return -1, errors.New("备份模块不可用")
		}
		return 0, r.Backup(ctx, job.PlanID, "cron", out)
	}
	return -1, fmt.Errorf("未知的任务类型：%s", job.Type)
}

func runShell(ctx context.Context, script string, out io.Writer) (int, error) {
	shell := "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-c", script)
	cmd.Dir = "/root"
	if _, err := os.Stat(cmd.Dir); err != nil {
		cmd.Dir = "/"
	}
	cmd.Env = append(os.Environ(), "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME="+cmd.Dir)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

func runURL(ctx context.Context, url string, out io.Writer) (int, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("User-Agent", "sshDesktop-cron")
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	fmt.Fprintf(out, "GET %s\nHTTP %d（%d ms）\n\n%s", url, resp.StatusCode, time.Since(start).Milliseconds(), body)
	if resp.StatusCode >= 400 {
		return resp.StatusCode, nil
	}
	return 0, nil
}

// protectedDirs 是清理日志时禁止作为根目录的路径。
var protectedDirs = map[string]bool{
	"/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true, "/lib": true, "/lib64": true, "/proc": true,
	"/root": true, "/sbin": true, "/sys": true, "/usr": true, "/var": true, "/home": true, "/run": true,
}

// CleanDirs 解析并校验要清理的目录（每行一个绝对路径）。
func CleanDirs(text string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		d := strings.TrimSpace(line)
		if d == "" {
			continue
		}
		d = filepath.Clean(d)
		if !filepath.IsAbs(d) {
			return nil, fmt.Errorf("必须是绝对路径：%s", d)
		}
		if protectedDirs[d] {
			return nil, fmt.Errorf("不能清理系统目录 %s，请填写更具体的目录（如 /var/log/nginx）", d)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return nil, errors.New("请至少填写一个目录")
	}
	return out, nil
}

// runCleanup 删除目录中文件名匹配、修改时间早于 N 天的普通文件（不跟随符号链接）。
func runCleanup(job store.CronJob, out io.Writer) error {
	dirs, err := CleanDirs(job.CleanDirs)
	if err != nil {
		return err
	}
	pattern := job.CleanPattern
	if pattern == "" {
		pattern = "*.log*"
	}
	cutoff := time.Now().AddDate(0, 0, -max(job.CleanDays, 0))
	var count int
	var freed int64
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(out, "跳过 %s：%s\n", p, err)
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if ok, _ := filepath.Match(pattern, d.Name()); !ok {
				return nil
			}
			info, err := d.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				return nil
			}
			if err := os.Remove(p); err != nil {
				fmt.Fprintf(out, "删除 %s 失败：%s\n", p, err)
				return nil
			}
			count++
			freed += info.Size()
			fmt.Fprintf(out, "已删除 %s\n", p)
			return nil
		})
		if err != nil {
			fmt.Fprintf(out, "%s：%s\n", dir, err)
		}
	}
	fmt.Fprintf(out, "共删除 %d 个文件，释放 %.1f MB\n", count, float64(freed)/(1<<20))
	return nil
}
