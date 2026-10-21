package cert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge/http01"
	"github.com/go-acme/lego/v5/lego"
	legolog "github.com/go-acme/lego/v5/log"
	"github.com/go-acme/lego/v5/providers/dns/alidns"
	"github.com/go-acme/lego/v5/providers/dns/cloudflare"
	"github.com/go-acme/lego/v5/providers/dns/tencentcloud"
	"github.com/go-acme/lego/v5/providers/http/webroot"
	"github.com/go-acme/lego/v5/registration"
)

// CA 列表：界面中的选项 → ACME 目录地址。
var CAs = map[string]string{
	"letsencrypt":         lego.DirectoryURLLetsEncrypt,
	"letsencrypt-staging": lego.DirectoryURLLetsEncryptStaging,
	"zerossl":             lego.DirectoryURLZeroSSL,
}

func caURL(c *store.Certificate) (string, error) {
	if c.CA == "custom" {
		if !strings.HasPrefix(c.CAURL, "https://") {
			return "", errors.New("自定义 CA 请填写 https:// 开头的 ACME 目录地址")
		}
		return c.CAURL, nil
	}
	if u, ok := CAs[c.CA]; ok {
		return u, nil
	}
	return "", fmt.Errorf("未知的证书颁发机构 %q", c.CA)
}

var keyTypes = map[string]certcrypto.KeyType{
	"ec256": certcrypto.EC256, "ec384": certcrypto.EC384, "rsa2048": certcrypto.RSA2048, "rsa4096": certcrypto.RSA4096,
}

type acmeUser struct {
	email string
	reg   *acme.ExtendedAccount
	key   crypto.Signer
}

func (u *acmeUser) GetEmail() string                       { return u.email }
func (u *acmeUser) GetRegistration() *acme.ExtendedAccount { return u.reg }
func (u *acmeUser) GetPrivateKey() crypto.Signer           { return u.key }

// account 读取或创建某个 CA 的 ACME 账号。
func (s *Service) account(ctx context.Context, url string, log func(string, ...any)) (*lego.Client, error) {
	var row store.AcmeAccount
	email := s.Email()
	found := s.d.Store.DB.Where("ca_url = ?", url).First(&row).Error == nil
	u := &acmeUser{email: email}
	if found {
		pemKey, err := s.d.Box.Open(row.Key)
		if err != nil {
			return nil, fmt.Errorf("解密 ACME 账号私钥失败：%w", err)
		}
		blk, _ := pem.Decode(pemKey)
		if blk == nil {
			return nil, errors.New("ACME 账号私钥损坏")
		}
		k, err := x509.ParseECPrivateKey(blk.Bytes)
		if err != nil {
			return nil, err
		}
		u.key = k
		var reg acme.ExtendedAccount
		if json.Unmarshal([]byte(row.Registration), &reg) == nil && reg.Location != "" {
			u.reg = &reg
		}
		u.email = row.Email
	} else {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		u.key = k
	}
	cfg := lego.NewConfig(u)
	cfg.CADirURL = url
	cfg.UserAgent = "sshdesktop/" + s.d.Version
	if s.httpClient != nil {
		cfg.HTTPClient = s.httpClient
	}
	client, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("连接证书颁发机构失败：%w", err)
	}
	if u.reg == nil {
		log("注册 ACME 账号（%s）", url)
		var reg *acme.ExtendedAccount
		if url == lego.DirectoryURLZeroSSL {
			if email == "" {
				return nil, errors.New("使用 ZeroSSL 需要先在证书设置中填写邮箱")
			}
			reg, err = registration.RegisterWithZeroSSL(ctx, client.Registration, email)
		} else {
			reg, err = client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		}
		if err != nil {
			return nil, fmt.Errorf("注册 ACME 账号失败：%w", err)
		}
		u.reg = reg
		der, _ := x509.MarshalECPrivateKey(u.key.(*ecdsa.PrivateKey))
		regJSON, _ := json.Marshal(reg)
		row = store.AcmeAccount{ID: row.ID, CAURL: url, Email: email, Registration: string(regJSON),
			Key: s.d.Box.Seal(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))}
		if err := s.d.Store.DB.Save(&row).Error; err != nil {
			return nil, err
		}
	}
	return client, nil
}

// taskLogger 把 lego 的日志写进任务日志。
type taskLogger struct{ log func(string, ...any) }

func (h taskLogger) Enabled(context.Context, slog.Level) bool { return true }
func (h taskLogger) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	h.log("%s", b.String())
	return nil
}
func (h taskLogger) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h taskLogger) WithGroup(string) slog.Handler      { return h }

// obtain 申请（或续期）证书，成功后写入证书文件并更新记录。
func (s *Service) obtain(ctx context.Context, t *task.Task, row *store.Certificate) error {
	s.issueMu.Lock()
	defer s.issueMu.Unlock()
	prev := legolog.Default()
	legolog.SetDefault(slog.New(taskLogger{log: t.Log}))
	defer legolog.SetDefault(prev)

	url, err := caURL(row)
	if err != nil {
		return err
	}
	domains := splitDomains(row.Domains)
	client, err := s.account(ctx, url, t.Log)
	if err != nil {
		return err
	}
	var cleanup func()
	switch row.Challenge {
	case "dns":
		p, err := s.dnsProvider(row.DNSAccountID)
		if err != nil {
			return err
		}
		if err := client.Challenge.SetDNS01Provider(p); err != nil {
			return err
		}
		t.Log("使用 DNS 验证")
	default:
		for _, d := range domains {
			if strings.HasPrefix(d, "*.") {
				return errors.New("通配符证书只能用 DNS 验证")
			}
		}
		cleanup, err = s.httpChallenge(ctx, client, domains, t.Log)
		if err != nil {
			return err
		}
	}
	if cleanup != nil {
		defer cleanup()
	}
	kt, ok := keyTypes[row.KeyType]
	if !ok {
		kt = certcrypto.EC256
	}
	t.Log("申请证书：%s", strings.Join(domains, "、"))
	res, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{Domains: domains, Bundle: true, KeyType: kt})
	if err != nil {
		return fmt.Errorf("申请失败：%w", err)
	}
	if err := s.writeFiles(row, res.Certificate, res.PrivateKey); err != nil {
		return err
	}
	t.Log("证书已保存：%s", row.CertPath)
	return nil
}

// httpChallenge 配置 HTTP-01 验证：有正在运行的 Web 服务器时把验证文件放进网站的验证目录，
// 否则在 80 端口临时启动内置的验证服务器。
func (s *Service) httpChallenge(ctx context.Context, client *lego.Client, domains []string, log func(string, ...any)) (func(), error) {
	if s.d.Web != nil {
		cleanup, err := s.d.Web.PrepareChallenge(ctx, domains)
		if err == nil {
			p, err := webroot.NewHTTPProvider(s.d.Web.AcmeRoot())
			if err != nil {
				cleanup()
				return nil, err
			}
			if err := client.Challenge.SetHTTP01Provider(p); err != nil {
				cleanup()
				return nil, err
			}
			log("使用 HTTP 验证：验证文件放在 %s，由 Nginx 提供", s.d.Web.AcmeRoot())
			return cleanup, nil
		}
		log("网站的 Web 服务器不可用（%v），改用内置验证服务器", err)
	}
	port := s.httpPort
	if port == "" {
		port = "80"
	}
	if err := client.Challenge.SetHTTP01Provider(http01.NewProviderServer("", port)); err != nil {
		return nil, err
	}
	log("使用 HTTP 验证：在 %s 端口临时启动验证服务器（需要该端口空闲并能从公网访问）", port)
	return nil, nil
}

func (s *Service) dnsProvider(id uint) (interface {
	Present(ctx context.Context, domain, token, keyAuth string) error
	CleanUp(ctx context.Context, domain, token, keyAuth string) error
}, error) {
	var acc store.DNSAccount
	if err := s.d.Store.DB.First(&acc, id).Error; err != nil {
		return nil, errors.New("DNS 账号不存在")
	}
	raw, err := s.d.Box.Open(acc.Secret)
	if err != nil {
		return nil, fmt.Errorf("解密 DNS 账号失败：%w", err)
	}
	var cred map[string]string
	json.Unmarshal(raw, &cred)
	switch acc.Provider {
	case "cloudflare":
		cfg := cloudflare.NewDefaultConfig()
		cfg.AuthToken = cred["token"]
		return cloudflare.NewDNSProviderConfig(cfg)
	case "alidns":
		cfg := alidns.NewDefaultConfig()
		cfg.APIKey, cfg.SecretKey = cred["accessKeyId"], cred["accessKeySecret"]
		return alidns.NewDNSProviderConfig(cfg)
	case "tencentcloud":
		cfg := tencentcloud.NewDefaultConfig()
		cfg.SecretID, cfg.SecretKey = cred["secretId"], cred["secretKey"]
		return tencentcloud.NewDNSProviderConfig(cfg)
	}
	return nil, fmt.Errorf("不支持的 DNS 服务商 %q", acc.Provider)
}

// DNSProviders 是支持的 DNS 服务商及其凭据字段。
var DNSProviders = []struct {
	Key    string   `json:"key"`
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}{
	{"cloudflare", "Cloudflare", []string{"token"}},
	{"alidns", "阿里云 DNS", []string{"accessKeyId", "accessKeySecret"}},
	{"tencentcloud", "腾讯云 DNSPod", []string{"secretId", "secretKey"}},
}

// renewDue 表示证书需要续期：剩余有效期不到 30 天；有效期较短的证书（如 45 天）在剩余三分之一时续期。
func renewDue(c store.Certificate, now time.Time) bool {
	if !c.AutoRenew || c.NotAfter == nil {
		return false
	}
	before := 30 * 24 * time.Hour
	if c.NotBefore != nil {
		if life := c.NotAfter.Sub(*c.NotBefore); life < 90*24*time.Hour {
			before = life / 3
		}
	}
	return c.NotAfter.Sub(now) < before
}
