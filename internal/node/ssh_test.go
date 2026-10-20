package node

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/metric"
	"github.com/apple2333usa/sshdesktop/internal/node/sshtest"
)

func testConfig(s *sshtest.Server) SSHConfig {
	return SSHConfig{ID: "n1", Host: s.Host, Port: s.Port, User: sshtest.User, Password: sshtest.Password, HostKey: s.HostKey}
}

func online(t *testing.T, n *SSH) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := n.Client(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestScanAndTestConnection(t *testing.T) {
	s := sshtest.Start(t)
	ctx := context.Background()
	k, err := ScanHostKey(ctx, s.Host, s.Port)
	if err != nil {
		t.Fatal(err)
	}
	if string(k.Marshal()) != string(s.HostKey.Marshal()) {
		t.Fatal("扫描到的主机密钥不一致")
	}
	cfg := testConfig(s)
	if err := TestConnection(ctx, cfg); err != nil {
		t.Fatalf("密码认证失败：%v", err)
	}
	keyCfg := cfg
	keyCfg.Password, keyCfg.Key = "", s.ClientKey
	if err := TestConnection(ctx, keyCfg); err != nil {
		t.Fatalf("私钥认证失败：%v", err)
	}
	bad := cfg
	bad.Password = "wrong"
	if err := TestConnection(ctx, bad); err == nil || !strings.Contains(err.Error(), "认证失败") {
		t.Fatalf("错误密码应提示认证失败，得到 %v", err)
	}
	other := sshtest.Start(t)
	mismatch := cfg
	mismatch.HostKey = other.HostKey
	var hk *HostKeyMismatchError
	if err := TestConnection(ctx, mismatch); !errors.As(err, &hk) {
		t.Fatalf("主机密钥不一致时应拒绝连接，得到 %v", err)
	}
}

func TestSSHNodeExecAndScript(t *testing.T) {
	s := sshtest.Start(t)
	var states []string
	n := NewSSH(testConfig(s), func(_ string, st Status) { states = append(states, st.State) })
	defer n.Close()
	online(t, n)
	ctx := context.Background()
	// 参数逐个加引号，不做 shell 展开
	res, err := n.Exec(ctx, "printf", "%s|", "a b", "$HOME", "it's")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Stdout); got != "a b|$HOME|it's|" {
		t.Fatalf("Exec 参数处理错误：%q", got)
	}
	res, _ = n.Exec(ctx, "sh", "-c", "exit 3")
	if res.ExitCode != 3 {
		t.Fatalf("退出码应为 3，得到 %d", res.ExitCode)
	}
	var out strings.Builder
	code, err := n.RunScript(ctx, "echo one; echo two >&2; exit 5", &out)
	if err != nil || code != 5 || !strings.Contains(out.String(), "one") || !strings.Contains(out.String(), "two") {
		t.Fatalf("RunScript：code=%d err=%v out=%q", code, err, out.String())
	}
	tctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := n.RunScript(tctx, "sleep 30", io.Discard); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("超时应终止命令：err=%v 用时 %s", err, time.Since(start))
	}
}

func TestSSHNodeReconnect(t *testing.T) {
	s := sshtest.Start(t)
	n := NewSSH(testConfig(s), nil)
	defer n.Close()
	online(t, n)
	s.DropConnections()
	// 断线后会自动重连
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		res, err := n.Exec(ctx, "echo", "back")
		cancel()
		if err == nil && strings.TrimSpace(string(res.Stdout)) == "back" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("断线后没有重连")
}

func TestSSHNodeSFTP(t *testing.T) {
	s := sshtest.Start(t)
	n := NewSSH(testConfig(s), nil)
	defer n.Close()
	online(t, n)
	dir := t.TempDir()
	fs := n.FS()
	if err := fs.MkdirAll(filepath.Join(dir, "a/b"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "a/b/hello.txt")
	f, err := fs.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(f, "hello sftp")
	f.Close()
	fi, err := os.Stat(p)
	if err != nil || fi.Size() != 10 || fi.Mode().Perm() != 0o640 {
		t.Fatalf("SFTP 写入结果不对：%v %v", fi, err)
	}
	if err := fs.Rename(p, p+".bak"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("不存在的文件应返回 ErrNotExist，得到 %v", err)
	}
	list, err := fs.ReadDir(filepath.Join(dir, "a/b"))
	if err != nil || len(list) != 1 || list[0].Name() != "hello.txt.bak" {
		t.Fatalf("ReadDir：%v %v", list, err)
	}
	if err := fs.RemoveAll(filepath.Join(dir, "a")); err != nil {
		t.Fatal(err)
	}
	if fs.HomeDir() == "" {
		t.Fatal("家目录为空")
	}
	if len(fs.MountList()) == 0 {
		t.Fatal("挂载点列表为空")
	}
	release, err := n.AcquireTransfer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

type fakeProvider struct{ bc *agent.Broadcaster }

func (p fakeProvider) Info() agent.Info                         { return agent.Info{Hostname: "fake-host"} }
func (p fakeProvider) Subscribe() (<-chan agent.Sample, func()) { return p.bc.Subscribe() }
func (p fakeProvider) History(from, to int64) []metric.Metric {
	return []metric.Metric{{TS: from, CPU: 12}}
}
func (p fakeProvider) Traffic(from, to int) []agent.TrafficRow {
	return []agent.TrafficRow{{Day: to, Iface: "eth0", Rx: 100, Tx: 200}}
}

func TestAgentOverSSH(t *testing.T) {
	s := sshtest.Start(t)
	sock := filepath.Join(t.TempDir(), "agent.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := fakeProvider{bc: agent.NewBroadcaster()}
	go agent.Serve(ctx, sock, p, "9.9.9", false)
	n := NewSSH(testConfig(s), nil)
	defer n.Close()
	online(t, n)
	cl := agent.NewClient(func(ctx context.Context) (net.Conn, error) { return n.DialUnix(ctx, sock) })
	var info agent.AgentInfo
	var err error
	for i := 0; i < 50; i++ {
		if info, err = cl.Info(ctx); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || info.Hostname != "fake-host" || info.AgentVersion != "9.9.9" || info.Panel {
		t.Fatalf("经 SSH 读取 agent 信息失败：%+v %v", info, err)
	}
	rows, err := cl.Traffic(ctx, 20260101, 20260131)
	if err != nil || len(rows) != 1 || rows[0].Tx != 200 {
		t.Fatalf("流量：%v %v", rows, err)
	}
	got := make(chan agent.Sample, 1)
	sctx, scancel := context.WithCancel(ctx)
	go cl.Stream(sctx, func(sm agent.Sample) {
		select {
		case got <- sm:
		default:
		}
	})
	go func() {
		for sctx.Err() == nil {
			p.bc.Publish(agent.Sample{Metric: metric.Metric{TS: 1, CPU: 42}})
			time.Sleep(50 * time.Millisecond)
		}
	}()
	select {
	case sm := <-got:
		if sm.CPU != 42 {
			t.Fatalf("实时样本不对：%+v", sm)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("没有收到实时样本")
	}
	scancel()
}

func TestParseDF(t *testing.T) {
	out := `Filesystem     Type     1024-blocks    Used Available Capacity Mounted on
/dev/sda1      ext4        41152736 8123456  31016512      21% /
tmpfs          tmpfs        2000000       0   2000000       0% /run
/dev/sdb1      xfs        104857600 1048576 103809024       1% /data disk
`
	list := ParseDF(out)
	if len(list) != 2 || list[0].Point != "/" || list[1].Point != "/data disk" || list[0].Total != 41152736*1024 {
		t.Fatalf("ParseDF：%+v", list)
	}
}
