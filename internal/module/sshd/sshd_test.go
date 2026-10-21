package sshd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apple2333usa/sshdesktop/internal/pending"
)

func TestRender(t *testing.T) {
	out := render(Settings{Port: 2222, PasswordAuth: false, PermitRootLogin: "prohibit-password", PubkeyAuth: true}, []int{22, 2222})
	for _, want := range []string{"Port 22\n", "Port 2222\n", "PasswordAuthentication no\n", "KbdInteractiveAuthentication no\n",
		"PermitRootLogin prohibit-password\n", "PubkeyAuthentication yes\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %q：\n%s", want, out)
		}
	}
	if out := render(Settings{Port: 22, PasswordAuth: true, PermitRootLogin: "no", PubkeyAuth: true}, []int{22}); strings.Contains(out, "KbdInteractive") {
		t.Errorf("开启密码登录时不改 KbdInteractiveAuthentication：\n%s", out)
	}
}

func TestCommentPorts(t *testing.T) {
	src := "Port 22\n  port 2222\n#Port 23\nListenAddress 0.0.0.0\nMatch User git\n    Port 2200\n"
	out, changed := commentPorts(src)
	if !changed {
		t.Fatal("应该有修改")
	}
	want := offMark + "Port 22\n" + offMark + "  port 2222\n#Port 23\nListenAddress 0.0.0.0\nMatch User git\n    Port 2200\n"
	if out != want {
		t.Fatalf("%q", out)
	}
	if _, changed := commentPorts("#Port 22\nPasswordAuthentication no\n"); changed {
		t.Fatal("没有 Port 时不应修改")
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "sshd_config")
	drop := filepath.Join(dir, "sshd_config.d")
	os.MkdirAll(drop, 0o755)
	os.WriteFile(main, []byte("Port 2200\nPermitRootLogin yes\n"), 0o644)
	os.WriteFile(filepath.Join(drop, "50-cloud.conf"), []byte("PasswordAuthentication yes\nPort 2201\n"), 0o600)
	s := New(Deps{Pending: pending.New(filepath.Join(dir, "pending")), DataDir: filepath.Join(dir, "data")})
	s.SetTesting(main, drop, func() string { return "" }, nil, filepath.Join(dir, "passwd"))
	if s.included() {
		t.Fatal("主配置没有 Include")
	}
	if err := s.write(Settings{Port: 30022, PermitRootLogin: "prohibit-password", PubkeyAuth: true}, []int{2200, 30022}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(main)
	if !strings.HasPrefix(string(b), "# 由 sshDesktop 添加") || !strings.Contains(string(b), "Include "+drop+"/*.conf") || !strings.Contains(string(b), offMark+"Port 2200") {
		t.Fatalf("主配置：\n%s", b)
	}
	if !s.included() {
		t.Fatal("写入后应包含 sshd_config.d")
	}
	b, _ = os.ReadFile(filepath.Join(drop, "50-cloud.conf"))
	if !strings.Contains(string(b), offMark+"Port 2201") {
		t.Fatalf("其他文件的 Port 应注释掉：\n%s", b)
	}
	if fi, _ := os.Stat(filepath.Join(drop, "50-cloud.conf")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("应保留原文件权限：%v", fi.Mode())
	}
	b, _ = os.ReadFile(filepath.Join(drop, managedName))
	if !strings.Contains(string(b), "Port 2200\nPort 30022\n") {
		t.Fatalf("面板的配置：\n%s", b)
	}
	// 修改前的原文件备份在数据目录
	matches, _ := filepath.Glob(filepath.Join(dir, "data", "takeover-backup", "ssh", "*", main))
	if len(matches) != 1 {
		t.Fatalf("没有备份主配置：%v", matches)
	}
}

func TestParseLogins(t *testing.T) {
	lines := []string{
		"2026-10-20T10:00:00+0800 host sshd[1]: Accepted publickey for root from 1.2.3.4 port 50000 ssh2: ED25519 SHA256:x",
		"2026-10-20T10:01:00+0800 host sshd[2]: Failed password for invalid user admin from 5.6.7.8 port 40000 ssh2",
		"2026-10-20T10:01:01+0800 host sshd[2]: Invalid user admin from 5.6.7.8 port 40000",
		"Oct 20 10:02:00 host sshd[3]: Failed password for root from 5.6.7.8 port 40001 ssh2",
		"Oct 20 10:03:00 host sshd[4]: Connection closed by 9.9.9.9 port 1 [preauth]",
	}
	got := ParseLogins(lines)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if got[0].Time != "Oct 20 10:02:00" || got[0].Success || got[0].User != "root" || got[0].Invalid {
		t.Fatalf("应按时间倒序：%+v", got[0])
	}
	if !got[1].Invalid || got[1].User != "admin" || got[1].IP != "5.6.7.8" {
		t.Fatalf("%+v", got[1])
	}
	if !got[2].Success || got[2].Method != "publickey" || got[2].Time != "2026-10-20T10:00:00" || got[2].Port != "50000" {
		t.Fatalf("%+v", got[2])
	}
}

func TestParseKeys(t *testing.T) {
	keys := parseKeys("# 注释\nssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ8a5vM5E6m1r0p1uQbqBuM8lM6cS4u8nJ0eF3ZhGk8o me@pc\n\nnot-a-key\n")
	if len(keys) != 2 || keys[0].Line != 2 || keys[0].Type != "ssh-ed25519" || keys[0].Comment != "me@pc" || keys[1].Error == "" || keys[1].Line != 4 {
		t.Fatalf("%+v", keys)
	}
}
