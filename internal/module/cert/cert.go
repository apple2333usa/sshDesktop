// Package cert 是证书管理（仅本机）：用 lego 向 Let's Encrypt / ZeroSSL 申请证书（HTTP-01 或 DNS-01），
// 到期前自动续期；也可上传自有证书。服务器上 certbot、acme.sh 的证书会被识别并列出，
// certbot 的证书可以迁移为面板续期。
package cert

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/pending"
	"github.com/apple2333usa/sshdesktop/internal/secret"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
)

const settingsKey = "cert.settings"

// Web 是网站模块提供给证书模块的能力。
type Web interface {
	AcmeRoot() string
	PrepareChallenge(ctx context.Context, domains []string) (func(), error)
	Reapply(ctx context.Context, certID uint) error
	CertInUse(certID uint) []string
}

type Deps struct {
	Store   *store.Store
	Tasks   *task.Manager
	Box     *secret.Box
	DataDir string
	Version string
	Audit   func(module, action string) gin.HandlerFunc
	Web     Web
	// PanelCert 返回面板 HTTPS 正在使用的证书 ID（0 表示没有使用）
	PanelCert func() uint
	// OnRenewed 在证书文件更新后调用
	OnRenewed func(id uint)
	// Notify 推送通知（续期失败、即将到期）
	Notify func(level, title, body string)
}

type Service struct {
	d       Deps
	issueMu sync.Mutex
	busy    sync.Map // 证书 ID → 正在申请

	// 测试中替换
	httpClient    *http.Client
	httpPort      string
	certbotRoot   string
	acmeShRoot    string
	renewInterval time.Duration
}

func New(d Deps) *Service {
	return &Service{d: d, certbotRoot: "/etc/letsencrypt", acmeShRoot: "/root/.acme.sh", renewInterval: 12 * time.Hour}
}

// SetTesting 仅用于测试：ACME 的 HTTP 客户端、内置验证服务器端口、certbot / acme.sh 目录。
func (s *Service) SetTesting(client *http.Client, httpPort, certbotRoot, acmeShRoot string) {
	s.httpClient, s.httpPort, s.certbotRoot, s.acmeShRoot = client, httpPort, certbotRoot, acmeShRoot
}

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	a := s.d.Audit
	g := r.Group("/certs")
	g.GET("", view, s.list)
	g.POST("", manage, a("cert", "create"), s.create)
	g.POST("/upload", manage, a("cert", "upload"), s.upload)
	g.GET("/settings", view, s.getSettings)
	g.PUT("/settings", manage, a("cert", "settings"), s.putSettings)
	g.GET("/external", view, s.external)
	g.POST("/external/migrate", manage, a("cert", "migrate-certbot"), s.migrate)
	g.GET("/dns-accounts", view, s.dnsAccounts)
	g.POST("/dns-accounts", manage, a("cert", "dns-account-create"), s.dnsAccountSave)
	g.PUT("/dns-accounts/:id", manage, a("cert", "dns-account-update"), s.dnsAccountSave)
	g.DELETE("/dns-accounts/:id", manage, a("cert", "dns-account-delete"), s.dnsAccountDelete)
	g.GET("/:id", view, s.get)
	g.PUT("/:id", manage, a("cert", "update"), s.update)
	g.DELETE("/:id", manage, a("cert", "delete"), s.delete)
	g.POST("/:id/issue", manage, a("cert", "issue"), s.issue)
	g.GET("/:id/download", manage, a("cert", "download"), s.download)
}

/* ---------- 设置 ---------- */

type settings struct {
	Email string `json:"email"`
}

// Email 是 ACME 账号的联系邮箱（可以为空）。
func (s *Service) Email() string {
	var st settings
	s.d.Store.GetSetting(settingsKey, &st)
	return st.Email
}

func (s *Service) getSettings(c *gin.Context) {
	api.OK(c, gin.H{"email": s.Email(), "cas": CAs, "dnsProviders": DNSProviders})
}

func (s *Service) putSettings(c *gin.Context) {
	var req settings
	if !api.Bind(c, &req) {
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email != "" && (!strings.Contains(req.Email, "@") || strings.ContainsAny(req.Email, " \n")) {
		api.Fail(c, api.BadRequest("邮箱格式不正确"))
		return
	}
	if err := s.d.Store.SetSetting(settingsKey, req); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

/* ---------- 证书文件 ---------- */

func splitDomains(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '，' }) {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

func (s *Service) dir(id uint) string {
	return filepath.Join(s.d.DataDir, "certs", strconv.Itoa(int(id)))
}

// Info 是从证书文件中读出的信息。
type Info struct {
	Domains   []string  `json:"domains"`
	Issuer    string    `json:"issuer"`
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
}

// ParsePair 校验证书和私钥配对，返回证书信息。
func ParsePair(certPEM, keyPEM []byte) (Info, error) {
	var info Info
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return info, fmt.Errorf("证书和私钥不匹配或格式错误：%w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return info, err
	}
	info.Domains = append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		info.Domains = append(info.Domains, ip.String())
	}
	if len(info.Domains) == 0 && leaf.Subject.CommonName != "" {
		info.Domains = []string{leaf.Subject.CommonName}
	}
	info.Issuer = leaf.Issuer.CommonName
	if info.Issuer == "" && len(leaf.Issuer.Organization) > 0 {
		info.Issuer = leaf.Issuer.Organization[0]
	}
	info.NotBefore, info.NotAfter = leaf.NotBefore, leaf.NotAfter
	return info, nil
}

func readPair(certPath, keyPath string) (Info, error) {
	cb, err := os.ReadFile(certPath)
	if err != nil {
		return Info{}, err
	}
	kb, err := os.ReadFile(keyPath)
	if err != nil {
		return Info{}, err
	}
	return ParsePair(cb, kb)
}

func applyInfo(row *store.Certificate, info Info) {
	row.Domains = strings.Join(info.Domains, " ")
	row.Issuer = info.Issuer
	nb, na := info.NotBefore, info.NotAfter
	row.NotBefore, row.NotAfter = &nb, &na
	row.Status, row.Error = "valid", ""
	if time.Now().After(na) {
		row.Status = "expired"
	}
}

// writeFiles 写入证书文件（先写临时文件再改名；原路径是软链接时替换链接本身）。
func (s *Service) writeFiles(row *store.Certificate, certPEM, keyPEM []byte) error {
	info, err := ParsePair(certPEM, keyPEM)
	if err != nil {
		return err
	}
	if row.CertPath == "" || row.KeyPath == "" {
		d := s.dir(row.ID)
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		row.CertPath, row.KeyPath = filepath.Join(d, "fullchain.pem"), filepath.Join(d, "privkey.pem")
	}
	if err := pending.WriteFile(row.KeyPath, keyPEM, 0o600); err != nil {
		return err
	}
	if err := pending.WriteFile(row.CertPath, certPEM, 0o644); err != nil {
		return err
	}
	applyInfo(row, info)
	return s.d.Store.DB.Save(row).Error
}

// Files 返回证书文件路径（网站和面板 HTTPS 使用）。
func (s *Service) Files(id uint) (string, string, error) {
	var row store.Certificate
	if err := s.d.Store.DB.First(&row, id).Error; err != nil {
		return "", "", errors.New("证书不存在")
	}
	if row.CertPath == "" || row.KeyPath == "" {
		return "", "", fmt.Errorf("证书 %s 还没有申请成功", row.Domains)
	}
	return row.CertPath, row.KeyPath, nil
}

// ImportFile 把服务器上已有的证书文件登记为证书（不复制、不续期），已登记过的直接返回。
func (s *Service) ImportFile(certPath, keyPath string) (uint, error) {
	var row store.Certificate
	if s.d.Store.DB.Where("cert_path = ?", certPath).First(&row).Error == nil {
		return row.ID, nil
	}
	info, err := readPair(certPath, keyPath)
	if err != nil {
		return 0, err
	}
	row = store.Certificate{Source: "file", CertPath: certPath, KeyPath: keyPath, Remark: "服务器上已有的证书文件"}
	applyInfo(&row, info)
	return row.ID, s.d.Store.DB.Create(&row).Error
}

// ImportPEM 导入证书内容（恢复网站备份时使用）：已有相同证书时直接返回它，否则作为上传的证书保存。
func (s *Service) ImportPEM(certPEM, keyPEM []byte, remark string) (uint, error) {
	if _, err := ParsePair(certPEM, keyPEM); err != nil {
		return 0, err
	}
	leaf := firstDER(certPEM)
	var rows []store.Certificate
	s.d.Store.DB.Find(&rows)
	for _, r := range rows {
		if b, err := os.ReadFile(r.CertPath); err == nil && leaf != nil && bytes.Equal(firstDER(b), leaf) {
			return r.ID, nil
		}
	}
	row := store.Certificate{Source: "upload", Remark: remark}
	if err := s.d.Store.DB.Create(&row).Error; err != nil {
		return 0, err
	}
	if err := s.writeFiles(&row, certPEM, keyPEM); err != nil {
		s.d.Store.DB.Delete(&row)
		return 0, err
	}
	return row.ID, nil
}

// firstDER 返回 PEM 中第一张证书的 DER 内容。
func firstDER(b []byte) []byte {
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return nil
		}
		if blk.Type == "CERTIFICATE" {
			return blk.Bytes
		}
	}
}

/* ---------- 列表与增删改 ---------- */

type certView struct {
	store.Certificate
	Sites   []string `json:"sites"`
	Panel   bool     `json:"panel"`
	Busy    bool     `json:"busy"`
	DaysAll int      `json:"daysLeft"`
}

func (s *Service) view(row store.Certificate) certView {
	v := certView{Certificate: row, Sites: []string{}}
	if s.d.Web != nil {
		v.Sites = s.d.Web.CertInUse(row.ID)
	}
	if s.d.PanelCert != nil {
		v.Panel = s.d.PanelCert() == row.ID
	}
	_, v.Busy = s.busy.Load(row.ID)
	if row.NotAfter != nil {
		v.DaysAll = int(time.Until(*row.NotAfter).Hours() / 24)
	}
	return v
}

func (s *Service) list(c *gin.Context) {
	var rows []store.Certificate
	s.d.Store.DB.Order("id").Find(&rows)
	out := make([]certView, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.view(r))
	}
	api.OK(c, out)
}

func (s *Service) row(c *gin.Context) (store.Certificate, bool) {
	var row store.Certificate
	if err := s.d.Store.DB.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("证书不存在"))
		return row, false
	}
	api.Target(c, row.Domains)
	return row, true
}

func (s *Service) get(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	v := gin.H{"cert": s.view(row)}
	if b, err := os.ReadFile(row.CertPath); err == nil {
		v["pem"] = string(b)
	}
	api.OK(c, v)
}

type acmeReq struct {
	Domains      string `json:"domains"`
	CA           string `json:"ca"`
	CAURL        string `json:"caUrl"`
	Challenge    string `json:"challenge"`
	DNSAccountID uint   `json:"dnsAccountId"`
	KeyType      string `json:"keyType"`
	AutoRenew    bool   `json:"autoRenew"`
	Remark       string `json:"remark"`
}

func (s *Service) validate(r *acmeReq) error {
	ds := splitDomains(r.Domains)
	if len(ds) == 0 {
		return errors.New("至少填写一个域名")
	}
	if len(ds) > 100 {
		return errors.New("一张证书最多 100 个域名")
	}
	r.Domains = strings.Join(ds, " ")
	if r.CA == "" {
		r.CA = "letsencrypt"
	}
	if _, err := caURL(&store.Certificate{CA: r.CA, CAURL: r.CAURL}); err != nil {
		return err
	}
	switch r.Challenge {
	case "", "http":
		r.Challenge = "http"
	case "dns":
		var n int64
		s.d.Store.DB.Model(&store.DNSAccount{}).Where("id = ?", r.DNSAccountID).Count(&n)
		if n == 0 {
			return errors.New("请选择 DNS 账号")
		}
	default:
		return errors.New("验证方式只能是 http 或 dns")
	}
	if r.KeyType == "" {
		r.KeyType = "ec256"
	}
	if _, ok := keyTypes[r.KeyType]; !ok {
		return errors.New("不支持的密钥类型")
	}
	return nil
}

// create 新建一张 ACME 证书并立即开始申请。
func (s *Service) create(c *gin.Context) {
	var req acmeReq
	if !api.Bind(c, &req) {
		return
	}
	if err := s.validate(&req); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	row := store.Certificate{Domains: req.Domains, Source: "acme", CA: req.CA, CAURL: req.CAURL, Challenge: req.Challenge,
		DNSAccountID: req.DNSAccountID, KeyType: req.KeyType, AutoRenew: req.AutoRenew, Remark: req.Remark, Status: "pending"}
	api.Target(c, row.Domains)
	if err := s.d.Store.DB.Create(&row).Error; err != nil {
		api.Fail(c, err)
		return
	}
	t := s.Issue(row, api.User(c).UserID)
	api.OK(c, gin.H{"cert": s.view(row), "task": t})
}

// update 修改 ACME 证书的申请参数（下次申请或续期生效）或备注、自动续期。
func (s *Service) update(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	var req acmeReq
	if !api.Bind(c, &req) {
		return
	}
	row.Remark = req.Remark
	if row.Source == "acme" || row.Source == "certbot" {
		if row.Source == "certbot" {
			req.Domains = row.Domains
		}
		if err := s.validate(&req); err != nil {
			api.Fail(c, api.BadRequest(err.Error()))
			return
		}
		row.Domains, row.CA, row.CAURL, row.Challenge = req.Domains, req.CA, req.CAURL, req.Challenge
		row.DNSAccountID, row.KeyType, row.AutoRenew = req.DNSAccountID, req.KeyType, req.AutoRenew
	}
	if err := s.d.Store.DB.Save(&row).Error; err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, s.view(row))
}

// Issue 在后台申请或续期证书。
func (s *Service) Issue(row store.Certificate, userID uint) *store.Task {
	return s.d.Tasks.Start("cert.issue", "申请证书："+row.Domains, userID, "local", func(ctx context.Context, t *task.Task) error {
		if _, loaded := s.busy.LoadOrStore(row.ID, true); loaded {
			return errors.New("这张证书正在申请中")
		}
		defer s.busy.Delete(row.ID)
		var cur store.Certificate
		if err := s.d.Store.DB.First(&cur, row.ID).Error; err != nil {
			return err
		}
		prevStatus := cur.Status
		s.d.Store.DB.Model(&cur).Updates(map[string]any{"status": "applying", "error": ""})
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := s.obtain(ctx, t, &cur); err != nil {
			st := "failed"
			if prevStatus == "valid" && cur.NotAfter != nil && time.Now().Before(*cur.NotAfter) {
				st = "valid" // 续期失败时原证书仍可用
			}
			s.d.Store.DB.Model(&store.Certificate{}).Where("id = ?", cur.ID).Updates(map[string]any{"status": st, "error": err.Error()})
			return err
		}
		s.afterChange(ctx, cur.ID, t.Log)
		return nil
	})
}

// afterChange 在证书文件更新后重新加载使用它的网站和面板。
func (s *Service) afterChange(ctx context.Context, id uint, log func(string, ...any)) {
	if s.d.Web != nil {
		if sites := s.d.Web.CertInUse(id); len(sites) > 0 {
			if err := s.d.Web.Reapply(ctx, id); err != nil {
				log("重新加载网站失败：%v", err)
			} else {
				log("已重新加载使用这张证书的网站：%s", strings.Join(sites, "、"))
			}
		}
	}
	if s.d.OnRenewed != nil {
		s.d.OnRenewed(id)
	}
}

func (s *Service) issue(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	if row.Source != "acme" && row.Source != "certbot" {
		api.Fail(c, api.BadRequest("上传或引用的证书不能由面板申请"))
		return
	}
	if _, busy := s.busy.Load(row.ID); busy {
		api.Fail(c, api.BadRequest("这张证书正在申请中"))
		return
	}
	api.OK(c, s.Issue(row, api.User(c).UserID))
}

// upload 上传自有证书（新建，或替换已有的上传证书）。
func (s *Service) upload(c *gin.Context) {
	var req struct {
		ID     uint   `json:"id"`
		Cert   string `json:"cert"`
		Key    string `json:"key"`
		Remark string `json:"remark"`
	}
	if !api.Bind(c, &req) {
		return
	}
	if _, err := ParsePair([]byte(req.Cert), []byte(req.Key)); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	row := store.Certificate{Source: "upload", Remark: req.Remark}
	if req.ID != 0 {
		if err := s.d.Store.DB.First(&row, req.ID).Error; err != nil || row.Source != "upload" {
			api.Fail(c, api.BadRequest("只能替换上传的证书"))
			return
		}
		row.Remark = req.Remark
	} else if err := s.d.Store.DB.Create(&row).Error; err != nil {
		api.Fail(c, err)
		return
	}
	if err := s.writeFiles(&row, []byte(strings.TrimSpace(req.Cert)+"\n"), []byte(strings.TrimSpace(req.Key)+"\n")); err != nil {
		if req.ID == 0 {
			s.d.Store.DB.Delete(&row)
		}
		api.Fail(c, err)
		return
	}
	api.Target(c, row.Domains)
	if req.ID != 0 {
		s.afterChange(c.Request.Context(), row.ID, func(string, ...any) {})
	}
	api.OK(c, s.view(row))
}

func (s *Service) delete(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	v := s.view(row)
	if len(v.Sites) > 0 {
		api.Fail(c, api.BadRequest("网站 "+strings.Join(v.Sites, "、")+" 正在使用这张证书"))
		return
	}
	if v.Panel {
		api.Fail(c, api.BadRequest("面板 HTTPS 正在使用这张证书"))
		return
	}
	if row.Source == "acme" || row.Source == "upload" {
		os.RemoveAll(s.dir(row.ID))
	}
	if row.Source == "certbot" && row.CertbotRenewal != "" {
		// 还原 certbot 的续期配置
		os.Rename(row.CertbotRenewal+offSuffix, row.CertbotRenewal)
	}
	s.d.Store.DB.Delete(&row)
	api.OK(c, nil)
}

// download 下载证书和私钥（PEM 文本）。
func (s *Service) download(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	cb, err := os.ReadFile(row.CertPath)
	if err != nil {
		api.Fail(c, err)
		return
	}
	kb, err := os.ReadFile(row.KeyPath)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, gin.H{"cert": string(cb), "key": string(kb)})
}

/* ---------- 自动续期 ---------- */

// Run 定期检查需要续期的证书，并在证书即将到期或续期失败时通知。
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(s.renewInterval)
	defer tick.Stop()
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	for {
		s.RenewDue()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// RenewDue 续期所有到期的证书（逐个进行）。
func (s *Service) RenewDue() {
	var rows []store.Certificate
	s.d.Store.DB.Where("auto_renew = ? AND source IN ?", true, []string{"acme", "certbot"}).Find(&rows)
	now := time.Now()
	for _, r := range rows {
		if !renewDue(r, now) {
			continue
		}
		if _, busy := s.busy.Load(r.ID); busy {
			continue
		}
		slog.Info("自动续期证书", "domains", r.Domains)
		t := s.Issue(r, 0)
		// 等待完成再续下一张，避免同时向 CA 发起大量请求
		for {
			time.Sleep(2 * time.Second)
			rec, _, err := s.d.Tasks.Get(t.ID)
			if err != nil || rec.Status != "running" {
				if err == nil && rec.Status == "failed" && s.d.Notify != nil {
					s.d.Notify("error", "证书续期失败", r.Domains+"："+rec.Error)
				}
				break
			}
		}
	}
	// 手动管理的证书（上传、引用）只提醒
	var others []store.Certificate
	s.d.Store.DB.Where("source IN ?", []string{"upload", "file"}).Find(&others)
	for _, r := range others {
		if r.NotAfter != nil && time.Until(*r.NotAfter) < 14*24*time.Hour && s.d.Notify != nil {
			s.d.Notify("warning", "证书即将到期", fmt.Sprintf("%s 将于 %s 到期，请更换", r.Domains, r.NotAfter.Local().Format("2006-01-02")))
		}
	}
}

/* ---------- DNS 账号 ---------- */

type dnsView struct {
	store.DNSAccount
	Keys []string `json:"keys"`
}

func (s *Service) dnsAccounts(c *gin.Context) {
	var rows []store.DNSAccount
	s.d.Store.DB.Order("id").Find(&rows)
	out := make([]dnsView, 0, len(rows))
	for _, r := range rows {
		v := dnsView{DNSAccount: r, Keys: []string{}}
		if raw, err := s.d.Box.Open(r.Secret); err == nil {
			var m map[string]string
			json.Unmarshal(raw, &m)
			for k := range m {
				v.Keys = append(v.Keys, k)
			}
			sort.Strings(v.Keys)
		}
		out = append(out, v)
	}
	api.OK(c, out)
}

func (s *Service) dnsAccountSave(c *gin.Context) {
	var req struct {
		Name     string            `json:"name"`
		Provider string            `json:"provider"`
		Fields   map[string]string `json:"fields"`
	}
	if !api.Bind(c, &req) {
		return
	}
	var fields []string
	for _, p := range DNSProviders {
		if p.Key == req.Provider {
			fields = p.Fields
		}
	}
	if fields == nil {
		api.Fail(c, api.BadRequest("不支持的 DNS 服务商"))
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		api.Fail(c, api.BadRequest("请填写名称"))
		return
	}
	row := store.DNSAccount{Name: req.Name, Provider: req.Provider}
	old := map[string]string{}
	if id := c.Param("id"); id != "" {
		if err := s.d.Store.DB.First(&row, "id = ?", id).Error; err != nil {
			api.Fail(c, api.NotFound("DNS 账号不存在"))
			return
		}
		if raw, err := s.d.Box.Open(row.Secret); err == nil && row.Provider == req.Provider {
			json.Unmarshal(raw, &old)
		}
		row.Name, row.Provider = req.Name, req.Provider
	}
	cred := map[string]string{}
	for _, f := range fields {
		v := strings.TrimSpace(req.Fields[f])
		if v == "" {
			v = old[f] // 修改时留空表示不变
		}
		if v == "" {
			api.Fail(c, api.BadRequest("请填写 "+f))
			return
		}
		cred[f] = v
	}
	b, _ := json.Marshal(cred)
	row.Secret = s.d.Box.Seal(b)
	api.Target(c, row.Name)
	if err := s.d.Store.DB.Save(&row).Error; err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, row)
}

func (s *Service) dnsAccountDelete(c *gin.Context) {
	var row store.DNSAccount
	if err := s.d.Store.DB.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		api.Fail(c, api.NotFound("DNS 账号不存在"))
		return
	}
	var n int64
	s.d.Store.DB.Model(&store.Certificate{}).Where("challenge = ? AND dns_account_id = ?", "dns", row.ID).Count(&n)
	if n > 0 {
		api.Fail(c, api.BadRequest(fmt.Sprintf("有 %d 张证书使用这个 DNS 账号", n)))
		return
	}
	api.Target(c, row.Name)
	s.d.Store.DB.Delete(&row)
	api.OK(c, nil)
}

/* ---------- certbot 与 acme.sh ---------- */

const offSuffix = ".sshdesktop-off"

// External 是服务器上由其他工具管理的证书。
type External struct {
	Tool     string   `json:"tool"` // certbot / acme.sh
	Name     string   `json:"name"`
	Cert     string   `json:"cert"`
	Key      string   `json:"key"`
	Renewal  string   `json:"renewal,omitempty"`
	Info     *Info    `json:"info,omitempty"`
	Error    string   `json:"error,omitempty"`
	Migrated bool     `json:"migrated"`
	Notes    []string `json:"notes"`
}

// Externals 列出 certbot（/etc/letsencrypt/live）和 acme.sh（/root/.acme.sh）的证书。
func (s *Service) Externals() []External {
	out := []External{}
	live := filepath.Join(s.certbotRoot, "live")
	if entries, err := os.ReadDir(live); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			x := External{Tool: "certbot", Name: e.Name(), Cert: filepath.Join(live, e.Name(), "fullchain.pem"),
				Key: filepath.Join(live, e.Name(), "privkey.pem"), Renewal: filepath.Join(s.certbotRoot, "renewal", e.Name()+".conf")}
			if _, err := os.Stat(x.Cert); err != nil {
				continue
			}
			s.fill(&x)
			if x.Migrated {
				x.Notes = []string{"已迁移为面板续期，certbot 的续期配置已停用"}
			} else {
				x.Notes = []string{"续期仍由 certbot 负责（certbot.timer 或 cron）", "可以迁移为面板续期：面板接手续期，证书文件位置不变，引用它的站点不用修改"}
			}
			out = append(out, x)
		}
	}
	if entries, err := os.ReadDir(s.acmeShRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() || !strings.Contains(e.Name(), ".") || e.Name() == "ca" || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			dom := strings.TrimSuffix(e.Name(), "_ecc")
			d := filepath.Join(s.acmeShRoot, e.Name())
			x := External{Tool: "acme.sh", Name: e.Name(), Cert: filepath.Join(d, "fullchain.cer"), Key: filepath.Join(d, dom+".key")}
			if _, err := os.Stat(x.Cert); err != nil {
				continue
			}
			s.fill(&x)
			x.Notes = []string{"续期仍由 acme.sh 负责（acme.sh 的 cron 任务）", "需要改由面板续期时，在面板中为这些域名申请新证书，再执行 acme.sh --remove -d " + dom}
			out = append(out, x)
		}
	}
	return out
}

func (s *Service) fill(x *External) {
	if info, err := readPair(x.Cert, x.Key); err == nil {
		x.Info = &info
	} else {
		x.Error = err.Error()
	}
	var n int64
	s.d.Store.DB.Model(&store.Certificate{}).Where("cert_path = ? AND source = ?", x.Cert, "certbot").Count(&n)
	x.Migrated = n > 0
}

func (s *Service) external(c *gin.Context) { api.OK(c, s.Externals()) }

// migrate 把 certbot 的证书迁移为面板续期：停用 certbot 的续期配置（改名，可还原），
// 证书继续写在原来的位置，引用它的 Nginx 站点不用修改。
func (s *Service) migrate(c *gin.Context) {
	var req struct {
		Name         string `json:"name"`
		Challenge    string `json:"challenge"`
		DNSAccountID uint   `json:"dnsAccountId"`
		CA           string `json:"ca"`
		Renew        bool   `json:"renew"`
	}
	if !api.Bind(c, &req) {
		return
	}
	var x *External
	for _, e := range s.Externals() {
		if e.Tool == "certbot" && e.Name == req.Name {
			x = &e
			break
		}
	}
	if x == nil || x.Info == nil {
		api.Fail(c, api.NotFound("certbot 证书不存在或无法读取"))
		return
	}
	if x.Migrated {
		api.Fail(c, api.BadRequest("已经迁移过了"))
		return
	}
	ar := acmeReq{Domains: strings.Join(x.Info.Domains, " "), CA: req.CA, Challenge: req.Challenge, DNSAccountID: req.DNSAccountID, AutoRenew: true}
	if err := s.validate(&ar); err != nil {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	api.Target(c, x.Name)
	row := store.Certificate{Source: "certbot", CertPath: x.Cert, KeyPath: x.Key, CA: ar.CA, Challenge: ar.Challenge,
		DNSAccountID: ar.DNSAccountID, KeyType: ar.KeyType, AutoRenew: true, Remark: "从 certbot 迁移：" + x.Name}
	// 接管站点时登记过的同一证书文件，升级为迁移的证书
	s.d.Store.DB.Where("cert_path = ? AND source = ?", x.Cert, "file").First(&row)
	row.Source, row.CA, row.Challenge, row.DNSAccountID, row.KeyType, row.AutoRenew = "certbot", ar.CA, ar.Challenge, ar.DNSAccountID, ar.KeyType, true
	row.Remark = "从 certbot 迁移：" + x.Name
	applyInfo(&row, *x.Info)
	if _, err := os.Stat(x.Renewal); err == nil {
		if err := os.Rename(x.Renewal, x.Renewal+offSuffix); err != nil {
			api.Fail(c, err)
			return
		}
		row.CertbotRenewal = x.Renewal
	}
	if err := s.d.Store.DB.Save(&row).Error; err != nil {
		api.Fail(c, err)
		return
	}
	out := gin.H{"cert": s.view(row)}
	if req.Renew {
		out["task"] = s.Issue(row, api.User(c).UserID)
	}
	api.OK(c, out)
}
