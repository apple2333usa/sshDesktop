package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// Result 是一次探测的结果。
type Result struct {
	OK        bool    `json:"ok"`
	LatencyMs float64 `json:"latencyMs"`
	Message   string  `json:"message"`
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Check 按类型执行一次探测。
func Check(ctx context.Context, p Probe) Result {
	timeout := time.Duration(p.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch p.Type {
	case "http":
		return checkHTTP(ctx, p)
	case "tcp":
		return checkTCP(ctx, p.Target)
	case "ping":
		return checkPing(ctx, p.Target)
	case "ssl":
		return checkSSL(ctx, p)
	}
	return Result{Message: "未知的探测类型"}
}

// statusOK 判断状态码是否符合期望，期望值形如 "200-399" 或 "200,301-302"，为空时为 200-399。
func statusOK(code int, expect string) bool {
	if strings.TrimSpace(expect) == "" {
		expect = "200-399"
	}
	for _, part := range strings.Split(expect, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 == nil && err2 == nil && code >= a && code <= b {
			return true
		}
	}
	return false
}

var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               nil, // 探测要反映面板服务器直接访问的结果
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("重定向次数过多")
		}
		return nil
	},
}

func shortErr(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "超时"
	case strings.Contains(err.Error(), "connection refused"):
		return "连接被拒绝"
	case strings.Contains(err.Error(), "no such host"):
		return "无法解析域名"
	}
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) && ue.Unwrap() != nil {
		return ue.Unwrap().Error()
	}
	return err.Error()
}

func checkHTTP(ctx context.Context, p Probe) Result {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Target, nil)
	if err != nil {
		return Result{Message: "地址无效"}
	}
	req.Header.Set("User-Agent", "sshDesktop-probe")
	start := time.Now()
	resp, err := httpClient.Do(req)
	if err != nil {
		return Result{Message: shortErr(err)}
	}
	defer resp.Body.Close()
	lat := ms(time.Since(start))
	if !statusOK(resp.StatusCode, p.ExpectStatus) {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return Result{LatencyMs: lat, Message: "状态码 " + strconv.Itoa(resp.StatusCode)}
	}
	if p.Keyword != "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if !bytes.Contains(body, []byte(p.Keyword)) {
			return Result{LatencyMs: lat, Message: "页面中没有关键字"}
		}
	}
	return Result{OK: true, LatencyMs: lat, Message: "状态码 " + strconv.Itoa(resp.StatusCode)}
}

func checkTCP(ctx context.Context, target string) Result {
	start := time.Now()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return Result{Message: shortErr(err)}
	}
	c.Close()
	return Result{OK: true, LatencyMs: ms(time.Since(start)), Message: "端口可连接"}
}

// SSL 目标形如 example.com 或 example.com:8443。
func sslAddr(target string) (addr, host string) {
	if h, _, err := net.SplitHostPort(target); err == nil {
		return target, h
	}
	return net.JoinHostPort(target, "443"), target
}

func checkSSL(ctx context.Context, p Probe) Result {
	addr, host := sslAddr(p.Target)
	start := time.Now()
	d := tls.Dialer{Config: &tls.Config{ServerName: host}}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		var ce *tls.CertificateVerificationError
		if errors.As(err, &ce) {
			return Result{Message: "证书无效：" + ce.Err.Error()}
		}
		return Result{Message: shortErr(err)}
	}
	defer c.Close()
	lat := ms(time.Since(start))
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return Result{LatencyMs: lat, Message: "没有证书"}
	}
	exp := certs[0].NotAfter
	days := int(time.Until(exp).Hours() / 24)
	warn := p.SSLWarnDays
	if warn <= 0 {
		warn = 14
	}
	msg := fmt.Sprintf("证书剩余 %d 天（%s 到期）", days, exp.Local().Format("2006-01-02"))
	return Result{OK: days >= warn, LatencyMs: lat, Message: msg}
}

func checkPing(ctx context.Context, host string) Result {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return Result{Message: "无法解析域名"}
	}
	ip := ips[0].IP
	for _, a := range ips {
		if a.IP.To4() != nil {
			ip = a.IP
			break
		}
	}
	d, err := icmpPing(ctx, ip)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		d, err = cmdPing(ctx, ip)
	}
	if err != nil {
		return Result{Message: shortErr(err)}
	}
	return Result{OK: true, LatencyMs: ms(d), Message: "有响应"}
}

var errNoSocket = errors.New("无法创建 ICMP socket")

// icmpPing 发送一个 ICMP Echo：先用原始 socket（root），再试非特权的 UDP ICMP socket。
func icmpPing(ctx context.Context, ip net.IP) (time.Duration, error) {
	v4 := ip.To4() != nil
	type mode struct{ network, listen string }
	modes := []mode{{"ip6:ipv6-icmp", "::"}, {"udp6", "::"}}
	proto := 58
	var reqType, replyType icmp.Type = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply
	if v4 {
		modes = []mode{{"ip4:icmp", "0.0.0.0"}, {"udp4", "0.0.0.0"}}
		proto, reqType, replyType = 1, ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply
	}
	for _, m := range modes {
		c, err := icmp.ListenPacket(m.network, m.listen)
		if err != nil {
			continue
		}
		defer c.Close()
		if dl, ok := ctx.Deadline(); ok {
			c.SetDeadline(dl)
		}
		seq := int(time.Now().UnixNano() & 0xffff)
		payload := []byte("sshdesktop-" + strconv.Itoa(seq))
		msg := icmp.Message{Type: reqType, Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: seq, Data: payload}}
		b, err := msg.Marshal(nil)
		if err != nil {
			return 0, err
		}
		var dst net.Addr = &net.IPAddr{IP: ip}
		if strings.HasPrefix(m.network, "udp") {
			dst = &net.UDPAddr{IP: ip}
		}
		start := time.Now()
		if _, err := c.WriteTo(b, dst); err != nil {
			return 0, err
		}
		buf := make([]byte, 1500)
		for {
			n, _, err := c.ReadFrom(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					return 0, context.DeadlineExceeded
				}
				return 0, err
			}
			rm, err := icmp.ParseMessage(proto, buf[:n])
			if err != nil || rm.Type != replyType {
				continue
			}
			// 非特权 socket 的 ID 由内核改写，只比较序号和内容
			if e, ok := rm.Body.(*icmp.Echo); ok && e.Seq == seq && bytes.Equal(e.Data, payload) {
				return time.Since(start), nil
			}
		}
	}
	return 0, errNoSocket
}

var pingTime = regexp.MustCompile(`time[=<]([0-9.]+) ?ms`)

// cmdPing 在没有 ICMP socket 权限时调用系统的 ping 命令。
func cmdPing(ctx context.Context, ip net.IP) (time.Duration, error) {
	secs := 5
	if dl, ok := ctx.Deadline(); ok {
		secs = max(1, int(time.Until(dl).Seconds()))
	}
	out, err := exec.CommandContext(ctx, "ping", "-n", "-c", "1", "-W", strconv.Itoa(secs), ip.String()).CombinedOutput()
	if m := pingTime.FindSubmatch(out); m != nil {
		v, _ := strconv.ParseFloat(string(m[1]), 64)
		return time.Duration(v * float64(time.Millisecond)), nil
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return 0, errors.New("没有响应")
		}
		return 0, err
	}
	return 0, errors.New("没有响应")
}
