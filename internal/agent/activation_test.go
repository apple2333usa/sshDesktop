package agent

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestActivationListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	f, err := ln.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fd := int(f.Fd())

	// 没有 LISTEN_PID 或不是本进程：不使用
	t.Setenv("LISTEN_PID", "1")
	t.Setenv("LISTEN_FDS", "1")
	if got, err := activationListener(fd); got != nil || err != nil {
		t.Fatalf("其他进程的 LISTEN_PID 不应使用：%v %v", got, err)
	}
	if os.Getenv("LISTEN_FDS") != "" {
		t.Fatal("应清除 LISTEN_* 环境变量")
	}

	dup, err := ln.(*net.UnixListener).File()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LISTEN_FDS", "1")
	got, err := activationListener(int(dup.Fd()))
	if err != nil || got == nil {
		t.Fatalf("应返回传入的 socket：%v %v", got, err)
	}
	defer got.Close()
	go func() {
		if c, err := got.Accept(); err == nil {
			c.Write([]byte("ok"))
			c.Close()
		}
	}()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	buf := make([]byte, 2)
	if _, err := c.Read(buf); err != nil || string(buf) != "ok" {
		t.Fatalf("读到 %q，%v", buf, err)
	}
}
