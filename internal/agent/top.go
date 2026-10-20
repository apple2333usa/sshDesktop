package agent

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Proc 是资源占用排行中的一个进程（只含普通用户也能看到的信息）。
type Proc struct {
	PID  int     `json:"pid"`
	Name string  `json:"name"`
	CPU  float64 `json:"cpu"` // 百分比，相对单个核心
	Mem  uint64  `json:"mem"` // RSS 字节
}

type procTimes struct {
	name  string
	ticks uint64
	rss   uint64
}

func readProcTimes() map[int]procTimes {
	out := map[int]procTimes{}
	entries, _ := os.ReadDir("/proc")
	page := uint64(os.Getpagesize())
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if l < 0 || r < l {
			continue
		}
		f := strings.Fields(s[r+1:])
		if len(f) < 22 {
			continue
		}
		ut, _ := strconv.ParseUint(f[11], 10, 64)
		st, _ := strconv.ParseUint(f[12], 10, 64)
		rss, _ := strconv.ParseUint(f[21], 10, 64)
		out[pid] = procTimes{name: s[l+1 : r], ticks: ut + st, rss: rss * page}
	}
	return out
}

// TopProcs 采样 0.5 秒，返回 CPU 占用最高的 n 个进程。
func TopProcs(n int) []Proc {
	if n <= 0 || n > 100 {
		n = 10
	}
	a := readProcTimes()
	start := time.Now()
	time.Sleep(500 * time.Millisecond)
	b := readProcTimes()
	secs := time.Since(start).Seconds()
	const hz = 100 // Linux 的 USER_HZ 几乎总是 100
	out := make([]Proc, 0, len(b))
	for pid, cur := range b {
		p := Proc{PID: pid, Name: cur.name, Mem: cur.rss}
		if prev, ok := a[pid]; ok && cur.ticks >= prev.ticks {
			p.CPU = float64(cur.ticks-prev.ticks) / hz / secs * 100
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CPU != out[j].CPU {
			return out[i].CPU > out[j].CPU
		}
		return out[i].Mem > out[j].Mem
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
