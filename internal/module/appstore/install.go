package appstore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/docker"
	dockermod "github.com/apple2333usa/sshdesktop/internal/module/docker"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// Network 是 1Panel 应用的 Compose 文件引用的外部网络。
const Network = "1panel-network"

/* ---------- 表单 ---------- */

const randChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomString(n int, chars string) string {
	b := make([]byte, n)
	for i := range b {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		b[i] = chars[k.Int64()]
	}
	return string(b)
}

// valueString 把默认值（字符串、数字、布尔）转换成 .env 中的字符串。
func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, p := range x {
			parts = append(parts, valueString(p))
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v)
}

// FieldView 是返回给前端的表单字段，Default 已按 random 生成随机值。
type FieldView struct {
	Field
	Value string `json:"value"`
}

func withDefaults(fields []Field) []FieldView {
	out := make([]FieldView, 0, len(fields))
	for _, f := range fields {
		v := FieldView{Field: f, Value: valueString(f.Default)}
		if f.Random {
			if f.Type == "password" {
				v.Value = randomString(16, randChars)
			} else if v.Value != "" {
				v.Value += "_" + randomString(6, "abcdefghijklmnopqrstuvwxyz0123456789")
			}
		}
		out = append(out, v)
	}
	return out
}

var (
	ruleCommon = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	ruleDomain = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$`)
)

func isPortField(f Field) bool {
	return f.Rule == "paramPort" || strings.Contains(f.EnvKey, "PORT") && f.Type == "number"
}

// validate 校验并补全参数：未提交的字段用默认值，返回最终的 envKey → 值。
func validate(fields []Field, in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range fields {
		if f.EnvKey == "" {
			continue
		}
		v, ok := in[f.EnvKey]
		if !ok {
			v = valueString(f.Default)
		}
		v = strings.TrimSpace(v)
		if strings.ContainsAny(v, "\n\r") {
			return nil, fmt.Errorf("%s 不能包含换行", f.Label)
		}
		if v == "" {
			if f.Required {
				return nil, fmt.Errorf("请填写%s", f.Label)
			}
			out[f.EnvKey] = ""
			continue
		}
		switch f.Type {
		case "number":
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return nil, fmt.Errorf("%s 必须是数字", f.Label)
			}
		case "select":
			if len(f.Values) > 0 {
				vals := []string{v}
				if f.Multiple {
					vals = strings.Split(v, ",")
				}
				for _, one := range vals {
					found := false
					for _, o := range f.Values {
						if valueString(o.Value) == one {
							found = true
							break
						}
					}
					if !found {
						return nil, fmt.Errorf("%s 的取值无效：%s", f.Label, one)
					}
				}
			}
		}
		switch f.Rule {
		case "paramPort":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 65535 {
				return nil, fmt.Errorf("%s 必须是 1-65535 之间的端口", f.Label)
			}
		case "paramPortRange":
			a, b, _ := strings.Cut(v, "-")
			x, e1 := strconv.Atoi(a)
			y, e2 := strconv.Atoi(b)
			if e1 != nil || e2 != nil || x < 1 || y > 65535 || x > y {
				return nil, fmt.Errorf("%s 必须是端口范围，例如 30000-30100", f.Label)
			}
		case "paramCommon":
			if !ruleCommon.MatchString(v) {
				return nil, fmt.Errorf("%s 只能包含字母、数字、点、下划线和短横线", f.Label)
			}
		case "paramComplexity":
			if len(v) < 6 || strings.ContainsAny(v, " '\"`$\\") {
				return nil, fmt.Errorf("%s 至少 6 位，且不能包含空格、引号、$ 和反斜杠", f.Label)
			}
		case "paramHttp", "paramExtUrl":
			if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
				return nil, fmt.Errorf("%s 必须以 http:// 或 https:// 开头", f.Label)
			}
		case "domain":
			if !ruleDomain.MatchString(v) {
				return nil, fmt.Errorf("%s 不是有效的域名", f.Label)
			}
		}
		out[f.EnvKey] = v
	}
	return out, nil
}

// portFree 判断宿主机端口是否空闲。
func portFree(port string) bool {
	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return false
	}
	l.Close()
	return true
}

/* ---------- .env 与 Compose ---------- */

var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// envQuote 按 Compose 的 .env 规则给值加引号：含空白、#、引号或 $ 时用单引号（不做变量展开）。
func envQuote(v string) string {
	if v == "" || !strings.ContainsAny(v, " \t#'\"$\\=`") {
		return v
	}
	if !strings.Contains(v, "'") {
		return "'" + v + "'"
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`)
	return `"` + r.Replace(v) + `"`
}

// writeEnv 写入 .env：已有文件中的其他行（例如初始化脚本生成的密钥）保留，表单字段按 keys 顺序更新或追加。
func writeEnv(path string, values map[string]string, keys []string) error {
	var lines []string
	set := map[string]bool{}
	if b, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			k, _, ok := strings.Cut(line, "=")
			k = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(k), "export "))
			if ok {
				if v, has := values[k]; has {
					line = k + "=" + envQuote(v)
					set[k] = true
				}
			}
			lines = append(lines, line)
		}
	}
	for _, k := range keys {
		if !set[k] {
			lines = append(lines, k+"="+envQuote(values[k]))
			set[k] = true
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// readEnv 读取 .env 中的键值（只处理面板写入的简单格式）。
func readEnv(path string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		} else if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = strings.NewReplacer(`\"`, `"`, `\$`, `$`, `\\`, `\`).Replace(v[1 : len(v)-1])
		}
		out[k] = v
	}
	return out
}

// bindHostIP 把 Compose 文件中形如 "${PORT}:80" 的端口映射改为 "${HOST_IP}:${PORT}:80"，
// 这样“仅本机访问”时端口只监听 127.0.0.1（与 1Panel 的处理一致）。
func bindHostIP(content []byte) ([]byte, bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, false, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return content, false, nil
	}
	changed := false
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "services" || root.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		svcs := root.Content[i+1]
		for j := 1; j < len(svcs.Content); j += 2 {
			svc := svcs.Content[j]
			if svc.Kind != yaml.MappingNode {
				continue
			}
			for k := 0; k+1 < len(svc.Content); k += 2 {
				if svc.Content[k].Value != "ports" || svc.Content[k+1].Kind != yaml.SequenceNode {
					continue
				}
				for _, p := range svc.Content[k+1].Content {
					if p.Kind == yaml.ScalarNode && strings.HasPrefix(p.Value, "${") && strings.Count(p.Value, ":") == 1 {
						p.Value = "${HOST_IP}:" + p.Value
						p.Style = yaml.DoubleQuotedStyle
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return content, false, nil
	}
	out, err := yaml.Marshal(&doc)
	return out, true, err
}

/* ---------- 应用包 ---------- */

// fetch 把应用某个版本的文件准备到临时目录，返回其中的版本目录（含 docker-compose.yml）。
func (s *Service) fetch(ctx context.Context, t *task.Task, a *App, v *Version) (string, func(), error) {
	tmp, err := os.MkdirTemp(s.cacheDir(), ".pkg-")
	if err != nil {
		if err := os.MkdirAll(s.cacheDir(), 0o755); err != nil {
			return "", nil, err
		}
		if tmp, err = os.MkdirTemp(s.cacheDir(), ".pkg-"); err != nil {
			return "", nil, err
		}
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if v.dir != "" {
		dst := filepath.Join(tmp, "pkg")
		if err := copyTree(v.dir, dst, false); err != nil {
			cleanup()
			return "", nil, err
		}
		return dst, cleanup, nil
	}
	t.Log("下载应用包 %s", v.downloadURL)
	archive := filepath.Join(tmp, "pkg.tar.gz")
	if err := download(ctx, v.downloadURL, archive, 512<<20); err != nil {
		cleanup()
		return "", nil, err
	}
	out := filepath.Join(tmp, "x")
	if err := untarGz(archive, out); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("解压应用包失败：%w", err)
	}
	dir := findVersionDir(out, a.Key, v.Name)
	if dir == "" {
		cleanup()
		return "", nil, errors.New("应用包中没有 docker-compose.yml")
	}
	return dir, cleanup, nil
}

// findVersionDir 在解压后的应用包中找版本目录：优先 <key>/<version>/，否则第一个含 docker-compose.yml 的目录。
func findVersionDir(root, key, version string) string {
	if p := filepath.Join(root, key, version); hasCompose(p) {
		return p
	}
	var found string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() && hasCompose(p) {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func untarGz(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(h.Name)
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("应用包包含非法路径：%s", h.Name)
		}
		p := filepath.Join(dst, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			w, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, io.LimitReader(tr, 256<<20))
			w.Close()
			if err != nil {
				return err
			}
		}
	}
}

// copyTree 复制目录。merge 为 true 时用于安装到已有目录（保留数据）：普通文件和 scripts 目录覆盖，
// 其他已存在的目录（通常是数据目录）保持不动。
func copyTree(src, dst string, merge bool) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		sp, dp := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		switch {
		case e.IsDir():
			if merge && e.Name() != "scripts" && exists(dp) {
				continue
			}
			if err := copyTree(sp, dp, false); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			b, err := os.ReadFile(sp)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dp, b, info.Mode().Perm()|0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

/* ---------- 安装 ---------- */

type installRequest struct {
	Source   string            `json:"source"`
	Key      string            `json:"key"`
	Version  string            `json:"version"`
	Name     string            `json:"name"`
	Params   map[string]string `json:"params"`
	External *bool             `json:"external"` // 端口允许外部访问（默认 true）
}

// storedParams 是保存在数据库中的安装参数。
type storedParams struct {
	Values   map[string]string `json:"values"`
	Keys     []string          `json:"keys"`
	External bool              `json:"external"`
}

func (s *Service) lock(name, taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.busy[name]; ok {
		return false
	}
	s.busy[name] = taskID
	return true
}

func (s *Service) unlock(name string) {
	s.mu.Lock()
	delete(s.busy, name)
	s.mu.Unlock()
}

func (s *Service) install(c *gin.Context) {
	var req installRequest
	if !api.Bind(c, &req) {
		return
	}
	a, ok := s.app(req.Source, req.Key)
	if !ok {
		api.Fail(c, api.NotFound("应用不存在，请先同步应用源"))
		return
	}
	if a.Unsupported != "" {
		api.Fail(c, api.Err(400, api.CodeAppUnsupported, a.Unsupported))
		return
	}
	if len(a.Architectures) > 0 && !contains(a.Architectures, arch()) {
		api.Fail(c, api.Err(400, api.CodeAppUnsupported, "该应用不支持本机架构 "+arch()))
		return
	}
	v, ok := a.version(req.Version)
	if !ok {
		api.Fail(c, api.NotFound("版本不存在"))
		return
	}
	if !dockermod.ProjectPattern.MatchString(req.Name) {
		api.Fail(c, api.BadRequest("名称只能包含小写字母、数字、下划线和短横线，且以字母或数字开头"))
		return
	}
	var n int64
	s.d.Store.DB.Model(&store.AppInstalled{}).Where("name = ?", req.Name).Count(&n)
	if n > 0 {
		api.Fail(c, api.Conflict(api.CodeConflict, "已有同名的应用"))
		return
	}
	if a.Limit == 1 {
		s.d.Store.DB.Model(&store.AppInstalled{}).Where("source = ? AND key = ?", a.Source, a.Key).Count(&n)
		if n > 0 {
			api.Fail(c, api.Conflict(api.CodeConflict, a.Name+" 只能安装一个"))
			return
		}
	}
	values, err := validate(v.Fields, req.Params)
	if err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	if err := s.d.Docker.Ping(ctx); err != nil {
		api.Fail(c, api.Err(503, api.CodeDockerUnavailable, "Docker 未安装或未运行，请先在 Docker 应用中安装"))
		return
	}
	if cts, err := s.d.Docker.Containers(ctx, true, map[string][]string{"name": {"^/" + req.Name + "$"}}); err == nil && len(cts) > 0 {
		api.Fail(c, api.Conflict(api.CodeConflict, "已有同名的容器："+req.Name))
		return
	}
	if cts, err := s.d.Docker.Containers(ctx, true, map[string][]string{"label": {docker.LabelProject + "=" + req.Name}}); err == nil && len(cts) > 0 {
		api.Fail(c, api.Conflict(api.CodeConflict, "已有同名的 Compose 项目："+req.Name))
		return
	}
	keys := []string{}
	for _, f := range v.Fields {
		if f.EnvKey == "" {
			continue
		}
		keys = append(keys, f.EnvKey)
		if isPortField(f) && values[f.EnvKey] != "" && !portFree(values[f.EnvKey]) {
			api.Fail(c, api.Err(409, api.CodePortInUse, fmt.Sprintf("端口 %s 已被占用，请换一个", values[f.EnvKey])))
			return
		}
	}
	external := req.External == nil || *req.External
	values["CONTAINER_NAME"] = req.Name
	values["HOST_IP"] = "0.0.0.0"
	if !external {
		values["HOST_IP"] = "127.0.0.1"
	}
	keys = append(keys, "CONTAINER_NAME", "HOST_IP")
	params, _ := json.Marshal(storedParams{Values: values, Keys: keys, External: external})
	dir := filepath.Join(s.appsDir(), a.Key, req.Name)
	row := store.AppInstalled{
		Name: req.Name, Source: a.Source, Key: a.Key, Title: a.Name, Version: v.Name, Dir: dir,
		Params: string(params), Status: "installing",
	}
	if err := s.d.Store.DB.Create(&row).Error; err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, fmt.Sprintf("%s %s → %s", a.Name, v.Name, req.Name))
	if !s.lock(req.Name, "") {
		api.Fail(c, api.Conflict(api.CodeConflict, "该应用正在操作中"))
		return
	}
	t := s.d.Tasks.Start("appstore.install", "安装 "+a.Name+"（"+req.Name+"）", api.User(c).UserID, "local", func(ctx context.Context, t *task.Task) error {
		defer s.unlock(req.Name)
		err := s.deploy(ctx, t, a, v, row, values, keys, external, false)
		s.finish(row.Name, "installed", err)
		return err
	})
	api.OK(c, gin.H{"task": t, "app": row})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Service) finish(name, okStatus string, err error) {
	upd := map[string]any{"status": okStatus, "message": ""}
	if err != nil {
		upd = map[string]any{"status": "failed", "message": err.Error()}
	}
	s.d.Store.DB.Model(&store.AppInstalled{}).Where("name = ?", name).Updates(upd)
}

func project(row store.AppInstalled) docker.Project {
	return docker.Project{Name: row.Name, Dir: row.Dir, Files: []string{filepath.Join(row.Dir, "docker-compose.yml")}}
}

// deploy 准备文件、写入 .env、执行初始化（或升级）脚本，然后拉取镜像并启动。
func (s *Service) deploy(ctx context.Context, t *task.Task, a *App, v *Version, row store.AppInstalled,
	values map[string]string, keys []string, external, upgrade bool) error {
	pkg, cleanup, err := s.fetch(ctx, t, a, v)
	if err != nil {
		return err
	}
	defer cleanup()
	t.Progress(10)
	merge := exists(row.Dir)
	if merge && !upgrade {
		t.Log("目录 %s 已存在，保留其中的数据目录", row.Dir)
	}
	if err := copyTree(pkg, row.Dir, merge); err != nil {
		return err
	}
	// 1Panel 的 data.yml 只是表单定义，不属于 Compose 项目
	os.Remove(filepath.Join(row.Dir, "data.yml"))
	composeFile := filepath.Join(row.Dir, "docker-compose.yml")
	if !external {
		b, err := os.ReadFile(composeFile)
		if err != nil {
			return err
		}
		if nb, changed, err := bindHostIP(b); err != nil {
			return fmt.Errorf("解析 docker-compose.yml 失败：%w", err)
		} else if changed {
			os.WriteFile(composeFile, nb, 0o644)
			t.Log("端口只监听 127.0.0.1")
		}
	}
	if err := writeEnv(filepath.Join(row.Dir, ".env"), values, keys); err != nil {
		return err
	}
	if err := dockermod.RegisterProject(s.d.Store.DB, row.Name, row.Dir, "docker-compose.yml"); err != nil {
		return err
	}
	if err := s.d.Docker.EnsureNetwork(ctx, Network); err != nil {
		return fmt.Errorf("创建网络 %s 失败：%w", Network, err)
	}
	script := "init.sh"
	if upgrade {
		script = "upgrade.sh"
	}
	if err := runScript(ctx, t, row.Dir, script); err != nil {
		return err
	}
	t.Progress(20)
	p := project(row)
	out := t.Lines()
	defer out.Flush()
	t.Log("拉取镜像")
	if err := docker.Compose(ctx, p, out, "pull"); err != nil {
		if ctx.Err() != nil {
			return err
		}
		// 镜像仓库限流或暂时连不上时，用本机已有的镜像继续；缺少的镜像在启动时会再拉取一次
		out.Flush()
		t.Log("拉取镜像失败（%v），尝试使用本机已有的镜像启动", err)
	}
	t.Progress(80)
	t.Log("启动")
	if err := docker.Compose(ctx, p, out, "up", "-d", "--remove-orphans"); err != nil {
		return err
	}
	t.Progress(100)
	return nil
}

// runScript 执行应用目录 scripts/ 下的脚本（如存在），工作目录为应用目录。
func runScript(ctx context.Context, t *task.Task, dir, name string) error {
	p := filepath.Join(dir, "scripts", name)
	if !exists(p) {
		return nil
	}
	t.Log("执行 scripts/%s", name)
	shell := "bash"
	if _, err := exec.LookPath("bash"); err != nil {
		shell = "sh"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, filepath.Join("scripts", name))
	cmd.Dir = dir
	out := t.Lines()
	defer out.Flush()
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("scripts/%s 执行失败：%w", name, err)
	}
	return nil
}

/* ---------- 已安装 ---------- */

type installedView struct {
	store.AppInstalled
	State      string   `json:"state"` // running / partial / stopped / installing / failed / upgrading
	Running    int      `json:"running"`
	Total      int      `json:"total"`
	Ports      []string `json:"ports"`
	Icon       string   `json:"icon"`
	Upgrades   []string `json:"upgrades"`
	External   bool     `json:"external"`
	Containers []string `json:"containers"`
	Busy       bool     `json:"busy"`
}

func (s *Service) views(ctx context.Context, rows []store.AppInstalled) []installedView {
	cts, _ := s.d.Docker.Containers(ctx, true, map[string][]string{"label": {docker.LabelProject}})
	byProject := map[string][]docker.Container{}
	for _, ct := range cts {
		p := ct.Labels[docker.LabelProject]
		byProject[p] = append(byProject[p], ct)
	}
	out := make([]installedView, 0, len(rows))
	for _, r := range rows {
		v := installedView{AppInstalled: r, Icon: iconURL(r.Source, r.Key), Ports: []string{}, Upgrades: []string{}, Containers: []string{}}
		var sp storedParams
		json.Unmarshal([]byte(r.Params), &sp)
		v.External = sp.External
		for _, ct := range byProject[r.Name] {
			v.Total++
			v.Containers = append(v.Containers, ct.Name())
			if ct.State == "running" {
				v.Running++
			}
		}
		switch {
		case r.Status != "installed":
			v.State = r.Status
		case v.Total == 0 || v.Running == 0:
			v.State = "stopped"
		case v.Running == v.Total:
			v.State = "running"
		default:
			v.State = "partial"
		}
		if a, ok := s.app(r.Source, r.Key); ok {
			if cur, ok := a.version(r.Version); ok {
				for _, f := range cur.Fields {
					if isPortField(f) && sp.Values[f.EnvKey] != "" {
						v.Ports = append(v.Ports, sp.Values[f.EnvKey])
					}
				}
			}
			for _, name := range a.Versions {
				if compareVersions(name, r.Version) > 0 && (a.CrossVersion || major(name) == major(r.Version)) {
					v.Upgrades = append(v.Upgrades, name)
				}
			}
		}
		s.mu.RLock()
		_, v.Busy = s.busy[r.Name]
		s.mu.RUnlock()
		out = append(out, v)
	}
	return out
}

func (s *Service) installed(c *gin.Context) {
	var rows []store.AppInstalled
	s.d.Store.DB.Order("created_at").Find(&rows)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	api.OK(c, s.views(ctx, rows))
}

func (s *Service) row(c *gin.Context) (store.AppInstalled, bool) {
	var row store.AppInstalled
	if err := s.d.Store.DB.First(&row, "name = ?", c.Param("name")).Error; err != nil {
		api.Fail(c, api.NotFound("应用不存在"))
		return row, false
	}
	return row, true
}

// installedDetail 返回应用详情和可修改的参数（edit 为 true 的字段）。
func (s *Service) installedDetail(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	v := s.views(ctx, []store.AppInstalled{row})[0]
	var sp storedParams
	json.Unmarshal([]byte(row.Params), &sp)
	fields := []FieldView{}
	if a, ok := s.app(row.Source, row.Key); ok {
		if ver, ok := a.version(row.Version); ok {
			for _, f := range ver.Fields {
				fv := FieldView{Field: f, Value: sp.Values[f.EnvKey]}
				if f.Type == "password" && !f.Edit {
					fv.Value = "" // 不可修改的密码不回显
				}
				fields = append(fields, fv)
			}
		}
	}
	api.OK(c, gin.H{"app": v, "fields": fields})
}

func (s *Service) control(c *gin.Context) {
	action := c.Param("action")
	args := map[string][]string{
		"start": {"up", "-d"}, "stop": {"stop"}, "restart": {"restart"}, "rebuild": {"up", "-d", "--force-recreate"},
	}[action]
	if args == nil {
		api.Fail(c, api.BadRequest("不支持的操作"))
		return
	}
	row, ok := s.row(c)
	if !ok {
		return
	}
	api.Target(c, action+" "+row.Name)
	if !s.lock(row.Name, "") {
		api.Fail(c, api.Conflict(api.CodeConflict, "该应用正在操作中"))
		return
	}
	names := map[string]string{"start": "启动", "stop": "停止", "restart": "重启", "rebuild": "重建"}
	t := s.d.Tasks.Start("appstore."+action, names[action]+" "+row.Title+"（"+row.Name+"）", api.User(c).UserID, "local",
		func(ctx context.Context, t *task.Task) error {
			defer s.unlock(row.Name)
			out := t.Lines()
			defer out.Flush()
			return docker.Compose(ctx, project(row), out, args...)
		})
	api.OK(c, t)
}

func (s *Service) logs(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	dockermod.StreamCompose(c, project(row), c.DefaultQuery("tail", "300"))
}

// updateParams 修改可编辑的参数（如端口），写入 .env 后重新创建容器。
func (s *Service) updateParams(c *gin.Context) {
	var req struct {
		Params   map[string]string `json:"params"`
		External *bool             `json:"external"`
	}
	if !api.Bind(c, &req) {
		return
	}
	row, ok := s.row(c)
	if !ok {
		return
	}
	a, ok := s.app(row.Source, row.Key)
	if !ok {
		api.Fail(c, api.NotFound("应用源中找不到该应用，无法修改参数"))
		return
	}
	ver, ok := a.version(row.Version)
	if !ok {
		api.Fail(c, api.NotFound("应用源中找不到当前版本"))
		return
	}
	var sp storedParams
	json.Unmarshal([]byte(row.Params), &sp)
	merged := map[string]string{}
	for k, v := range sp.Values {
		merged[k] = v
	}
	for _, f := range ver.Fields {
		if v, ok := req.Params[f.EnvKey]; ok && f.Edit {
			merged[f.EnvKey] = v
		}
	}
	values, err := validate(ver.Fields, merged)
	if err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	for _, f := range ver.Fields {
		if isPortField(f) && values[f.EnvKey] != sp.Values[f.EnvKey] && !portFree(values[f.EnvKey]) {
			api.Fail(c, api.Err(409, api.CodePortInUse, fmt.Sprintf("端口 %s 已被占用", values[f.EnvKey])))
			return
		}
	}
	for _, k := range []string{"CONTAINER_NAME", "HOST_IP"} {
		values[k] = sp.Values[k]
	}
	if req.External != nil && *req.External != sp.External {
		sp.External = *req.External
		values["HOST_IP"] = "0.0.0.0"
		if !sp.External {
			values["HOST_IP"] = "127.0.0.1"
			composeFile := filepath.Join(row.Dir, "docker-compose.yml")
			if b, err := os.ReadFile(composeFile); err == nil {
				if nb, changed, err := bindHostIP(b); err == nil && changed {
					os.WriteFile(composeFile, nb, 0o644)
				}
			}
		}
	}
	sp.Values = values
	if sp.Keys == nil {
		sp.Keys = sortedKeys(values)
	}
	api.Target(c, row.Name)
	if !s.lock(row.Name, "") {
		api.Fail(c, api.Conflict(api.CodeConflict, "该应用正在操作中"))
		return
	}
	t := s.d.Tasks.Start("appstore.params", "修改 "+row.Title+"（"+row.Name+"）的参数", api.User(c).UserID, "local",
		func(ctx context.Context, t *task.Task) error {
			defer s.unlock(row.Name)
			if err := writeEnv(filepath.Join(row.Dir, ".env"), values, sp.Keys); err != nil {
				return err
			}
			b, _ := json.Marshal(sp)
			s.d.Store.DB.Model(&store.AppInstalled{}).Where("name = ?", row.Name).Update("params", string(b))
			out := t.Lines()
			defer out.Flush()
			return docker.Compose(ctx, project(row), out, "up", "-d", "--force-recreate", "--remove-orphans")
		})
	api.OK(c, t)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// upgrade 升级到新版本：备份当前的 Compose 文件、.env 和脚本，失败时还原并重新启动旧版本。
func (s *Service) upgrade(c *gin.Context) {
	var req struct {
		Version string `json:"version"`
	}
	if !api.Bind(c, &req) {
		return
	}
	row, ok := s.row(c)
	if !ok {
		return
	}
	a, ok := s.app(row.Source, row.Key)
	if !ok {
		api.Fail(c, api.NotFound("应用源中找不到该应用"))
		return
	}
	v, ok := a.version(req.Version)
	if !ok {
		api.Fail(c, api.NotFound("版本不存在"))
		return
	}
	if compareVersions(v.Name, row.Version) <= 0 {
		api.Fail(c, api.BadRequest("只能升级到更新的版本"))
		return
	}
	var sp storedParams
	json.Unmarshal([]byte(row.Params), &sp)
	// 新版本新增的字段用默认值
	merged := map[string]string{}
	for _, f := range withDefaults(v.Fields) {
		merged[f.EnvKey] = f.Value
	}
	for k, val := range sp.Values {
		merged[k] = val
	}
	values, err := validate(v.Fields, merged)
	if err != nil {
		api.Fail(c, api.BadRequest("新版本参数校验失败："+err.Error()))
		return
	}
	for _, k := range []string{"CONTAINER_NAME", "HOST_IP"} {
		values[k] = sp.Values[k]
	}
	keys := sp.Keys
	for _, f := range v.Fields {
		if f.EnvKey != "" && !contains(keys, f.EnvKey) {
			keys = append(keys, f.EnvKey)
		}
	}
	api.Target(c, fmt.Sprintf("%s %s → %s", row.Name, row.Version, v.Name))
	if !s.lock(row.Name, "") {
		api.Fail(c, api.Conflict(api.CodeConflict, "该应用正在操作中"))
		return
	}
	s.d.Store.DB.Model(&store.AppInstalled{}).Where("name = ?", row.Name).Update("status", "upgrading")
	t := s.d.Tasks.Start("appstore.upgrade", fmt.Sprintf("升级 %s（%s）到 %s", row.Title, row.Name, v.Name), api.User(c).UserID, "local",
		func(ctx context.Context, t *task.Task) error {
			defer s.unlock(row.Name)
			backup := filepath.Join(row.Dir, ".sshdesktop-upgrade-"+row.Version)
			os.RemoveAll(backup)
			if err := os.MkdirAll(backup, 0o700); err != nil {
				return err
			}
			for _, n := range []string{"docker-compose.yml", ".env", "scripts"} {
				if p := filepath.Join(row.Dir, n); exists(p) {
					if fi, _ := os.Stat(p); fi.IsDir() {
						copyTree(p, filepath.Join(backup, n), false)
					} else if b, err := os.ReadFile(p); err == nil {
						os.WriteFile(filepath.Join(backup, n), b, 0o600)
					}
				}
			}
			t.Log("已备份当前版本的配置到 %s", backup)
			err := s.deploy(ctx, t, a, v, row, values, keys, sp.External, true)
			if err != nil {
				t.Log("升级失败，还原 %s 并重新启动：%s", row.Version, err)
				os.RemoveAll(filepath.Join(row.Dir, "scripts"))
				copyTree(backup, row.Dir, false)
				out := t.Lines()
				docker.Compose(context.Background(), project(row), out, "up", "-d", "--remove-orphans")
				out.Flush()
				s.finish(row.Name, "installed", nil)
				s.d.Store.DB.Model(&store.AppInstalled{}).Where("name = ?", row.Name).Update("message", "升级到 "+v.Name+" 失败："+err.Error())
				return err
			}
			sp.Values, sp.Keys = values, keys
			b, _ := json.Marshal(sp)
			s.d.Store.DB.Model(&store.AppInstalled{}).Where("name = ?", row.Name).
				Updates(map[string]any{"version": v.Name, "params": string(b)})
			s.finish(row.Name, "installed", nil)
			return nil
		})
	api.OK(c, t)
}

// uninstall 卸载应用：down 并取消登记；deleteData 时同时删除存储卷和应用目录（二级确认）。
func (s *Service) uninstall(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	deleteData := c.Query("data") == "1"
	api.Target(c, row.Name)
	if deleteData && !s.d.Confirm(c) {
		return
	}
	if !s.lock(row.Name, "") {
		api.Fail(c, api.Conflict(api.CodeConflict, "该应用正在操作中"))
		return
	}
	t := s.d.Tasks.Start("appstore.uninstall", "卸载 "+row.Title+"（"+row.Name+"）", api.User(c).UserID, "local",
		func(ctx context.Context, t *task.Task) error {
			defer s.unlock(row.Name)
			if exists(filepath.Join(row.Dir, "docker-compose.yml")) {
				args := []string{"down", "--remove-orphans"}
				if deleteData {
					args = append(args, "-v")
				}
				out := t.Lines()
				err := docker.Compose(ctx, project(row), out, args...)
				out.Flush()
				if err != nil && row.Status == "installed" {
					return err
				}
				runScript(ctx, t, row.Dir, "uninstall.sh")
			}
			s.d.Store.DB.Delete(&store.AppInstalled{}, "name = ?", row.Name)
			s.d.Store.DB.Delete(&store.ComposeProject{}, "name = ?", row.Name)
			if deleteData {
				if !strings.HasPrefix(row.Dir, s.appsDir()+string(os.PathSeparator)) {
					return fmt.Errorf("目录 %s 不在应用目录中，未删除", row.Dir)
				}
				if err := os.RemoveAll(row.Dir); err != nil {
					return err
				}
				os.Remove(filepath.Dir(row.Dir)) // 同一应用的上级目录为空时一并删除
				t.Log("已删除 %s", row.Dir)
			} else {
				t.Log("数据保留在 %s，用同样的名称重新安装可以继续使用", row.Dir)
			}
			return nil
		})
	api.OK(c, t)
}
