package files

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/bodgit/sevenzip"
	"github.com/gin-gonic/gin"
	"github.com/ulikunitz/xz"
)

// archiveFormat 根据文件名判断压缩格式。
func archiveFormat(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".zip"):
		return "zip"
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(n, ".tar.xz"), strings.HasSuffix(n, ".txz"):
		return "tar.xz"
	case strings.HasSuffix(n, ".tar.bz2"), strings.HasSuffix(n, ".tbz2"):
		return "tar.bz2"
	case strings.HasSuffix(n, ".tar"):
		return "tar"
	case strings.HasSuffix(n, ".7z"):
		return "7z"
	}
	return ""
}

func (s *Service) compress(c *gin.Context) {
	var req struct {
		Paths []string `json:"paths"`
		Dest  string   `json:"dest"` // 压缩包完整路径，扩展名决定格式：.zip / .tar.gz / .tar.xz
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	dest, err := cleanPath(req.Dest)
	if err != nil {
		api.Fail(c, err)
		return
	}
	format := archiveFormat(dest)
	if format != "zip" && format != "tar.gz" && format != "tar.xz" {
		api.Fail(c, api.Err(400, api.CodeUnsupported, "只支持压缩为 zip、tar.gz、tar.xz"))
		return
	}
	if _, err := f.Lstat(dest); err == nil {
		api.Fail(c, api.Conflict(api.CodeFileExists, "压缩包已存在："+path.Base(dest)))
		return
	}
	var srcs []string
	for _, p := range req.Paths {
		p, err := cleanPath(p)
		if err != nil {
			api.Fail(c, err)
			return
		}
		srcs = append(srcs, p)
	}
	if len(srcs) == 0 {
		api.Fail(c, api.BadRequest("没有要压缩的文件"))
		return
	}
	api.Target(c, strings.Join(srcs, ", ")+" -> "+dest)
	t := s.d.Tasks.Start("files.compress", "压缩为 "+path.Base(dest), api.User(c).UserID, c.Param("node"),
		func(ctx context.Context, t *task.Task) error {
			err := doCompress(ctx, t, f, srcs, dest, format)
			if err != nil {
				f.Remove(dest)
			}
			return err
		})
	api.OK(c, t)
}

func doCompress(ctx context.Context, t *task.Task, f vfs.FS, srcs []string, dest, format string) error {
	pr := &progress{t: t}
	for _, p := range srcs {
		if fi, err := f.Lstat(p); err == nil {
			if fi.IsDir() {
				sz, _, _, _ := walkSize(ctx, f, p)
				pr.total += sz
			} else {
				pr.total += fi.Size()
			}
		}
	}
	out, err := f.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	add := func(visit func(p, name string, fi fs.FileInfo) error) error {
		var walk func(p, name string) error
		walk = func(p, name string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if p == dest {
				return nil // 压缩包本身在被压缩的目录里
			}
			fi, err := f.Lstat(p)
			if err != nil {
				return err
			}
			if err := visit(p, name, fi); err != nil {
				return err
			}
			if fi.IsDir() {
				infos, err := f.ReadDir(p)
				if err != nil {
					return err
				}
				for _, ci := range infos {
					if err := walk(path.Join(p, ci.Name()), name+"/"+ci.Name()); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for _, p := range srcs {
			if err := walk(p, path.Base(p)); err != nil {
				return err
			}
		}
		return nil
	}
	copyIn := func(w io.Writer, p string) error {
		fh, err := f.Open(p)
		if err != nil {
			return err
		}
		defer fh.Close()
		_, err = io.Copy(w, &ctxReader{ctx: ctx, r: fh, pr: pr})
		return err
	}

	if format == "zip" {
		zw := zip.NewWriter(out)
		err := add(func(p, name string, fi fs.FileInfo) error {
			hdr, err := zip.FileInfoHeader(fi)
			if err != nil {
				return err
			}
			hdr.Name = name
			if fi.IsDir() {
				hdr.Name += "/"
			} else {
				hdr.Method = zip.Deflate
			}
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			switch {
			case fi.Mode()&fs.ModeSymlink != 0:
				target, _ := f.Readlink(p)
				_, err = io.WriteString(w, target)
				return err
			case fi.Mode().IsRegular():
				return copyIn(w, p)
			}
			return nil
		})
		if err != nil {
			zw.Close()
			return err
		}
		return zw.Close()
	}

	var cw io.WriteCloser
	if format == "tar.gz" {
		cw = gzip.NewWriter(out)
	} else {
		xw, err := xz.NewWriter(out)
		if err != nil {
			return err
		}
		cw = xw
	}
	tw := tar.NewWriter(cw)
	err = add(func(p, name string, fi fs.FileInfo) error {
		link := ""
		if fi.Mode()&fs.ModeSymlink != 0 {
			link, _ = f.Readlink(p)
		}
		hdr, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return nil // 设备文件等跳过
		}
		hdr.Name = name
		if fi.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uid, hdr.Gid = vfs.Owner(fi)
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			return copyIn(tw, p)
		}
		return nil
	})
	if err != nil {
		tw.Close()
		cw.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return cw.Close()
}

func (s *Service) extract(c *gin.Context) {
	var req struct {
		Path      string `json:"path"`
		Dest      string `json:"dest"`
		Overwrite bool   `json:"overwrite"`
	}
	if !api.Bind(c, &req) {
		return
	}
	f, ok := s.fs(c)
	if !ok {
		return
	}
	src, err := cleanPath(req.Path)
	if err != nil {
		api.Fail(c, err)
		return
	}
	dest, err := cleanPath(req.Dest)
	if err != nil {
		api.Fail(c, err)
		return
	}
	format := archiveFormat(src)
	if format == "" {
		api.Fail(c, api.Err(400, api.CodeUnsupported, "不支持的压缩格式，支持 zip、tar、tar.gz、tar.xz、tar.bz2、7z"))
		return
	}
	api.Target(c, src+" -> "+dest)
	t := s.d.Tasks.Start("files.extract", "解压 "+path.Base(src), api.User(c).UserID, c.Param("node"),
		func(ctx context.Context, t *task.Task) error {
			if err := f.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			return doExtract(ctx, t, f, src, dest, format, req.Overwrite)
		})
	api.OK(c, t)
}

// extractor 负责把压缩包中的条目安全地写到 dest 下。
type extractor struct {
	ctx       context.Context
	t         *task.Task
	f         vfs.FS
	dest      string
	overwrite bool
	pr        *progress
	skipped   int
}

// target 计算条目的目标路径，拒绝绝对路径和 .. 逃逸（zip slip）。
func (e *extractor) target(name string) (string, bool) {
	name = strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/")
	p := path.Join(e.dest, name)
	if p != e.dest && !strings.HasPrefix(p, strings.TrimSuffix(e.dest, "/")+"/") {
		e.t.Log("跳过不安全的路径：%s", name)
		return "", false
	}
	// 不允许经由压缩包内创建的符号链接写到目标目录之外
	for dir := path.Dir(p); dir != e.dest && strings.HasPrefix(dir, e.dest); dir = path.Dir(dir) {
		if fi, err := e.f.Lstat(dir); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			e.t.Log("跳过经由符号链接的路径：%s", name)
			return "", false
		}
	}
	return p, true
}

func (e *extractor) dir(name string, mode fs.FileMode) error {
	p, ok := e.target(name)
	if !ok {
		return nil
	}
	if err := e.f.MkdirAll(p, 0o755); err != nil {
		return err
	}
	if mode != 0 {
		e.f.Chmod(p, mode.Perm()|0o700)
	}
	return nil
}

func (e *extractor) file(name string, mode fs.FileMode, r io.Reader) error {
	p, ok := e.target(name)
	if !ok {
		return nil
	}
	if _, err := e.f.Lstat(p); err == nil {
		if !e.overwrite {
			e.skipped++
			return nil
		}
		if err := e.f.RemoveAll(p); err != nil {
			return err
		}
	}
	if err := e.f.MkdirAll(path.Dir(p), 0o755); err != nil {
		return err
	}
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := e.f.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, &ctxReader{ctx: e.ctx, r: r, pr: e.pr})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func (e *extractor) symlink(name, target string) error {
	p, ok := e.target(name)
	if !ok {
		return nil
	}
	resolved := target
	if !path.IsAbs(resolved) {
		resolved = path.Join(path.Dir(p), target)
	}
	if !strings.HasPrefix(path.Clean(resolved)+"/", strings.TrimSuffix(e.dest, "/")+"/") {
		e.t.Log("跳过指向目标目录之外的符号链接：%s -> %s", name, target)
		return nil
	}
	if _, err := e.f.Lstat(p); err == nil {
		if !e.overwrite {
			e.skipped++
			return nil
		}
		e.f.RemoveAll(p)
	}
	e.f.MkdirAll(path.Dir(p), 0o755)
	return e.f.Symlink(target, p)
}

func doExtract(ctx context.Context, t *task.Task, f vfs.FS, src, dest, format string, overwrite bool) error {
	fh, err := f.Open(src)
	if err != nil {
		return err
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		return err
	}
	e := &extractor{ctx: ctx, t: t, f: f, dest: path.Clean(dest), overwrite: overwrite, pr: &progress{t: t}}
	defer func() {
		if e.skipped > 0 {
			t.Log("已跳过 %d 个已存在的文件", e.skipped)
		}
	}()

	switch format {
	case "zip":
		zr, err := zip.NewReader(fh, fi.Size())
		if err != nil {
			return fmt.Errorf("无法读取 zip：%w", err)
		}
		for _, zf := range zr.File {
			e.pr.total += int64(zf.UncompressedSize64)
		}
		for _, zf := range zr.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			mode := zf.Mode()
			switch {
			case mode.IsDir() || strings.HasSuffix(zf.Name, "/"):
				err = e.dir(zf.Name, mode)
			case mode&fs.ModeSymlink != 0:
				rc, err2 := zf.Open()
				if err2 != nil {
					return err2
				}
				b, _ := io.ReadAll(io.LimitReader(rc, 4096))
				rc.Close()
				err = e.symlink(zf.Name, string(b))
			default:
				rc, err2 := zf.Open()
				if err2 != nil {
					return err2
				}
				err = e.file(zf.Name, mode, rc)
				rc.Close()
			}
			if err != nil {
				return err
			}
		}
		return nil
	case "7z":
		zr, err := sevenzip.NewReader(fh, fi.Size())
		if err != nil {
			return fmt.Errorf("无法读取 7z：%w", err)
		}
		for _, zf := range zr.File {
			e.pr.total += int64(zf.UncompressedSize)
		}
		for _, zf := range zr.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			mode := zf.Mode()
			if mode.IsDir() {
				err = e.dir(zf.Name, mode)
			} else {
				rc, err2 := zf.Open()
				if err2 != nil {
					return err2
				}
				err = e.file(zf.Name, mode, rc)
				rc.Close()
			}
			if err != nil {
				return err
			}
		}
		return nil
	}

	// tar 系列：按已读取的压缩包字节数计算进度，解压出的内容不再重复计数
	counted := &ctxReader{ctx: ctx, r: fh, pr: &progress{t: t, total: fi.Size()}}
	var r io.Reader = counted
	e.pr = nil
	switch format {
	case "tar.gz":
		gz, err := gzip.NewReader(counted)
		if err != nil {
			return fmt.Errorf("无法读取 gzip：%w", err)
		}
		defer gz.Close()
		r = gz
	case "tar.xz":
		xr, err := xz.NewReader(counted)
		if err != nil {
			return fmt.Errorf("无法读取 xz：%w", err)
		}
		r = xr
	case "tar.bz2":
		r = bzip2.NewReader(counted)
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("读取 tar 失败：%w", err)
		}
		mode := fs.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			err = e.dir(hdr.Name, mode)
		case tar.TypeReg, tar.TypeRegA:
			err = e.file(hdr.Name, mode, tr)
		case tar.TypeSymlink:
			err = e.symlink(hdr.Name, hdr.Linkname)
		case tar.TypeLink:
			// 硬链接按复制处理
			if src, ok := e.target(hdr.Linkname); ok {
				if in, err2 := e.f.Open(src); err2 == nil {
					err = e.file(hdr.Name, mode, in)
					in.Close()
				}
			}
		default:
			continue
		}
		if err != nil {
			return err
		}
	}
}
