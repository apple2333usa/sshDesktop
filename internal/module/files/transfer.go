package files

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/gin-gonic/gin"
)

const maxChunk = 64 << 20

// 分片上传：init 返回已接收的字节数，客户端从该位置继续，实现断点续传。
// 临时文件放在目标目录（.<文件名>.<id>.sdpart），完成后原子改名，避免跨文件系统复制。
type uploadReq struct {
	Dir       string `json:"dir" form:"dir"`
	Name      string `json:"name" form:"name"`
	Size      int64  `json:"size" form:"size"`
	Overwrite bool   `json:"overwrite" form:"overwrite"`
}

func (s *Service) uploadPaths(c *gin.Context, r uploadReq) (target, part string, err error) {
	dir, err := cleanPath(r.Dir)
	if err != nil {
		return "", "", err
	}
	if err := validName(r.Name); err != nil {
		return "", "", err
	}
	if r.Size < 0 {
		return "", "", api.BadRequest("文件大小无效")
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s|%d", api.User(c).UserID, dir, r.Name, r.Size)))
	id := hex.EncodeToString(h[:])[:16]
	return path.Join(dir, r.Name), path.Join(dir, "."+r.Name+"."+id+".sdpart"), nil
}

func (s *Service) uploadInit(c *gin.Context) {
	var req uploadReq
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	target, part, err := s.uploadPaths(c, req)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if fi, err := f.Stat(path.Dir(target)); err != nil || !fi.IsDir() {
		api.Fail(c, api.Err(http.StatusBadRequest, api.CodeNotDir, "目标目录不存在"))
		return
	}
	if fi, err := f.Lstat(target); err == nil {
		if !req.Overwrite || fi.IsDir() {
			api.Fail(c, api.Conflict(api.CodeFileExists, "已存在同名文件："+req.Name))
			return
		}
	}
	var offset int64
	if fi, err := f.Stat(part); err == nil {
		offset = fi.Size()
		if offset > req.Size {
			f.Remove(part)
			offset = 0
		}
	} else {
		fh, err := f.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			api.Fail(c, err)
			return
		}
		fh.Close()
	}
	api.OK(c, gin.H{"offset": offset})
}

func (s *Service) uploadChunk(c *gin.Context) {
	var req uploadReq
	if err := c.ShouldBindQuery(&req); err != nil {
		api.Fail(c, api.BadRequest("参数错误"))
		return
	}
	offset, _ := strconv.ParseInt(c.Query("offset"), 10, 64)
	f, ok := s.fs(c)
	if !ok {
		return
	}
	_, part, err := s.uploadPaths(c, req)
	if err != nil {
		api.Fail(c, err)
		return
	}
	fi, err := f.Stat(part)
	if err != nil {
		api.Fail(c, api.BadRequest("上传未初始化或已过期"))
		return
	}
	if fi.Size() != offset {
		e := api.Conflict(api.CodeConflict, "分片位置不匹配")
		e.Data = gin.H{"offset": fi.Size()}
		api.Fail(c, e)
		return
	}
	fh, err := f.OpenFile(part, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		api.Fail(c, err)
		return
	}
	n, err := io.Copy(fh, io.LimitReader(c.Request.Body, maxChunk))
	cerr := fh.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		api.Fail(c, err)
		return
	}
	if offset+n > req.Size {
		f.Remove(part)
		api.Fail(c, api.BadRequest("上传的数据超过文件大小"))
		return
	}
	api.OK(c, gin.H{"offset": offset + n})
}

func (s *Service) uploadComplete(c *gin.Context) {
	var req uploadReq
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	target, part, err := s.uploadPaths(c, req)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, target)
	fi, err := f.Stat(part)
	if err != nil {
		api.Fail(c, api.BadRequest("上传未初始化或已过期"))
		return
	}
	if fi.Size() != req.Size {
		e := api.Conflict(api.CodeConflict, "文件尚未传输完整")
		e.Data = gin.H{"offset": fi.Size()}
		api.Fail(c, e)
		return
	}
	if _, err := f.Lstat(target); err == nil && !req.Overwrite {
		api.Fail(c, api.Conflict(api.CodeFileExists, "已存在同名文件："+req.Name))
		return
	}
	f.Chmod(part, 0o644)
	if err := f.Rename(part, target); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, gin.H{"path": target})
}

func attachment(name string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, url.PathEscape(name))
}

// download 下载单个文件（支持 Range 断点续传）；目录或多个文件打包成 zip 流式下载。
func (s *Service) download(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	var paths []string
	for _, p := range c.QueryArray("path") {
		p, err := cleanPath(p)
		if err != nil {
			api.Fail(c, err)
			return
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		api.Fail(c, api.BadRequest("没有要下载的文件"))
		return
	}
	api.Target(c, strings.Join(paths, ", "))
	if len(paths) == 1 {
		fi, err := f.Stat(paths[0])
		if err != nil {
			api.Fail(c, err)
			return
		}
		if !fi.IsDir() {
			fh, err := f.Open(paths[0])
			if err != nil {
				api.Fail(c, err)
				return
			}
			defer fh.Close()
			c.Header("Content-Disposition", attachment(path.Base(paths[0])))
			c.Header("Content-Type", "application/octet-stream")
			http.ServeContent(c.Writer, c.Request, "", fi.ModTime(), fh)
			return
		}
	}
	name := "files-" + time.Now().Format("20060102-150405") + ".zip"
	if len(paths) == 1 {
		name = path.Base(paths[0]) + ".zip"
	}
	c.Header("Content-Disposition", attachment(name))
	c.Header("Content-Type", "application/zip")
	zw := zip.NewWriter(c.Writer)
	for _, p := range paths {
		if err := zipAdd(c, f, zw, p, path.Base(p)); err != nil {
			break // 响应头已发出，只能中断
		}
	}
	zw.Close()
}

func zipAdd(c *gin.Context, f vfs.FS, zw *zip.Writer, p, name string) error {
	if err := c.Request.Context().Err(); err != nil {
		return err
	}
	fi, err := f.Lstat(p)
	if err != nil {
		return nil
	}
	hdr, err := zip.FileInfoHeader(fi)
	if err != nil {
		return nil
	}
	hdr.Name = name
	switch {
	case fi.IsDir():
		hdr.Name += "/"
		if _, err := zw.CreateHeader(hdr); err != nil {
			return err
		}
		infos, _ := f.ReadDir(p)
		for _, ci := range infos {
			if err := zipAdd(c, f, zw, path.Join(p, ci.Name()), name+"/"+ci.Name()); err != nil {
				return err
			}
		}
	case fi.Mode()&fs.ModeSymlink != 0:
		t, _ := f.Readlink(p)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		io.WriteString(w, t)
	case fi.Mode().IsRegular():
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		fh, err := f.Open(p)
		if err != nil {
			return nil
		}
		_, err = io.Copy(w, fh)
		fh.Close()
		return err
	}
	return nil
}

// 允许在面板页面内直接预览的类型；其余一律作为附件下载。
var inlineTypes = []string{"image/", "video/", "audio/", "application/pdf", "text/plain"}

// raw 返回文件原始内容，用于图片、视频、PDF 预览。预览响应加 CSP sandbox，
// 防止上传的 HTML / SVG 在面板域名下执行脚本。
func (s *Service) raw(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	p, err := cleanPath(c.Query("path"))
	if err != nil {
		api.Fail(c, err)
		return
	}
	fi, err := f.Stat(p)
	if err != nil {
		api.Fail(c, err)
		return
	}
	if fi.IsDir() {
		api.Fail(c, api.BadRequest("这是一个目录"))
		return
	}
	fh, err := f.Open(p)
	if err != nil {
		api.Fail(c, err)
		return
	}
	defer fh.Close()
	ct := mime.TypeByExtension(strings.ToLower(path.Ext(p)))
	if ct == "" || strings.HasPrefix(ct, "text/") {
		ct = "text/plain; charset=utf-8"
	}
	inline := false
	if c.Query("inline") == "1" {
		for _, t := range inlineTypes {
			if strings.HasPrefix(ct, t) {
				inline = true
				break
			}
		}
	}
	if inline {
		c.Header("Content-Disposition", "inline")
		if ct == "application/pdf" {
			// 浏览器内置的 PDF 阅读器不能在 sandbox 中运行；PDF 不会被当作 HTML 解析
			c.Header("Content-Security-Policy", "frame-ancestors 'self'")
		} else {
			c.Header("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'")
		}
		c.Header("X-Frame-Options", "SAMEORIGIN")
	} else {
		c.Header("Content-Disposition", attachment(path.Base(p)))
		ct = "application/octet-stream"
	}
	c.Header("Content-Type", ct)
	c.Header("Cache-Control", "private, no-cache")
	http.ServeContent(c.Writer, c.Request, "", fi.ModTime(), fh)
}
