// Package backup 提供备份与恢复的接口：备份目标、备份计划、备份记录和恢复。
package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/backup"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
)

// Scheduler 是计划任务模块中与定时备份有关的部分。
type Scheduler interface {
	SaveBackupJob(planID uint, name, schedule string) (uint, error)
	BackupSchedule(planID uint) string
}

type Deps struct {
	Store   *store.Store
	Tasks   *task.Manager
	Engine  *backup.Engine
	Cron    Scheduler
	Confirm func(c *gin.Context) bool
	Audit   func(module, action string) gin.HandlerFunc
	// Restart 在面板数据恢复完成后重启面板
	Restart func()
}

type Service struct{ d Deps }

func New(d Deps) *Service { return &Service{d: d} }

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/backup")
	g.GET("/info", view, s.info)
	g.GET("/targets", view, s.targets)
	g.POST("/targets", manage, a("backup", "target-create"), s.targetSave)
	g.PUT("/targets/:id", manage, a("backup", "target-update"), s.targetSave)
	g.DELETE("/targets/:id", manage, a("backup", "target-delete"), s.targetDelete)
	g.POST("/targets/test", manage, s.targetTest)
	g.GET("/targets/:id/files", view, s.targetFiles)
	g.DELETE("/targets/:id/files", manage, a("backup", "file-delete"), s.targetFileDelete)

	g.GET("/plans", view, s.plans)
	g.POST("/plans", manage, a("backup", "plan-create"), s.planSave)
	g.PUT("/plans/:id", manage, a("backup", "plan-update"), s.planSave)
	g.DELETE("/plans/:id", manage, a("backup", "plan-delete"), s.planDelete)
	g.POST("/plans/:id/run", manage, a("backup", "run"), s.planRun)

	g.GET("/records", view, s.records)
	g.DELETE("/records/:id", manage, a("backup", "record-delete"), s.recordDelete)
	g.GET("/records/:id/download", manage, a("backup", "download"), s.recordDownload)
	g.POST("/restore", manage, a("backup", "restore"), s.restore)
}

func (s *Service) info(c *gin.Context) {
	api.OK(c, gin.H{
		"decryptCommand": backup.DecryptCommand,
		"dataDir":        s.d.Engine.DataDir,
		"defaultLocal":   filepath.Join(s.d.Engine.DataDir, "backups", "local"),
	})
}

/* ---------- 备份目标 ---------- */

type targetView struct {
	store.BackupTarget
	Config    backup.TargetConfig `json:"config"`
	HasSecret bool                `json:"hasSecret"`
	Plans     int64               `json:"plans"`
}

type targetReq struct {
	Name   string              `json:"name"`
	Type   string              `json:"type"`
	Config backup.TargetConfig `json:"config"`
	// Secret 为空时保留原有密钥
	Secret backup.TargetSecret `json:"secret"`
}

func (s *Service) targets(c *gin.Context) {
	var list []store.BackupTarget
	s.d.Store.DB.Order("id").Find(&list)
	var plans []store.BackupPlan
	s.d.Store.DB.Select("id", "target_ids").Find(&plans)
	out := make([]targetView, 0, len(list))
	for _, t := range list {
		cfg, _ := backup.ParseTarget(t)
		v := targetView{BackupTarget: t, Config: cfg, HasSecret: len(t.Secret) > 0}
		for _, p := range plans {
			for _, id := range backup.TargetIDs(p.TargetIDs) {
				if id == t.ID {
					v.Plans++
				}
			}
		}
		out = append(out, v)
	}
	api.OK(c, out)
}

func (r *targetReq) normalize() error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len([]rune(r.Name)) > 64 {
		return errors.New("名称不能为空，也不能超过 64 个字")
	}
	cfg := &r.Config
	cfg.Path = strings.TrimSpace(cfg.Path)
	switch r.Type {
	case "local":
		if !filepath.IsAbs(cfg.Path) || filepath.Clean(cfg.Path) == "/" {
			return errors.New("本地目录必须是绝对路径，且不能是根目录")
		}
		cfg.Path = filepath.Clean(cfg.Path)
		*cfg = backup.TargetConfig{Path: cfg.Path}
	case "sftp":
		if cfg.NodeID == "" {
			return errors.New("请选择服务器")
		}
		if !strings.HasPrefix(cfg.Path, "/") || path.Clean(cfg.Path) == "/" {
			return errors.New("远程目录必须是绝对路径，且不能是根目录")
		}
		*cfg = backup.TargetConfig{Path: path.Clean(cfg.Path), NodeID: cfg.NodeID}
	case "s3":
		cfg.Endpoint, cfg.Bucket = strings.TrimSpace(cfg.Endpoint), strings.TrimSpace(cfg.Bucket)
		if cfg.Endpoint == "" || cfg.Bucket == "" || strings.TrimSpace(cfg.AccessKey) == "" {
			return errors.New("请填写服务地址、存储桶和 Access Key")
		}
		cfg.Path = strings.Trim(cfg.Path, "/")
		cfg.URL, cfg.Username, cfg.NodeID = "", "", ""
	case "webdav":
		cfg.URL = strings.TrimSpace(cfg.URL)
		if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
			return errors.New("WebDAV 地址必须以 http:// 或 https:// 开头")
		}
		*cfg = backup.TargetConfig{URL: cfg.URL, Path: strings.Trim(cfg.Path, "/"), Username: strings.TrimSpace(cfg.Username)}
	default:
		return errors.New("备份目标类型只能是 local、sftp、s3 或 webdav")
	}
	return nil
}

// secretFor 合并表单中的密钥和已保存的密钥（表单留空表示不修改）。
func (s *Service) secretFor(old *store.BackupTarget, in backup.TargetSecret) (backup.TargetSecret, error) {
	if old == nil || len(old.Secret) == 0 {
		return in, nil
	}
	sec, err := s.d.Engine.Secret(*old)
	if err != nil {
		return in, nil
	}
	if in.SecretKey != "" {
		sec.SecretKey = in.SecretKey
	}
	if in.Password != "" {
		sec.Password = in.Password
	}
	return sec, nil
}

func (s *Service) targetSave(c *gin.Context) {
	var req targetReq
	if !api.Bind(c, &req) {
		return
	}
	if err := req.normalize(); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	var old *store.BackupTarget
	row := store.BackupTarget{}
	if id := c.Param("id"); id != "" {
		if err := s.d.Store.DB.First(&row, "id = ?", id).Error; err != nil {
			api.Fail(c, api.NotFound("备份目标不存在"))
			return
		}
		cp := row
		old = &cp
	}
	sec, _ := s.secretFor(old, req.Secret)
	if req.Type != "s3" {
		sec.SecretKey = ""
	}
	if req.Type != "webdav" {
		sec.Password = ""
	}
	cfg, _ := json.Marshal(req.Config)
	row.Name, row.Type, row.Config = req.Name, req.Type, string(cfg)
	row.Secret = nil
	if sec != (backup.TargetSecret{}) {
		b, _ := json.Marshal(sec)
		row.Secret = s.d.Engine.Box.Seal(b)
	}
	api.Target(c, row.Name)
	if err := s.d.Store.DB.Save(&row).Error; err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, row)
}

func (s *Service) target(c *gin.Context) (store.BackupTarget, bool) {
	var t store.BackupTarget
	if err := s.d.Store.DB.First(&t, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("备份目标不存在"))
		return t, false
	}
	api.Target(c, t.Name)
	return t, true
}

func (s *Service) targetDelete(c *gin.Context) {
	t, ok := s.target(c)
	if !ok {
		return
	}
	var plans []store.BackupPlan
	s.d.Store.DB.Find(&plans)
	for _, p := range plans {
		for _, id := range backup.TargetIDs(p.TargetIDs) {
			if id == t.ID {
				api.Fail(c, api.BadRequest(fmt.Sprintf("备份计划“%s”正在使用这个目标，请先修改计划", p.Name)))
				return
			}
		}
	}
	// 只删除目标配置，目标中的备份文件保留
	s.d.Store.DB.Delete(&store.BackupRecord{}, "target_id = ?", t.ID)
	s.d.Store.DB.Delete(&t)
	api.OK(c, nil)
}

// targetTest 测试连接：列出文件，并写入、删除一个测试文件。
func (s *Service) targetTest(c *gin.Context) {
	var req struct {
		targetReq
		ID uint `json:"id"`
	}
	if !api.Bind(c, &req) {
		return
	}
	if req.Name == "" {
		req.Name = "test"
	}
	if err := req.normalize(); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	var old *store.BackupTarget
	if req.ID > 0 {
		var row store.BackupTarget
		if s.d.Store.DB.First(&row, req.ID).Error == nil {
			old = &row
		}
	}
	sec, _ := s.secretFor(old, req.Secret)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	st, err := backup.Open(ctx, req.Type, req.Config, sec, s.d.Engine.Remote)
	if err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	defer st.Close()
	list, err := st.List(ctx)
	if err != nil {
		api.Fail(c, api.BadRequest("连接失败："+err.Error()))
		return
	}
	key := fmt.Sprintf(".sshdesktop-test-%d", time.Now().UnixNano())
	body := "sshDesktop backup target test\n"
	if err := st.Put(ctx, key, strings.NewReader(body), int64(len(body))); err != nil {
		api.Fail(c, api.BadRequest("可以连接，但写入失败："+err.Error()))
		return
	}
	if err := st.Delete(ctx, key); err != nil {
		api.Fail(c, api.BadRequest("可以写入，但删除测试文件失败："+err.Error()))
		return
	}
	api.OK(c, gin.H{"files": len(list)})
}

func (s *Service) openTarget(c *gin.Context, t store.BackupTarget) (backup.Storage, context.Context, context.CancelFunc, bool) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
	st, err := s.d.Engine.OpenTarget(ctx, t)
	if err != nil {
		cancel()
		api.Fail(c, api.Err(http.StatusBadGateway, api.CodeBackupFailed, err.Error()))
		return nil, nil, nil, false
	}
	return st, ctx, cancel, true
}

// targetFiles 列出目标中的备份文件，标出面板中有记录的文件。
func (s *Service) targetFiles(c *gin.Context) {
	t, ok := s.target(c)
	if !ok {
		return
	}
	st, ctx, cancel, ok := s.openTarget(c, t)
	if !ok {
		return
	}
	defer cancel()
	defer st.Close()
	list, err := st.List(ctx)
	if err != nil {
		api.Fail(c, api.Err(http.StatusBadGateway, api.CodeBackupFailed, "列出文件失败："+err.Error()))
		return
	}
	var recs []store.BackupRecord
	s.d.Store.DB.Select("id", "key").Where("target_id = ? AND status = ?", t.ID, "success").Find(&recs)
	known := map[string]uint{}
	for _, r := range recs {
		known[r.Key] = r.ID
	}
	type fileView struct {
		backup.Object
		RecordID  uint `json:"recordId,omitempty"`
		Encrypted bool `json:"encrypted"`
	}
	out := make([]fileView, 0, len(list))
	for _, o := range list {
		out = append(out, fileView{Object: o, RecordID: known[o.Key], Encrypted: strings.HasSuffix(o.Key, ".enc")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	api.OK(c, out)
}

func (s *Service) targetFileDelete(c *gin.Context) {
	t, ok := s.target(c)
	if !ok {
		return
	}
	key := c.Query("key")
	api.Target(c, t.Name+":"+key)
	if !s.d.Confirm(c) {
		return
	}
	st, ctx, cancel, ok := s.openTarget(c, t)
	if !ok {
		return
	}
	defer cancel()
	defer st.Close()
	if err := st.Delete(ctx, key); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	s.d.Store.DB.Delete(&store.BackupRecord{}, "target_id = ? AND key = ?", t.ID, key)
	api.OK(c, nil)
}

/* ---------- 备份计划 ---------- */

type planView struct {
	store.BackupPlan
	TargetIDs   []uint              `json:"targetIds"`
	HasPassword bool                `json:"hasPassword"`
	Schedule    string              `json:"schedule"`
	Last        *store.BackupRecord `json:"last,omitempty"`
}

type planReq struct {
	store.BackupPlan
	TargetIDs []uint `json:"targetIds"`
	Schedule  string `json:"schedule"`
	// Password 为空时保留原密码
	Password string `json:"password"`
}

func (s *Service) plans(c *gin.Context) {
	var list []store.BackupPlan
	s.d.Store.DB.Order("id").Find(&list)
	out := make([]planView, 0, len(list))
	for _, p := range list {
		v := planView{BackupPlan: p, TargetIDs: backup.TargetIDs(p.TargetIDs), HasPassword: len(p.Password) > 0,
			Schedule: s.d.Cron.BackupSchedule(p.ID)}
		if v.TargetIDs == nil {
			v.TargetIDs = []uint{}
		}
		var last store.BackupRecord
		if s.d.Store.DB.Where("plan_id = ?", p.ID).Not(map[string]any{"trigger": "snapshot"}).Order("started_at DESC, id DESC").First(&last).Error == nil {
			v.Last = &last
		}
		out = append(out, v)
	}
	api.OK(c, out)
}

func (s *Service) validatePlan(req *planReq, old *store.BackupPlan) error {
	p := &req.BackupPlan
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len([]rune(p.Name)) > 64 {
		return errors.New("名称不能为空，也不能超过 64 个字")
	}
	p.Source = strings.TrimSpace(p.Source)
	switch p.Type {
	case "dir":
		if !filepath.IsAbs(p.Source) {
			return errors.New("请填写要备份的目录（绝对路径）")
		}
		p.Source = filepath.Clean(p.Source)
		if p.Source == "/" {
			return errors.New("不能备份整个根目录，请选择具体的目录")
		}
	case "app":
		var n int64
		s.d.Store.DB.Model(&store.AppInstalled{}).Where("name = ?", p.Source).Count(&n)
		if n == 0 {
			return errors.New("请选择已安装的应用")
		}
	case "website":
		var n int64
		s.d.Store.DB.Model(&store.Website{}).Where("name = ?", p.Source).Count(&n)
		if n == 0 {
			return errors.New("请选择网站")
		}
	case "panel":
		p.Source = ""
	default:
		return errors.New("备份类型只能是 dir、app、website 或 panel")
	}
	if len(req.TargetIDs) == 0 {
		return errors.New("请至少选择一个备份目标")
	}
	var n int64
	s.d.Store.DB.Model(&store.BackupTarget{}).Where("id IN ?", req.TargetIDs).Count(&n)
	if int(n) != len(uniq(req.TargetIDs)) {
		return errors.New("备份目标不存在")
	}
	if p.KeepCount < 0 || p.KeepDays < 0 || p.KeepCount > 10000 || p.KeepDays > 36500 {
		return errors.New("保留份数或天数不正确")
	}
	if p.Encrypt && req.Password == "" && (old == nil || len(old.Password) == 0) {
		return errors.New("加密备份需要设置备份密码")
	}
	if req.Password != "" && len(req.Password) < 8 {
		return errors.New("备份密码至少 8 位")
	}
	if len(p.Excludes) > 8192 {
		return errors.New("排除规则太长")
	}
	return nil
}

func uniq(ids []uint) []uint {
	seen := map[uint]bool{}
	var out []uint
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func joinIDs(ids []uint) string {
	parts := make([]string, 0, len(ids))
	for _, id := range uniq(ids) {
		parts = append(parts, strconv.FormatUint(uint64(id), 10))
	}
	return strings.Join(parts, ",")
}

func (s *Service) planSave(c *gin.Context) {
	var req planReq
	if !api.Bind(c, &req) {
		return
	}
	var old *store.BackupPlan
	if id := c.Param("id"); id != "" {
		var row store.BackupPlan
		if err := s.d.Store.DB.First(&row, "id = ?", id).Error; err != nil {
			api.Fail(c, api.NotFound("备份计划不存在"))
			return
		}
		old = &row
	}
	if err := s.validatePlan(&req, old); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	p := req.BackupPlan
	p.TargetIDs = joinIDs(req.TargetIDs)
	p.Password = nil
	if old != nil {
		p.ID, p.CreatedAt, p.Password = old.ID, old.CreatedAt, old.Password
	} else {
		p.ID = 0
	}
	if req.Password != "" {
		p.Password = s.d.Engine.Box.Seal([]byte(req.Password))
	}
	if !p.Encrypt {
		p.Password = nil
	}
	api.Target(c, p.Name)
	if err := s.d.Store.DB.Save(&p).Error; err != nil {
		api.Fail(c, err)
		return
	}
	if _, err := s.d.Cron.SaveBackupJob(p.ID, p.Name, req.Schedule); err != nil {
		api.Fail(c, api.BadRequest("计划已保存，但定时设置失败："+err.Error()))
		return
	}
	api.OK(c, p)
}

func (s *Service) plan(c *gin.Context) (store.BackupPlan, bool) {
	var p store.BackupPlan
	if err := s.d.Store.DB.First(&p, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("备份计划不存在"))
		return p, false
	}
	api.Target(c, p.Name)
	return p, true
}

// planDelete 删除计划和它的定时任务；已有的备份文件和记录保留，仍可恢复。
func (s *Service) planDelete(c *gin.Context) {
	p, ok := s.plan(c)
	if !ok {
		return
	}
	if _, err := s.d.Cron.SaveBackupJob(p.ID, p.Name, ""); err != nil {
		api.Fail(c, err)
		return
	}
	s.d.Store.DB.Delete(&p)
	api.OK(c, nil)
}

func (s *Service) planRun(c *gin.Context) {
	p, ok := s.plan(c)
	if !ok {
		return
	}
	t := s.d.Tasks.Start("backup.run", "备份："+p.Name, api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		out := t.Lines()
		defer out.Flush()
		return s.d.Engine.RunPlan(ctx, p.ID, "manual", out)
	})
	api.OK(c, t)
}

/* ---------- 备份记录与恢复 ---------- */

func (s *Service) records(c *gin.Context) {
	q := s.d.Store.DB.Order("started_at DESC, id DESC")
	if v := c.Query("plan"); v != "" {
		q = q.Where("plan_id = ?", v)
	}
	if v := c.Query("target"); v != "" {
		q = q.Where("target_id = ?", v)
	}
	if v := c.Query("type"); v != "" {
		q = q.Where("type = ?", v)
	}
	if c.Query("snapshot") == "1" {
		q = q.Where(map[string]any{"trigger": "snapshot"})
	} else if c.Query("snapshot") == "0" {
		q = q.Not(map[string]any{"trigger": "snapshot"})
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var list []store.BackupRecord
	q.Limit(limit).Find(&list)
	api.OK(c, list)
}

func (s *Service) record(c *gin.Context) (store.BackupRecord, bool) {
	var r store.BackupRecord
	if err := s.d.Store.DB.First(&r, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("备份记录不存在"))
		return r, false
	}
	api.Target(c, r.TargetName+":"+r.Key)
	return r, true
}

// recordStorage 打开记录所在的存储（TargetID 为 0 的是本机的恢复前快照）。
func (s *Service) recordStorage(ctx context.Context, r store.BackupRecord) (backup.Storage, error) {
	if r.TargetID == 0 {
		return backup.Open(ctx, "local", backup.TargetConfig{Path: filepath.Join(s.d.Engine.DataDir, "backups", "snapshots")}, backup.TargetSecret{}, nil)
	}
	var t store.BackupTarget
	if err := s.d.Store.DB.First(&t, r.TargetID).Error; err != nil {
		return nil, errors.New("备份目标不存在")
	}
	return s.d.Engine.OpenTarget(ctx, t)
}

func (s *Service) recordDelete(c *gin.Context) {
	r, ok := s.record(c)
	if !ok {
		return
	}
	if !s.d.Confirm(c) {
		return
	}
	if r.Status == "success" && r.Key != "" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), time.Minute)
		defer cancel()
		st, err := s.recordStorage(ctx, r)
		if err == nil {
			err = st.Delete(ctx, r.Key)
			st.Close()
		}
		if err != nil && c.Query("force") != "1" {
			api.Fail(c, api.Err(http.StatusBadGateway, api.CodeBackupFailed, "删除备份文件失败："+err.Error()))
			return
		}
	}
	s.d.Store.DB.Delete(&r)
	api.OK(c, nil)
}

func (s *Service) recordDownload(c *gin.Context) {
	r, ok := s.record(c)
	if !ok {
		return
	}
	if r.Status != "success" {
		api.Fail(c, api.BadRequest("这份备份没有成功完成"))
		return
	}
	ctx := c.Request.Context()
	st, err := s.recordStorage(ctx, r)
	if err != nil {
		api.Fail(c, api.Err(http.StatusBadGateway, api.CodeBackupFailed, err.Error()))
		return
	}
	defer st.Close()
	rc, err := st.Get(ctx, r.Key)
	if err != nil {
		api.Fail(c, api.Err(http.StatusBadGateway, api.CodeBackupFailed, "读取备份文件失败："+err.Error()))
		return
	}
	defer rc.Close()
	name := path.Base(r.Key)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(name, `"`, "")))
	size := r.Size
	if size <= 0 {
		size = -1
	}
	c.DataFromReader(http.StatusOK, size, "application/octet-stream", rc, nil)
}

func (s *Service) restore(c *gin.Context) {
	var req backup.RestoreRequest
	if !api.Bind(c, &req) {
		return
	}
	if req.RecordID == 0 && req.Key == "" {
		api.Fail(c, api.BadRequest("请选择要恢复的备份"))
		return
	}
	title := req.Key
	if req.RecordID > 0 {
		var r store.BackupRecord
		if err := s.d.Store.DB.First(&r, req.RecordID).Error; err != nil {
			api.Fail(c, api.NotFound("备份记录不存在"))
			return
		}
		title = r.Key
	}
	api.Target(c, title)
	if !s.d.Confirm(c) {
		return
	}
	t := s.d.Tasks.Start("backup.restore", "恢复："+path.Base(title), api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		out := t.Lines()
		req.TaskID = t.ID()
		res, err := s.d.Engine.Restore(ctx, req, out)
		if err == nil && res.Snapshot != "" {
			fmt.Fprintf(out, "如需撤销，可在“恢复前快照”中恢复 %s\n", res.Snapshot)
		}
		if err == nil && res.NeedRestart {
			if s.d.Restart != nil {
				fmt.Fprintln(out, "即将重启面板…")
				out.Flush()
				go func() {
					time.Sleep(2 * time.Second)
					s.d.Restart()
				}()
			} else {
				fmt.Fprintln(out, "请手动重启面板（systemctl restart sshdesktop）使恢复生效")
			}
		}
		out.Flush()
		return err
	})
	api.OK(c, t)
}
