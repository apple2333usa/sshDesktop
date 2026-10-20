package agent

import (
	"strconv"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/mountinfo"
)

// 没装 agent 的远程节点：面板在 SSH 连接上运行一个常驻的 sh 循环，每 5 秒输出一次 /proc 等信息，
// 由 ProcSampler 解析成与 agent 相同的 Sample。只依赖 POSIX sh、/proc 和 df。

// SampleInterval 是命令采集的间隔（秒）。
const SampleInterval = 5

// SampleScript 是命令采集的循环脚本。每轮以 @@END 结束。
const SampleScript = `export LC_ALL=C
while :; do
echo @@TS; date +%s
echo @@UPTIME; cat /proc/uptime
echo @@STAT; grep '^cpu' /proc/stat
echo @@LOAD; cat /proc/loadavg
echo @@MEM; grep -E '^(MemTotal|MemFree|MemAvailable|Buffers|Cached|SwapTotal|SwapFree):' /proc/meminfo
echo @@NET; tail -n +3 /proc/net/dev
echo @@BLOCK; for d in /sys/block/*; do [ -e "$d/device" ] && echo "${d##*/}"; done
echo @@DISKIO; cat /proc/diskstats
echo @@DF; df -PTk 2>/dev/null
echo @@TCP; awk 'FNR>1' /proc/net/tcp /proc/net/tcp6 2>/dev/null | wc -l
echo @@PROCS; ls -d /proc/[0-9]* 2>/dev/null | wc -l
echo @@END
sleep 5
done`

// InfoScript 一次性读取主机静态信息。
const InfoScript = `export LC_ALL=C
echo @@HOST; hostname 2>/dev/null || cat /proc/sys/kernel/hostname
echo @@KERNEL; uname -r
echo @@ARCH; uname -m
echo @@OS; cat /etc/os-release 2>/dev/null
echo @@CPU; grep -m1 'model name' /proc/cpuinfo; grep -c '^processor' /proc/cpuinfo
echo @@BOOT; grep '^btime' /proc/stat
echo @@VIRT; systemd-detect-virt 2>/dev/null
echo @@END`

// Sections 把输出按 @@NAME 分段。
func Sections(block string) map[string][]string {
	out := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "@@") {
			cur = strings.TrimSpace(line[2:])
			if _, ok := out[cur]; !ok {
				out[cur] = nil
			}
			continue
		}
		if cur != "" && strings.TrimSpace(line) != "" {
			out[cur] = append(out[cur], line)
		}
	}
	return out
}

// ParseInfo 解析 InfoScript 的输出。
func ParseInfo(out string) Info {
	s := Sections(out)
	in := Info{OS: "linux"}
	first := func(k string) string {
		if l := s[k]; len(l) > 0 {
			return strings.TrimSpace(l[0])
		}
		return ""
	}
	in.Hostname, in.Kernel, in.Arch = first("HOST"), first("KERNEL"), first("ARCH")
	for _, l := range s["OS"] {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			in.Platform = v
		case "VERSION_ID":
			in.PlatformVer = v
		}
	}
	for _, l := range s["CPU"] {
		if strings.HasPrefix(l, "model name") {
			if _, v, ok := strings.Cut(l, ":"); ok {
				in.CPUModel = strings.TrimSpace(v)
			}
		} else if n, err := strconv.Atoi(strings.TrimSpace(l)); err == nil {
			in.CPUThreads, in.CPUCores = n, n
		}
	}
	if f := strings.Fields(first("BOOT")); len(f) == 2 {
		in.BootTime, _ = strconv.ParseUint(f[1], 10, 64)
	}
	if v := first("VIRT"); v != "none" {
		in.Virtualization = v
	}
	return in
}

type cpuTimes struct{ total, idle float64 }

// ProcSampler 根据相邻两轮输出计算 CPU、网络、磁盘 IO 的速率。
type ProcSampler struct {
	prevCPU  []cpuTimes
	prevNet  map[string][2]uint64
	prevDisk map[string][2]uint64
	prevAt   time.Time
	hasPrev  bool
	LastNet  map[string][2]uint64
}

func parseCPU(line string) (cpuTimes, bool) {
	f := strings.Fields(line)
	if len(f) < 5 {
		return cpuTimes{}, false
	}
	var t cpuTimes
	for i, v := range f[1:] {
		if i >= 8 { // guest 与 guest_nice 已计入 user / nice
			break
		}
		x, _ := strconv.ParseFloat(v, 64)
		t.total += x
		if i == 3 || i == 4 { // idle、iowait
			t.idle += x
		}
	}
	return t, true
}

func busyPct(a, b cpuTimes) float64 {
	total := b.total - a.total
	if total <= 0 {
		return 0
	}
	v := (total - (b.idle - a.idle)) / total * 100
	return max(0, min(100, v))
}

func atoiU(s string) uint64 {
	v, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	return v
}

// Parse 解析一轮输出；第一轮只建立基线，返回 nil。
func (p *ProcSampler) Parse(block string) *Sample {
	sec := Sections(block)
	s := &Sample{}
	now := time.Now()
	if l := sec["TS"]; len(l) > 0 {
		if ts, err := strconv.ParseInt(strings.TrimSpace(l[0]), 10, 64); err == nil {
			now = time.Unix(ts, 0)
		}
	}
	s.TS = now.Unix()
	if l := sec["UPTIME"]; len(l) > 0 {
		if f := strings.Fields(l[0]); len(f) > 0 {
			up, _ := strconv.ParseFloat(f[0], 64)
			s.Uptime = uint64(up)
		}
	}
	var cpus []cpuTimes
	for _, l := range sec["STAT"] {
		if t, ok := parseCPU(l); ok {
			cpus = append(cpus, t)
		}
	}
	if len(cpus) > 0 && len(p.prevCPU) == len(cpus) {
		s.CPU = busyPct(p.prevCPU[0], cpus[0])
		s.CPUCores = make([]float64, len(cpus)-1)
		for i := 1; i < len(cpus); i++ {
			s.CPUCores[i-1] = busyPct(p.prevCPU[i], cpus[i])
		}
	}
	p.prevCPU = cpus
	if l := sec["LOAD"]; len(l) > 0 {
		f := strings.Fields(l[0])
		if len(f) >= 3 {
			s.Load1, _ = strconv.ParseFloat(f[0], 64)
			s.Load5, _ = strconv.ParseFloat(f[1], 64)
			s.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	mem := map[string]uint64{}
	for _, l := range sec["MEM"] {
		k, v, ok := strings.Cut(l, ":")
		if ok {
			mem[k] = atoiU(strings.TrimSuffix(strings.TrimSpace(v), " kB")) * 1024
		}
	}
	s.MemTotal = mem["MemTotal"]
	avail, ok := mem["MemAvailable"]
	if !ok {
		avail = mem["MemFree"] + mem["Buffers"] + mem["Cached"]
	}
	if s.MemTotal >= avail {
		s.MemUsed = s.MemTotal - avail
	}
	s.SwapTotal = mem["SwapTotal"]
	if s.SwapTotal >= mem["SwapFree"] {
		s.SwapUsed = s.SwapTotal - mem["SwapFree"]
	}

	nets := map[string][2]uint64{}
	for _, l := range sec["NET"] {
		name, rest, ok := strings.Cut(l, ":")
		name = strings.TrimSpace(name)
		if !ok || !CountedNIC(name) {
			continue
		}
		f := strings.Fields(rest)
		if len(f) >= 9 {
			nets[name] = [2]uint64{atoiU(f[0]), atoiU(f[8])}
		}
	}
	p.LastNet = nets
	physical := map[string]bool{}
	for _, l := range sec["BLOCK"] {
		physical[strings.TrimSpace(l)] = true
	}
	disks := map[string][2]uint64{}
	for _, l := range sec["DISKIO"] {
		f := strings.Fields(l)
		if len(f) >= 10 && physical[f[2]] {
			disks[f[2]] = [2]uint64{atoiU(f[5]) * 512, atoiU(f[9]) * 512}
		}
	}
	elapsed := now.Sub(p.prevAt).Seconds()
	if p.hasPrev && elapsed > 0 {
		var rx, tx, rd, wr uint64
		for k, v := range nets {
			if pv, ok := p.prevNet[k]; ok && v[0] >= pv[0] && v[1] >= pv[1] {
				rx += v[0] - pv[0]
				tx += v[1] - pv[1]
			}
		}
		for k, v := range disks {
			if pv, ok := p.prevDisk[k]; ok && v[0] >= pv[0] && v[1] >= pv[1] {
				rd += v[0] - pv[0]
				wr += v[1] - pv[1]
			}
		}
		s.NetRxBps, s.NetTxBps = float64(rx)/elapsed, float64(tx)/elapsed
		s.DiskReadBps, s.DiskWriteBps = float64(rd)/elapsed, float64(wr)/elapsed
	}

	seen := map[string]bool{}
	for i, l := range sec["DF"] {
		f := strings.Fields(l)
		if i == 0 && len(f) > 0 && f[0] == "Filesystem" || len(f) < 7 {
			continue
		}
		point := strings.Join(f[6:], " ")
		if f[1] == "tmpfs" || f[1] == "overlay" || f[1] == "devtmpfs" || seen[f[0]] || mountinfo.HiddenMount(f[1], point) {
			continue
		}
		seen[f[0]] = true
		d := DiskUsage{Mount: point, Device: f[0], FSType: f[1], Total: atoiU(f[2]) * 1024, Used: atoiU(f[3]) * 1024}
		if d.Total == 0 {
			continue
		}
		s.Disks = append(s.Disks, d)
		s.DiskTotal += d.Total
		s.DiskUsed += d.Used
	}
	if l := sec["TCP"]; len(l) > 0 {
		s.TCPConns = float64(atoiU(l[0]))
	}
	if l := sec["PROCS"]; len(l) > 0 {
		s.Procs = float64(atoiU(l[0]))
	}

	first := !p.hasPrev
	p.prevNet, p.prevDisk, p.prevAt, p.hasPrev = nets, disks, now, true
	if first {
		return nil
	}
	return s
}
