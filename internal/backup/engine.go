// Package backup 实现备份与恢复：打包（目录 / 应用 / 面板）、加密、上传到备份目标、保留策略和恢复。
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/secret"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
)

// AppNetwork 是应用商店应用使用的外部网络（与 1Panel 应用包一致）。
const AppNetwork = "1panel-network"

// 恢复前快照保存在本地，最多保留 keepSnapshots 份。
const keepSnapshots = 10

type Engine struct {
	Store   *store.Store
	Box     *secret.Box
	DataDir string
	// ConfigPath 是面板配置文件，面板备份时一并保存
	ConfigPath string
	Version    string
	Docker     *docker.Client
	// Remote 返回 SFTP 目标所在节点的文件系统，用完调用返回的函数释放。
	Remote func(ctx context.Context, nodeID string) (vfs.FS, func(), error)
	// Sites 由网站模块提供，恢复网站备份时写回站点并重新生成配置；命令行执行的计划任务中为空（只备份不恢复）
	Sites SiteRestorer

	mu      sync.Mutex
	running map[uint]bool
}

func (e *Engine) tmpDir() (string, error) {
	d := filepath.Join(e.DataDir, "backups", "tmp")
	return d, os.MkdirAll(d, 0o700)
}

func (e *Engine) snapshotDir() string { return filepath.Join(e.DataDir, "backups", "snapshots") }

// Cleanup 在启动时清理上次中断留下的临时文件和“进行中”的记录。
func (e *Engine) Cleanup() {
	os.RemoveAll(filepath.Join(e.DataDir, "backups", "tmp"))
	now := time.Now()
	e.Store.DB.Model(&store.BackupRecord{}).Where("status = ?", "running").
		Updates(map[string]any{"status": "failed", "error": "面板重启，备份中断", "finished_at": now})
}

/* ---------- 目标与密码 ---------- */

// ParseTarget 解析目标配置。
func ParseTarget(t store.BackupTarget) (TargetConfig, error) {
	var cfg TargetConfig
	if t.Config != "" {
		if err := json.Unmarshal([]byte(t.Config), &cfg); err != nil {
			return cfg, fmt.Errorf("备份目标配置损坏：%w", err)
		}
	}
	return cfg, nil
}

// Secret 解密目标密钥。
func (e *Engine) Secret(t store.BackupTarget) (TargetSecret, error) {
	var sec TargetSecret
	if len(t.Secret) == 0 {
		return sec, nil
	}
	b, err := e.Box.Open(t.Secret)
	if err != nil {
		return sec, errors.New("备份目标的密钥无法解密（主密钥可能已更换），请重新填写")
	}
	return sec, json.Unmarshal(b, &sec)
}

// OpenTarget 连接备份目标。
func (e *Engine) OpenTarget(ctx context.Context, t store.BackupTarget) (Storage, error) {
	cfg, err := ParseTarget(t)
	if err != nil {
		return nil, err
	}
	sec, err := e.Secret(t)
	if err != nil {
		return nil, err
	}
	return Open(ctx, t.Type, cfg, sec, e.Remote)
}

// Open 按类型创建存储，供保存前测试连接时直接使用表单中的配置。
func Open(ctx context.Context, typ string, cfg TargetConfig, sec TargetSecret,
	remote func(context.Context, string) (vfs.FS, func(), error)) (Storage, error) {
	switch typ {
	case "local":
		root, err := localRoot(cfg.Path)
		if err != nil {
			return nil, err
		}
		return NewFS(vfs.Local{}, root, nil), nil
	case "sftp":
		if remote == nil || cfg.NodeID == "" {
			return nil, errors.New("请选择 SFTP 目标所在的服务器")
		}
		root := strings.TrimSpace(cfg.Path)
		if !strings.HasPrefix(root, "/") || root == "/" {
			return nil, errors.New("SFTP 目录必须是绝对路径，且不能是根目录")
		}
		f, closer, err := remote(ctx, cfg.NodeID)
		if err != nil {
			return nil, err
		}
		return NewFS(f, root, closer), nil
	case "s3":
		return NewS3(cfg, sec)
	case "webdav":
		return NewWebDAV(cfg, sec)
	}
	return nil, fmt.Errorf("不支持的备份目标类型：%s", typ)
}

// PlanPassword 解密计划的备份密码。
func (e *Engine) PlanPassword(p store.BackupPlan) (string, error) {
	if !p.Encrypt {
		return "", nil
	}
	b, err := e.Box.Open(p.Password)
	if err != nil || len(b) == 0 {
		return "", errors.New("备份密码无法解密（主密钥可能已更换），请在计划中重新设置密码")
	}
	return string(b), nil
}

// TargetIDs 解析计划中的目标列表。
func TargetIDs(s string) []uint {
	var out []uint
	for _, f := range strings.Split(s, ",") {
		if n, err := strconv.ParseUint(strings.TrimSpace(f), 10, 64); err == nil && n > 0 {
			out = append(out, uint(n))
		}
	}
	return out
}

/* ---------- 执行备份 ---------- */

func logger(w io.Writer) func(string, ...any) {
	return func(format string, args ...any) {
		if w != nil {
			fmt.Fprintf(w, format+"\n", args...)
		}
	}
}

var nonSlug = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func slug(s, fallback string) string {
	s = strings.Trim(nonSlug.ReplaceAllString(s, "-"), "-.")
	if s == "" {
		return fallback
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// objectKey 生成备份文件名：plan-<id>/<类型>_<来源>_<时间>.tar.gz[.enc]
func objectKey(p store.BackupPlan, now time.Time, enc bool) string {
	src := p.Source
	if p.Type == "panel" {
		src = "panel"
	}
	k := fmt.Sprintf("plan-%d/%s_%s_%s.tar.gz", p.ID, p.Type, slug(filepath.Base(src), p.Type), now.Format("20060102-150405"))
	if enc {
		k += ".enc"
	}
	return k
}

// RunPlan 执行备份计划：打包一次，上传到每个目标，然后按保留策略清理。任一目标失败都返回错误。
func (e *Engine) RunPlan(ctx context.Context, planID uint, trigger string, w io.Writer) error {
	log := logger(w)
	var plan store.BackupPlan
	if err := e.Store.DB.First(&plan, planID).Error; err != nil {
		return errors.New("备份计划不存在")
	}
	e.mu.Lock()
	if e.running == nil {
		e.running = map[uint]bool{}
	}
	if e.running[planID] {
		e.mu.Unlock()
		return fmt.Errorf("备份计划“%s”正在执行", plan.Name)
	}
	e.running[planID] = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, planID)
		e.mu.Unlock()
	}()

	var targets []store.BackupTarget
	if ids := TargetIDs(plan.TargetIDs); len(ids) > 0 {
		e.Store.DB.Where("id IN ?", ids).Order("id").Find(&targets)
	}
	if len(targets) == 0 {
		return errors.New("备份计划没有可用的备份目标")
	}
	password, err := e.PlanPassword(plan)
	if err != nil {
		return err
	}
	start := time.Now()
	key := objectKey(plan, start, password != "")
	log("开始备份“%s”（%s）", plan.Name, describe(plan))
	file, size, err := e.build(ctx, plan, password, log)
	if file != "" {
		defer os.Remove(file)
	}
	if err != nil {
		e.failAll(plan, targets, key, trigger, start, err)
		return err
	}
	log("打包完成，大小 %s", humanSize(size))

	var failed []string
	for _, t := range targets {
		rec := store.BackupRecord{
			PlanID: plan.ID, PlanName: plan.Name, Type: plan.Type, Source: plan.Source,
			TargetID: t.ID, TargetName: t.Name, Key: key, Size: size, Encrypted: password != "",
			Status: "running", Trigger: trigger, StartedAt: start,
		}
		e.Store.DB.Create(&rec)
		log("上传到“%s”…", t.Name)
		err := e.upload(ctx, t, key, file, size, func(st Storage) {
			e.Store.DB.Model(&rec).Updates(map[string]any{"status": "success", "finished_at": time.Now()})
			log("已上传到“%s”：%s", t.Name, key)
			e.retain(ctx, st, plan, t.ID, log)
		})
		if err != nil {
			log("上传到“%s”失败：%s", t.Name, err)
			failed = append(failed, t.Name)
			e.Store.DB.Model(&rec).Updates(map[string]any{"status": "failed", "error": err.Error(), "finished_at": time.Now()})
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("上传到 %s 失败", strings.Join(failed, "、"))
	}
	log("备份完成，用时 %s", time.Since(start).Round(time.Second))
	return nil
}

func (e *Engine) failAll(plan store.BackupPlan, targets []store.BackupTarget, key, trigger string, start time.Time, err error) {
	now := time.Now()
	for _, t := range targets {
		e.Store.DB.Create(&store.BackupRecord{
			PlanID: plan.ID, PlanName: plan.Name, Type: plan.Type, Source: plan.Source,
			TargetID: t.ID, TargetName: t.Name, Key: key, Encrypted: plan.Encrypt,
			Status: "failed", Trigger: trigger, Error: err.Error(), StartedAt: start, FinishedAt: &now,
		})
	}
}

// upload 上传到一个目标，成功后在连接关闭前调用 done（记录状态、执行保留策略）。
func (e *Engine) upload(ctx context.Context, t store.BackupTarget, key, file string, size int64, done func(Storage)) error {
	st, err := e.OpenTarget(ctx, t)
	if err != nil {
		return err
	}
	defer st.Close()
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := st.Put(ctx, key, f, size); err != nil {
		return err
	}
	done(st)
	return nil
}

func describe(p store.BackupPlan) string {
	switch p.Type {
	case "dir":
		return "目录 " + p.Source
	case "app":
		return "应用 " + p.Source
	case "website":
		return "网站 " + p.Source
	}
	return "面板数据"
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// retain 按保留策略删除旧备份：保留最近 KeepCount 份，删除超过 KeepDays 天的；最新一份始终保留。
func (e *Engine) retain(ctx context.Context, st Storage, plan store.BackupPlan, targetID uint, log func(string, ...any)) {
	if plan.KeepCount <= 0 && plan.KeepDays <= 0 {
		return
	}
	var recs []store.BackupRecord
	e.Store.DB.Where("plan_id = ? AND target_id = ? AND status = ?", plan.ID, targetID, "success").
		Order("started_at DESC, id DESC").Find(&recs)
	cutoff := time.Now().AddDate(0, 0, -plan.KeepDays)
	for i, r := range recs {
		if i == 0 {
			continue
		}
		if (plan.KeepCount > 0 && i >= plan.KeepCount) || (plan.KeepDays > 0 && r.StartedAt.Before(cutoff)) {
			if err := st.Delete(ctx, r.Key); err != nil {
				log("删除旧备份 %s 失败：%s", r.Key, err)
				continue
			}
			e.Store.DB.Delete(&store.BackupRecord{}, r.ID)
			log("已按保留策略删除旧备份 %s", r.Key)
		}
	}
}

// build 把计划的内容打包到临时文件，返回路径和大小。
func (e *Engine) build(ctx context.Context, plan store.BackupPlan, password string, log func(string, ...any)) (string, int64, error) {
	dir, err := e.tmpDir()
	if err != nil {
		return "", 0, err
	}
	f, err := os.CreateTemp(dir, "backup-*")
	if err != nil {
		return "", 0, err
	}
	name := f.Name()
	size, err := e.writeArchive(ctx, f, plan, password, log)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return name, size, err
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (e *Engine) writeArchive(ctx context.Context, out io.Writer, plan store.BackupPlan, password string, log func(string, ...any)) (int64, error) {
	cw := &countWriter{w: out}
	var w io.Writer = cw
	var enc io.WriteCloser
	if password != "" {
		var err error
		if enc, err = NewEncrypter(cw, password); err != nil {
			return 0, err
		}
		w = enc
	}
	host, _ := os.Hostname()
	m := Manifest{Format: 1, Type: plan.Type, Source: plan.Source, Plan: plan.Name, Host: host, Version: e.Version, CreatedAt: time.Now()}
	ex := ParseExcludes(plan.Excludes)
	var content func(*tarWriter) error
	switch plan.Type {
	case "dir":
		src := filepath.Clean(plan.Source)
		if err := checkDir(src); err != nil {
			return 0, err
		}
		content = func(t *tarWriter) error { return t.addDir(ctx, src, "data", ex, log) }
	case "app":
		var row store.AppInstalled
		if err := e.Store.DB.First(&row, "name = ?", plan.Source).Error; err != nil {
			return 0, fmt.Errorf("应用 %s 不存在（可能已卸载）", plan.Source)
		}
		b, _ := json.Marshal(appRecord{row, row.Params})
		m.App = b
		if plan.StopApp {
			p := appProject(row)
			log("暂停应用 %s 的容器", row.Name)
			if err := docker.Compose(ctx, p, nil, "stop"); err != nil {
				log("暂停容器失败（继续备份）：%s", err)
			}
			defer func() {
				log("重新启动应用 %s 的容器", row.Name)
				if err := docker.Compose(context.WithoutCancel(ctx), p, nil, "start"); err != nil {
					log("启动容器失败：%s", err)
				}
			}()
		}
		content = func(t *tarWriter) error { return t.addDir(ctx, row.Dir, "app", ex, log) }
	case "website":
		var site store.Website
		if err := e.Store.DB.First(&site, "name = ?", plan.Source).Error; err != nil {
			return 0, fmt.Errorf("网站 %s 不存在（可能已删除）", plan.Source)
		}
		rec := siteRecord{Site: site}
		var cert store.Certificate
		if site.HTTPS && site.CertID != 0 && e.Store.DB.First(&cert, site.CertID).Error == nil &&
			regularFile(cert.CertPath) && regularFile(cert.KeyPath) {
			rec.Cert = true
		}
		if site.Type == "static" && site.Raw == "" && dirExists(site.Root) {
			rec.Files = true
		}
		m.Site, _ = json.Marshal(rec)
		content = func(t *tarWriter) error {
			if rec.Cert {
				log("包含证书 %s", cert.Domains)
				if err := t.addDir(ctx, cert.CertPath, "site/cert/fullchain.pem", nil, log); err != nil {
					return err
				}
				if err := t.addDir(ctx, cert.KeyPath, "site/cert/privkey.pem", nil, log); err != nil {
					return err
				}
			}
			if rec.Files {
				return t.addDir(ctx, site.Root, "site/files", ex, log)
			}
			return nil
		}
	case "panel":
		db, err := e.dumpDB()
		if err != nil {
			return 0, err
		}
		defer os.Remove(db)
		m.KeyID = e.keyID()
		content = func(t *tarWriter) error {
			if err := t.addDir(ctx, db, "panel/sshdesktop.db", nil, log); err != nil {
				return err
			}
			if plan.IncludeSecretKey {
				log("包含加密主密钥 secret.key")
				if err := t.addDir(ctx, filepath.Join(e.DataDir, "secret.key"), "panel/secret.key", nil, log); err != nil {
					return err
				}
			}
			if cfg := e.ConfigPath; cfg != "" {
				if _, err := os.Stat(cfg); err == nil {
					if err := t.addDir(ctx, cfg, "panel/config.yaml", nil, log); err != nil {
						return err
					}
				}
			}
			// 面板申请和上传的证书
			if d := filepath.Join(e.DataDir, "certs"); dirExists(d) {
				if err := t.addDir(ctx, d, "panel/certs", nil, log); err != nil {
					return err
				}
			}
			if d := filepath.Join(e.DataDir, "compose"); dirExists(d) {
				return t.addDir(ctx, d, "panel/compose", ex, log)
			}
			return nil
		}
	default:
		return 0, fmt.Errorf("不支持的备份类型：%s", plan.Type)
	}
	tw, gz, err := newTar(w, m)
	if err != nil {
		return 0, err
	}
	if err := content(tw); err != nil {
		return 0, err
	}
	if err := tw.close(gz); err != nil {
		return 0, err
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			return 0, err
		}
	}
	log("共打包 %d 个文件（原始大小 %s）", tw.files, humanSize(tw.bytes))
	return cw.n, nil
}

// appRecord 让应用备份带上安装参数（AppInstalled.Params 不输出到 JSON）。
type appRecord struct {
	store.AppInstalled
	Params string `json:"params"`
}

func appProject(row store.AppInstalled) docker.Project {
	return docker.Project{Name: row.Name, Dir: row.Dir, Files: []string{filepath.Join(row.Dir, "docker-compose.yml")}}
}

// dumpDB 用 VACUUM INTO 生成数据库的一致性副本。
func (e *Engine) dumpDB() (string, error) {
	dir, err := e.tmpDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, fmt.Sprintf("db-%d.sqlite", time.Now().UnixNano()))
	if err := e.Store.DB.Exec("VACUUM INTO ?", p).Error; err != nil {
		return "", fmt.Errorf("导出数据库失败：%w", err)
	}
	return p, nil
}

// keyID 是主密钥的指纹，恢复时据此判断加密的凭据能否解开。
func (e *Engine) keyID() string {
	b, err := os.ReadFile(filepath.Join(e.DataDir, "secret.key"))
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// 不允许备份或整体恢复的系统目录。
var protectedDirs = map[string]bool{
	"/": true, "/proc": true, "/sys": true, "/dev": true, "/run": true, "/boot": true,
	"/bin": true, "/sbin": true, "/lib": true, "/lib64": true, "/usr": true, "/etc": true, "/var": true,
}

func checkDir(p string) error {
	if !filepath.IsAbs(p) {
		return errors.New("目录必须是绝对路径")
	}
	if p == "/" || p == "/proc" || p == "/sys" || p == "/dev" || p == "/run" {
		return fmt.Errorf("不能备份 %s", p)
	}
	if !dirExists(p) {
		return fmt.Errorf("目录 %s 不存在", p)
	}
	return nil
}

// siteRecord 是网站备份中的站点信息：站点记录，以及备份包里是否有网站文件（静态网站）和证书。
type siteRecord struct {
	Site  store.Website `json:"site"`
	Files bool          `json:"files"`
	Cert  bool          `json:"cert"`
}

// SiteRestorer 写回网站记录、导入证书并重新生成配置，返回需要提示的警告。
type SiteRestorer interface {
	RestoreSite(ctx context.Context, w store.Website, cert, key []byte) (string, error)
}

func regularFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
