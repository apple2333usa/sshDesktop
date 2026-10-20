package service

import "testing"

func TestParseUnits(t *testing.T) {
	out := `  cron.service      loaded    active   running Regular background program processing daemon
● nginx.service     loaded    failed   failed  A high performance web server
ssh.service         loaded    active   running OpenBSD Secure Shell server
dev-sda1.device     loaded    active   plugged /dev/sda1
`
	units := ParseUnits(out)
	if len(units) != 3 {
		t.Fatalf("应解析出 3 个服务，得到 %d：%+v", len(units), units)
	}
	if units[1].Name != "nginx.service" || units[1].Active != "failed" || units[1].Description != "A high performance web server" {
		t.Fatalf("解析结果不正确：%+v", units[1])
	}
}

func TestParseUnitFiles(t *testing.T) {
	m := ParseUnitFiles("cron.service enabled enabled\nfoo@.service static -\nx.socket enabled enabled\n")
	if m["cron.service"] != "enabled" || m["foo@.service"] != "static" || len(m) != 2 {
		t.Fatalf("解析结果不正确：%v", m)
	}
}

func TestUnitNameValidation(t *testing.T) {
	for _, ok := range []string{"nginx.service", "getty@tty1.service", "systemd-journald.service"} {
		if !unitName.MatchString(ok) {
			t.Fatalf("%s 应合法", ok)
		}
	}
	for _, bad := range []string{"nginx", "../x.service", "a b.service", "x.service;rm"} {
		if unitName.MatchString(bad) {
			t.Fatalf("%s 应被拒绝", bad)
		}
	}
}
