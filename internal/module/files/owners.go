package files

import (
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/gin-gonic/gin"
	"github.com/pkg/sftp"
)

// ownerCache 缓存某个节点的 /etc/passwd 与 /etc/group，文件变化时重新加载。
type ownerCache struct {
	f         vfs.FS
	mu        sync.Mutex
	loadedAt  time.Time
	checkedAt time.Time
	users     map[int]string
	groups    map[int]string
	byUser    map[string]int
	byGroup   map[string]int
}

// ownerSet 按文件系统（每个节点一个）保存 ownerCache。
type ownerSet struct {
	mu sync.Mutex
	m  map[vfs.FS]*ownerCache
}

func newOwnerSet() *ownerSet { return &ownerSet{m: map[vfs.FS]*ownerCache{}} }

func (o *ownerSet) of(f vfs.FS) *ownerCache {
	o.mu.Lock()
	defer o.mu.Unlock()
	c := o.m[f]
	if c == nil {
		c = &ownerCache{f: f}
		o.m[f] = c
	}
	return c
}

func readSmall(f vfs.FS, p string) ([]byte, error) {
	fh, err := f.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return io.ReadAll(io.LimitReader(fh, 8<<20))
}

func parseIDFile(f vfs.FS, p string) (map[int]string, map[string]int) {
	ids, names := map[int]string{}, map[string]int{}
	b, err := readSmall(f, p)
	if err != nil {
		return ids, names
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		if _, ok := ids[id]; !ok {
			ids[id] = f[0]
		}
		names[f[0]] = id
	}
	return ids, names
}

func (o *ownerCache) refresh() {
	// 远程节点每次检查都要一次往返，所以最多 10 秒检查一次
	if o.users != nil && time.Since(o.checkedAt) < 10*time.Second {
		return
	}
	o.checkedAt = time.Now()
	newest := time.Time{}
	for _, p := range []string{"/etc/passwd", "/etc/group"} {
		if fi, err := o.f.Stat(p); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	if o.users != nil && !newest.After(o.loadedAt) {
		return
	}
	o.users, o.byUser = parseIDFile(o.f, "/etc/passwd")
	o.groups, o.byGroup = parseIDFile(o.f, "/etc/group")
	o.loadedAt = newest
}

func (o *ownerCache) user(uid int) string {
	if uid < 0 {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.refresh()
	if n, ok := o.users[uid]; ok {
		return n
	}
	return strconv.Itoa(uid)
}

func (o *ownerCache) group(gid int) string {
	if gid < 0 {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.refresh()
	if n, ok := o.groups[gid]; ok {
		return n
	}
	return strconv.Itoa(gid)
}

// lookup 把用户名 / 组名（或数字 ID）转换成 ID；空字符串返回 -1（不修改）。
func (o *ownerCache) lookup(userName, groupName string) (int, int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.refresh()
	resolve := func(name string, m map[string]int, kind string) (int, error) {
		if name == "" {
			return -1, nil
		}
		if id, ok := m[name]; ok {
			return id, nil
		}
		if id, err := strconv.Atoi(name); err == nil && id >= 0 {
			return id, nil
		}
		return 0, api.BadRequest(kind + " " + name + " 不存在")
	}
	uid, err := resolve(userName, o.byUser, "用户")
	if err != nil {
		return 0, 0, err
	}
	gid, err := resolve(groupName, o.byGroup, "用户组")
	return uid, gid, err
}

type idName struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}

func (s *Service) ownerList(c *gin.Context) {
	f, ok := s.fs(c)
	if !ok {
		return
	}
	o := s.owners.of(f)
	o.mu.Lock()
	o.refresh()
	var users, groups []idName
	for id, n := range o.users {
		users = append(users, idName{n, id})
	}
	for id, n := range o.groups {
		groups = append(groups, idName{n, id})
	}
	o.mu.Unlock()
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	api.OK(c, gin.H{"users": users, "groups": groups})
}

// times 返回访问时间和状态改变时间（毫秒）。
func times(fi fs.FileInfo) (int64, int64) {
	switch st := fi.Sys().(type) {
	case *syscall.Stat_t:
		return time.Unix(st.Atim.Unix()).UnixMilli(), time.Unix(st.Ctim.Unix()).UnixMilli()
	case *sftp.FileStat: // SFTP 不提供状态改变时间
		return int64(st.Atime) * 1000, 0
	}
	return 0, 0
}
