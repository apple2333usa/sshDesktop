package vfs

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Local 是本机文件系统。
type Local struct{}

func (Local) Stat(n string) (fs.FileInfo, error)  { return os.Stat(n) }
func (Local) Lstat(n string) (fs.FileInfo, error) { return os.Lstat(n) }
func (Local) Readlink(n string) (string, error)   { return os.Readlink(n) }
func (Local) Open(n string) (File, error)         { return os.Open(n) }
func (Local) OpenFile(n string, flag int, perm os.FileMode) (File, error) {
	return os.OpenFile(n, flag, perm)
}
func (Local) Mkdir(n string, perm os.FileMode) error    { return os.Mkdir(n, perm) }
func (Local) MkdirAll(n string, perm os.FileMode) error { return os.MkdirAll(n, perm) }
func (Local) Rename(o, n string) error                  { return os.Rename(o, n) }
func (Local) Remove(n string) error                     { return os.Remove(n) }
func (Local) RemoveAll(n string) error                  { return os.RemoveAll(n) }
func (Local) Symlink(o, n string) error                 { return os.Symlink(o, n) }
func (Local) Chmod(n string, m os.FileMode) error       { return os.Chmod(n, m) }
func (Local) Chown(n string, uid, gid int) error        { return os.Lchown(n, uid, gid) }
func (Local) Chtimes(n string, a, m time.Time) error    { return os.Chtimes(n, a, m) }

func (Local) ReadDir(n string) ([]fs.FileInfo, error) {
	entries, err := os.ReadDir(n)
	if err != nil {
		return nil, err
	}
	out := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue // 读取过程中被删除
		}
		out = append(out, fi)
	}
	return out, nil
}

// TrashRoot：本机按挂载点使用 <挂载点>/.sshdesktop-trash。
func (Local) TrashRoot(name string) (string, error) {
	mp := MountPointOf(name)
	return filepath.Join(mp, ".sshdesktop-trash"), nil
}

func ownerOf(fi fs.FileInfo) (int, int) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	type owner interface{ OwnerIDs() (int, int) }
	if o, ok := fi.Sys().(owner); ok {
		return o.OwnerIDs()
	}
	return -1, -1
}

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
			if pseudoFS[m.FSType] || strings.HasPrefix(m.Point, "/proc") || strings.HasPrefix(m.Point, "/sys") ||
				strings.HasPrefix(m.Point, "/dev") || strings.HasPrefix(m.Point, "/run") ||
				strings.HasPrefix(m.Point, "/snap/") || strings.HasPrefix(m.Point, "/var/lib/docker/") ||
				strings.HasPrefix(m.Point, "/boot/efi") {
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
