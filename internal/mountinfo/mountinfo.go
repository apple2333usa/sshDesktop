// Package mountinfo 读取本机挂载点，面板与独立 agent 共用。
package mountinfo

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Mount 是一条挂载记录。
type Mount struct {
	Point  string `json:"point"`
	Device string `json:"device"`
	FSType string `json:"fsType"`
}

// 不展示给用户、也不作为回收站位置的虚拟文件系统。
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devpts": true, "cgroup": true, "cgroup2": true, "securityfs": true,
	"debugfs": true, "tracefs": true, "pstore": true, "bpf": true, "mqueue": true, "hugetlbfs": true,
	"configfs": true, "fusectl": true, "binfmt_misc": true, "autofs": true, "rpc_pipefs": true,
	"nsfs": true, "efivarfs": true, "devtmpfs": true, "squashfs": true, "ramfs": true,
}

// Mounts 读取 /proc/self/mountinfo。all 为 false 时过滤掉虚拟文件系统和 /run、/dev 等系统目录。
func Mounts(all bool) []Mount {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return []Mount{{Point: "/", FSType: "unknown"}}
	}
	defer f.Close()
	var out []Mount
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
		fields := strings.Fields(sc.Text())
		sep := -1
		for i, s := range fields {
			if s == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || len(fields) < sep+3 || len(fields) < 5 {
			continue
		}
		m := Mount{Point: unescapeMount(fields[4]), FSType: fields[sep+1], Device: fields[sep+2]}
		if !all {
			if HiddenMount(m.FSType, m.Point) {
				continue
			}
			if fi, err := os.Stat(m.Point); err != nil || !fi.IsDir() {
				continue // 绑定挂载的单个文件（容器里的 /etc/hosts 等）
			}
		}
		if seen[m.Point] {
			continue
		}
		seen[m.Point] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Point < out[j].Point })
	return out
}

// HiddenMount 判断挂载点是否属于不展示的虚拟文件系统或系统目录。
func HiddenMount(fsType, point string) bool {
	if pseudoFS[fsType] || fsType == "tmpfs" && point != "/tmp" || fsType == "overlay" && point != "/" {
		return true
	}
	for _, p := range []string{"/proc", "/sys", "/dev", "/run", "/snap/", "/var/lib/docker/", "/boot/efi"} {
		if point == p || strings.HasPrefix(point, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

// MountPointOf 返回 path 所在的挂载点（最长前缀匹配）。
func MountPointOf(path string) string {
	path = filepath.Clean(path)
	best := "/"
	for _, m := range Mounts(true) {
		p := m.Point
		if (path == p || strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/")) && len(p) > len(best) {
			best = p
		}
	}
	return best
}
