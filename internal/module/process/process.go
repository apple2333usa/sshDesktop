// Package process 是进程管理：列表、详情、发送信号。仅本机。
package process

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v4/mem"
	gproc "github.com/shirou/gopsutil/v4/process"
)

const clkTck = 100 // Linux 的 USER_HZ 固定为 100

type Proc struct {
	PID     int32   `json:"pid"`
	PPID    int32   `json:"ppid"`
	Name    string  `json:"name"`
	User    string  `json:"user"`
	State   string  `json:"state"`
	CPU     float64 `json:"cpu"` // 与 top 一致，单核满载为 100
	RSS     uint64  `json:"rss"`
	Mem     float64 `json:"mem"`
	Threads int     `json:"threads"`
	Started int64   `json:"started"` // 毫秒
	Cmdline string  `json:"cmdline"`
}

type sample struct {
	ticks uint64
	at    time.Time
}

type Service struct {
	mu      sync.Mutex
	prev    map[int32]sample
	users   map[uint32]string
	usersAt time.Time
	boot    time.Time
	audit   func(module, action string) gin.HandlerFunc
}

func New(audit func(module, action string) gin.HandlerFunc) *Service {
	s := &Service{prev: map[int32]sample{}, audit: audit}
	if b, err := os.ReadFile("/proc/stat"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "btime ") {
				sec, _ := strconv.ParseInt(strings.TrimSpace(line[6:]), 10, 64)
				s.boot = time.Unix(sec, 0)
			}
		}
	}
	return s
}

func (s *Service) Register(r *gin.RouterGroup, view, manage gin.HandlerFunc) {
	r.GET("/processes", view, s.list)
	r.GET("/processes/:pid", view, s.detail)
	r.POST("/processes/:pid/signal", manage, s.audit("process", "signal"), s.signal)
}

func (s *Service) userName(uid uint32) string {
	if s.users == nil || time.Since(s.usersAt) > time.Minute {
		s.users = map[uint32]string{}
		s.usersAt = time.Now()
		if b, err := os.ReadFile("/etc/passwd"); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				f := strings.Split(line, ":")
				if len(f) > 2 {
					if id, err := strconv.ParseUint(f[2], 10, 32); err == nil {
						s.users[uint32(id)] = f[0]
					}
				}
			}
		}
	}
	if n, ok := s.users[uid]; ok {
		return n
	}
	return strconv.FormatUint(uint64(uid), 10)
}

// readProc 解析 /proc/<pid>/stat、status、cmdline。
func (s *Service) readProc(pid int32, memTotal uint64, now time.Time, cur map[int32]sample) (*Proc, bool) {
	dir := "/proc/" + strconv.Itoa(int(pid))
	b, err := os.ReadFile(dir + "/stat")
	if err != nil {
		return nil, false
	}
	// 第二个字段 comm 在括号中，可能包含空格
	l, r := bytes.IndexByte(b, '('), bytes.LastIndexByte(b, ')')
	if l < 0 || r < l {
		return nil, false
	}
	p := &Proc{PID: pid, Name: string(b[l+1 : r])}
	f := strings.Fields(string(b[r+1:]))
	if len(f) < 22 {
		return nil, false
	}
	p.State = f[0]
	ppid, _ := strconv.Atoi(f[1])
	p.PPID = int32(ppid)
	ut, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	p.Threads, _ = strconv.Atoi(f[17])
	start, _ := strconv.ParseUint(f[19], 10, 64)
	rssPages, _ := strconv.ParseUint(f[21], 10, 64)
	p.RSS = rssPages * uint64(os.Getpagesize())
	if memTotal > 0 {
		p.Mem = float64(p.RSS) / float64(memTotal) * 100
	}
	p.Started = s.boot.Add(time.Duration(start) * time.Second / clkTck).UnixMilli()
	ticks := ut + st
	if prev, ok := s.prev[pid]; ok && ticks >= prev.ticks {
		if dt := now.Sub(prev.at).Seconds(); dt > 0 {
			p.CPU = float64(ticks-prev.ticks) / clkTck / dt * 100
		}
	}
	cur[pid] = sample{ticks: ticks, at: now}
	if fi, err := os.Stat(dir); err == nil {
		if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
			p.User = s.userName(sys.Uid)
		}
	}
	if cl, err := os.ReadFile(dir + "/cmdline"); err == nil {
		cmd := strings.TrimSpace(strings.ReplaceAll(string(cl), "\x00", " "))
		if len(cmd) > 512 {
			cmd = cmd[:512] + "…"
		}
		p.Cmdline = cmd
	}
	return p, true
}

func (s *Service) list(c *gin.Context) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		api.Fail(c, err)
		return
	}
	var memTotal uint64
	if v, err := mem.VirtualMemory(); err == nil {
		memTotal = v.Total
	}
	now := time.Now()
	cur := map[int32]sample{}
	out := []*Proc{}
	s.mu.Lock()
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if p, ok := s.readProc(int32(pid), memTotal, now, cur); ok {
			out = append(out, p)
		}
	}
	s.prev = cur
	s.mu.Unlock()
	api.OK(c, gin.H{"processes": out, "memTotal": memTotal, "self": os.Getpid()})
}

func (s *Service) detail(c *gin.Context) {
	pid, err := strconv.Atoi(c.Param("pid"))
	if err != nil {
		api.Fail(c, api.BadRequest("进程号无效"))
		return
	}
	p, err := gproc.NewProcess(int32(pid))
	if err != nil {
		api.Fail(c, api.NotFound("进程不存在"))
		return
	}
	res := gin.H{"pid": pid}
	res["name"], _ = p.Name()
	res["cmdline"], _ = p.CmdlineSlice()
	res["exe"], _ = p.Exe()
	res["cwd"], _ = p.Cwd()
	res["status"], _ = p.Status()
	res["nice"], _ = p.Nice()
	res["threads"], _ = p.NumThreads()
	res["fds"], _ = p.NumFDs()
	res["memory"], _ = p.MemoryInfo()
	res["io"], _ = p.IOCounters()
	res["username"], _ = p.Username()
	res["ppid"], _ = p.Ppid()
	res["createTime"], _ = p.CreateTime()
	if files, err := p.OpenFiles(); err == nil {
		if len(files) > 500 {
			files = files[:500]
		}
		res["openFiles"] = files
	}
	if conns, err := p.Connections(); err == nil {
		res["connections"] = conns
	}
	if children, err := p.Children(); err == nil {
		var pids []int32
		for _, ch := range children {
			pids = append(pids, ch.Pid)
		}
		res["children"] = pids
	}
	api.OK(c, res)
}

var signals = map[string]syscall.Signal{
	"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL, "HUP": syscall.SIGHUP, "INT": syscall.SIGINT,
	"STOP": syscall.SIGSTOP, "CONT": syscall.SIGCONT, "USR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2,
}

func (s *Service) signal(c *gin.Context) {
	var req struct {
		Signal string `json:"signal"`
	}
	if !api.Bind(c, &req) {
		return
	}
	pid, err := strconv.Atoi(c.Param("pid"))
	if err != nil || pid <= 1 {
		api.Fail(c, api.BadRequest("不能向该进程发送信号"))
		return
	}
	sig, ok := signals[strings.ToUpper(req.Signal)]
	if !ok {
		api.Fail(c, api.BadRequest("不支持的信号"))
		return
	}
	name := ""
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		name = strings.TrimSpace(string(b))
	}
	api.Target(c, fmt.Sprintf("%d (%s) SIG%s", pid, name, strings.ToUpper(req.Signal)))
	if pid == os.Getpid() {
		api.Fail(c, api.BadRequest("不能结束面板自身，请在系统服务中重启 sshdesktop"))
		return
	}
	if err := syscall.Kill(pid, sig); err != nil {
		api.Fail(c, api.Err(400, api.CodeCommandFailed, "发送信号失败："+err.Error()))
		return
	}
	api.OK(c, nil)
}
