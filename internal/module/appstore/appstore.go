// Package appstore 是应用商店（仅本机）：从应用源（默认 1Panel 应用商店）读取应用定义，
// 填写表单后渲染 Compose 项目并启动；已安装的应用可启停、升级、修改参数、查看日志、卸载。
package appstore

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
)

const sourcesKey = "appstore.sources"

type Deps struct {
	Store   *store.Store
	Tasks   *task.Manager
	Docker  *docker.Client
	DataDir string
	Confirm func(c *gin.Context) bool
	Audit   func(module, action string) gin.HandlerFunc
}

type Service struct {
	d Deps

	mu       sync.RWMutex
	catalogs map[string]*Catalog
	errs     map[string]string
	syncing  bool
	syncMu   sync.Mutex
	// 正在安装、升级或卸载的应用（名称 → 任务 ID），避免同时操作
	busy map[string]string

	titles   map[string]string
	titlesAt time.Time
}

func New(d Deps) *Service {
	s := &Service{d: d, catalogs: map[string]*Catalog{}, errs: map[string]string{}, busy: map[string]string{}}
	// 上次面板退出时仍在进行的安装标记为失败
	d.Store.DB.Model(&store.AppInstalled{}).Where("status IN ?", []string{"installing", "upgrading"}).
		Updates(map[string]any{"status": "failed", "message": "面板重启，操作中断"})
	return s
}

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/appstore")
	g.GET("/apps", view, s.list)
	g.GET("/apps/:source/:key", view, s.detail)
	g.GET("/icon/:source/:key", view, s.icon)
	g.POST("/sync", manage, a("appstore", "sync"), s.sync)
	g.GET("/sources", view, s.sourceList)
	g.PUT("/sources", manage, a("appstore", "sources"), s.sourceSave)

	g.GET("/installed", view, s.installed)
	g.POST("/installed", manage, a("appstore", "install"), s.install)
	g.GET("/installed/:name", view, s.installedDetail)
	g.GET("/installed/:name/logs", view, s.logs)
	g.PUT("/installed/:name/params", manage, a("appstore", "params"), s.updateParams)
	g.POST("/installed/:name/upgrade", manage, a("appstore", "upgrade"), s.upgrade)
	g.POST("/installed/:name/:action", manage, a("appstore", "control"), s.control)
	g.DELETE("/installed/:name", manage, a("appstore", "uninstall"), s.uninstall)
}

func (s *Service) appsDir() string  { return filepath.Join(s.d.DataDir, "apps") }
func (s *Service) cacheDir() string { return filepath.Join(s.d.DataDir, "appstore") }

func (s *Service) sourceCache(id string) string { return filepath.Join(s.cacheDir(), id) }

// Sources 返回配置的应用源（内置的 1Panel 源始终在第一位）。
func (s *Service) Sources() []Source {
	list := defaultSources()
	var saved []Source
	s.d.Store.GetSetting(sourcesKey, &saved)
	for _, src := range saved {
		if src.ID == list[0].ID {
			list[0].Enabled = src.Enabled
			if src.URL != "" {
				list[0].URL = src.URL
			}
			continue
		}
		src.Builtin = false
		list = append(list, src)
	}
	return list
}

func (s *Service) source(id string) (Source, bool) {
	for _, src := range s.Sources() {
		if src.ID == id {
			return src, true
		}
	}
	return Source{}, false
}

// Load 从缓存加载所有应用源；从未同步过的 index 源在后台同步一次。
func (s *Service) Load() {
	needSync := false
	for _, src := range s.Sources() {
		if !src.Enabled {
			continue
		}
		cat, err := loadSource(src, s.sourceCache(src.ID))
		s.mu.Lock()
		if err != nil {
			delete(s.catalogs, src.ID)
			if os.IsNotExist(err) {
				needSync = true
			} else {
				s.errs[src.ID] = err.Error()
			}
		} else {
			s.catalogs[src.ID] = cat
			delete(s.errs, src.ID)
		}
		s.mu.Unlock()
	}
	if needSync {
		go s.syncAll(context.Background(), func(string, ...any) {})
	}
}

func (s *Service) syncAll(ctx context.Context, log func(string, ...any)) error {
	// 已有同步在进行（例如启动时的自动同步）时等它结束再同步一次，这样刚添加的源也会被同步
	if !s.syncMu.TryLock() {
		log("另一次同步正在进行，等它完成后再同步")
		s.syncMu.Lock()
	}
	defer s.syncMu.Unlock()
	s.mu.Lock()
	s.syncing = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.syncing = false
		s.mu.Unlock()
	}()
	var failed []string
	for _, src := range s.Sources() {
		if !src.Enabled {
			continue
		}
		err := syncSource(ctx, src, s.sourceCache(src.ID), log)
		var cat *Catalog
		if err == nil {
			cat, err = loadSource(src, s.sourceCache(src.ID))
		}
		s.mu.Lock()
		if err != nil {
			s.errs[src.ID] = err.Error()
			failed = append(failed, src.Name+"："+err.Error())
			slog.Warn("应用源同步失败", "source", src.ID, "err", err)
		} else {
			s.catalogs[src.ID] = cat
			delete(s.errs, src.ID)
			log("%s：%d 个应用", src.Name, len(cat.Apps))
		}
		s.mu.Unlock()
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s", strings.Join(failed, "；"))
	}
	return nil
}

func (s *Service) app(source, key string) (*App, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cat := s.catalogs[source]
	if cat == nil {
		return nil, false
	}
	for _, a := range cat.Apps {
		if a.Key == key {
			return a, true
		}
	}
	return nil, false
}

// arch 返回 1Panel 应用定义使用的架构名。
func arch() string { return runtime.GOARCH }

type appView struct {
	*App
	Icon      string `json:"icon"`
	Installed int    `json:"installed"`
}

func (s *Service) installedCount() map[string]int {
	var rows []store.AppInstalled
	s.d.Store.DB.Select("source", "key").Find(&rows)
	m := map[string]int{}
	for _, r := range rows {
		m[r.Source+"/"+r.Key]++
	}
	return m
}

func iconURL(source, key string) string {
	return "/api/v1/nodes/local/appstore/icon/" + source + "/" + key
}

func (s *Service) list(c *gin.Context) {
	counts := s.installedCount()
	s.mu.RLock()
	defer s.mu.RUnlock()
	apps := []appView{}
	tags := map[string]Tag{}
	for _, src := range s.Sources() {
		cat := s.catalogs[src.ID]
		if !src.Enabled || cat == nil {
			continue
		}
		for _, t := range cat.Tags {
			if _, ok := tags[t.Key]; !ok {
				tags[t.Key] = t
			}
		}
		for _, a := range cat.Apps {
			v := appView{App: a, Icon: iconURL(a.Source, a.Key), Installed: counts[a.Source+"/"+a.Key]}
			apps = append(apps, v)
		}
	}
	sort.SliceStable(apps, func(i, j int) bool {
		if apps[i].Recommend != apps[j].Recommend {
			return apps[i].Recommend > apps[j].Recommend
		}
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
	tagList := make([]Tag, 0, len(tags))
	for _, t := range tags {
		tagList = append(tagList, t)
	}
	sort.Slice(tagList, func(i, j int) bool { return tagList[i].Sort < tagList[j].Sort })
	errs := map[string]string{}
	for k, v := range s.errs {
		errs[k] = v
	}
	api.OK(c, gin.H{"apps": apps, "tags": tagList, "errors": errs, "syncing": s.syncing, "arch": arch()})
}

func (s *Service) detail(c *gin.Context) {
	a, ok := s.app(c.Param("source"), c.Param("key"))
	if !ok {
		api.Fail(c, api.NotFound("应用不存在，请先同步应用源"))
		return
	}
	ver := c.Query("version")
	if ver == "" && len(a.versions) > 0 {
		ver = a.versions[0].Name
	}
	v, ok := a.version(ver)
	if !ok {
		api.Fail(c, api.NotFound("版本不存在"))
		return
	}
	fields := withDefaults(v.Fields)
	api.OK(c, gin.H{
		"app":    appView{App: a, Icon: iconURL(a.Source, a.Key), Installed: s.installedCount()[a.Source+"/"+a.Key]},
		"readme": a.readme, "version": v.Name, "fields": fields,
		"suggestName": s.suggestName(a.Key), "arch": arch(),
	})
}

// suggestName 给新安装的应用起一个不重复的名称。
func (s *Service) suggestName(key string) string {
	base := strings.ToLower(regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(strings.ToLower(key), "-"))
	base = strings.Trim(base, "-_")
	if base == "" {
		base = "app"
	}
	name := base
	for i := 2; ; i++ {
		var n int64
		s.d.Store.DB.Model(&store.AppInstalled{}).Where("name = ?", name).Count(&n)
		if n == 0 && !exists(filepath.Join(s.appsDir(), key, name)) {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// icon 返回应用图标：目录源直接读文件，index 源下载后缓存。
func (s *Service) icon(c *gin.Context) {
	a, ok := s.app(c.Param("source"), c.Param("key"))
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	p := a.iconPath
	if p == "" && a.iconURL != "" {
		h := sha1.Sum([]byte(a.iconURL))
		p = filepath.Join(s.sourceCache(a.Source), "icons", hex.EncodeToString(h[:8]))
		if !exists(p) {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
			defer cancel()
			if err := download(ctx, a.iconURL, p, 2<<20); err != nil {
				c.Status(http.StatusNotFound)
				return
			}
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	ct := http.DetectContentType(b)
	if strings.HasSuffix(p, ".svg") || strings.Contains(string(b[:min(len(b), 256)]), "<svg") {
		// SVG 可能带脚本，按图片返回且禁止执行
		ct = "image/svg+xml"
		c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	}
	if !strings.HasPrefix(ct, "image/") {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "private, max-age=86400")
	c.Data(http.StatusOK, ct, b)
}

func (s *Service) sync(c *gin.Context) {
	t := s.d.Tasks.Start("appstore.sync", "同步应用源", api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		return s.syncAll(ctx, t.Log)
	})
	api.OK(c, t)
}

type sourceView struct {
	Source
	Apps   int    `json:"apps"`
	Error  string `json:"error,omitempty"`
	Synced int64  `json:"synced,omitempty"`
}

func (s *Service) sourceList(c *gin.Context) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []sourceView{}
	for _, src := range s.Sources() {
		v := sourceView{Source: src, Error: s.errs[src.ID]}
		if cat := s.catalogs[src.ID]; cat != nil {
			v.Apps = len(cat.Apps)
		}
		probe := filepath.Join(s.sourceCache(src.ID), "index.json")
		if src.Type == "git" {
			probe = filepath.Join(s.sourceCache(src.ID), "repo")
		}
		if fi, err := os.Stat(probe); err == nil && src.Type != "dir" {
			v.Synced = fi.ModTime().Unix()
		}
		out = append(out, v)
	}
	api.OK(c, out)
}

var sourceID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func (s *Service) sourceSave(c *gin.Context) {
	var req []Source
	if !api.Bind(c, &req) {
		return
	}
	seen := map[string]bool{}
	for i := range req {
		src := &req[i]
		src.Name, src.URL = strings.TrimSpace(src.Name), strings.TrimSpace(src.URL)
		if src.ID == "" {
			src.ID = fmt.Sprintf("src-%d", time.Now().UnixNano()%1e9+int64(i))
		}
		if !sourceID.MatchString(src.ID) || seen[src.ID] {
			api.Fail(c, api.BadRequest("应用源 ID 无效或重复"))
			return
		}
		seen[src.ID] = true
		if src.ID == "1panel" {
			if src.URL != "" && !strings.HasPrefix(src.URL, "https://") && !strings.HasPrefix(src.URL, "http://") {
				api.Fail(c, api.BadRequest("索引地址必须以 http:// 或 https:// 开头"))
				return
			}
			continue
		}
		if src.Name == "" || src.URL == "" {
			api.Fail(c, api.BadRequest("请填写应用源的名称和地址"))
			return
		}
		switch src.Type {
		case "index", "git":
			if !strings.HasPrefix(src.URL, "https://") && !strings.HasPrefix(src.URL, "http://") {
				api.Fail(c, api.BadRequest("地址必须以 http:// 或 https:// 开头"))
				return
			}
		case "dir":
			if !filepath.IsAbs(src.URL) {
				api.Fail(c, api.BadRequest("本地目录必须是绝对路径"))
				return
			}
		default:
			api.Fail(c, api.BadRequest("应用源类型只能是 index、git 或 dir"))
			return
		}
	}
	api.Target(c, fmt.Sprintf("%d 个应用源", len(req)))
	if err := s.d.Store.SetSetting(sourcesKey, req); err != nil {
		api.Fail(c, err)
		return
	}
	s.mu.Lock()
	for id := range s.catalogs {
		if !seen[id] {
			delete(s.catalogs, id)
		}
	}
	s.mu.Unlock()
	s.Load()
	api.OK(c, s.Sources())
}

// AppOf 返回 Compose 项目对应的已安装应用名称（供 Docker 模块标注），结果缓存 5 秒。
func (s *Service) AppOf(project string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.titles == nil || time.Since(s.titlesAt) > 5*time.Second {
		var rows []store.AppInstalled
		s.d.Store.DB.Select("name", "title").Find(&rows)
		s.titles = map[string]string{}
		for _, r := range rows {
			s.titles[r.Name] = r.Title
		}
		s.titlesAt = time.Now()
	}
	return s.titles[project]
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
