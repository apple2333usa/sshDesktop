// Package agentbin 提供要上传到远程节点的独立 agent 二进制。
//
// 发布版构建（make build / make dist）会把 amd64、arm64 两个 agent 嵌入面板，远程服务器不需要访问外网。
// 没有嵌入时（开发构建），从 GitHub Release 下载同版本的 agent 并校验 SHA-256，缓存到数据目录。
package agentbin

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed all:bin
var embedded embed.FS

const repo = "apple2333usa/sshDesktop"

// Name 返回某架构的 agent 文件名。
func Name(arch string) string { return "sshdesktop-agent-linux-" + arch }

// Arch 把 uname -m 的输出转换成 Go 的架构名；不支持时返回空。
func Arch(uname string) string {
	switch strings.TrimSpace(uname) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return ""
}

// Get 返回指定架构的 agent 二进制。version 是面板版本（例如 0.2.0），cacheDir 用于缓存下载结果。
func Get(ctx context.Context, arch, version, cacheDir string) ([]byte, error) {
	if b, err := embedded.ReadFile("bin/" + Name(arch)); err == nil && len(b) > 0 {
		return b, nil
	}
	cache := filepath.Join(cacheDir, "v"+version+"-"+Name(arch))
	if b, err := os.ReadFile(cache); err == nil && len(b) > 0 {
		return b, nil
	}
	if version == "" || version == "dev" {
		return nil, errors.New("这是开发构建，没有内置 agent；请用 make build 构建，或安装正式版")
	}
	b, err := download(ctx, arch, version)
	if err != nil {
		return nil, err
	}
	os.MkdirAll(cacheDir, 0o700)
	os.WriteFile(cache, b, 0o600)
	return b, nil
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s 失败：%s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func download(ctx context.Context, arch, version string) ([]byte, error) {
	base := "https://github.com/" + repo + "/releases/download/v" + version + "/"
	sums, err := fetch(ctx, base+"SHA256SUMS", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("面板没有内置 agent，从 GitHub 下载也失败了：%w", err)
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == Name(arch) {
			want = f[0]
		}
	}
	if want == "" {
		return nil, errors.New("发布包里没有 " + Name(arch))
	}
	b, err := fetch(ctx, base+Name(arch), 64<<20)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != want {
		return nil, errors.New("agent 的 SHA-256 校验失败")
	}
	return b, nil
}
