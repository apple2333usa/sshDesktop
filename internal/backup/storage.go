package backup

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Object 是存储中的一个备份文件。Key 为相对存储根目录的路径（用 / 分隔）。
type Object struct {
	Key      string    `json:"key"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// Storage 是备份目标的统一接口。
type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context) ([]Object, error)
	Close() error
}

// TargetConfig 是备份目标不含密钥的配置（JSON 存入数据库）。
type TargetConfig struct {
	Path      string `json:"path,omitempty"` // 本地目录、S3 前缀、WebDAV 目录、SFTP 目录
	Endpoint  string `json:"endpoint,omitempty"`
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	AccessKey string `json:"accessKey,omitempty"`
	PathStyle bool   `json:"pathStyle,omitempty"`
	URL       string `json:"url,omitempty"` // WebDAV 服务地址
	Username  string `json:"username,omitempty"`
	NodeID    string `json:"nodeId,omitempty"` // SFTP：服务器列表中的节点
}

// TargetSecret 是加密保存的密钥。
type TargetSecret struct {
	SecretKey string `json:"secretKey,omitempty"`
	Password  string `json:"password,omitempty"`
}

func validKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") {
		return fmt.Errorf("备份文件路径无效：%s", key)
	}
	for _, p := range strings.Split(key, "/") {
		if p == "" || p == "." || p == ".." {
			return fmt.Errorf("备份文件路径无效：%s", key)
		}
	}
	return nil
}

/* ---------- 本地目录与 SFTP（vfs） ---------- */

// fsStorage 用 vfs 实现本地目录和 SFTP 目标。
type fsStorage struct {
	fs    vfs.FS
	root  string
	close func()
}

// NewFS 返回以 root 为根目录的存储（root 不存在时自动创建）。
func NewFS(f vfs.FS, root string, closer func()) Storage {
	if closer == nil {
		closer = func() {}
	}
	return &fsStorage{fs: f, root: path.Clean(root), close: closer}
}

func (s *fsStorage) path(key string) string { return path.Join(s.root, key) }

func (s *fsStorage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := validKey(key); err != nil {
		return err
	}
	p := s.path(key)
	if err := s.fs.MkdirAll(path.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".part"
	f, err := s.fs.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, ctxReader{ctx, r})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		s.fs.Remove(tmp)
		return err
	}
	s.fs.Remove(p)
	return s.fs.Rename(tmp, p)
}

func (s *fsStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	return s.fs.Open(s.path(key))
}

func (s *fsStorage) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	err := s.fs.Remove(s.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (s *fsStorage) List(ctx context.Context) ([]Object, error) {
	var out []Object
	var walk func(dir, prefix string, depth int) error
	walk = func(dir, prefix string, depth int) error {
		list, err := s.fs.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, fi := range list {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			key := prefix + fi.Name()
			switch {
			case fi.IsDir() && depth < 4:
				if err := walk(path.Join(dir, fi.Name()), key+"/", depth+1); err != nil {
					return err
				}
			case fi.Mode().IsRegular() && isBackupName(fi.Name()):
				out = append(out, Object{Key: key, Size: fi.Size(), Modified: fi.ModTime()})
			}
		}
		return nil
	}
	if _, err := s.fs.Stat(s.root); errors.Is(err, fs.ErrNotExist) {
		return []Object{}, nil
	}
	err := walk(s.root, "", 0)
	return out, err
}

func (s *fsStorage) Close() error { s.close(); return nil }

func isBackupName(n string) bool {
	return strings.HasSuffix(n, ".tar.gz") || strings.HasSuffix(n, ".tar.gz.enc")
}

// ctxReader 在 ctx 取消后让复制尽快结束。
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

/* ---------- S3 兼容存储 ---------- */

type s3Storage struct {
	cl     *minio.Client
	bucket string
	prefix string
}

// NewS3 创建 S3 兼容存储（阿里云 OSS、腾讯云 COS、Cloudflare R2、MinIO 等）。
func NewS3(cfg TargetConfig, sec TargetSecret) (Storage, error) {
	ep := strings.TrimSpace(cfg.Endpoint)
	secure := true
	switch {
	case strings.HasPrefix(ep, "http://"):
		ep, secure = strings.TrimPrefix(ep, "http://"), false
	case strings.HasPrefix(ep, "https://"):
		ep = strings.TrimPrefix(ep, "https://")
	}
	ep = strings.TrimRight(ep, "/")
	if ep == "" || strings.Contains(ep, "/") {
		return nil, errors.New("Endpoint 应为域名（可带端口），例如 s3.amazonaws.com 或 https://<账号>.r2.cloudflarestorage.com")
	}
	if cfg.Bucket == "" || cfg.AccessKey == "" || sec.SecretKey == "" {
		return nil, errors.New("请填写 Bucket、Access Key 和 Secret Key")
	}
	opt := &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, sec.SecretKey, ""),
		Secure: secure, Region: cfg.Region, BucketLookup: minio.BucketLookupAuto,
	}
	if cfg.PathStyle {
		opt.BucketLookup = minio.BucketLookupPath
	}
	cl, err := minio.New(ep, opt)
	if err != nil {
		return nil, err
	}
	prefix := strings.Trim(cfg.Path, "/")
	if prefix != "" {
		prefix += "/"
	}
	return &s3Storage{cl: cl, bucket: cfg.Bucket, prefix: prefix}, nil
}

func (s *s3Storage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := validKey(key); err != nil {
		return err
	}
	_, err := s.cl.PutObject(ctx, s.bucket, s.prefix+key, r, size, minio.PutObjectOptions{
		ContentType: "application/octet-stream", PartSize: 64 << 20,
	})
	return err
}

func (s *s3Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	obj, err := s.cl.GetObject(ctx, s.bucket, s.prefix+key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, err
	}
	return obj, nil
}

func (s *s3Storage) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	return s.cl.RemoveObject(ctx, s.bucket, s.prefix+key, minio.RemoveObjectOptions{})
}

func (s *s3Storage) List(ctx context.Context) ([]Object, error) {
	out := []Object{}
	for o := range s.cl.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix, Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		if isBackupName(path.Base(o.Key)) {
			out = append(out, Object{Key: strings.TrimPrefix(o.Key, s.prefix), Size: o.Size, Modified: o.LastModified})
		}
	}
	return out, nil
}

func (s *s3Storage) Close() error { return nil }

/* ---------- WebDAV ---------- */

type davStorage struct {
	base     *url.URL // 以 / 结尾
	root     *url.URL // 用户填写的服务地址（假定已存在），base = root + 目录
	sub      string   // 目录（相对 root）
	user     string
	password string
	hc       *http.Client
}

// NewWebDAV 创建 WebDAV 存储（坚果云、Nextcloud、群晖等）。
func NewWebDAV(cfg TargetConfig, sec TargetSecret) (Storage, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("WebDAV 地址应以 http:// 或 https:// 开头")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	root := *u
	sub := strings.Trim(cfg.Path, "/")
	u.Path = strings.TrimRight(u.Path+sub, "/") + "/"
	return &davStorage{base: u, root: &root, sub: sub, user: cfg.Username, password: sec.Password, hc: &http.Client{Timeout: 0}}, nil
}

func (d *davStorage) url(rel string) string {
	u := *d.base
	u.Path += rel
	return u.String()
}

func (d *davStorage) do(ctx context.Context, method, rel string, body io.Reader, size int64, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.url(rel), body)
	if err != nil {
		return nil, err
	}
	if size >= 0 && body != nil {
		req.ContentLength = size
	}
	if d.user != "" || d.password != "" {
		req.SetBasicAuth(d.user, d.password)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return d.hc.Do(req)
}

func davErr(resp *http.Response, what string) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(b))
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New(what + "失败：用户名或密码错误")
	}
	if len(msg) > 200 || strings.Contains(msg, "<") {
		msg = ""
	}
	return fmt.Errorf("%s失败：HTTP %d %s", what, resp.StatusCode, msg)
}

// mkcol 从服务地址开始逐级创建目录（已存在时服务器返回 405，忽略）。
func (d *davStorage) mkcol(ctx context.Context, dir string) error {
	full := strings.Trim(path.Join(d.sub, dir), "/")
	if full == "" || full == "." {
		return nil
	}
	cur := ""
	for _, p := range strings.Split(full, "/") {
		if p == "" || p == "." {
			continue
		}
		cur += p + "/"
		u := *d.root
		u.Path += cur
		req, err := http.NewRequestWithContext(ctx, "MKCOL", u.String(), nil)
		if err != nil {
			return err
		}
		if d.user != "" || d.password != "" {
			req.SetBasicAuth(d.user, d.password)
		}
		resp, err := d.hc.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 400 && resp.StatusCode != http.StatusMethodNotAllowed {
			return davErr(resp, "创建目录")
		}
	}
	return nil
}

func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func (d *davStorage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	if err := validKey(key); err != nil {
		return err
	}
	if err := d.mkcol(ctx, path.Dir(key)); err != nil {
		return err
	}
	resp, err := d.do(ctx, http.MethodPut, escapeKey(key), r, size, map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return davErr(resp, "上传")
	}
	return nil
}

func (d *davStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	resp, err := d.do(ctx, http.MethodGet, escapeKey(key), nil, -1, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, davErr(resp, "下载")
	}
	return resp.Body, nil
}

func (d *davStorage) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	resp, err := d.do(ctx, http.MethodDelete, escapeKey(key), nil, -1, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		return davErr(resp, "删除")
	}
	return nil
}

type multistatus struct {
	Responses []struct {
		Href  string `xml:"href"`
		Props []struct {
			Prop struct {
				Length       int64  `xml:"getcontentlength"`
				Modified     string `xml:"getlastmodified"`
				ResourceType struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
			} `xml:"prop"`
		} `xml:"propstat"`
	} `xml:"response"`
}

const propfind = `<?xml version="1.0" encoding="utf-8"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/><d:getcontentlength/><d:getlastmodified/></d:prop></d:propfind>`

func (d *davStorage) List(ctx context.Context) ([]Object, error) {
	out := []Object{}
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		resp, err := d.do(ctx, "PROPFIND", rel, strings.NewReader(propfind), int64(len(propfind)),
			map[string]string{"Depth": "1", "Content-Type": "application/xml"})
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound && rel == "" {
			return nil
		}
		if resp.StatusCode != http.StatusMultiStatus {
			return davErr(resp, "列出文件")
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return err
		}
		var ms multistatus
		if err := xml.NewDecoder(bytes.NewReader(b)).Decode(&ms); err != nil {
			return fmt.Errorf("解析 WebDAV 响应失败：%w", err)
		}
		for _, r := range ms.Responses {
			hu, err := url.Parse(r.Href)
			if err != nil {
				continue
			}
			p, _ := url.PathUnescape(hu.Path)
			basePath, _ := url.PathUnescape(d.base.Path)
			if !strings.HasPrefix(p, basePath) {
				continue
			}
			if strings.TrimSuffix(p, "/") == strings.TrimSuffix(basePath+unescape(rel), "/") {
				continue // 目录自身
			}
			key := strings.TrimPrefix(p, basePath)
			var length int64
			var mod time.Time
			dir := false
			for _, ps := range r.Props {
				if ps.Prop.ResourceType.Collection != nil {
					dir = true
				}
				if ps.Prop.Length > 0 {
					length = ps.Prop.Length
				}
				if t, err := http.ParseTime(ps.Prop.Modified); err == nil {
					mod = t
				}
			}
			key = strings.TrimSuffix(key, "/")
			if dir {
				if depth < 4 {
					if err := walk(escapeKey(key)+"/", depth+1); err != nil {
						return err
					}
				}
				continue
			}
			if isBackupName(path.Base(key)) {
				out = append(out, Object{Key: key, Size: length, Modified: mod})
			}
		}
		return nil
	}
	err := walk("", 0)
	return out, err
}

func unescape(s string) string {
	u, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return u
}

func (d *davStorage) Close() error { return nil }

// sortObjects 按修改时间从新到旧排序。
func sortObjects(list []Object) {
	sort.Slice(list, func(i, j int) bool { return list[i].Modified.After(list[j].Modified) })
}

// localRoot 规范化本地目录。
func localRoot(p string) (string, error) {
	p = filepath.Clean(strings.TrimSpace(p))
	if !filepath.IsAbs(p) {
		return "", errors.New("本地目录必须是绝对路径")
	}
	if p == "/" {
		return "", errors.New("不能使用根目录作为备份目录")
	}
	return p, nil
}
