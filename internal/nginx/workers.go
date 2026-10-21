package nginx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// reload 的信号只是通知主进程，命令返回时新的工作进程可能还没起来，紧接着的请求仍由旧配置处理
// （例如新站点返回 default_server 的 444）。这里按 /proc 中的工作进程判断新配置是否已经生效。

// masters 返回本运行方式下 Nginx 主进程的 PID。
func (rt Runtime) masters(ctx context.Context) []int {
	if rt.Mode == ModeContainer {
		out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Pid}}", rt.Container).Output()
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(out))); err == nil && pid > 0 {
			return []int{pid}
		}
		return nil
	}
	// 宿主机：只看与面板在同一挂载命名空间的主进程，排除容器里的 Nginx
	self, _ := os.Readlink("/proc/self/ns/mnt")
	var out []int
	for _, pid := range procs() {
		if !strings.Contains(cmdline(pid), "master process") {
			continue
		}
		if ns, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/ns/mnt"); ns != self {
			continue
		}
		out = append(out, pid)
	}
	return out
}

// workers 返回这些主进程下正在接受连接的工作进程（不含正在退出的）。
func workers(masters []int) map[int]bool {
	set := map[int]bool{}
	if len(masters) == 0 {
		return set
	}
	isMaster := map[int]bool{}
	for _, m := range masters {
		isMaster[m] = true
	}
	for _, pid := range procs() {
		if !isMaster[ppid(pid)] {
			continue
		}
		if c := cmdline(pid); strings.Contains(c, "worker process") && !strings.Contains(c, "shutting down") {
			set[pid] = true
		}
	}
	return set
}

// snapshot 记录每个主进程当前的工作进程。
func snapshot(masters []int) map[int]map[int]bool {
	out := map[int]map[int]bool{}
	for _, m := range masters {
		out[m] = workers([]int{m})
	}
	return out
}

// waitWorkers 等到某个主进程的工作进程全部换成新的（最多 10 秒），即收到 reload 的那个主进程已经用上新配置。
// 同一台机器上可能有多个 Nginx，按主进程分别判断；找不到主进程时直接返回。
func waitWorkers(ctx context.Context, before map[int]map[int]bool) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		checked := false
		for m, old := range before {
			if len(old) == 0 {
				continue
			}
			checked = true
			cur := workers([]int{m})
			fresh := len(cur) > 0
			for pid := range cur {
				if old[pid] {
					fresh = false
					break
				}
			}
			if fresh {
				return
			}
		}
		if !checked {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func procs() []int {
	entries, _ := os.ReadDir("/proc")
	var out []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, pid)
		}
	}
	return out
}

func cmdline(pid int) string {
	b, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	return string(bytes.ReplaceAll(b, []byte{0}, []byte{' '}))
}

// ppid 读取 /proc/<pid>/stat 的第 4 个字段（进程名可能含空格和括号，从最后一个 ')' 之后开始数）。
func ppid(pid int) int {
	b, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}
