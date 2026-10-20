package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// TrafficRow 是某块网卡某一天的收发字节数。
type TrafficRow struct {
	Day   int    `json:"day"` // YYYYMMDD
	Iface string `json:"iface"`
	Rx    uint64 `json:"rx"`
	Tx    uint64 `json:"tx"`
}

// Traffic 按天累计每块网卡的流量。网卡计数器在重启后归零，所以只累加增量。
type Traffic struct {
	mu    sync.Mutex
	days  map[int]map[string]*TrafficRow
	prev  map[string][2]uint64
	dirty map[[2]string]bool
}

func NewTraffic() *Traffic {
	return &Traffic{days: map[int]map[string]*TrafficRow{}, prev: map[string][2]uint64{}, dirty: map[[2]string]bool{}}
}

// DayOf 把时间转换成 YYYYMMDD（本地时区）。
func DayOf(t time.Time) int {
	y, m, d := t.Date()
	return y*10000 + int(m)*100 + d
}

// Observe 记录一次各网卡的累计计数（rx, tx）。
func (t *Traffic) Observe(now time.Time, counters map[string][2]uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	day := DayOf(now)
	for name, cur := range counters {
		p, ok := t.prev[name]
		t.prev[name] = cur
		if !ok {
			continue // 第一次只建立基线
		}
		var drx, dtx uint64
		if cur[0] >= p[0] {
			drx = cur[0] - p[0]
		} else {
			drx = cur[0] // 计数器归零（重启或网卡重建）
		}
		if cur[1] >= p[1] {
			dtx = cur[1] - p[1]
		} else {
			dtx = cur[1]
		}
		if drx == 0 && dtx == 0 {
			continue
		}
		t.add(day, name, drx, dtx)
	}
}

func (t *Traffic) add(day int, iface string, rx, tx uint64) {
	m := t.days[day]
	if m == nil {
		m = map[string]*TrafficRow{}
		t.days[day] = m
	}
	r := m[iface]
	if r == nil {
		r = &TrafficRow{Day: day, Iface: iface}
		m[iface] = r
	}
	r.Rx += rx
	r.Tx += tx
	t.dirty[[2]string{strconv.Itoa(day), iface}] = true
}

// Load 载入已保存的数据（覆盖同一天同一网卡的值）。
func (t *Traffic) Load(rows []TrafficRow) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range rows {
		m := t.days[r.Day]
		if m == nil {
			m = map[string]*TrafficRow{}
			t.days[r.Day] = m
		}
		rr := r
		m[r.Iface] = &rr
	}
}

// Rows 返回 [from, to] 日期范围内的数据，按日期和网卡排序。
func (t *Traffic) Rows(from, to int) []TrafficRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []TrafficRow{}
	for day, m := range t.days {
		if day < from || day > to {
			continue
		}
		for _, r := range m {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].Iface < out[j].Iface
	})
	return out
}

// TakeDirty 返回自上次调用以来变化过的行。
func (t *Traffic) TakeDirty() []TrafficRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []TrafficRow
	for k := range t.dirty {
		day, _ := strconv.Atoi(k[0])
		if r := t.days[day][k[1]]; r != nil {
			out = append(out, *r)
		}
	}
	t.dirty = map[[2]string]bool{}
	return out
}

// Prune 删除早于 keepDays 天的数据。
func (t *Traffic) Prune(now time.Time, keepDays int) {
	cut := DayOf(now.AddDate(0, 0, -keepDays))
	t.mu.Lock()
	defer t.mu.Unlock()
	for day := range t.days {
		if day < cut {
			delete(t.days, day)
		}
	}
}

// LoadFile / SaveFile 用于独立 agent 的持久化（JSON 文件，写入时先写临时文件再改名）。
func (t *Traffic) LoadFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var rows []TrafficRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return err
	}
	t.Load(rows)
	return nil
}

func (t *Traffic) SaveFile(path string) error {
	rows := t.Rows(0, 99991231)
	t.TakeDirty()
	b, _ := json.Marshal(rows)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
