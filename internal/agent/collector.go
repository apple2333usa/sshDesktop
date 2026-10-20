// Package agent 是采集模块，面板与独立 agent（M2）共用。
package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// DiskUsage 是一个挂载点的使用情况。
type DiskUsage struct {
	Mount  string `json:"mount"`
	Device string `json:"device"`
	FSType string `json:"fsType"`
	Total  uint64 `json:"total"`
	Used   uint64 `json:"used"`
}

// Sample 是每秒一次的实时指标。
type Sample struct {
	store.Metric
	Uptime   uint64      `json:"uptime"`
	CPUCores []float64   `json:"cpuCores"`
	Disks    []DiskUsage `json:"disks"`
}

// Info 是主机静态信息。
type Info struct {
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	Platform      string `json:"platform"`
	PlatformVer   string `json:"platformVersion"`
	Kernel        string `json:"kernel"`
	Arch          string `json:"arch"`
	CPUModel      string `json:"cpuModel"`
	CPUCores      int    `json:"cpuCores"`
	CPUThreads    int    `json:"cpuThreads"`
	BootTime      uint64 `json:"bootTime"`
	Virtualization string `json:"virtualization"`
}

const ringSize = 600 // 内存中保留最近 10 分钟的秒级数据

type Collector struct {
	mu   sync.RWMutex
	ring []Sample
	info Info

	prevCPU   []cpu.TimesStat
	prevCPUT  cpu.TimesStat
	prevNet   map[string]net.IOCountersStat
	prevDisk  map[string]disk.IOCountersStat
	prevAt    time.Time
	disks     []DiskUsage
	tcp, procs float64

	// OnSample 在每次采样后调用（用于实时推送）。
	OnSample func(Sample)
	// OnMinute 在每分钟结束时调用，参数为该分钟的平均值。
	OnMinute func(store.Metric)
}

func NewCollector() *Collector {
	c := &Collector{}
	c.info = readInfo()
	return c
}

func readInfo() Info {
	in := Info{Arch: runtime.GOARCH}
	if h, err := host.Info(); err == nil {
		in.Hostname, in.OS, in.Platform, in.PlatformVer = h.Hostname, h.OS, h.Platform, h.PlatformVersion
		in.Kernel, in.BootTime = h.KernelVersion, h.BootTime
		in.Virtualization = h.VirtualizationSystem
		if h.KernelArch != "" {
			in.Arch = h.KernelArch
		}
	}
	if cs, err := cpu.Info(); err == nil && len(cs) > 0 {
		in.CPUModel = strings.TrimSpace(cs[0].ModelName)
	}
	in.CPUCores, _ = cpu.Counts(false)
	in.CPUThreads, _ = cpu.Counts(true)
	return in
}

func (c *Collector) Info() Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	in := c.info
	if h, err := os.Hostname(); err == nil {
		in.Hostname = h
	}
	return in
}

// Recent 返回最近 n 秒的实时数据。
func (c *Collector) Recent(n int) []Sample {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if n > len(c.ring) {
		n = len(c.ring)
	}
	out := make([]Sample, n)
	copy(out, c.ring[len(c.ring)-n:])
	return out
}

func (c *Collector) Latest() (Sample, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.ring) == 0 {
		return Sample{}, false
	}
	return c.ring[len(c.ring)-1], true
}

// Run 每秒采样一次，直到 ctx 结束。
func (c *Collector) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var minute []store.Metric
	curMinute := time.Now().Unix() / 60
	tick := 0
	for {
		if tick%10 == 0 {
			c.disks = diskUsages()
		}
		if tick%5 == 0 {
			c.tcp, c.procs = countTCP(), countProcs()
		}
		s := c.sample()
		tick++
		if s != nil {
			c.mu.Lock()
			c.ring = append(c.ring, *s)
			if len(c.ring) > ringSize {
				c.ring = append(c.ring[:0:0], c.ring[len(c.ring)-ringSize:]...)
			}
			c.mu.Unlock()
			if c.OnSample != nil {
				c.OnSample(*s)
			}
			m := s.TS / 60
			if m != curMinute {
				if len(minute) > 0 && c.OnMinute != nil {
					avg := Average(minute)
					avg.TS = curMinute * 60
					c.OnMinute(avg)
				}
				minute, curMinute = minute[:0], m
			}
			minute = append(minute, s.Metric)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Collector) sample() *Sample {
	now := time.Now()
	s := &Sample{}
	s.TS = now.Unix()
	s.NodeID = "local"

	if per, err := cpu.Times(true); err == nil {
		if len(c.prevCPU) == len(per) {
			s.CPUCores = make([]float64, len(per))
			for i := range per {
				s.CPUCores[i] = busy(c.prevCPU[i], per[i])
			}
		}
		c.prevCPU = per
	}
	if tot, err := cpu.Times(false); err == nil && len(tot) > 0 {
		if c.prevCPUT.Total() > 0 {
			s.CPU = busy(c.prevCPUT, tot[0])
		}
		c.prevCPUT = tot[0]
	}
	if l, err := load.Avg(); err == nil {
		s.Load1, s.Load5, s.Load15 = l.Load1, l.Load5, l.Load15
	}
	if v, err := mem.VirtualMemory(); err == nil {
		s.MemTotal = v.Total
		s.MemUsed = v.Total - v.Available
	}
	if sw, err := mem.SwapMemory(); err == nil {
		s.SwapTotal, s.SwapUsed = sw.Total, sw.Used
	}
	for _, d := range c.disks {
		s.DiskTotal += d.Total
		s.DiskUsed += d.Used
	}
	s.Disks = c.disks
	s.TCPConns, s.Procs = c.tcp, c.procs
	if up, err := host.Uptime(); err == nil {
		s.Uptime = up
	}

	elapsed := now.Sub(c.prevAt).Seconds()
	nets, _ := net.IOCounters(true)
	curNet := map[string]net.IOCountersStat{}
	for _, n := range nets {
		if countedNIC(n.Name) {
			curNet[n.Name] = n
		}
	}
	disks, _ := disk.IOCounters(physicalDisks()...)
	if c.prevNet != nil && elapsed > 0 {
		var rx, tx uint64
		for name, n := range curNet {
			if p, ok := c.prevNet[name]; ok && n.BytesRecv >= p.BytesRecv && n.BytesSent >= p.BytesSent {
				rx += n.BytesRecv - p.BytesRecv
				tx += n.BytesSent - p.BytesSent
			}
		}
		s.NetRxBps, s.NetTxBps = float64(rx)/elapsed, float64(tx)/elapsed
		var rd, wr uint64
		for name, d := range disks {
			if p, ok := c.prevDisk[name]; ok && d.ReadBytes >= p.ReadBytes && d.WriteBytes >= p.WriteBytes {
				rd += d.ReadBytes - p.ReadBytes
				wr += d.WriteBytes - p.WriteBytes
			}
		}
		s.DiskReadBps, s.DiskWriteBps = float64(rd)/elapsed, float64(wr)/elapsed
	}
	first := c.prevNet == nil
	c.prevNet, c.prevDisk, c.prevAt = curNet, disks, now
	if first {
		return nil // 第一次只建立基线
	}
	return s
}

func busy(a, b cpu.TimesStat) float64 {
	idle := func(t cpu.TimesStat) float64 { return t.Idle + t.Iowait }
	total := b.Total() - a.Total()
	if total <= 0 {
		return 0
	}
	v := (total - (idle(b) - idle(a))) / total * 100
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// countedNIC 排除回环和容器虚拟网卡，避免流量重复计算。
func countedNIC(name string) bool {
	for _, p := range []string{"lo", "veth", "docker", "br-", "virbr", "cni", "flannel", "cali", "vxlan", "kube"} {
		if name == p || strings.HasPrefix(name, p) && p != "lo" {
			return false
		}
	}
	return true
}

// physicalDisks 返回 /sys/block 中有物理设备的块设备（排除分区、loop、dm 等，避免重复计算）。
func physicalDisks() []string {
	entries, _ := os.ReadDir("/sys/block")
	var out []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join("/sys/block", e.Name(), "device")); err == nil {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func diskUsages() []DiskUsage {
	var out []DiskUsage
	seen := map[string]bool{}
	for _, m := range vfs.Mounts(false) {
		if m.FSType == "tmpfs" || m.FSType == "overlay" || seen[m.Device] {
			continue
		}
		u, err := disk.Usage(m.Point)
		if err != nil || u.Total == 0 {
			continue
		}
		seen[m.Device] = true
		out = append(out, DiskUsage{Mount: m.Point, Device: m.Device, FSType: m.FSType, Total: u.Total, Used: u.Used})
	}
	if len(out) == 0 {
		if u, err := disk.Usage("/"); err == nil {
			out = append(out, DiskUsage{Mount: "/", FSType: u.Fstype, Total: u.Total, Used: u.Used})
		}
	}
	return out
}

func countTCP() float64 {
	n := 0
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Count(string(b), "\n")
		if lines > 0 {
			n += lines - 1
		}
	}
	return float64(n)
}

func countProcs() float64 {
	entries, _ := os.ReadDir("/proc")
	n := 0
	for _, e := range entries {
		if name := e.Name(); name[0] >= '0' && name[0] <= '9' {
			n++
		}
	}
	return float64(n)
}

// Average 计算一组指标的平均值（容量类取最后一个值）。
func Average(ms []store.Metric) store.Metric {
	var a store.Metric
	if len(ms) == 0 {
		return a
	}
	n := float64(len(ms))
	var memUsed, swapUsed, diskUsed float64
	for _, m := range ms {
		a.CPU += m.CPU / n
		a.Load1 += m.Load1 / n
		a.Load5 += m.Load5 / n
		a.Load15 += m.Load15 / n
		memUsed += float64(m.MemUsed) / n
		swapUsed += float64(m.SwapUsed) / n
		diskUsed += float64(m.DiskUsed) / n
		a.DiskReadBps += m.DiskReadBps / n
		a.DiskWriteBps += m.DiskWriteBps / n
		a.NetRxBps += m.NetRxBps / n
		a.NetTxBps += m.NetTxBps / n
		a.TCPConns += m.TCPConns / n
		a.Procs += m.Procs / n
	}
	last := ms[len(ms)-1]
	a.NodeID = last.NodeID
	a.MemTotal, a.SwapTotal, a.DiskTotal = last.MemTotal, last.SwapTotal, last.DiskTotal
	a.MemUsed, a.SwapUsed, a.DiskUsed = uint64(memUsed), uint64(swapUsed), uint64(diskUsed)
	return a
}
