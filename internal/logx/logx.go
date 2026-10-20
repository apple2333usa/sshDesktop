// Package logx 配置 slog：同时输出到标准输出（journald）和按天切割的日志文件。
package logx

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const keepDays = 14

// Setup 初始化全局 logger。dir 为空时只输出到标准输出。
func Setup(dir, level string) (io.Closer, error) {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	var w io.Writer = os.Stdout
	var closer io.Closer = io.NopCloser(nil)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
		rw := &dailyWriter{dir: dir}
		w = io.MultiWriter(os.Stdout, rw)
		closer = rw
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv})))
	return closer, nil
}

type dailyWriter struct {
	mu   sync.Mutex
	dir  string
	day  string
	file *os.File
}

func (d *dailyWriter) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	day := time.Now().Format("2006-01-02")
	if d.file == nil || day != d.day {
		if d.file != nil {
			d.file.Close()
		}
		f, err := os.OpenFile(filepath.Join(d.dir, "sshdesktop-"+day+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return 0, err
		}
		d.file, d.day = f, day
		go cleanup(d.dir)
	}
	return d.file.Write(p)
}

func (d *dailyWriter) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file != nil {
		return d.file.Close()
	}
	return nil
}

func cleanup(dir string) {
	files, _ := filepath.Glob(filepath.Join(dir, "sshdesktop-*.log"))
	sort.Strings(files)
	cutoff := time.Now().AddDate(0, 0, -keepDays).Format("2006-01-02")
	for _, f := range files {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "sshdesktop-"), ".log")
		if day < cutoff {
			os.Remove(f)
		}
	}
}
