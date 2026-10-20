package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Compose 标签：docker compose 给每个容器打上项目名、工作目录和配置文件。
const (
	LabelProject     = "com.docker.compose.project"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelService     = "com.docker.compose.service"
)

// ComposeFiles 是在目录中查找 Compose 文件时依次尝试的文件名。
var ComposeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}

// FindComposeFile 返回目录中的 Compose 文件；找不到时返回 os.ErrNotExist。
func FindComposeFile(dir string) (string, error) {
	for _, n := range ComposeFiles {
		p := filepath.Join(dir, n)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

// ComposeCommand 返回可用的 Compose 命令（优先 docker compose 插件，其次独立的 docker-compose）和版本号。
func ComposeCommand(ctx context.Context) ([]string, string, error) {
	try := func(args ...string) (string, bool) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, args[0], append(args[1:], "version", "--short")...).Output()
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}
	if v, ok := try("docker", "compose"); ok {
		return []string{"docker", "compose"}, v, nil
	}
	if v, ok := try("docker-compose"); ok {
		return []string{"docker-compose"}, v, nil
	}
	return nil, "", errors.New("没有找到 docker compose，请安装 Docker Compose 插件")
}

// Project 描述一个 Compose 项目的位置。
type Project struct {
	Name  string
	Dir   string
	Files []string // 为空时由 compose 在 Dir 中自动查找
	// EnvFile 为空时使用 Dir 下的 .env（compose 默认行为）
	EnvFile string
}

func (p Project) args() []string {
	a := []string{"-p", p.Name}
	for _, f := range p.Files {
		a = append(a, "-f", f)
	}
	if p.EnvFile != "" {
		a = append(a, "--env-file", p.EnvFile)
	}
	return a
}

// Compose 在项目目录执行 compose 子命令，输出（stdout 和 stderr）写入 out。
func Compose(ctx context.Context, p Project, out io.Writer, args ...string) error {
	base, _, err := ComposeCommand(ctx)
	if err != nil {
		return err
	}
	full := append(append(append([]string{}, base[1:]...), p.args()...), args...)
	cmd := exec.CommandContext(ctx, base[0], full...)
	cmd.Dir = p.Dir
	cmd.Env = append(os.Environ(), "COMPOSE_ANSI=never", "COMPOSE_PROGRESS=plain", "COMPOSE_STATUS_STDOUT=1")
	var tail bytes.Buffer
	w := io.Writer(&tailWriter{b: &tail})
	if out != nil {
		w = io.MultiWriter(out, w)
	}
	cmd.Stdout, cmd.Stderr = w, w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := strings.TrimSpace(tail.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 && out != nil {
			msg = msg[i+1:] // 完整输出已写入 out，错误只带最后一行
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("docker compose %s 失败：%s", strings.Join(args, " "), msg)
	}
	return nil
}

// ComposeOutput 执行 compose 子命令并返回输出。
func ComposeOutput(ctx context.Context, p Project, args ...string) (string, error) {
	var b bytes.Buffer
	err := Compose(ctx, p, &b, args...)
	return b.String(), err
}

// tailWriter 只保留最后 8 KB，用于错误信息。
type tailWriter struct{ b *bytes.Buffer }

func (t *tailWriter) Write(p []byte) (int, error) {
	t.b.Write(p)
	if t.b.Len() > 8<<10 {
		rest := append([]byte(nil), t.b.Bytes()[t.b.Len()-(8<<10):]...)
		t.b.Reset()
		t.b.Write(rest)
	}
	return len(p), nil
}

// ValidateCompose 用 docker compose config 校验目录 dir 中的 Compose 文件 file（可以是临时文件）。
func ValidateCompose(ctx context.Context, name, dir, file string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := ComposeOutput(ctx, Project{Name: name, Dir: dir, Files: []string{file}}, "config", "-q")
	return err
}
