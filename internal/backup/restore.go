package backup

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"gorm.io/gorm"
)

// RestoreRequest 指定要恢复的备份：RecordID，或 TargetID + Key（目标中没有记录的文件，例如从其他服务器迁移）。
type RestoreRequest struct {
	RecordID uint   `json:"recordId"`
	TargetID uint   `json:"targetId"`
	Key      string `json:"key"`
	Password string `json:"password"`
	// Dest 为目录备份恢复到的位置，为空时恢复到原目录
	Dest string `json:"dest"`
	// NoSnapshot 为 true 时恢复前不做快照
	NoSnapshot bool `json:"noSnapshot"`
	// TaskID 是执行恢复的后台任务，恢复面板数据时随恢复前快照一起带到恢复后的数据库
	TaskID string `json:"-"`
}

// RestoreResult 描述恢复结果。NeedRestart 表示面板数据已准备好，需要重启面板生效。
type RestoreResult struct {
	Type        string `json:"type"`
	Dest        string `json:"dest"`
	Files       int    `json:"files"`
	Snapshot    string `json:"snapshot,omitempty"`
	NeedRestart bool   `json:"needRestart"`
	Warning     string `json:"warning,omitempty"`
}

// Restore 下载并恢复一份备份。恢复前对将被覆盖的内容做一份本地快照，出错时可以用快照恢复回来。
func (e *Engine) Restore(ctx context.Context, req RestoreRequest, w io.Writer) (*RestoreResult, error) {
	log := logger(w)
	file, err := e.fetchBackup(ctx, &req, log)
	if file != "" {
		defer os.Remove(file)
	}
	if err != nil {
		return nil, err
	}
	open := func() (io.Reader, func(), error) {
		f, err := os.Open(file)
		if err != nil {
			return nil, nil, err
		}
		r, err := plainReader(f, req.Password)
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		return r, func() { f.Close() }, nil
	}
	r, done, err := open()
	if err != nil {
		return nil, err
	}
	m, err := readManifest(r)
	done()
	if err != nil {
		if errors.Is(err, ErrBadPassword) {
			return nil, err
		}
		return nil, err
	}
	log("备份内容：%s，创建于 %s（%s，sshDesktop %s）", typeName(m.Type), m.CreatedAt.Local().Format("2006-01-02 15:04:05"), m.Host, m.Version)
	switch m.Type {
	case "dir":
		return e.restoreDir(ctx, m, req, open, log)
	case "app":
		return e.restoreApp(ctx, m, req, open, log)
	case "panel":
		return e.restorePanel(ctx, m, req, open, log)
	}
	return nil, fmt.Errorf("不支持的备份类型：%s", m.Type)
}

func typeName(t string) string {
	switch t {
	case "dir":
		return "目录"
	case "app":
		return "应用"
	case "panel":
		return "面板数据"
	}
	return t
}

// plainReader 识别加密文件（Salted__ 开头）并解密。
func plainReader(f io.Reader, password string) (io.Reader, error) {
	br := bufio.NewReader(f)
	head, _ := br.Peek(8)
	if string(head) != saltMagic {
		return br, nil
	}
	if password == "" {
		return nil, errors.New("这份备份已加密，请输入备份密码")
	}
	return NewDecrypter(br, password)
}

// fetchBackup 把备份下载到本地临时文件，并补全密码（记录所属计划保存的密码）。
func (e *Engine) fetchBackup(ctx context.Context, req *RestoreRequest, log func(string, ...any)) (string, error) {
	var target store.BackupTarget
	key := req.Key
	if req.RecordID > 0 {
		var rec store.BackupRecord
		if err := e.Store.DB.First(&rec, req.RecordID).Error; err != nil {
			return "", errors.New("备份记录不存在")
		}
		if rec.Status != "success" {
			return "", errors.New("这份备份没有成功完成，不能恢复")
		}
		key = rec.Key
		req.TargetID = rec.TargetID
		if req.Password == "" && rec.Encrypted {
			var plan store.BackupPlan
			if e.Store.DB.First(&plan, rec.PlanID).Error == nil {
				req.Password, _ = e.PlanPassword(plan)
			}
		}
	}
	if err := validKey(key); err != nil {
		return "", err
	}
	var st Storage
	if req.TargetID == 0 {
		st = NewFS(vfs.Local{}, e.snapshotDir(), nil)
		target.Name = "恢复前快照"
	} else {
		if err := e.Store.DB.First(&target, req.TargetID).Error; err != nil {
			return "", errors.New("备份目标不存在")
		}
		var err error
		if st, err = e.OpenTarget(ctx, target); err != nil {
			return "", err
		}
	}
	defer st.Close()
	log("从“%s”下载 %s", target.Name, key)
	rc, err := st.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("下载备份失败：%w", err)
	}
	defer rc.Close()
	dir, err := e.tmpDir()
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "restore-*")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, ctxReader{ctx, rc})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return f.Name(), fmt.Errorf("下载备份失败：%w", err)
	}
	log("下载完成，大小 %s", humanSize(n))
	return f.Name(), nil
}

/* ---------- 恢复前快照 ---------- */

// snapshot 用与普通备份相同的格式把将被覆盖的内容保存到本地快照目录，返回快照文件名。
func (e *Engine) snapshot(ctx context.Context, plan store.BackupPlan, log func(string, ...any)) (string, error) {
	log("恢复前先给现有内容做快照")
	now := time.Now()
	plan.ID = 0
	key := strings.Replace(objectKey(plan, now, false), "plan-0/", "", 1)
	dir := e.snapshotDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".snap-*")
	if err != nil {
		return "", err
	}
	plan.IncludeSecretKey = true // 快照只在本机，包含主密钥才能完整回退
	size, err := e.writeArchive(ctx, tmp, plan, "", func(string, ...any) {})
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("恢复前快照失败：%w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, key)); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	end := time.Now()
	e.Store.DB.Create(&store.BackupRecord{
		PlanName: "恢复前快照", Type: plan.Type, Source: plan.Source, TargetName: "恢复前快照",
		Key: key, Size: size, Status: "success", Trigger: "snapshot", StartedAt: now, FinishedAt: &end,
	})
	log("快照已保存：%s（%s）", key, humanSize(size))
	// 只保留最近的若干份快照
	var old []store.BackupRecord
	e.Store.DB.Where(map[string]any{"trigger": "snapshot", "target_id": 0}).Order("started_at DESC, id DESC").Offset(keepSnapshots).Find(&old)
	for _, r := range old {
		if validKey(r.Key) == nil {
			os.Remove(filepath.Join(dir, r.Key))
		}
		e.Store.DB.Delete(&store.BackupRecord{}, r.ID)
	}
	return key, nil
}

/* ---------- 目录 ---------- */

func (e *Engine) restoreDir(ctx context.Context, m Manifest, req RestoreRequest,
	open func() (io.Reader, func(), error), log func(string, ...any)) (*RestoreResult, error) {
	dest := strings.TrimSpace(req.Dest)
	if dest == "" {
		dest = m.Source
	}
	dest = filepath.Clean(dest)
	if !filepath.IsAbs(dest) {
		return nil, errors.New("恢复位置必须是绝对路径")
	}
	if protectedDirs[dest] {
		return nil, fmt.Errorf("不能整体恢复到系统目录 %s，请换一个恢复位置", dest)
	}
	res := &RestoreResult{Type: "dir", Dest: dest}
	if dirExists(dest) && !req.NoSnapshot {
		snap, err := e.snapshot(ctx, store.BackupPlan{Name: "恢复前快照", Type: "dir", Source: dest}, log)
		if err != nil {
			return nil, err
		}
		res.Snapshot = snap
	}
	n, err := replaceDir(dest, "data", open, log)
	if err != nil {
		return nil, err
	}
	res.Files = n
	log("已恢复 %d 个文件到 %s", n, dest)
	return res, nil
}

// replaceDir 把备份包中 prefix 下的内容解压到 dest，替换 dest 原有内容。
// 先解压到 dest 内的临时目录（同一文件系统，挂载点也适用），全部成功后再替换，解压失败时原内容不受影响。
func replaceDir(dest, prefix string, open func() (io.Reader, func(), error), log func(string, ...any)) (int, error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return 0, err
	}
	stageName := fmt.Sprintf(".sshdesktop-restore-%d", time.Now().UnixNano())
	stage := filepath.Join(dest, stageName)
	if err := os.Mkdir(stage, 0o700); err != nil {
		return 0, err
	}
	defer os.RemoveAll(stage)
	r, done, err := open()
	if err != nil {
		return 0, err
	}
	_, n, err := extract(r, stage, prefix)
	done()
	if err != nil {
		return 0, err
	}
	old, err := os.ReadDir(dest)
	if err != nil {
		return 0, err
	}
	for _, d := range old {
		if d.Name() == stageName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dest, d.Name())); err != nil {
			return 0, fmt.Errorf("清理原有文件失败：%w", err)
		}
	}
	list, err := os.ReadDir(stage)
	if err != nil {
		return 0, err
	}
	for _, d := range list {
		if err := os.Rename(filepath.Join(stage, d.Name()), filepath.Join(dest, d.Name())); err != nil {
			return 0, err
		}
	}
	// 目录自身的权限和属主以备份为准
	if fi, err := os.Stat(stage); err == nil {
		os.Chmod(dest, fi.Mode().Perm())
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			os.Lchown(dest, int(st.Uid), int(st.Gid))
		}
	}
	return n, nil
}

/* ---------- 应用 ---------- */

func (e *Engine) restoreApp(ctx context.Context, m Manifest, req RestoreRequest,
	open func() (io.Reader, func(), error), log func(string, ...any)) (*RestoreResult, error) {
	if len(m.App) == 0 {
		return nil, errors.New("备份中没有应用的安装信息")
	}
	var rec appRecord
	if err := json.Unmarshal(m.App, &rec); err != nil {
		return nil, fmt.Errorf("备份中的应用信息损坏：%w", err)
	}
	row := rec.AppInstalled
	row.Params = rec.Params
	if row.Name == "" || row.Key == "" || slug(row.Key, "") != row.Key || slug(row.Name, "") != row.Name {
		return nil, errors.New("备份中的应用信息不完整")
	}
	if e.Docker == nil || e.Docker.Ping(ctx) != nil {
		return nil, errors.New("Docker 未运行，无法恢复应用")
	}
	var cur store.AppInstalled
	exists := e.Store.DB.First(&cur, "name = ?", row.Name).Error == nil
	dest := filepath.Join(e.DataDir, "apps", row.Key, row.Name)
	if exists {
		dest = cur.Dir
	}
	res := &RestoreResult{Type: "app", Dest: dest}
	if exists && dirExists(cur.Dir) {
		if !req.NoSnapshot {
			snap, err := e.snapshot(ctx, store.BackupPlan{Name: "恢复前快照", Type: "app", Source: cur.Name}, log)
			if err != nil {
				return nil, err
			}
			res.Snapshot = snap
		}
		log("停止并移除应用 %s 的容器（数据卷保留）", cur.Name)
		if err := docker.Compose(ctx, appProject(cur), w2(log), "down"); err != nil {
			log("停止失败（继续恢复）：%s", err)
		}
	}
	n, err := replaceDir(dest, "app", open, log)
	if err != nil {
		return nil, err
	}
	res.Files = n
	row.Dir = dest
	row.Status = "installed"
	row.Message = ""
	row.UpdatedAt = time.Now()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = row.UpdatedAt
	}
	if err := e.Store.DB.Save(&row).Error; err != nil {
		return nil, err
	}
	e.Store.DB.Save(&store.ComposeProject{Name: row.Name, Dir: dest, File: filepath.Join(dest, "docker-compose.yml"), CreatedAt: time.Now()})
	if err := e.Docker.EnsureNetwork(ctx, AppNetwork); err != nil {
		return nil, err
	}
	log("启动应用 %s", row.Name)
	if err := docker.Compose(ctx, appProject(row), w2(log), "up", "-d", "--remove-orphans"); err != nil {
		e.Store.DB.Model(&row).Updates(map[string]any{"status": "failed", "message": err.Error()})
		return res, fmt.Errorf("文件已恢复，但启动失败：%w", err)
	}
	log("应用 %s 已恢复并启动", row.Name)
	return res, nil
}

// w2 把日志函数包装成 io.Writer，供 compose 输出使用。
func w2(log func(string, ...any)) io.Writer {
	return writerFunc(func(p []byte) (int, error) {
		for _, l := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
			if l = strings.TrimRight(l, "\r"); l != "" {
				log("%s", l)
			}
		}
		return len(p), nil
	})
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

/* ---------- 面板 ---------- */

// PendingDir 是等待下次启动时应用的面板恢复数据。
func PendingDir(dataDir string) string { return filepath.Join(dataDir, "restore-pending") }

// carryFile 记录换库后要从原数据库带过来的内容，由 CarryOverPanelRestore 在打开新数据库后处理。
const carryFile = "restore-carry.json"

// panelCarry 写在等待目录的 READY 文件中。恢复前快照、所恢复的备份记录、恢复任务和备份之后的审计日志都晚于备份产生，
// 新数据库里没有，换库后从原数据库带过来，否则快照无从撤销，审计日志也等于被删除了。
type panelCarry struct {
	Manifest Manifest `json:"manifest"`
	Old      string   `json:"old,omitempty"` // 原数据移到的 restore-old-<时间> 目录
	Snapshot string   `json:"snapshot,omitempty"`
	RecordID uint     `json:"recordId,omitempty"`
	TaskID   string   `json:"taskId,omitempty"`
}

func (e *Engine) restorePanel(ctx context.Context, m Manifest, req RestoreRequest,
	open func() (io.Reader, func(), error), log func(string, ...any)) (*RestoreResult, error) {
	res := &RestoreResult{Type: "panel", Dest: e.DataDir, NeedRestart: true}
	if !req.NoSnapshot {
		snap, err := e.snapshot(ctx, store.BackupPlan{Name: "恢复前快照", Type: "panel"}, log)
		if err != nil {
			return nil, err
		}
		res.Snapshot = snap
	}
	pending := PendingDir(e.DataDir)
	os.RemoveAll(pending)
	if err := os.MkdirAll(pending, 0o700); err != nil {
		return nil, err
	}
	r, done, err := open()
	if err != nil {
		return nil, err
	}
	_, n, err := extract(r, pending, "panel")
	done()
	if err != nil {
		os.RemoveAll(pending)
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(pending, "sshdesktop.db")); err != nil {
		os.RemoveAll(pending)
		return nil, errors.New("备份中没有面板数据库")
	}
	res.Files = n
	_, hasKey := os.Stat(filepath.Join(pending, "secret.key"))
	if hasKey != nil && m.KeyID != "" && m.KeyID != e.keyID() {
		res.Warning = "这份备份不含主密钥，且与本机的主密钥不同：服务器登录凭据、备份目标密钥和备份密码需要重新填写。"
		log("%s", res.Warning)
	}
	b, _ := json.Marshal(panelCarry{Manifest: m, Snapshot: res.Snapshot, RecordID: req.RecordID, TaskID: req.TaskID})
	if err := os.WriteFile(filepath.Join(pending, "READY"), b, 0o600); err != nil {
		return nil, err
	}
	log("面板数据已准备好，重启面板后生效（当前登录会话会失效，需要重新登录）")
	return res, nil
}

// ApplyPendingPanelRestore 在面板启动、打开数据库之前调用：把等待中的面板恢复数据换上，原数据移到 restore-old-<时间>。
// 备份中的 config.yaml 不会覆盖当前配置（端口、域名、证书路径与服务器有关），而是保存为 config.yaml.from-backup 供参考。
func ApplyPendingPanelRestore(dataDir, configPath string) error {
	pending := PendingDir(dataDir)
	ready, err := os.ReadFile(filepath.Join(pending, "READY"))
	if err != nil {
		if dirExists(pending) {
			os.RemoveAll(pending) // 没准备完的残留
		}
		return nil
	}
	old := filepath.Join(dataDir, "restore-old-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(old, 0o700); err != nil {
		return err
	}
	move := func(name string) error {
		src := filepath.Join(pending, name)
		if _, err := os.Lstat(src); err != nil {
			return nil
		}
		dst := filepath.Join(dataDir, name)
		if _, err := os.Lstat(dst); err == nil {
			if err := os.Rename(dst, filepath.Join(old, name)); err != nil {
				return err
			}
		}
		return os.Rename(src, dst)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if p := filepath.Join(dataDir, "sshdesktop.db"+suffix); fileExists(p) {
			os.Rename(p, filepath.Join(old, "sshdesktop.db"+suffix))
		}
	}
	for _, name := range []string{"sshdesktop.db", "secret.key", "compose"} {
		if err := move(name); err != nil {
			return fmt.Errorf("应用面板恢复数据失败：%w", err)
		}
	}
	if configPath != "" && fileExists(filepath.Join(pending, "config.yaml")) {
		os.Rename(filepath.Join(pending, "config.yaml"), configPath+".from-backup")
	}
	os.RemoveAll(pending)
	var carry panelCarry
	if json.Unmarshal(ready, &carry) == nil {
		carry.Old = old
		b, _ := json.Marshal(carry)
		os.WriteFile(filepath.Join(dataDir, carryFile), b, 0o600)
	}
	slog.Warn("已应用面板恢复数据，原数据保存在 "+old, "dir", old)
	return nil
}

// CarryOverPanelRestore 在应用面板恢复数据、打开新数据库之后调用：从原数据库带过恢复前快照、所恢复的备份记录、
// 恢复任务及其输出，以及新数据库里最后一条之后的审计日志。失败只记日志，不影响启动。
func CarryOverPanelRestore(st *store.Store, dataDir string) {
	p := filepath.Join(dataDir, carryFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	os.Remove(p)
	var c panelCarry
	if json.Unmarshal(b, &c) != nil || c.Old == "" {
		return
	}
	old, err := store.Open(filepath.Join(c.Old, "sshdesktop.db"))
	if err != nil {
		slog.Warn("无法打开恢复前的数据库，恢复前快照和恢复记录没有带过来", "err", err)
		return
	}
	defer old.Close()
	recs, tasks, audits := carryOver(old.DB, st.DB, c)
	slog.Info("已从恢复前的数据库带过记录", "records", recs, "tasks", tasks, "audit", audits)
}

func carryOver(from, to *gorm.DB, c panelCarry) (recs, tasks, audits int) {
	var list []store.BackupRecord
	if c.Snapshot != "" {
		var r store.BackupRecord
		if from.Where(map[string]any{"trigger": "snapshot", "target_id": 0, "key": c.Snapshot}).First(&r).Error == nil {
			list = append(list, r)
		}
	}
	if c.RecordID > 0 {
		var r store.BackupRecord
		if from.First(&r, c.RecordID).Error == nil && sameTarget(from, to, r.TargetID) {
			list = append(list, r)
		}
	}
	for _, r := range list {
		var n int64
		to.Model(&store.BackupRecord{}).Where("target_id = ? AND key = ?", r.TargetID, r.Key).Count(&n)
		if n > 0 {
			continue
		}
		var plan store.BackupPlan
		if r.PlanID > 0 && (to.First(&plan, r.PlanID).Error != nil || plan.Name != r.PlanName) {
			r.PlanID = 0 // 新数据库里没有这个计划，或同一编号是别的计划
		}
		r.ID = 0
		if to.Create(&r).Error == nil {
			recs++
		}
	}
	if c.TaskID != "" {
		var t store.Task
		var n int64
		to.Model(&store.Task{}).Where("id = ?", c.TaskID).Count(&n)
		if n == 0 && from.First(&t, "id = ?", c.TaskID).Error == nil && to.Create(&t).Error == nil {
			tasks++
			var logs []store.TaskLog
			from.Where("task_id = ?", t.ID).Order("id").Find(&logs)
			for i := range logs {
				logs[i].ID = 0
			}
			if len(logs) > 0 {
				to.CreateInBatches(&logs, 200)
			}
		}
	}
	var last store.AuditLog
	q := from.Order("id")
	if to.Order("at DESC, id DESC").First(&last).Error == nil {
		q = q.Where("at > ?", last.At)
	}
	var rows []store.AuditLog
	q.FindInBatches(&rows, 500, func(tx *gorm.DB, _ int) error {
		cp := make([]store.AuditLog, len(rows)) // rows 的编号还要用来取下一批，不能改
		for i, r := range rows {
			r.ID = 0
			cp[i] = r
		}
		if err := to.Create(&cp).Error; err != nil {
			return err
		}
		audits += len(cp)
		return nil
	})
	return
}

// sameTarget 判断备份记录的目标在新数据库里是否还是同一个（编号、类型和名称都相同）。
func sameTarget(from, to *gorm.DB, id uint) bool {
	if id == 0 {
		return true
	}
	var a, b store.BackupTarget
	return from.First(&a, id).Error == nil && to.First(&b, id).Error == nil && a.Type == b.Type && a.Name == b.Name
}

func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
