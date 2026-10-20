package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ProjectPattern 是 Compose 项目名的规则（与 docker compose 一致）。
var ProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

type projectView struct {
	Name       string          `json:"name"`
	Dir        string          `json:"dir"`
	Files      []string        `json:"files"`
	Managed    bool            `json:"managed"`  // 目录由面板创建
	Recorded   bool            `json:"recorded"` // 已登记在面板中（容器都删除后仍会列出）
	App        string          `json:"app,omitempty"`
	Running    int             `json:"running"`
	Total      int             `json:"total"`
	Services   []string        `json:"services"`
	Status     string          `json:"status"` // running / partial / stopped / missing
	Created    int64           `json:"created"`
	Containers []containerView `json:"containers"`
}

// ComposeDir 是面板新建的 Compose 项目所在目录。
func (s *Service) composeRoot() string { return filepath.Join(s.d.DataDir, "compose") }

// projects 汇总已登记的项目和从容器标签识别出的项目。
func (s *Service) projects(ctx context.Context) ([]*projectView, error) {
	by := map[string]*projectView{}
	var rows []store.ComposeProject
	s.d.Store.DB.Find(&rows)
	for _, r := range rows {
		v := &projectView{Name: r.Name, Dir: r.Dir, Managed: r.Managed, Recorded: true, Created: r.CreatedAt.Unix()}
		if r.File != "" {
			v.Files = []string{filepath.Join(r.Dir, r.File)}
		}
		by[r.Name] = v
	}
	cts, err := s.d.Docker.Containers(ctx, true, map[string][]string{"label": {docker.LabelProject}})
	if err != nil {
		return nil, err
	}
	for _, ct := range cts {
		name := ct.Labels[docker.LabelProject]
		v := by[name]
		if v == nil {
			v = &projectView{Name: name, Dir: ct.Labels[docker.LabelWorkingDir], Created: ct.Created}
			if f := ct.Labels[docker.LabelConfigFiles]; f != "" {
				v.Files = strings.Split(f, ",")
			}
			by[name] = v
		}
		v.Total++
		if ct.State == "running" {
			v.Running++
		}
		if svc := ct.Labels[docker.LabelService]; svc != "" {
			v.Services = append(v.Services, svc)
		}
		v.Containers = append(v.Containers, toView(ct, nil))
		if ct.Created < v.Created || v.Created == 0 {
			v.Created = ct.Created
		}
	}
	out := make([]*projectView, 0, len(by))
	for _, v := range by {
		if s.d.AppOf != nil {
			v.App = s.d.AppOf(v.Name)
		}
		switch {
		case v.Total == 0:
			v.Status = "stopped"
		case v.Running == v.Total:
			v.Status = "running"
		case v.Running == 0:
			v.Status = "stopped"
		default:
			v.Status = "partial"
		}
		if v.Files == nil {
			if f, err := docker.FindComposeFile(v.Dir); err == nil {
				v.Files = []string{f}
			} else {
				v.Files = []string{}
			}
		}
		if len(v.Files) == 0 || !exists(v.Files[0]) {
			v.Status = "missing"
		}
		if v.Services == nil {
			v.Services = []string{}
		}
		if v.Containers == nil {
			v.Containers = []containerView{}
		}
		sort.Strings(v.Services)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (s *Service) project(ctx context.Context, name string) (*projectView, error) {
	list, err := s.projects(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, api.NotFound("Compose 项目不存在：" + name)
}

func (p *projectView) compose() docker.Project {
	return docker.Project{Name: p.Name, Dir: p.Dir, Files: p.Files}
}

func (s *Service) composeList(c *gin.Context) {
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	list, err := s.projects(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	api.OK(c, list)
}

func (s *Service) composeGet(c *gin.Context) {
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	p, err := s.project(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	var content, env string
	if len(p.Files) > 0 {
		if b, err := os.ReadFile(p.Files[0]); err == nil {
			content = string(b)
		}
	}
	if b, err := os.ReadFile(filepath.Join(p.Dir, ".env")); err == nil {
		env = string(b)
	}
	api.OK(c, gin.H{"project": p, "content": content, "env": env})
}

// composeCreate 新建项目：粘贴 YAML（写入面板的 compose 目录或指定目录），或登记已有目录。然后 up -d。
func (s *Service) composeCreate(c *gin.Context) {
	var req struct {
		Name    string `json:"name"`
		Mode    string `json:"mode"` // yaml / dir
		Dir     string `json:"dir"`
		Content string `json:"content"`
		Env     string `json:"env"`
		Start   *bool  `json:"start"`
	}
	if !api.Bind(c, &req) {
		return
	}
	if !ProjectPattern.MatchString(req.Name) {
		api.Fail(c, api.BadRequest("项目名只能包含小写字母、数字、下划线和短横线，且以字母或数字开头"))
		return
	}
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	if p, _ := s.project(ctx, req.Name); p != nil {
		api.Fail(c, api.Conflict(api.CodeConflict, "已有同名的 Compose 项目"))
		return
	}
	row := store.ComposeProject{Name: req.Name}
	var file string
	switch req.Mode {
	case "dir":
		dir := filepath.Clean(strings.TrimSpace(req.Dir))
		if !filepath.IsAbs(dir) {
			api.Fail(c, api.BadRequest("请填写目录的绝对路径"))
			return
		}
		f, err := docker.FindComposeFile(dir)
		if err != nil {
			api.Fail(c, api.BadRequest("目录中没有 compose.yaml 或 docker-compose.yml"))
			return
		}
		row.Dir, row.File, file = dir, filepath.Base(f), f
	default:
		if strings.TrimSpace(req.Content) == "" {
			api.Fail(c, api.BadRequest("请填写 Compose 文件内容"))
			return
		}
		dir := strings.TrimSpace(req.Dir)
		if dir == "" {
			dir = filepath.Join(s.composeRoot(), req.Name)
			row.Managed = true
		}
		dir = filepath.Clean(dir)
		if !filepath.IsAbs(dir) {
			api.Fail(c, api.BadRequest("请填写目录的绝对路径"))
			return
		}
		if _, err := docker.FindComposeFile(dir); err == nil {
			api.Fail(c, api.BadRequest("该目录中已有 Compose 文件，请改用“已有目录”方式"))
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			api.Fail(c, err)
			return
		}
		file = filepath.Join(dir, "docker-compose.yml")
		if err := os.WriteFile(file, []byte(req.Content), 0o644); err != nil {
			api.Fail(c, err)
			return
		}
		if req.Env != "" {
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(req.Env), 0o600); err != nil {
				api.Fail(c, err)
				return
			}
		}
		if err := docker.ValidateCompose(ctx, req.Name, dir, file); err != nil {
			os.Remove(file)
			if row.Managed {
				os.RemoveAll(dir)
			}
			api.Fail(c, api.BadRequest(err.Error()))
			return
		}
		row.Dir, row.File = dir, "docker-compose.yml"
	}
	row.CreatedAt = time.Now()
	if err := s.d.Store.DB.Create(&row).Error; err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, req.Name+" @ "+row.Dir)
	if req.Start != nil && !*req.Start {
		api.OK(c, gin.H{"project": row.Name})
		return
	}
	p := docker.Project{Name: row.Name, Dir: row.Dir, Files: []string{file}}
	t := s.d.Tasks.Start("docker.compose-up", "部署 Compose 项目 "+row.Name, api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		return runCompose(ctx, t, p, "up", "-d", "--remove-orphans")
	})
	api.OK(c, gin.H{"project": row.Name, "task": t})
}

func runCompose(ctx context.Context, t *task.Task, p docker.Project, args ...string) error {
	out := t.Lines()
	defer out.Flush()
	t.Log("$ docker compose -p %s %s", p.Name, strings.Join(args, " "))
	return docker.Compose(ctx, p, out, args...)
}

// composeSave 保存 Compose 文件和 .env：先用 docker compose config 校验，原文件备份为 .bak；可选立即重新部署。
func (s *Service) composeSave(c *gin.Context) {
	var req struct {
		Content  string  `json:"content"`
		Env      *string `json:"env"`
		Redeploy bool    `json:"redeploy"`
	}
	if !api.Bind(c, &req) {
		return
	}
	ctx, cancel := timeout(c, 30*time.Second)
	defer cancel()
	p, err := s.project(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if len(p.Files) == 0 {
		api.Fail(c, api.BadRequest("找不到该项目的 Compose 文件"))
		return
	}
	file := p.Files[0]
	api.Target(c, file)
	envPath := filepath.Join(p.Dir, ".env")
	oldEnv, envErr := os.ReadFile(envPath)
	if req.Env != nil {
		if err := writeKeepMode(envPath, []byte(*req.Env), 0o600); err != nil {
			api.Fail(c, err)
			return
		}
	}
	tmp := filepath.Join(filepath.Dir(file), ".sshdesktop-check-"+filepath.Base(file))
	if err := os.WriteFile(tmp, []byte(req.Content), 0o644); err != nil {
		api.Fail(c, err)
		return
	}
	defer os.Remove(tmp)
	files := append([]string{tmp}, p.Files[1:]...)
	if _, err := docker.ComposeOutput(ctx, docker.Project{Name: p.Name, Dir: p.Dir, Files: files}, "config", "-q"); err != nil {
		if req.Env != nil { // 校验失败时还原 .env
			if envErr == nil {
				os.WriteFile(envPath, oldEnv, 0o600)
			} else {
				os.Remove(envPath)
			}
		}
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	if old, err := os.ReadFile(file); err == nil {
		os.WriteFile(file+".bak", old, 0o644)
	}
	if err := writeKeepMode(file, []byte(req.Content), 0o644); err != nil {
		api.Fail(c, err)
		return
	}
	if !req.Redeploy {
		api.OK(c, gin.H{})
		return
	}
	t := s.d.Tasks.Start("docker.compose-up", "重新部署 Compose 项目 "+p.Name, api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		return runCompose(ctx, t, p.compose(), "up", "-d", "--remove-orphans")
	})
	api.OK(c, gin.H{"task": t})
}

// writeKeepMode 写文件并保留原有权限（文件不存在时用 mode）。
func writeKeepMode(p string, b []byte, mode os.FileMode) error {
	if fi, err := os.Stat(p); err == nil {
		mode = fi.Mode().Perm()
	}
	return os.WriteFile(p, b, mode)
}

var composeActions = map[string][]string{
	"up":      {"up", "-d", "--remove-orphans"},
	"start":   {"start"},
	"stop":    {"stop"},
	"restart": {"restart"},
	"down":    {"down"},
	"pull":    {"pull"},
}

var actionNames = map[string]string{
	"up": "启动", "start": "启动", "stop": "停止", "restart": "重启", "down": "停止并删除容器", "pull": "更新",
}

func (s *Service) composeAction(c *gin.Context) {
	action := c.Param("action")
	args, ok := composeActions[action]
	if !ok {
		api.Fail(c, api.BadRequest("不支持的操作"))
		return
	}
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	p, err := s.project(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if p.Status == "missing" {
		api.Fail(c, api.BadRequest("找不到该项目的 Compose 文件（可能已被移动或删除），只能按容器管理"))
		return
	}
	api.Target(c, action+" "+p.Name)
	t := s.d.Tasks.Start("docker.compose-"+action, actionNames[action]+" Compose 项目 "+p.Name, api.User(c).UserID, "local",
		func(ctx context.Context, t *task.Task) error {
			if err := runCompose(ctx, t, p.compose(), args...); err != nil {
				return err
			}
			if action == "pull" { // 更新 = pull + up -d
				return runCompose(ctx, t, p.compose(), composeActions["up"]...)
			}
			return nil
		})
	api.OK(c, t)
}

// composeDelete 停止并删除项目的容器（down），取消登记；可选删除存储卷和面板创建的目录（二级确认）。
func (s *Service) composeDelete(c *gin.Context) {
	ctx, cancel := timeout(c, 20*time.Second)
	defer cancel()
	p, err := s.project(ctx, c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	if p.App != "" {
		api.Fail(c, api.BadRequest("这是应用商店安装的应用，请在应用商店中卸载"))
		return
	}
	volumes, files := c.Query("volumes") == "1", c.Query("files") == "1"
	if files && !p.Managed {
		api.Fail(c, api.BadRequest("只能删除面板创建的项目目录"))
		return
	}
	api.Target(c, p.Name)
	if (volumes || files) && !s.d.Confirm(c) {
		return
	}
	t := s.d.Tasks.Start("docker.compose-delete", "删除 Compose 项目 "+p.Name, api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		if p.Status != "missing" {
			args := []string{"down", "--remove-orphans"}
			if volumes {
				args = append(args, "-v")
			}
			if err := runCompose(ctx, t, p.compose(), args...); err != nil {
				return err
			}
		} else if p.Total > 0 {
			return errors.New("找不到 Compose 文件，无法执行 down；请先在容器列表中删除该项目的容器")
		}
		s.d.Store.DB.Delete(&store.ComposeProject{}, "name = ?", p.Name)
		if files {
			if !strings.HasPrefix(p.Dir, s.composeRoot()+string(os.PathSeparator)) {
				return fmt.Errorf("目录 %s 不在面板的 compose 目录中，未删除", p.Dir)
			}
			if err := os.RemoveAll(p.Dir); err != nil {
				return err
			}
			t.Log("已删除目录 %s", p.Dir)
		}
		return nil
	})
	api.OK(c, t)
}

func (s *Service) composeLogs(c *gin.Context) {
	p, err := s.project(c.Request.Context(), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	StreamCompose(c, p.compose(), c.DefaultQuery("tail", "300"))
}

// StreamCompose 经 WebSocket 推送 Compose 项目的日志（应用商店也用它）。
func StreamCompose(c *gin.Context, p docker.Project, tail string) {
	if _, err := strconv.Atoi(tail); err != nil {
		tail = "300"
	}
	streamLines(c, func(ctx context.Context, w io.Writer) error {
		return docker.Compose(ctx, p, w, "logs", "-f", "--no-color", "--tail", tail)
	})
}

// RegisterProject 登记一个 Compose 项目（应用商店安装应用时调用）。
func RegisterProject(db *gorm.DB, name, dir, file string) error {
	return db.Save(&store.ComposeProject{Name: name, Dir: dir, File: file, CreatedAt: time.Now()}).Error
}
