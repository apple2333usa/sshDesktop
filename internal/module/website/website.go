// Package website 是网站管理（仅本机）：接管宿主机已有的 Nginx / OpenResty，或安装面板管理的 OpenResty 容器；
// 面板新建的站点（反向代理、静态网站）写入独立目录，保存前用 nginx -t 校验，失败自动回滚。
package website

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/nginx"
	"github.com/apple2333usa/sshdesktop/internal/pending"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
)

const runtimeKey = "website.runtime"

// WWWRoot 是面板网站的文件目录：sites/<名称>/index（静态网站默认目录）、logs/（日志）、acme/（证书验证文件）。
// 不放在数据目录下：数据目录只有 root 能进入，Nginx 的工作进程读不到。
const WWWRoot = "/var/www/sshdesktop"

type Deps struct {
	Store   *store.Store
	Tasks   *task.Manager
	Docker  *docker.Client
	DataDir string
	Confirm func(c *gin.Context) bool
	Audit   func(module, action string) gin.HandlerFunc
	// CertFiles 返回证书的文件路径；ImportCert 把服务器上已有的证书文件登记为证书（接管已有站点时使用）
	CertFiles  func(id uint) (cert, key string, err error)
	ImportCert func(cert, key string) (uint, error)
	// ImportPEM 导入证书内容（恢复网站备份时使用），已有相同证书时返回它
	ImportPEM func(cert, key []byte, remark string) (uint, error)
}

type Service struct {
	d    Deps
	www  string
	ipv6 bool
	mu   sync.Mutex // 修改 Nginx 配置时串行

	// 测试中替换：宿主机 Nginx 的查找
	findHost func() string
}

func New(d Deps) *Service {
	return &Service{d: d, www: WWWRoot, findHost: nginx.FindHost, ipv6: HasIPv6()}
}

// SetCerts 设置证书模块提供的函数（两个模块互相依赖，创建后再设置）。
func (s *Service) SetCerts(files func(id uint) (string, string, error), importCert func(cert, key string) (uint, error),
	importPEM func(cert, key []byte, remark string) (uint, error)) {
	s.d.CertFiles, s.d.ImportCert, s.d.ImportPEM = files, importCert, importPEM
}

// SetPaths 仅用于测试：网站文件目录和宿主机 Nginx。
func (s *Service) SetPaths(www string, findHost func() string) {
	s.www, s.findHost = www, findHost
}

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/websites")
	g.GET("/runtime", view, s.runtimeStatus)
	g.POST("/runtime/setup", manage, a("website", "runtime-setup"), s.setup)
	g.POST("/runtime/action/:action", manage, a("website", "runtime-control"), s.runtimeControl)
	g.GET("/runtime/main-conf", view, s.mainConf)
	g.GET("/sites", view, s.list)
	g.POST("/sites", manage, a("website", "create"), s.save)
	g.POST("/preview", view, s.preview)
	g.GET("/sites/:id", view, s.get)
	g.PUT("/sites/:id", manage, a("website", "update"), s.save)
	g.DELETE("/sites/:id", manage, a("website", "delete"), s.delete)
	g.POST("/sites/:id/enable", manage, a("website", "enable"), s.enable)
	g.GET("/sites/:id/log", view, s.log)
	g.POST("/sites/:id/untakeover", manage, a("website", "untakeover"), s.untakeover)
	g.GET("/existing", view, s.existing)
	g.GET("/existing/file", view, s.existingFile)
	g.PUT("/existing/file", manage, a("website", "existing-file"), s.existingSave)
	g.POST("/existing/takeover", manage, a("website", "takeover"), s.takeover)
}

/* ---------- 运行环境 ---------- */

type runtimeSetting struct {
	Mode string `json:"mode"`
}

func (s *Service) mode() string {
	var rs runtimeSetting
	s.d.Store.GetSetting(runtimeKey, &rs)
	return rs.Mode
}

func (s *Service) containerRuntime() nginx.Runtime {
	dir := filepath.Join(s.d.DataDir, "openresty")
	return nginx.Runtime{
		Mode: nginx.ModeContainer, Flavor: "openresty", Version: openrestyVersion,
		Conf: filepath.Join(dir, "conf", "nginx.conf"), SiteDir: filepath.Join(dir, "conf.d"),
		LogDir: filepath.Join(s.www, "logs"), Container: nginx.ContainerName, ComposeDir: dir,
	}
}

// ErrNoRuntime 表示还没有选择 Web 服务器。
var ErrNoRuntime = api.Err(409, api.CodeWebServerMissing, "还没有可用的 Web 服务器，请先在“网站”中接管已有的 Nginx 或安装 OpenResty")

// Runtime 返回当前使用的 Web 服务器。
func (s *Service) Runtime(ctx context.Context) (nginx.Runtime, error) {
	switch s.mode() {
	case nginx.ModeContainer:
		return s.containerRuntime(), nil
	case nginx.ModeHost:
		bin := s.findHost()
		if bin == "" {
			return nginx.Runtime{}, api.Err(409, api.CodeWebServerMissing, "已接管的 Nginx 不见了（找不到 nginx 或 openresty 命令）")
		}
		rt, err := nginx.DetectHost(ctx, bin)
		rt.LogDir = filepath.Join(s.www, "logs")
		return rt, err
	}
	return nginx.Runtime{}, ErrNoRuntime
}

// AcmeRoot 是 HTTP-01 验证文件目录（面板站点的 /.well-known/acme-challenge/ 指向这里）。
func (s *Service) AcmeRoot() string { return filepath.Join(s.www, "acme") }

// SiteRoot 是站点的默认目录。
func (s *Service) SiteRoot(name string) string { return filepath.Join(s.www, "sites", name, "index") }

func (s *Service) ensureDirs() error {
	for _, d := range []string{filepath.Join(s.www, "sites"), filepath.Join(s.www, "logs"), filepath.Join(s.AcmeRoot(), ".well-known", "acme-challenge")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

type runtimeView struct {
	Mode       string         `json:"mode"`
	HostBinary string         `json:"hostBinary"`
	Runtime    *nginx.Runtime `json:"runtime,omitempty"`
	Running    bool           `json:"running"`
	Error      string         `json:"error,omitempty"`
	Docker     bool           `json:"docker"`
	// Ports 是 80、443 端口的占用情况（端口 → 占用者），未被占用时不出现
	Ports map[string]string `json:"ports"`
	Sites int               `json:"sites"`
	// Included 表示面板站点目录已包含进生效的配置（host 模式）
	Included bool `json:"included"`
}

// Status 返回 Web 服务器的状态，供界面和环境体检使用。
func (s *Service) Status(ctx context.Context) runtimeView {
	v := runtimeView{Mode: s.mode(), HostBinary: s.findHost(), Ports: map[string]string{}}
	v.Docker = s.d.Docker.Ping(ctx) == nil
	var n int64
	s.d.Store.DB.Model(&store.Website{}).Count(&n)
	v.Sites = int(n)
	for _, p := range []int{80, 443} {
		if who := PortOwner(p); who != "" {
			v.Ports[strconv.Itoa(p)] = who
		}
	}
	if v.Mode == "" {
		return v
	}
	rt, err := s.Runtime(ctx)
	if err != nil {
		v.Error = err.Error()
		return v
	}
	v.Runtime = &rt
	v.Running = rt.Running(ctx)
	if rt.Mode == nginx.ModeHost {
		if files, err := rt.Dump(ctx); err == nil {
			v.Included = nginx.Included(files, s.stubPath(rt)) || includesSiteDir(files, rt.SiteDir)
		} else {
			v.Error = err.Error()
		}
	} else {
		v.Included = true
	}
	return v
}

func (s *Service) runtimeStatus(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	api.OK(c, s.Status(ctx))
}

// PortOwner 返回占用 TCP 端口的进程（ss -ltnp），没有被占用时为空。
func PortOwner(port int) string {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err == nil {
		ln.Close()
		return ""
	}
	if who := listenerProcess(port); who != "" {
		return who
	}
	return "未知程序"
}

func (s *Service) stubPath(rt nginx.Runtime) string {
	return filepath.Join(filepath.Dir(rt.Conf), "conf.d", "sshdesktop.conf")
}

func includeLine(dir string) string { return "include " + nginx.Quote(dir+"/*.conf") + ";" }

// includesSiteDir 表示生效配置中有包含面板站点目录的 include。
func includesSiteDir(files []nginx.File, dir string) bool {
	want := dir + "/*.conf"
	for _, f := range files {
		ds, err := nginx.Parse(f.Content)
		if err != nil {
			continue
		}
		found := false
		nginx.Walk(ds, func(d *nginx.Directive, _ []*nginx.Directive) {
			if d.Name == "include" && len(d.Args) == 1 && d.Args[0] == want {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

func (s *Service) takeoverDir() string {
	return filepath.Join(s.d.DataDir, "takeover-backup", "nginx", time.Now().Format("20060102-150405"))
}

// backupFile 把要修改的已有文件原样复制到 takeover-backup，返回备份位置。
func (s *Service) backupFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(s.takeoverDir(), path)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	return dst, os.WriteFile(dst, b, 0o600)
}

// setupHost 接管宿主机 Nginx：让主配置包含面板站点目录。优先在 conf.d 放一个 include 文件，
// 主配置没有包含 conf.d/*.conf 时才在 http 块末尾加一行 include（修改前备份）。
func (s *Service) setupHost(ctx context.Context, log func(string, ...any)) error {
	bin := s.findHost()
	if bin == "" {
		return errors.New("没有找到 nginx 或 openresty")
	}
	rt, err := nginx.DetectHost(ctx, bin)
	if err != nil {
		return err
	}
	log("找到 %s %s，主配置 %s", rt.Flavor, rt.Version, rt.Conf)
	if err := rt.Test(ctx); err != nil {
		return fmt.Errorf("现有配置没有通过校验，请先修正：%w", err)
	}
	if err := os.MkdirAll(rt.SiteDir, 0o755); err != nil {
		return err
	}
	if err := s.ensureDirs(); err != nil {
		return err
	}
	files, err := rt.Dump(ctx)
	if err != nil {
		return err
	}
	if includesSiteDir(files, rt.SiteDir) {
		log("主配置已包含 %s", rt.SiteDir)
		return nil
	}
	stub := s.stubPath(rt)
	if err := pending.WriteFile(stub, []byte("# 由 sshDesktop 添加：包含面板管理的站点\n"+includeLine(rt.SiteDir)+"\n"), 0o644); err != nil {
		return err
	}
	if files, err = rt.Dump(ctx); err == nil && nginx.Included(files, stub) {
		log("已添加 %s", stub)
		return nil
	}
	os.Remove(stub)
	main, err := os.ReadFile(rt.Conf)
	if err != nil {
		return err
	}
	next, err := nginx.InsertInclude(string(main), includeLine(rt.SiteDir))
	if err != nil {
		return err
	}
	bak, err := s.backupFile(rt.Conf)
	if err != nil {
		return err
	}
	log("主配置没有包含 conf.d/*.conf，在 http 块末尾添加 include（原文件备份在 %s）", bak)
	if err := pending.WriteFile(rt.Conf, []byte(next), 0o644); err != nil {
		return err
	}
	if err := rt.Test(ctx); err != nil {
		pending.WriteFile(rt.Conf, main, 0o644)
		return err
	}
	return nil
}

func (s *Service) setup(c *gin.Context) {
	var req struct {
		Mode string `json:"mode"`
	}
	if !api.Bind(c, &req) {
		return
	}
	if req.Mode != nginx.ModeHost && req.Mode != nginx.ModeContainer {
		api.Fail(c, api.BadRequest("mode 只能是 host 或 container"))
		return
	}
	var n int64
	s.d.Store.DB.Model(&store.Website{}).Count(&n)
	if cur := s.mode(); cur != "" && cur != req.Mode && n > 0 {
		api.Fail(c, api.BadRequest("已有网站使用当前的 Web 服务器，不能切换"))
		return
	}
	api.Target(c, req.Mode)
	title := "接管 Nginx"
	if req.Mode == nginx.ModeContainer {
		title = "安装 OpenResty"
	}
	t := s.d.Tasks.Start("website.setup", title, api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		var err error
		if req.Mode == nginx.ModeHost {
			err = s.setupHost(ctx, t.Log)
		} else {
			err = s.setupContainer(ctx, t)
		}
		if err != nil {
			return err
		}
		if err := s.d.Store.SetSetting(runtimeKey, runtimeSetting{Mode: req.Mode}); err != nil {
			return err
		}
		if err := s.rewriteAll(ctx); err != nil {
			return err
		}
		t.Log("完成")
		return nil
	})
	api.OK(c, t)
}

func (s *Service) runtimeControl(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	rt, err := s.Runtime(ctx)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, c.Param("action"))
	switch c.Param("action") {
	case "reload", "start":
		if err = rt.Test(ctx); err == nil {
			err = rt.Reload(ctx)
		}
	case "stop", "restart":
		err = controlRuntime(ctx, rt, c.Param("action"))
	case "test":
		err = rt.Test(ctx)
	default:
		err = api.BadRequest("不支持的操作")
	}
	if err != nil {
		api.Fail(c, asAPI(err))
		return
	}
	api.OK(c, nil)
}

func (s *Service) mainConf(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	rt, err := s.Runtime(ctx)
	if err != nil {
		api.Fail(c, err)
		return
	}
	b, err := os.ReadFile(rt.Conf)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, gin.H{"path": rt.Conf, "content": string(b)})
}

func asAPI(err error) error {
	var te *nginx.TestError
	if errors.As(err, &te) {
		return api.Err(400, api.CodeNginxTest, te.Error())
	}
	return err
}

/* ---------- 写入配置 ---------- */

func confFile(rt nginx.Runtime, name string, enabled bool) string {
	p := filepath.Join(rt.SiteDir, name+".conf")
	if !enabled {
		p += ".off"
	}
	return p
}

func (s *Service) paths(rt nginx.Runtime, w *store.Website) (Paths, error) {
	p := Paths{AcmeRoot: s.AcmeRoot(), LogDir: rt.LogDir, Version: rt.Version, IPv6: s.ipv6}
	if w.HTTPS {
		cert, key, err := s.d.CertFiles(w.CertID)
		if err != nil {
			return p, err
		}
		p.Cert, p.Key = cert, key
	}
	return p, nil
}

// commit 把一组文件改动写入（内容为 nil 表示删除），校验并重新加载；失败时还原。
func (s *Service) commit(ctx context.Context, rt nginx.Runtime, changes map[string][]byte) error {
	paths := make([]string, 0, len(changes))
	for p := range changes {
		paths = append(paths, p)
	}
	snap, err := pending.Snap(paths...)
	if err != nil {
		return err
	}
	for p, b := range changes {
		if b == nil {
			if err = os.Remove(p); errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		} else {
			err = pending.WriteFile(p, b, 0o644)
		}
		if err != nil {
			pending.Restore(snap)
			return err
		}
	}
	if err := rt.Test(ctx); err != nil {
		pending.Restore(snap)
		return err
	}
	if err := rt.Reload(ctx); err != nil {
		pending.Restore(snap)
		rt.Reload(ctx)
		return err
	}
	return nil
}

// siteChanges 返回写入站点 w（替换 old）所需的文件改动。
func (s *Service) siteChanges(rt nginx.Runtime, w *store.Website, old *store.Website) (map[string][]byte, error) {
	ch := map[string][]byte{}
	if old != nil {
		ch[confFile(rt, old.Name, true)] = nil
		ch[confFile(rt, old.Name, false)] = nil
	}
	if w != nil {
		p, err := s.paths(rt, w)
		if err != nil {
			return nil, err
		}
		ch[confFile(rt, w.Name, !w.Enabled)] = nil
		ch[confFile(rt, w.Name, w.Enabled)] = []byte(Render(w, p))
	}
	return ch, nil
}

// rewriteAll 按数据库重写所有站点（切换或重新安装 Web 服务器后）。
func (s *Service) rewriteAll(ctx context.Context) error {
	rt, err := s.Runtime(ctx)
	if err != nil {
		return err
	}
	var sites []store.Website
	s.d.Store.DB.Find(&sites)
	ch := map[string][]byte{}
	for i := range sites {
		c, err := s.siteChanges(rt, &sites[i], nil)
		if err != nil {
			return err
		}
		for k, v := range c {
			ch[k] = v
		}
	}
	if len(ch) == 0 {
		if rt.Running(ctx) {
			return nil
		}
		if err := rt.Test(ctx); err != nil {
			return err
		}
		return rt.Reload(ctx)
	}
	return s.commit(ctx, rt, ch)
}

// Reapply 重写使用证书 certID 的站点并重新加载 Nginx（证书续期或更换后调用）。
func (s *Service) Reapply(ctx context.Context, certID uint) error {
	var sites []store.Website
	s.d.Store.DB.Where("cert_id = ? AND https = ?", certID, true).Find(&sites)
	if len(sites) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, err := s.Runtime(ctx)
	if err != nil {
		return err
	}
	ch := map[string][]byte{}
	for i := range sites {
		c, err := s.siteChanges(rt, &sites[i], nil)
		if err != nil {
			return err
		}
		for k, v := range c {
			ch[k] = v
		}
	}
	return s.commit(ctx, rt, ch)
}

// CertInUse 返回使用证书的站点名。
func (s *Service) CertInUse(certID uint) []string {
	var names []string
	s.d.Store.DB.Model(&store.Website{}).Where("cert_id = ? AND https = ?", certID, true).Pluck("name", &names)
	return names
}

// Domains 返回所有面板站点的域名（证书 HTTP-01 验证可以直接使用这些站点）。
func (s *Service) Domains() map[string]bool {
	var sites []store.Website
	s.d.Store.DB.Where("enabled = ?", true).Find(&sites)
	out := map[string]bool{}
	for _, w := range sites {
		if w.HTTPPort != 80 {
			continue
		}
		for _, d := range SplitDomains(w.Domains) {
			out[d] = true
		}
	}
	return out
}

// PrepareChallenge 为不属于面板站点的域名临时写一个只提供验证文件的 80 端口站点，返回清理函数。
// 没有可用的 Web 服务器时返回 ErrNoRuntime，由证书模块改用内置的验证服务器。
func (s *Service) PrepareChallenge(ctx context.Context, domains []string) (func(), error) {
	rt, err := s.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	if !rt.Running(ctx) {
		return nil, ErrNoRuntime
	}
	if err := s.ensureDirs(); err != nil {
		return nil, err
	}
	have := s.Domains()
	var need []string
	for _, d := range domains {
		if !have[d] {
			need = append(need, d)
		}
	}
	if len(need) == 0 {
		s.waitChallenge(ctx, domains)
		return func() {}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := filepath.Join(rt.SiteDir, "zz-acme-challenge.conf")
	v6 := ""
	if s.ipv6 {
		v6 = "    listen [::]:80;\n"
	}
	conf := fmt.Sprintf("# 由 sshDesktop 临时添加：证书验证，完成后自动删除\nserver {\n    listen 80;\n%s    server_name %s;\n    location ^~ /.well-known/acme-challenge/ {\n        root %s;\n        default_type text/plain;\n    }\n    location / {\n        return 404;\n    }\n}\n",
		v6, strings.Join(need, " "), nginx.Quote(s.AcmeRoot()))
	if err := s.commit(ctx, rt, map[string][]byte{p: []byte(conf)}); err != nil {
		return nil, err
	}
	s.waitChallenge(ctx, domains)
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.commit(context.Background(), rt, map[string][]byte{p: nil})
	}, nil
}

// waitChallenge 等到本机 80 端口能为这些域名提供验证文件（最多 10 秒）。
// nginx -s reload 是异步的，刚改完配置就让 CA 来验证，请求可能还落在旧的工作进程上。
func (s *Service) waitChallenge(ctx context.Context, domains []string) {
	name := "sshdesktop-probe-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	dir := filepath.Join(s.AcmeRoot(), ".well-known", "acme-challenge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte(name), 0o644); err != nil {
		return
	}
	defer os.Remove(file)
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	deadline := time.Now().Add(10 * time.Second)
	for _, d := range domains {
		for time.Now().Before(deadline) && ctx.Err() == nil {
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1/.well-known/acme-challenge/"+name, nil)
			req.Host = d
			if resp, err := client.Do(req); err == nil {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
				resp.Body.Close()
				if resp.StatusCode == 200 && string(b) == name {
					break
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

/* ---------- 站点 ---------- */

type siteView struct {
	store.Website
	File     string     `json:"file"`
	CertName string     `json:"certName,omitempty"`
	CertEnd  *time.Time `json:"certEnd,omitempty"`
}

func (s *Service) view(rt *nginx.Runtime, w store.Website) siteView {
	v := siteView{Website: w}
	if rt != nil {
		v.File = confFile(*rt, w.Name, w.Enabled)
	}
	if w.CertID != 0 {
		var cert store.Certificate
		if s.d.Store.DB.First(&cert, w.CertID).Error == nil {
			v.CertName, v.CertEnd = cert.Domains, cert.NotAfter
		}
	}
	return v
}

func (s *Service) list(c *gin.Context) {
	var sites []store.Website
	s.d.Store.DB.Order("id").Find(&sites)
	var rtp *nginx.Runtime
	if rt, err := s.Runtime(c.Request.Context()); err == nil {
		rtp = &rt
	}
	out := make([]siteView, 0, len(sites))
	for _, w := range sites {
		out = append(out, s.view(rtp, w))
	}
	api.OK(c, out)
}

func (s *Service) site(c *gin.Context) (store.Website, bool) {
	var w store.Website
	if err := s.d.Store.DB.First(&w, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("网站不存在"))
		return w, false
	}
	api.Target(c, w.Name)
	return w, true
}

func (s *Service) get(c *gin.Context) {
	w, ok := s.site(c)
	if !ok {
		return
	}
	v := gin.H{"site": s.view(nil, w)}
	if rt, err := s.Runtime(c.Request.Context()); err == nil {
		if p, err := s.paths(rt, &w); err == nil {
			v["conf"] = Render(&w, p)
		}
		v["file"] = confFile(rt, w.Name, w.Enabled)
	}
	api.OK(c, v)
}

func (s *Service) preview(c *gin.Context) {
	var w store.Website
	if !api.Bind(c, &w) {
		return
	}
	if w.Type == "static" && strings.TrimSpace(w.Root) == "" {
		w.Root = s.SiteRoot(SafeName(firstDomain(w.Domains)))
	}
	if err := Normalize(&w); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	rt, err := s.Runtime(c.Request.Context())
	if err != nil {
		rt = nginx.Runtime{LogDir: filepath.Join(s.www, "logs"), Version: openrestyVersion}
	}
	p, err := s.paths(rt, &w)
	if err != nil {
		p.Cert, p.Key = "<证书>", "<私钥>"
	}
	api.OK(c, gin.H{"conf": Render(&w, p)})
}

func firstDomain(s string) string {
	if ds := SplitDomains(s); len(ds) > 0 {
		return ds[0]
	}
	return ""
}

// conflict 检查其他面板站点是否在同一端口使用了相同域名。
func (s *Service) conflict(w *store.Website) error {
	var sites []store.Website
	s.d.Store.DB.Where("id <> ? AND enabled = ?", w.ID, true).Find(&sites)
	mine := map[string]bool{}
	for _, d := range SplitDomains(w.Domains) {
		mine[d] = true
	}
	for _, o := range sites {
		if o.Name == w.Name {
			return fmt.Errorf("名称 %s 已被使用", w.Name)
		}
		ports := map[int]bool{o.HTTPPort: true}
		if o.HTTPS {
			ports[o.HTTPSPort] = true
		}
		if !ports[w.HTTPPort] && !(w.HTTPS && ports[w.HTTPSPort]) {
			continue
		}
		for _, d := range SplitDomains(o.Domains) {
			if mine[d] {
				return fmt.Errorf("域名 %s 已被网站 %s 使用", d, o.Name)
			}
		}
	}
	return nil
}

// Create 新建站点（应用商店创建反向代理时也用它）。
func (s *Service) Create(ctx context.Context, w *store.Website) error {
	return s.write(ctx, w, nil)
}

// RestoreSite 恢复网站备份中的站点：导入证书，按名称覆盖已有站点或新建，然后写入配置。
func (s *Service) RestoreSite(ctx context.Context, w store.Website, cert, key []byte) (string, error) {
	warn := ""
	if w.HTTPS {
		w.CertID = 0
		if len(cert) > 0 && s.d.ImportPEM != nil {
			id, err := s.d.ImportPEM(cert, key, "恢复网站 "+w.Name+" 时导入")
			if err != nil {
				return "", fmt.Errorf("导入证书失败：%w", err)
			}
			w.CertID = id
		} else {
			w.HTTPS, w.ForceHTTPS, w.HSTS = false, false, false
			warn = "备份中没有证书，已关闭 HTTPS，请重新选择证书"
		}
	}
	var cur store.Website
	var old *store.Website
	if s.d.Store.DB.First(&cur, "name = ?", w.Name).Error == nil {
		w.ID, w.CreatedAt, w.TakeoverFrom, w.TakeoverBak = cur.ID, cur.CreatedAt, cur.TakeoverFrom, cur.TakeoverBak
		old = &cur
	} else {
		// 原服务器上接管前的备份文件不在这台服务器上
		w.ID, w.TakeoverBak = 0, ""
	}
	if err := s.write(ctx, &w, old); err != nil {
		return "", err
	}
	return warn, nil
}

func (s *Service) write(ctx context.Context, w *store.Website, old *store.Website) error {
	if w.Type == "static" && strings.TrimSpace(w.Root) == "" && strings.TrimSpace(w.Raw) == "" {
		name := w.Name
		if name == "" {
			name = SafeName(firstDomain(w.Domains))
		}
		w.Root = s.SiteRoot(name)
	}
	if err := Normalize(w); err != nil {
		return api.BadRequest(err.Error())
	}
	if err := s.conflict(w); err != nil {
		return api.BadRequest(err.Error())
	}
	if old == nil {
		var n int64
		s.d.Store.DB.Model(&store.Website{}).Where("name = ?", w.Name).Count(&n)
		if n > 0 {
			return api.BadRequest("名称 " + w.Name + " 已被使用")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rt, err := s.Runtime(ctx)
	if err != nil {
		return err
	}
	if rt.Mode == nginx.ModeContainer && w.Type == "static" && w.Raw == "" && !strings.HasPrefix(w.Root+"/", "/var/www/") {
		return api.BadRequest("使用 OpenResty 容器时，网站目录必须在 /var/www 下（容器只挂载了这个目录）")
	}
	if err := s.ensureDirs(); err != nil {
		return err
	}
	createdRoot := false
	if w.Type == "static" && w.Raw == "" {
		if _, err := os.Stat(w.Root); os.IsNotExist(err) {
			if err := os.MkdirAll(w.Root, 0o755); err != nil {
				return err
			}
			createdRoot = true
			if w.Root == s.SiteRoot(w.Name) {
				os.WriteFile(filepath.Join(w.Root, "index.html"), []byte(defaultIndex(w)), 0o644)
			}
		}
	}
	ch, err := s.siteChanges(rt, w, old)
	if err != nil {
		return err
	}
	if err := s.commit(ctx, rt, ch); err != nil {
		if createdRoot && w.Root == s.SiteRoot(w.Name) {
			os.RemoveAll(filepath.Dir(w.Root))
		}
		return asAPI(err)
	}
	if err := s.d.Store.DB.Save(w).Error; err != nil {
		return err
	}
	return nil
}

func defaultIndex(w *store.Website) string {
	return "<!doctype html>\n<html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><title>" + firstDomain(w.Domains) +
		"</title></head>\n<body style=\"font-family:sans-serif;text-align:center;padding-top:15vh\"><h1>" + firstDomain(w.Domains) +
		"</h1><p>网站已创建。把网站文件上传到 " + w.Root + " 即可。</p><p style=\"color:#888\">sshDesktop</p></body></html>\n"
}

func (s *Service) save(c *gin.Context) {
	var req store.Website
	if !api.Bind(c, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	w := req
	var old *store.Website
	if c.Param("id") != "" {
		o, ok := s.site(c)
		if !ok {
			return
		}
		old = &o
		// 启用 / 停用只走 /enable，修改时保持原状态（请求里没有这个字段时不会把站点停用）
		w.ID, w.CreatedAt, w.TakeoverFrom, w.TakeoverBak, w.Enabled = o.ID, o.CreatedAt, o.TakeoverFrom, o.TakeoverBak, o.Enabled
		if w.AppName == "" {
			w.AppName = o.AppName
		}
	} else {
		w.ID = 0
		w.Enabled = true
		w.TakeoverFrom, w.TakeoverBak = "", ""
	}
	api.Target(c, firstDomain(w.Domains))
	if err := s.write(ctx, &w, old); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, s.view(nil, w))
}

func (s *Service) enable(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !api.Bind(c, &req) {
		return
	}
	w, ok := s.site(c)
	if !ok {
		return
	}
	old := w
	w.Enabled = req.Enabled
	if err := s.write(c.Request.Context(), &w, &old); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) delete(c *gin.Context) {
	w, ok := s.site(c)
	if !ok {
		return
	}
	files := c.Query("files") == "1"
	// 同时删除网站文件需要二级确认
	if files && !s.d.Confirm(c) {
		return
	}
	if err := s.Delete(c.Request.Context(), w, files); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

// Delete 删除站点；deleteFiles 时一并删除面板创建的默认网站目录（自定义目录不删）。
func (s *Service) Delete(ctx context.Context, w store.Website, deleteFiles bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rt, err := s.Runtime(ctx); err == nil {
		ch, _ := s.siteChanges(rt, nil, &w)
		if err := s.commit(ctx, rt, ch); err != nil {
			return asAPI(err)
		}
	}
	if deleteFiles && w.Root == s.SiteRoot(w.Name) {
		os.RemoveAll(filepath.Dir(w.Root))
	}
	for _, suffix := range []string{".access.log", ".error.log"} {
		os.Remove(filepath.Join(s.www, "logs", w.Name+suffix))
	}
	return s.d.Store.DB.Delete(&w).Error
}

func (s *Service) log(c *gin.Context) {
	w, ok := s.site(c)
	if !ok {
		return
	}
	kind := c.DefaultQuery("kind", "access")
	if kind != "access" && kind != "error" {
		api.Fail(c, api.BadRequest("kind 只能是 access 或 error"))
		return
	}
	n, _ := strconv.Atoi(c.DefaultQuery("lines", "200"))
	if n <= 0 || n > 5000 {
		n = 200
	}
	p := filepath.Join(s.www, "logs", w.Name+"."+kind+".log")
	lines, err := tail(p, n)
	if errors.Is(err, os.ErrNotExist) {
		api.OK(c, gin.H{"path": p, "lines": []string{}})
		return
	}
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, gin.H{"path": p, "lines": lines})
}

// tail 读取文件最后 n 行（最多读最后 2 MB）。
func tail(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const max = 2 << 20
	off := int64(0)
	if fi.Size() > max {
		off = fi.Size() - max
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && len(buf) > 0 {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	return lines, nil
}
