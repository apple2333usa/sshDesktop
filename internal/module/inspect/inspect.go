// Package inspect 是环境体检：检查服务器上已有的 Nginx、证书、Docker、防火墙、计划任务、SSH、端口和系统，
// 说明面板能接管到什么程度、有哪些注意事项和建议操作。首次登录时自动运行一次，之后可随时重新运行。
package inspect

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/module/cert"
	"github.com/apple2333usa/sshdesktop/internal/module/firewall"
	"github.com/apple2333usa/sshdesktop/internal/module/sshd"
	"github.com/apple2333usa/sshdesktop/internal/module/website"
	"github.com/apple2333usa/sshdesktop/internal/nginx"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/gin-gonic/gin"
)

const reportKey = "inspect.report"

// 检查项状态
const (
	Managed   = "managed"   // 已接管
	Partial   = "partial"   // 部分接管
	Unmanaged = "unmanaged" // 无法接管 / 尚未接管
	Info      = "info"      // 仅提示
	Warning   = "warning"   // 需要注意
)

type Item struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	State   string   `json:"state"`
	Summary string   `json:"summary"`
	Notes   []string `json:"notes"`
	Actions []string `json:"actions"`
	// App 是处理这一项的应用（前端打开对应窗口）
	App string `json:"app,omitempty"`
}

type Report struct {
	At    time.Time `json:"at"`
	Items []Item    `json:"items"`
}

type Deps struct {
	Store     *store.Store
	Docker    *docker.Client
	Website   *website.Service
	Cert      *cert.Service
	Firewall  *firewall.Service
	SSHD      *sshd.Service
	PanelPort int
	DataDir   string
}

type Service struct {
	d  Deps
	mu sync.Mutex
}

func New(d Deps) *Service { return &Service{d: d} }

func (s *Service) Register(r *gin.RouterGroup, view gin.HandlerFunc) {
	r.GET("/inspect", view, s.get)
	r.POST("/inspect/run", view, s.run)
}

func (s *Service) get(c *gin.Context) {
	var rep *Report
	s.d.Store.GetSetting(reportKey, &rep)
	api.OK(c, rep)
}

func (s *Service) run(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	api.OK(c, s.Run(ctx))
}

// Run 执行一次体检并保存报告。
func (s *Service) Run(ctx context.Context) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := Report{At: time.Now()}
	checks := []func(context.Context) Item{s.nginx, s.certs, s.docker, s.firewall, s.cron, s.ssh, s.ports, s.system}
	items := make([]Item, len(checks))
	var wg sync.WaitGroup
	for i, fn := range checks {
		wg.Add(1)
		go func(i int, fn func(context.Context) Item) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					items[i] = Item{Key: fmt.Sprint(i), Title: "检查出错", State: Warning, Summary: fmt.Sprint(r)}
				}
			}()
			it := fn(ctx)
			if it.Notes == nil {
				it.Notes = []string{}
			}
			if it.Actions == nil {
				it.Actions = []string{}
			}
			items[i] = it
		}(i, fn)
	}
	wg.Wait()
	rep.Items = items
	s.d.Store.SetSetting(reportKey, rep)
	return rep
}

func (s *Service) nginx(ctx context.Context) Item {
	it := Item{Key: "nginx", Title: "Nginx / OpenResty", App: "websites"}
	st := s.d.Website.Status(ctx)
	switch {
	case st.Mode == nginx.ModeContainer:
		it.State, it.Summary = Managed, "网站使用面板安装的 OpenResty 容器"
		if !st.Running {
			it.State = Warning
			it.Notes = append(it.Notes, "OpenResty 容器没有运行")
		}
	case st.Mode == nginx.ModeHost && st.Runtime != nil:
		it.State = Managed
		it.Summary = fmt.Sprintf("已接管宿主机上的 %s %s（%s）", st.Runtime.Flavor, st.Runtime.Version, st.Runtime.Conf)
		if !strings.HasPrefix(st.Runtime.Conf, "/etc/nginx/") && !strings.HasPrefix(st.Runtime.Conf, "/usr/local/openresty/") {
			it.Notes = append(it.Notes, "主配置不在默认位置："+st.Runtime.Conf)
		}
		if list, err := s.d.Website.Existing(ctx); err == nil {
			var complex []string
			simple := 0
			for _, f := range list {
				if f.Takeover != nil {
					simple++
				} else {
					complex = append(complex, f.Path+"："+f.Reason)
				}
			}
			if simple > 0 {
				it.Actions = append(it.Actions, fmt.Sprintf("有 %d 个已有站点可以接管为面板站点（转换成表单编辑）", simple))
			}
			if len(complex) > 0 {
				it.State = Partial
				it.Notes = append(it.Notes, "以下站点用到了复杂配置，只能按原始配置编辑：")
				it.Notes = append(it.Notes, complex...)
			}
		}
	case st.HostBinary != "":
		it.State, it.Summary = Unmanaged, "服务器上有 "+st.HostBinary+"，尚未接管"
		it.Actions = append(it.Actions, "在“网站”中接管已有的 Nginx：面板站点写入独立目录，原有站点保持不变")
	default:
		it.State, it.Summary = Info, "没有安装 Nginx"
		it.Actions = append(it.Actions, "需要建网站时，在“网站”中一键安装面板管理的 OpenResty 容器（需要 Docker）")
	}
	if st.Error != "" {
		it.State = Warning
		it.Notes = append(it.Notes, st.Error)
	}
	return it
}

func (s *Service) certs(ctx context.Context) Item {
	it := Item{Key: "certs", Title: "证书", App: "certs"}
	ext := s.d.Cert.Externals()
	var n int64
	s.d.Store.DB.Model(&store.Certificate{}).Count(&n)
	certbot, migrated, acmesh := 0, 0, 0
	for _, x := range ext {
		switch {
		case x.Tool == "certbot" && x.Migrated:
			migrated++
		case x.Tool == "certbot":
			certbot++
		default:
			acmesh++
		}
	}
	it.Summary = fmt.Sprintf("面板管理 %d 张证书", n)
	it.State = Managed
	if certbot > 0 {
		it.State = Partial
		it.Notes = append(it.Notes, fmt.Sprintf("有 %d 张 certbot 证书，续期仍由 certbot 负责", certbot))
		it.Actions = append(it.Actions, "在“证书”中把 certbot 证书迁移为面板续期（证书文件位置不变）")
	}
	if migrated > 0 {
		it.Notes = append(it.Notes, fmt.Sprintf("%d 张 certbot 证书已迁移为面板续期", migrated))
	}
	if acmesh > 0 {
		it.State = Partial
		it.Notes = append(it.Notes, fmt.Sprintf("有 %d 张 acme.sh 证书，续期仍由 acme.sh 负责", acmesh))
	}
	if n == 0 && len(ext) == 0 {
		it.State = Info
	}
	return it
}

func (s *Service) docker(ctx context.Context) Item {
	it := Item{Key: "docker", Title: "Docker 与 Compose", App: "docker"}
	v, err := s.d.Docker.Version(ctx)
	if err != nil {
		if _, lerr := exec.LookPath("docker"); lerr == nil {
			it.State, it.Summary = Warning, "已安装 Docker，但服务没有运行"
			it.Actions = append(it.Actions, "在“Docker”中启动 Docker 服务")
		} else {
			it.State, it.Summary = Info, "没有安装 Docker"
			it.Actions = append(it.Actions, "需要应用商店或 OpenResty 时，在“Docker”中一键安装")
		}
		return it
	}
	it.State, it.Summary = Managed, "Docker "+v.Version+"，全部容器、镜像、网络、存储卷和 Compose 项目都可以在面板中管理"
	if _, cv, err := docker.ComposeCommand(ctx); err != nil {
		it.State = Partial
		it.Notes = append(it.Notes, "没有 Compose 插件，Compose 项目和应用商店不可用")
	} else {
		it.Summary += "；Compose " + cv
	}
	cts, _ := s.d.Docker.Containers(ctx, true, map[string][]string{"label": {docker.LabelWorkingDir}})
	missing := map[string]bool{}
	for _, ct := range cts {
		dir := ct.Labels[docker.LabelWorkingDir]
		if _, err := os.Stat(dir); err != nil {
			missing[ct.Labels[docker.LabelProject]] = true
		}
	}
	for p := range missing {
		it.State = Partial
		it.Notes = append(it.Notes, "Compose 项目 "+p+" 的目录已被移动或删除，只能按容器管理")
	}
	return it
}

func (s *Service) firewall(ctx context.Context) Item {
	it := Item{Key: "firewall", Title: "防火墙", App: "firewall"}
	st := s.d.Firewall.Status(ctx)
	switch st.Backend {
	case "none":
		it.State, it.Summary = Unmanaged, "没有安装 ufw 或 firewalld"
		if st.Raw != "" {
			it.Summary = "只有手写的 " + st.RawTool + " 规则，面板只读展示"
		}
		it.Notes = append(it.Notes, st.Notes...)
	default:
		it.State = Managed
		state := "未启用"
		if st.Active {
			state = "已启用"
		}
		it.Summary = fmt.Sprintf("%s %s，%d 条规则", st.Backend, state, len(st.Rules))
		readonly := 0
		for _, r := range st.Rules {
			if !r.Editable {
				readonly++
			}
		}
		if readonly > 0 {
			it.State = Partial
			it.Notes = append(it.Notes, fmt.Sprintf("%d 条规则较复杂，只能查看，需要用 %s 命令修改", readonly, st.Backend))
		}
		if !st.Active {
			it.Actions = append(it.Actions, "在“防火墙”中放行需要的端口后启用；启用时会自动放行 SSH 端口")
		}
	}
	return it
}

func (s *Service) cron(ctx context.Context) Item {
	it := Item{Key: "cron", Title: "计划任务", App: "cron", State: Managed}
	files := 0
	for _, g := range []string{"/etc/crontab", "/etc/cron.d/*", "/var/spool/cron/crontabs/*", "/var/spool/cron/*"} {
		m, _ := filepath.Glob(g)
		for _, p := range m {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() && filepath.Base(p) != "sshdesktop" {
				files++
			}
		}
	}
	it.Summary = fmt.Sprintf("系统中有 %d 个 crontab 文件，可在面板中启用、停用和编辑", files)
	if _, err := os.Stat("/etc/cron.d"); err != nil {
		it.State, it.Summary = Info, "没有安装 cron"
		it.Actions = append(it.Actions, "在“计划任务”中一键安装 cron")
	}
	out, _ := exec.CommandContext(ctx, "systemctl", "list-timers", "--all", "--no-legend").Output()
	if n := len(strings.Split(strings.TrimSpace(string(out)), "\n")); strings.TrimSpace(string(out)) != "" {
		it.Notes = append(it.Notes, fmt.Sprintf("另有 %d 个 systemd 定时器，只读展示", n))
	}
	return it
}

func (s *Service) ssh(ctx context.Context) Item {
	it := Item{Key: "ssh", Title: "SSH", App: "sshd"}
	st := s.d.SSHD.Status(ctx)
	if !st.Installed {
		it.State, it.Summary = Info, "没有安装 OpenSSH 服务端"
		return it
	}
	it.State = Managed
	it.Summary = st.Version
	if st.Effective != nil {
		ports := make([]string, len(st.Effective.Ports))
		for i, p := range st.Effective.Ports {
			ports[i] = fmt.Sprint(p)
		}
		it.Summary += "，端口 " + strings.Join(ports, "、")
		if st.Effective.PasswordAuth && st.Effective.PermitRootLogin == "yes" {
			it.Notes = append(it.Notes, "允许 root 用密码登录，容易被暴力破解")
			it.Actions = append(it.Actions, "在“SSH 设置”中改为只允许密钥登录 root")
		}
	}
	it.Notes = append(it.Notes, st.Notes...)
	if st.Socket {
		it.Notes = append(it.Notes, "使用 systemd socket 激活（ssh.socket），改端口时面板会重新生成并重启 ssh.socket")
	}
	if st.Error != "" {
		it.State = Warning
		it.Notes = append(it.Notes, st.Error)
	}
	return it
}

func (s *Service) ports(ctx context.Context) Item {
	it := Item{Key: "ports", Title: "端口占用", State: Info}
	var used []string
	for _, p := range []int{80, 443} {
		if who := website.PortOwner(p); who != "" {
			used = append(used, fmt.Sprintf("%d：%s", p, who))
			if !strings.Contains(who, "nginx") && !strings.Contains(who, "openresty") {
				it.State = Warning
				it.Notes = append(it.Notes, fmt.Sprintf("端口 %d 被 %s 占用，网站需要用其他端口，或先停止它", p, who))
			}
		}
	}
	it.Summary = fmt.Sprintf("面板端口 %d", s.d.PanelPort)
	if len(used) > 0 {
		it.Summary += "；" + strings.Join(used, "，")
	} else {
		it.Summary += "；80、443 空闲"
	}
	return it
}

func (s *Service) system(ctx context.Context) Item {
	it := Item{Key: "system", Title: "系统", State: Info}
	name := runtime.GOOS
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
				name = strings.Trim(v, `"`)
			}
		}
	}
	it.Summary = name + "，" + runtime.GOARCH
	if out, err := exec.CommandContext(ctx, "getenforce").Output(); err == nil {
		mode := strings.TrimSpace(string(out))
		it.Summary += "，SELinux " + mode
		if mode == "Enforcing" {
			it.State = Warning
			it.Notes = append(it.Notes, "SELinux 处于强制模式：Nginx 反向代理到本机端口可能需要 setsebool -P httpd_can_network_connect 1，网站目录可能需要正确的安全上下文")
		}
	}
	if b, err := os.ReadFile("/sys/module/apparmor/parameters/enabled"); err == nil && strings.TrimSpace(string(b)) == "Y" {
		it.Summary += "，AppArmor 已启用"
	}
	for _, p := range []string{"/", s.d.DataDir} {
		var fs syscall.Statfs_t
		if syscall.Statfs(p, &fs) != nil {
			continue
		}
		free := fs.Bavail * uint64(fs.Bsize)
		if free < 1<<30 {
			it.State = Warning
			it.Notes = append(it.Notes, fmt.Sprintf("%s 所在磁盘只剩 %.1f GB", p, float64(free)/(1<<30)))
		}
	}
	return it
}
