package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ManifestName 是备份包中的第一个文件，描述备份内容，恢复时据此判断类型。
const ManifestName = ".sshdesktop-backup.json"

type Manifest struct {
	Format    int       `json:"format"`
	Type      string    `json:"type"` // dir / app / panel / website
	Source    string    `json:"source"`
	Plan      string    `json:"plan"`
	Host      string    `json:"host"`
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	// KeyID 是面板备份时主密钥的指纹
	KeyID string `json:"keyId,omitempty"`
	// App 是应用备份时的安装记录（JSON），用于在新服务器上恢复
	App json.RawMessage `json:"app,omitempty"`
	// Site 是网站备份时的站点记录（siteRecord 的 JSON）
	Site json.RawMessage `json:"site,omitempty"`
}

// tarWriter 包装 tar.Writer，统计写入的文件。
type tarWriter struct {
	tw    *tar.Writer
	files int
	bytes int64
}

func newTar(w io.Writer, m Manifest) (*tarWriter, *gzip.Writer, error) {
	gz := gzip.NewWriter(w)
	t := &tarWriter{tw: tar.NewWriter(gz)}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := t.file(ManifestName, b, 0o600); err != nil {
		return nil, nil, err
	}
	return t, gz, nil
}

func (t *tarWriter) file(name string, b []byte, mode int64) error {
	if err := t.tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(b)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := t.tw.Write(b)
	return err
}

// Excluder 判断相对路径是否被排除：模式匹配完整相对路径或任一级名称，例如 node_modules、*.log、cache/tmp。
type Excluder []string

func ParseExcludes(text string) Excluder {
	var out Excluder
	for _, l := range strings.Split(text, "\n") {
		if l = strings.Trim(strings.TrimSpace(l), "/"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (e Excluder) Match(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range e {
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if !strings.Contains(p, "/") {
			for _, part := range parts {
				if ok, _ := path.Match(p, part); ok {
					return true
				}
			}
		} else if strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

// addDir 把目录 root 打包到 prefix 下（prefix 为空时放在包的根目录）。不跟随符号链接，跳过 socket 等特殊文件。
func (t *tarWriter) addDir(ctx context.Context, root, prefix string, ex Excluder, log func(string, ...any)) error {
	root = filepath.Clean(root)
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if p == root {
				return err
			}
			log("跳过 %s：%s", p, err)
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel != "." && ex.Match(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			log("跳过 %s：%s", p, err)
			return nil
		}
		var link string
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, _ = os.Readlink(p)
		case info.IsDir(), info.Mode().IsRegular():
		default:
			return nil
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return nil
		}
		name := rel
		if prefix != "" {
			name = path.Join(prefix, rel)
		}
		if name == "." {
			return nil
		}
		if info.IsDir() {
			name += "/"
		}
		h.Name = name
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			h.Uid, h.Gid = int(st.Uid), int(st.Gid)
		}
		h.Uname, h.Gname = "", ""
		if !info.Mode().IsRegular() {
			return t.tw.WriteHeader(h)
		}
		f, err := os.Open(p)
		if err != nil {
			log("跳过 %s：%s", p, err)
			return nil
		}
		defer f.Close()
		if err := t.tw.WriteHeader(h); err != nil {
			return err
		}
		// 打包过程中文件变短时用 0 补齐，变长时只取开头，保证 tar 结构正确
		n, err := io.Copy(t.tw, io.LimitReader(f, h.Size))
		if err != nil {
			return err
		}
		if n < h.Size {
			log("%s 在打包时变小了", p)
			if _, err := io.CopyN(t.tw, zeroReader{}, h.Size-n); err != nil {
				return err
			}
		}
		t.files++
		t.bytes += h.Size
		return nil
	})
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func (t *tarWriter) close(gz *gzip.Writer) error {
	if err := t.tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// readManifest 读取备份包的清单（第一个文件）。
func readManifest(r io.Reader) (Manifest, error) {
	var m Manifest
	gz, err := gzip.NewReader(r)
	if err != nil {
		return m, errors.New("不是有效的备份文件（或密码错误）")
	}
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil || h.Name != ManifestName {
		return m, errors.New("备份文件中没有清单，不是 sshDesktop 的备份")
	}
	if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("备份清单格式错误：%w", err)
	}
	return m, nil
}

// extract 把备份包解压到 dst。strip 不为空时只解压该前缀下的内容并去掉前缀。
// 拒绝绝对路径、.. 和指向包外的硬链接，保留权限、属主和修改时间。
func extract(r io.Reader, dst, strip string) (Manifest, int, error) {
	var m Manifest
	gz, err := gzip.NewReader(r)
	if err != nil {
		return m, 0, errors.New("不是有效的备份文件（或密码错误）")
	}
	tr := tar.NewReader(gz)
	files := 0
	type dirTime struct {
		p    string
		t    time.Time
		mode os.FileMode
	}
	var dirs []dirTime
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return m, files, fmt.Errorf("备份文件已损坏：%w", err)
		}
		if h.Name == ManifestName {
			json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m)
			continue
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return m, files, fmt.Errorf("备份文件包含非法路径：%s", h.Name)
		}
		if strip != "" {
			if name != strip && !strings.HasPrefix(name, strip+"/") {
				continue
			}
			name = strings.TrimPrefix(strings.TrimPrefix(name, strip), "/")
			if name == "" {
				if h.Typeflag == tar.TypeDir { // 前缀目录自身对应 dst
					os.Lchown(dst, h.Uid, h.Gid)
					dirs = append(dirs, dirTime{dst, h.ModTime, os.FileMode(h.Mode) & os.ModePerm})
				}
				continue
			}
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return m, files, fmt.Errorf("备份文件包含非法路径：%s", h.Name)
		}
		p := filepath.Join(dst, filepath.FromSlash(name))
		mode := os.FileMode(h.Mode) & os.ModePerm
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o700); err != nil {
				return m, files, err
			}
			os.Lchown(p, h.Uid, h.Gid)
			dirs = append(dirs, dirTime{p, h.ModTime, mode})
			continue
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return m, files, err
			}
			os.Remove(p)
			f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return m, files, err
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return m, files, fmt.Errorf("备份文件已损坏：%w", err)
			}
			os.Lchown(p, h.Uid, h.Gid)
			os.Chmod(p, mode)
			files++
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return m, files, err
			}
			os.Remove(p)
			if err := os.Symlink(h.Linkname, p); err != nil {
				return m, files, err
			}
			os.Lchown(p, h.Uid, h.Gid)
			continue
		default:
			continue
		}
		os.Chtimes(p, h.ModTime, h.ModTime)
	}
	// 目录权限最后设置，避免只读目录挡住其中文件的解压
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Chmod(dirs[i].p, dirs[i].mode)
		os.Chtimes(dirs[i].p, dirs[i].t, dirs[i].t)
	}
	return m, files, nil
}
