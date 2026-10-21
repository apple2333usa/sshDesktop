package firewall

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// firewalld：管理默认区域的端口、服务和富规则（rich rule），改动写入永久配置后 --reload。
// 回滚时还原区域的 XML 文件，并按修改前的状态启动或停止 firewalld。
type firewalld struct{}

func (f *firewalld) name() string { return "firewalld" }

func (f *firewalld) zone(ctx context.Context) string {
	if out, err := run(ctx, "firewall-cmd", "--get-default-zone"); err == nil {
		if z := strings.TrimSpace(out); z != "" {
			return z
		}
	}
	return "public"
}

func (f *firewalld) files(ctx context.Context) []string {
	return []string{"/etc/firewalld/zones/" + f.zone(ctx) + ".xml"}
}

func (f *firewalld) status(ctx context.Context) (Status, error) {
	st := Status{Backend: "firewalld", Rules: []Rule{}}
	out, _ := run(ctx, "firewall-cmd", "--state")
	st.Active = strings.TrimSpace(out) == "running"
	if v, err := run(ctx, "firewall-cmd", "--version"); err == nil {
		st.Version = strings.TrimSpace(v)
	}
	st.Zone = f.zone(ctx)
	// 未运行时用 firewall-offline-cmd 读取永久配置
	cmd := "firewall-cmd"
	args := []string{"--permanent", "--zone=" + st.Zone}
	if !st.Active {
		cmd, args = "firewall-offline-cmd", []string{"--zone=" + st.Zone}
	}
	list := func(what string) []string {
		out, err := run(ctx, cmd, append(args, "--list-"+what)...)
		if err != nil {
			return nil
		}
		if what == "rich-rules" {
			return nonEmpty(strings.Split(out, "\n"))
		}
		return strings.Fields(out)
	}
	if target, err := run(ctx, cmd, append(args, "--get-target")...); err == nil {
		st.Default = strings.TrimSpace(target)
		if st.Default == "default" {
			st.Default = "reject"
		}
	}
	for _, sv := range list("services") {
		st.Rules = append(st.Rules, Rule{Action: "allow", Service: sv, Raw: "service " + sv})
	}
	for _, p := range list("ports") {
		port, proto, _ := strings.Cut(p, "/")
		st.Rules = append(st.Rules, Rule{Action: "allow", Port: port, Proto: proto, Raw: "port " + p, Editable: true})
	}
	for _, rr := range list("rich-rules") {
		st.Rules = append(st.Rules, parseRich(rr))
	}
	for i := range st.Rules {
		st.Rules[i].ID = fmt.Sprintf("%d:%s", i+1, st.Rules[i].Raw)
		if st.Rules[i].Service != "" {
			st.Rules[i].Editable = true
		}
	}
	if !st.Active {
		st.Notes = append(st.Notes, "firewalld 没有运行，上面是它的永久配置")
	}
	return st, nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

var (
	reRichFamily = regexp.MustCompile(`family="?(ipv4|ipv6)"?`)
	reRichSource = regexp.MustCompile(`source address="?([^"\s]+)"?`)
	reRichPort   = regexp.MustCompile(`port port="?([0-9-]+)"? protocol="?(tcp|udp)"?`)
	reRichAction = regexp.MustCompile(`\s(accept|drop|reject)(\s|$)`)
)

// parseRich 解析面板能表示的富规则：[family] [source address] [port] accept|drop|reject；其他只读展示。
func parseRich(s string) Rule {
	r := Rule{Raw: "rich " + s}
	rest := s
	for _, re := range []*regexp.Regexp{reRichFamily, reRichSource, reRichPort} {
		rest = re.ReplaceAllString(rest, "")
	}
	f := strings.Fields(rest)
	if len(f) != 2 || f[0] != "rule" || !reRichAction.MatchString(" "+f[1]) {
		return r
	}
	r.Action = map[string]string{"accept": "allow", "drop": "deny", "reject": "reject"}[f[1]]
	if m := reRichFamily.FindStringSubmatch(s); m != nil {
		r.Family = m[1]
	}
	if m := reRichSource.FindStringSubmatch(s); m != nil {
		r.Source = m[1]
	}
	if m := reRichPort.FindStringSubmatch(s); m != nil {
		r.Port, r.Proto = m[1], m[2]
	}
	r.Editable = true
	return r
}

func richRule(r Rule, proto string) string {
	var b strings.Builder
	b.WriteString("rule")
	if r.Family != "" {
		fmt.Fprintf(&b, ` family="%s"`, r.Family)
	}
	if r.Source != "" {
		fmt.Fprintf(&b, ` source address="%s"`, r.Source)
	}
	if r.Port != "" {
		fmt.Fprintf(&b, ` port port="%s" protocol="%s"`, r.Port, proto)
	}
	b.WriteString(" " + map[string]string{"allow": "accept", "deny": "drop", "reject": "reject"}[r.Action])
	return b.String()
}

func protos(r Rule) []string {
	if r.Proto != "" {
		return []string{r.Proto}
	}
	return []string{"tcp", "udp"}
}

func (f *firewalld) apply(ctx context.Context, r Rule, add bool) error {
	op := "--remove-"
	if add {
		op = "--add-"
	}
	running := true
	if out, _ := run(ctx, "firewall-cmd", "--state"); strings.TrimSpace(out) != "running" {
		running = false
	}
	z := "--zone=" + f.zone(ctx)
	exec := func(arg string) error {
		var err error
		if running {
			_, err = run(ctx, "firewall-cmd", "--permanent", z, arg)
		} else {
			_, err = run(ctx, "firewall-offline-cmd", z, arg)
		}
		return err
	}
	switch {
	case strings.HasPrefix(r.Raw, "rich "):
		if err := exec(op + "rich-rule=" + strings.TrimPrefix(r.Raw, "rich ")); err != nil {
			return err
		}
	case r.Service != "":
		if err := exec(op + "service=" + r.Service); err != nil {
			return err
		}
	case r.Action == "allow" && r.Source == "" && r.Port != "":
		for _, p := range protos(r) {
			if err := exec(op + "port=" + r.Port + "/" + p); err != nil {
				return err
			}
		}
	default:
		if r.Port == "" {
			if err := exec(op + "rich-rule=" + richRule(r, "")); err != nil {
				return err
			}
		} else {
			for _, p := range protos(r) {
				if err := exec(op + "rich-rule=" + richRule(r, p)); err != nil {
					return err
				}
			}
		}
	}
	if running {
		_, err := run(ctx, "firewall-cmd", "--reload")
		return err
	}
	return nil
}

func (f *firewalld) add(ctx context.Context, r Rule) error { return f.apply(ctx, r, true) }

func (f *firewalld) remove(ctx context.Context, r Rule) error {
	if !r.Editable {
		return fmt.Errorf("这条规则不能在面板中删除：%s", r.Raw)
	}
	return f.apply(ctx, r, false)
}

func (f *firewalld) enable(ctx context.Context, on bool) error {
	var err error
	if on {
		_, err = run(ctx, "systemctl", "enable", "--now", "firewalld")
	} else {
		_, err = run(ctx, "systemctl", "disable", "--now", "firewalld")
	}
	return err
}

func (f *firewalld) reload(ctx context.Context, active bool) error {
	out, _ := run(ctx, "firewall-cmd", "--state")
	running := strings.TrimSpace(out) == "running"
	switch {
	case active && running:
		_, err := run(ctx, "firewall-cmd", "--reload")
		return err
	case active:
		return f.enable(ctx, true)
	case running:
		return f.enable(ctx, false)
	}
	return nil
}
