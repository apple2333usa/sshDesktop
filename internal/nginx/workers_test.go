package nginx

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// 重新加载后立即请求，应该已经是新配置的响应。
func TestReloadWaitsForNewWorkers(t *testing.T) {
	bin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("没有安装 nginx")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	dir := t.TempDir()
	conf := filepath.Join(dir, "nginx.conf")
	write := func(body string) {
		c := fmt.Sprintf(`worker_processes 2;
pid %[1]s/nginx.pid;
error_log %[1]s/error.log;
events { worker_connections 64; }
http {
    access_log off;
    client_body_temp_path %[1]s/tmp; proxy_temp_path %[1]s/tmp; fastcgi_temp_path %[1]s/tmp; uwsgi_temp_path %[1]s/tmp; scgi_temp_path %[1]s/tmp;
    server { listen 127.0.0.1:%[2]d; location / { return 200 %[3]q; } }
}
`, dir, port, body)
		if err := os.WriteFile(conf, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("v0")
	if out, err := exec.Command(bin, "-c", conf).CombinedOutput(); err != nil {
		t.Skipf("无法启动测试用 nginx：%v %s", err, out)
	}
	rt := Runtime{Mode: ModeHost, Binary: bin, Conf: conf}
	t.Cleanup(func() { rt.cmd(context.Background(), "-s", "stop").Run() })

	get := func() string {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			return err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	if b := get(); b != "v0" {
		t.Fatalf("初始响应：%q", b)
	}
	for i := 1; i <= 5; i++ {
		want := fmt.Sprintf("v%d", i)
		write(want)
		if err := rt.signalReload(context.Background()); err != nil {
			t.Fatal(err)
		}
		if b := get(); b != want {
			t.Fatalf("第 %d 次重新加载后立即请求得到 %q，应为 %q", i, b, want)
		}
	}
}
