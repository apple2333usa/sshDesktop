package firewall

import (
	"strings"
	"testing"
)

const userRules = `*filter
### RULES ###

### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in
-A ufw-user-input -p tcp --dport 22 -j ACCEPT

### tuple ### deny any any 0.0.0.0/0 any 1.2.3.4 in
### tuple ### allow tcp 8000:8100 0.0.0.0/0 any 0.0.0.0/0 in comment=776562
### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 OpenSSH - in
### tuple ### allow tcp 80,443 0.0.0.0/0 any 0.0.0.0/0 in
### tuple ### allow any any 0.0.0.0/0 any 0.0.0.0/0 out
### END RULES ###
`

const user6Rules = `### tuple ### allow tcp 22 ::/0 any ::/0 in
### tuple ### allow tcp 443 ::/0 any 2001:db8::/32 in
`

func TestParseUfw(t *testing.T) {
	v4 := parseTuples(userRules, "ipv4")
	if len(v4) != 6 {
		t.Fatalf("%d 条：%+v", len(v4), v4)
	}
	if r := v4[1]; r.Action != "deny" || r.Source != "1.2.3.4" || r.Port != "" || r.Proto != "" || !r.Editable {
		t.Fatalf("%+v", r)
	}
	if r := v4[2]; r.Port != "8000-8100" || r.Comment != "web" || !r.Editable {
		t.Fatalf("%+v", r)
	}
	if r := v4[3]; r.Service != "OpenSSH" || !r.Editable {
		t.Fatalf("%+v", r)
	}
	if v4[4].Editable || v4[5].Editable {
		t.Fatal("多端口和出站规则应只读")
	}
	rules := mergeFamilies(v4, parseTuples(user6Rules, "ipv6"))
	if len(rules) != 7 || rules[0].Family != "" || rules[6].Family != "ipv6" || rules[6].Source != "2001:db8::/32" {
		t.Fatalf("合并 IPv4/IPv6 不对：%+v", rules)
	}
	if !strings.HasPrefix(rules[0].ID, "1:") || !strings.HasPrefix(rules[6].ID, "7:") {
		t.Fatalf("id：%s %s", rules[0].ID, rules[6].ID)
	}
}

func TestUfwSpec(t *testing.T) {
	cases := []struct {
		r    Rule
		want string
	}{
		{Rule{Action: "allow", Port: "80", Proto: "tcp"}, "allow proto tcp from any to any port 80"},
		{Rule{Action: "deny", Source: "1.2.3.4"}, "deny from 1.2.3.4 to any"},
		{Rule{Action: "allow", Port: "8000-8100", Proto: "udp", Source: "10.0.0.0/8"}, "allow proto udp from 10.0.0.0/8 to any port 8000:8100"},
		{Rule{Action: "allow", Service: "OpenSSH"}, "allow OpenSSH"},
	}
	for _, c := range cases {
		if got := strings.Join(ufwSpec(c.r), " "); got != c.want {
			t.Errorf("%+v：%q，应为 %q", c.r, got, c.want)
		}
	}
}

func TestRich(t *testing.T) {
	r := parseRich(`rule family="ipv4" source address="10.0.0.0/8" port port="8443" protocol="tcp" accept`)
	if !r.Editable || r.Action != "allow" || r.Source != "10.0.0.0/8" || r.Port != "8443" || r.Proto != "tcp" || r.Family != "ipv4" {
		t.Fatalf("%+v", r)
	}
	if got := richRule(r, "tcp"); got != `rule family="ipv4" source address="10.0.0.0/8" port port="8443" protocol="tcp" accept` {
		t.Fatal(got)
	}
	if r := parseRich(`rule family="ipv4" source address="1.2.3.4" drop`); !r.Editable || r.Action != "deny" {
		t.Fatalf("%+v", r)
	}
	if r := parseRich(`rule family="ipv4" source address="1.2.3.4" log prefix="x" level="info" accept`); r.Editable {
		t.Fatalf("带 log 的富规则应只读：%+v", r)
	}
}

func TestNormalize(t *testing.T) {
	r := Rule{Action: " ALLOW ", Proto: "TCP", Port: "8000:8100", Source: "10.1.2.3/8"}
	if err := normalize(&r); err != nil {
		t.Fatal(err)
	}
	if r.Action != "allow" || r.Proto != "tcp" || r.Port != "8000-8100" || r.Source != "10.0.0.0/8" || r.Family != "ipv4" {
		t.Fatalf("%+v", r)
	}
	for _, bad := range []Rule{{Action: "accept"}, {Action: "allow", Proto: "icmp"}, {Action: "allow", Port: "70000"},
		{Action: "allow", Port: "90-80"}, {Action: "allow", Source: "1.2.3"}} {
		if err := normalize(&bad); err == nil {
			t.Errorf("应该校验失败：%+v", bad)
		}
	}
}

func TestGuards(t *testing.T) {
	s := &Service{d: Deps{ProtectedPorts: func() map[int]string { return map[int]string{22: "SSH", 33333: "面板"} }}}
	ssh := Rule{ID: "1", Action: "allow", Port: "22", Proto: "tcp"}
	panel := Rule{ID: "2", Action: "allow", Port: "30000-40000"}
	st := Status{Active: true, Rules: []Rule{ssh, panel}}
	if err := s.guardDelete(st, ssh); err == nil {
		t.Fatal("唯一放行 SSH 的规则不能删除")
	}
	if err := s.guardDelete(st, panel); err == nil {
		t.Fatal("唯一放行面板端口的规则不能删除")
	}
	st.Rules = append(st.Rules, Rule{ID: "3", Action: "allow", Service: "ssh"})
	if err := s.guardDelete(st, ssh); err != nil {
		t.Fatalf("还有 ssh 服务规则时可以删除：%v", err)
	}
	if err := s.guardDelete(Status{Active: false, Rules: st.Rules}, panel); err != nil {
		t.Fatal("防火墙未启用时不限制")
	}
	if err := s.guardAdd(Rule{Action: "deny", Port: "22", Proto: "tcp"}); err == nil {
		t.Fatal("不能拒绝所有来源访问 SSH")
	}
	if err := s.guardAdd(Rule{Action: "deny"}); err == nil {
		t.Fatal("不能拒绝所有端口")
	}
	if err := s.guardAdd(Rule{Action: "deny", Port: "22", Source: "1.2.3.4"}); err != nil {
		t.Fatal("拒绝单个来源可以")
	}
	if err := s.guardAdd(Rule{Action: "deny", Port: "8080"}); err != nil {
		t.Fatal(err)
	}
}

func TestManagers(t *testing.T) {
	raw := `*filter
:INPUT ACCEPT [0:0]
:1PANEL_INPUT - [0:0]
:f2b-sshd - [0:0]
-A INPUT -j 1PANEL_INPUT
-A INPUT -p tcp -m multiport --dports 22 -j f2b-sshd
`
	m := managers(raw)
	if strings.Join(m, ",") != "1Panel,fail2ban" {
		t.Fatalf("%v", m)
	}
	notes := strings.Join(noneNotes(raw, m), "\n")
	if strings.Contains(notes, "建议：") || !strings.Contains(notes, "不建议在这里安装") || !strings.Contains(notes, "fail2ban") {
		t.Fatal(notes)
	}
	if m := managers("-A INPUT -p tcp --dport 22 -j ACCEPT\n"); len(m) != 0 {
		t.Fatalf("%v", m)
	}
	if notes := strings.Join(noneNotes("-A INPUT -j ACCEPT\n", nil), "\n"); !strings.Contains(notes, "建议：") {
		t.Fatal(notes)
	}
}
