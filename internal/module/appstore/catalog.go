package appstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 应用源格式兼容 1Panel 应用商店：
//   - index：1Panel 的应用索引 JSON（默认 https://apps-assets.fit2cloud.com/stable/1panel.json），每个版本一个 tar.gz 包
//   - git：与 1Panel appstore 仓库结构相同的 Git 仓库（apps/<key>/<version>/docker-compose.yml），用 git clone 同步
//   - dir：同样结构的本地目录（自定义应用）

const DefaultIndexURL = "https://apps-assets.fit2cloud.com/stable/1panel.json"

type Source struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"` // index / git / dir
	URL     string `json:"url"`  // index 的 JSON 地址、git 仓库地址或本地目录
	Enabled bool   `json:"enabled"`
	Builtin bool   `json:"builtin"`
}

func defaultSources() []Source {
	return []Source{{ID: "1panel", Name: "1Panel 应用商店", Type: "index", URL: DefaultIndexURL, Enabled: true, Builtin: true}}
}

// Label 是多语言文案，取中文。
type Label map[string]string

func (l Label) zh() string {
	for _, k := range []string{"zh", "zh-Hans", "zh-CN", "en"} {
		if v := l[k]; v != "" {
			return v
		}
	}
	return ""
}

type Option struct {
	Label string `json:"label" yaml:"label"`
	Value any    `json:"value" yaml:"value"`
}

// Field 是安装表单中的一个字段（1Panel 的 formFields）。
type Field struct {
	EnvKey      string   `json:"envKey" yaml:"envKey"`
	Type        string   `json:"type" yaml:"type"` // text / password / number / select / apps / service
	Default     any      `json:"default" yaml:"default"`
	Required    bool     `json:"required" yaml:"required"`
	Rule        string   `json:"rule,omitempty" yaml:"rule"`
	Random      bool     `json:"random,omitempty" yaml:"random"`
	Edit        bool     `json:"edit,omitempty" yaml:"edit"`
	Disabled    bool     `json:"disabled,omitempty" yaml:"disabled"`
	Multiple    bool     `json:"multiple,omitempty" yaml:"multiple"`
	Values      []Option `json:"values,omitempty" yaml:"values"`
	LabelZh     string   `json:"-" yaml:"labelZh"`
	LabelEn     string   `json:"-" yaml:"labelEn"`
	LabelI18n   Label    `json:"-" yaml:"label"`
	Description any      `json:"-" yaml:"description"`
	// 输出给前端的中文名称与说明
	Label string `json:"label" yaml:"-"`
	Hint  string `json:"hint,omitempty" yaml:"-"`
}

// UnmarshalJSON 兼容索引中 label 为对象、description 为对象或字符串的写法。
func (f *Field) UnmarshalJSON(b []byte) error {
	type plain Field
	var raw struct {
		plain
		LabelI18n   Label  `json:"label"`
		LabelZh     string `json:"labelZh"`
		LabelEn     string `json:"labelEn"`
		Description any    `json:"description"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*f = Field(raw.plain)
	f.LabelI18n, f.LabelZh, f.LabelEn, f.Description = raw.LabelI18n, raw.LabelZh, raw.LabelEn, raw.Description
	f.normalize()
	return nil
}

func (f *Field) normalize() {
	f.Label = f.LabelI18n.zh()
	if f.LabelZh != "" {
		f.Label = f.LabelZh
	}
	if f.Label == "" {
		f.Label = f.LabelEn
	}
	if f.Label == "" {
		f.Label = f.EnvKey
	}
	switch d := f.Description.(type) {
	case string:
		f.Hint = d
	case map[string]any:
		l := Label{}
		for k, v := range d {
			if s, ok := v.(string); ok {
				l[k] = s
			}
		}
		f.Hint = l.zh()
	}
}

type Version struct {
	Name        string  `json:"name"`
	Fields      []Field `json:"fields"`
	downloadURL string
	dir         string // dir / git 源中的版本目录
}

type App struct {
	Source         string   `json:"source"`
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Type           string   `json:"type"`
	Tags           []string `json:"tags"`
	Website        string   `json:"website,omitempty"`
	Github         string   `json:"github,omitempty"`
	Document       string   `json:"document,omitempty"`
	Architectures  []string `json:"architectures"`
	MemoryRequired int      `json:"memoryRequired,omitempty"`
	Recommend      int      `json:"recommend"`
	Limit          int      `json:"limit"`
	CrossVersion   bool     `json:"crossVersionUpdate"`
	Versions       []string `json:"versions"`
	// Unsupported 不为空表示暂不能安装（原因）
	Unsupported string `json:"unsupported,omitempty"`

	readme   string
	iconURL  string
	iconPath string
	versions []Version
}

// Tag 是应用分类。
type Tag struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Sort int    `json:"sort"`
}

// Catalog 是一个应用源加载后的内容。
type Catalog struct {
	Apps []*App
	Tags []Tag
}

func (a *App) version(name string) (*Version, bool) {
	for i := range a.versions {
		if a.versions[i].Name == name {
			return &a.versions[i], true
		}
	}
	return nil, false
}

// finish 在加载后计算版本列表和是否支持安装。
func (a *App) finish() {
	a.Versions = make([]string, 0, len(a.versions))
	for _, v := range a.versions {
		a.Versions = append(a.Versions, v.Name)
	}
	if a.Tags == nil {
		a.Tags = []string{}
	}
	if a.Architectures == nil {
		a.Architectures = []string{}
	}
	if len(a.versions) == 0 {
		a.Unsupported = "没有可安装的版本"
		return
	}
	for _, f := range a.versions[0].Fields {
		if f.Type == "apps" || f.Type == "service" {
			a.Unsupported = "需要先安装数据库等依赖应用，暂不支持（数据库管理在后续版本提供）"
			return
		}
	}
}

/* ---------- index 源 ---------- */

type indexFile struct {
	Apps []struct {
		ID          string   `json:"id"`
		Icon        string   `json:"icon"`
		ReadMe      string   `json:"readMe"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Title       string   `json:"title"`
		Props       appProps `json:"additionalProperties"`
		Versions    []struct {
			Name        string `json:"name"`
			DownloadURL string `json:"downloadUrl"`
			Props       struct {
				FormFields []Field `json:"formFields"`
			} `json:"additionalProperties"`
		} `json:"versions"`
	} `json:"apps"`
	Props struct {
		Tags []struct {
			Key     string `json:"key"`
			Name    string `json:"name"`
			Sort    int    `json:"sort"`
			Locales Label  `json:"locales"`
		} `json:"tags"`
	} `json:"additionalProperties"`
}

// appProps 是应用的 additionalProperties（index 与 data.yml 相同）。
type appProps struct {
	Key            string   `json:"key" yaml:"key"`
	Name           string   `json:"name" yaml:"name"`
	Tags           []string `json:"tags" yaml:"tags"`
	ShortDescZh    string   `json:"shortDescZh" yaml:"shortDescZh"`
	Description    Label    `json:"-" yaml:"-"`
	Type           string   `json:"type" yaml:"type"`
	CrossVersion   bool     `json:"crossVersionUpdate" yaml:"crossVersionUpdate"`
	Limit          int      `json:"limit" yaml:"limit"`
	Recommend      int      `json:"recommend" yaml:"recommend"`
	Website        string   `json:"website" yaml:"website"`
	Github         string   `json:"github" yaml:"github"`
	Document       string   `json:"document" yaml:"document"`
	MemoryRequired int      `json:"memoryRequired" yaml:"memoryRequired"`
	Architectures  []string `json:"architectures" yaml:"architectures"`
}

func (p appProps) app(source, fallbackKey, name, desc string) *App {
	a := &App{
		Source: source, Key: p.Key, Name: name, Description: p.ShortDescZh, Type: p.Type, Tags: p.Tags,
		Website: p.Website, Github: p.Github, Document: p.Document, MemoryRequired: p.MemoryRequired,
		Recommend: p.Recommend, Limit: p.Limit, CrossVersion: p.CrossVersion,
	}
	if a.Key == "" {
		a.Key = fallbackKey
	}
	if a.Name == "" {
		a.Name = p.Name
	}
	if a.Description == "" {
		a.Description = desc
	}
	seen := map[string]bool{}
	for _, arch := range p.Architectures {
		if !seen[arch] {
			seen[arch] = true
			a.Architectures = append(a.Architectures, arch)
		}
	}
	return a
}

func parseIndex(source string, b []byte) (*Catalog, error) {
	var f indexFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("应用索引格式错误：%w", err)
	}
	cat := &Catalog{}
	for _, t := range f.Props.Tags {
		name := t.Locales.zh()
		if name == "" {
			name = t.Name
		}
		cat.Tags = append(cat.Tags, Tag{Key: t.Key, Name: name, Sort: t.Sort})
	}
	for _, ia := range f.Apps {
		desc := ia.Description
		if desc == "" {
			desc = ia.Title
		}
		a := ia.Props.app(source, ia.ID, ia.Name, desc)
		if a.Key == "" {
			continue
		}
		a.readme, a.iconURL = ia.ReadMe, ia.Icon
		for _, v := range ia.Versions {
			if v.DownloadURL == "" {
				continue
			}
			a.versions = append(a.versions, Version{Name: v.Name, Fields: v.Props.FormFields, downloadURL: v.DownloadURL})
		}
		a.finish()
		cat.Apps = append(cat.Apps, a)
	}
	return cat, nil
}

/* ---------- 目录源（git / dir） ---------- */

type appData struct {
	Name        string   `yaml:"name"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Props       appProps `yaml:"additionalProperties"`
}

type versionData struct {
	Props struct {
		FormFields []Field `yaml:"formFields"`
	} `yaml:"additionalProperties"`
}

// appsRoot 返回目录源中放应用的目录：有 apps/ 子目录时用它（1Panel appstore 仓库），否则用根目录。
func appsRoot(dir string) string {
	if fi, err := os.Stat(filepath.Join(dir, "apps")); err == nil && fi.IsDir() {
		return filepath.Join(dir, "apps")
	}
	return dir
}

func loadDir(source, dir string) (*Catalog, error) {
	root := appsRoot(dir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	cat := &Catalog{}
	tags := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		a, err := loadAppDir(source, filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		for _, t := range a.Tags {
			tags[t] = true
		}
		cat.Apps = append(cat.Apps, a)
	}
	for t := range tags {
		cat.Tags = append(cat.Tags, Tag{Key: t, Name: t, Sort: 1000})
	}
	return cat, nil
}

func loadAppDir(source, dir string) (*App, error) {
	b, err := os.ReadFile(filepath.Join(dir, "data.yml"))
	if err != nil {
		return nil, err
	}
	var d appData
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	desc := d.Description
	if desc == "" {
		desc = d.Title
	}
	a := d.Props.app(source, filepath.Base(dir), d.Name, desc)
	if rm, err := os.ReadFile(filepath.Join(dir, "README.md")); err == nil {
		a.readme = string(rm)
	}
	for _, n := range []string{"logo.png", "logo.svg", "logo.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			a.iconPath = filepath.Join(dir, n)
			break
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		vdir := filepath.Join(dir, e.Name())
		if !e.IsDir() || !hasCompose(vdir) {
			continue
		}
		v := Version{Name: e.Name(), dir: vdir}
		if vb, err := os.ReadFile(filepath.Join(vdir, "data.yml")); err == nil {
			var vd versionData
			if yaml.Unmarshal(vb, &vd) == nil {
				v.Fields = vd.Props.FormFields
			}
		}
		for i := range v.Fields {
			v.Fields[i].normalize()
		}
		a.versions = append(a.versions, v)
	}
	// 新版本在前
	sort.Slice(a.versions, func(i, j int) bool { return compareVersions(a.versions[i].Name, a.versions[j].Name) > 0 })
	a.finish()
	return a, nil
}

func hasCompose(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "docker-compose.yml"))
	return err == nil
}

/* ---------- 同步 ---------- */

var httpClient = &http.Client{Timeout: 2 * time.Minute}

// download 下载到文件（限制大小），用于索引、图标和应用包。
func download(ctx context.Context, url, dst string, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "sshDesktop")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 失败：HTTP %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	f.Close()
	if err == nil && n > limit {
		err = fmt.Errorf("文件超过 %d MB", limit>>20)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// syncSource 更新应用源的本地缓存。
func syncSource(ctx context.Context, src Source, cacheDir string, log func(string, ...any)) error {
	switch src.Type {
	case "index":
		log("下载应用索引 %s", src.URL)
		return download(ctx, src.URL, filepath.Join(cacheDir, "index.json"), 256<<20)
	case "git":
		repo := filepath.Join(cacheDir, "repo")
		if _, err := os.Stat(filepath.Join(repo, ".git")); err == nil {
			log("更新 Git 仓库 %s", src.URL)
			return git(ctx, repo, "pull", "--ff-only", "--depth", "1")
		}
		log("克隆 Git 仓库 %s", src.URL)
		os.RemoveAll(repo)
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return err
		}
		return git(ctx, cacheDir, "clone", "--depth", "1", "--", src.URL, "repo")
	case "dir":
		if fi, err := os.Stat(src.URL); err != nil || !fi.IsDir() {
			return fmt.Errorf("目录不存在：%s", src.URL)
		}
		return nil
	}
	return errors.New("未知的应用源类型")
}

func git(ctx context.Context, dir string, args ...string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("服务器上没有安装 git，无法使用 Git 应用源")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s 失败：%s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

// loadSource 从缓存加载应用源；还没有同步过时返回 os.ErrNotExist。
func loadSource(src Source, cacheDir string) (*Catalog, error) {
	switch src.Type {
	case "index":
		b, err := os.ReadFile(filepath.Join(cacheDir, "index.json"))
		if err != nil {
			return nil, err
		}
		return parseIndex(src.ID, b)
	case "git":
		repo := filepath.Join(cacheDir, "repo")
		if _, err := os.Stat(repo); err != nil {
			return nil, err
		}
		return loadDir(src.ID, repo)
	case "dir":
		return loadDir(src.ID, src.URL)
	}
	return nil, errors.New("未知的应用源类型")
}

// compareVersions 比较版本号（按数字段比较，其余按字符串），a > b 返回正数。
func compareVersions(a, b string) int {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, y := pa[i], pb[i]
		xn, xok := num(x)
		yn, yok := num(y)
		switch {
		case xok && yok && xn != yn:
			if xn > yn {
				return 1
			}
			return -1
		case !(xok && yok) && x != y:
			return strings.Compare(x, y)
		}
	}
	// 多出的段都是 0 时视为相同（1.2 与 1.2.0）
	rest, sign := pa[min(len(pa), len(pb)):], 1
	if len(pb) > len(pa) {
		rest, sign = pb[len(pa):], -1
	}
	for _, x := range rest {
		if n, ok := num(x); !ok || n != 0 {
			return sign
		}
	}
	return 0
}

func splitVersion(v string) []string {
	v = strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	return strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '_' || r == '+' })
}

func num(s string) (int, bool) {
	if s == "" || len(s) > 9 {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// major 返回版本号的第一段（用于不允许跨大版本升级的应用）。
func major(v string) string {
	if p := splitVersion(v); len(p) > 0 {
		return p[0]
	}
	return v
}
