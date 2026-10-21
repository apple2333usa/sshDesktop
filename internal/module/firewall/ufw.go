package firewall

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ufw：规则从 /etc/ufw/user.rules、user6.rules 中的 "### tuple ###" 行读取（ufw 自己保存规则的格式），
// 修改通过 ufw 命令完成，回滚时还原这两个文件和 ufw.conf 后重新加载。
type ufw struct{ root string }

func (u *ufw) name() string { return "ufw" }

func (u *ufw) path(p string) string { return u.root + p }

func (u *ufw) files(context.Context) []string {
	return []string{u.path("/etc/ufw/user.rules"), u.path("/etc/ufw/user6.rules"), u.path("/etc/ufw/ufw.conf")}
}

var reUfwDefault = regexp.MustCompile(`Default: (\w+) \(incoming\)`)

func (u *ufw) status(ctx context.Context) (Status, error) {
	st := Status{Backend: "ufw", Rules: []Rule{}}
	out, err := run(ctx, "ufw", "status", "verbose")
	if err != nil {
		return st, err
	}
	st.Active = strings.Contains(out, "Status: active")
	if m := reUfwDefault.FindStringSubmatch(out); m != nil {
		st.Default = m[1]
	}
	if v, err := run(ctx, "ufw", "version"); err == nil {
		st.Version = strings.TrimPrefix(strings.SplitN(v, "\n", 2)[0], "ufw ")
	}
	var v4, v6 []Rule
	if b, err := os.ReadFile(u.path("/etc/ufw/user.rules")); err == nil {
		v4 = parseTuples(string(b), "ipv4")
	}
	if b, err := os.ReadFile(u.path("/etc/ufw/user6.rules")); err == nil {
		v6 = parseTuples(string(b), "ipv6")
	}
	st.Rules = mergeFamilies(v4, v6)
	if !st.Active && st.Default == "" {
		st.Default = "deny"
	}
	return st, nil
}

// parseTuples 解析 ufw 的 tuple 行：action proto dport dst sport src [dapp sapp] direction [comment=十六进制]。
func parseTuples(s, family string) []Rule {
	var out []Rule
	for _, l := range strings.Split(s, "\n") {
		t, ok := strings.CutPrefix(strings.TrimSpace(l), "### tuple ###")
		if !ok {
			continue
		}
		raw := strings.TrimSpace(t)
		f := strings.Fields(raw)
		r := Rule{Raw: raw, Family: family}
		if n := len(f); n > 0 && strings.HasPrefix(f[n-1], "comment=") {
			if b, err := hex.DecodeString(strings.TrimPrefix(f[n-1], "comment=")); err == nil {
				r.Comment = string(b)
			}
			f = f[:n-1]
		}
		if len(f) != 7 && len(f) != 9 {
			out = append(out, r)
			continue
		}
		action, proto, dport, dst, sport, src := f[0], f[1], f[2], f[3], f[4], f[5]
		dir := f[len(f)-1]
		r.Action, r.Direction = strings.TrimSuffix(action, "_log-all"), dir
		r.Action = strings.TrimSuffix(r.Action, "_log")
		if proto != "any" {
			r.Proto = proto
		}
		if dport != "any" {
			r.Port = strings.ReplaceAll(dport, ":", "-")
		}
		if src != "0.0.0.0/0" && src != "::/0" {
			r.Source = src
		}
		if len(f) == 9 && f[6] != "-" {
			r.Service = strings.ReplaceAll(f[6], "%20", " ")
		}
		anyDst := dst == "0.0.0.0/0" || dst == "::/0"
		r.Editable = dir == "in" && anyDst && sport == "any" && !strings.Contains(dport, ",") && (len(f) == 7 || f[7] == "-")
		out = append(out, r)
	}
	return out
}

// mergeFamilies 把 IPv4 和 IPv6 中相同的“任意来源”规则合并为一条（ufw 默认同时写两份）。
func mergeFamilies(v4, v6 []Rule) []Rule {
	key := func(r Rule) string {
		return strings.Join([]string{r.Action, r.Proto, r.Port, r.Source, r.Service, r.Direction, r.Comment}, "|")
	}
	idx := map[string]int{}
	out := []Rule{}
	for _, r := range v4 {
		if r.Source == "" {
			idx[key(r)] = len(out)
		}
		out = append(out, r)
	}
	for _, r := range v6 {
		if i, ok := idx[key(r)]; ok && r.Source == "" && out[i].Family == "ipv4" {
			out[i].Family = ""
			delete(idx, key(r))
			continue
		}
		out = append(out, r)
	}
	for i := range out {
		out[i].ID = fmt.Sprintf("%d:%s", i+1, out[i].Raw)
	}
	return out
}

// spec 生成 ufw 规则参数（添加和删除共用）。
func ufwSpec(r Rule) []string {
	if r.Service != "" && r.Source == "" {
		return []string{r.Action, r.Service}
	}
	a := []string{r.Action}
	if r.Proto != "" {
		a = append(a, "proto", r.Proto)
	}
	src := "any"
	if r.Source != "" {
		src = r.Source
	}
	a = append(a, "from", src, "to", "any")
	if r.Service != "" {
		a = append(a, "app", r.Service)
	} else if r.Port != "" {
		a = append(a, "port", strings.ReplaceAll(r.Port, "-", ":"))
	}
	return a
}

func (u *ufw) add(ctx context.Context, r Rule) error {
	spec := ufwSpec(r)
	if r.Port != "" && strings.Contains(r.Port, "-") && r.Proto == "" {
		// ufw 的端口范围必须指定协议：分别添加 tcp 和 udp
		for _, p := range []string{"tcp", "udp"} {
			x := r
			x.Proto = p
			if err := u.add(ctx, x); err != nil {
				return err
			}
		}
		return nil
	}
	if r.Comment != "" {
		spec = append(spec, "comment", r.Comment)
	}
	// 拒绝某个来源的规则放在最前面，才能先于放行规则生效
	if r.Action != "allow" && r.Source != "" {
		if _, err := run(ctx, "ufw", append([]string{"prepend"}, spec...)...); err == nil {
			return nil
		}
	}
	_, err := run(ctx, "ufw", spec...)
	return err
}

func (u *ufw) remove(ctx context.Context, r Rule) error {
	if !r.Editable {
		return fmt.Errorf("这条规则不能在面板中删除：%s", r.Raw)
	}
	_, err := run(ctx, "ufw", append([]string{"--force", "delete"}, ufwSpec(r)...)...)
	return err
}

func (u *ufw) enable(ctx context.Context, on bool) error {
	var err error
	if on {
		_, err = run(ctx, "ufw", "--force", "enable")
	} else {
		_, err = run(ctx, "ufw", "disable")
	}
	return err
}

func (u *ufw) reload(ctx context.Context, active bool) error {
	if !active {
		_, err := run(ctx, "ufw", "disable")
		return err
	}
	if _, err := run(ctx, "ufw", "--force", "enable"); err != nil {
		return err
	}
	_, err := run(ctx, "ufw", "reload")
	return err
}
