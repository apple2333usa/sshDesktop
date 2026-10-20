package files

import (
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/gin-gonic/gin"
)

// ownerCache 缓存 /etc/passwd 与 /etc/group，文件变化时重新加载。
type ownerCache struct {
	mu       sync.Mutex
	loadedAt time.Time
	users    map[int]string
	groups   map[int]string
	byUser   map[string]int
	byGroup  map[string]int
}

func newOwnerCache() *ownerCache { return &ownerCache{} }

func parseIDFile(p string) (map[int]string, map[string]int) {
	ids, names := map[int]string{}, map[string]int{}
	b, err := os.ReadFile(p)
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
	newest := time.Time{}
	for _, p := range []string{"/etc/passwd", "/etc/group"} {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	if o.users != nil && !newest.After(o.loadedAt) {
		return
	}
	o.users, o.byUser = parseIDFile("/etc/passwd")
	o.groups, o.byGroup = parseIDFile("/etc/group")
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
	s.owners.mu.Lock()
	s.owners.refresh()
	var users, groups []idName
	for id, n := range s.owners.users {
		users = append(users, idName{n, id})
	}
	for id, n := range s.owners.groups {
		groups = append(groups, idName{n, id})
	}
	s.owners.mu.Unlock()
	sort.Slice(users, func(i, j int) bool { return users[i].ID < users[j].ID })
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	api.OK(c, gin.H{"users": users, "groups": groups})
}

// times 返回访问时间和状态改变时间（毫秒）。
func times(fi fs.FileInfo) (int64, int64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Atim.Unix()).UnixMilli(), time.Unix(st.Ctim.Unix()).UnixMilli()
	}
	return 0, 0
}
