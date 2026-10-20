package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/metric"
)

// MinuteStore 是独立 agent 的分钟级指标存储：每天一个 JSON Lines 文件，保留 7 天。
// 面板断线期间的数据靠它补齐。
type MinuteStore struct {
	dir string
	mu  sync.Mutex
}

const historyDays = 7

func NewMinuteStore(dir string) *MinuteStore { return &MinuteStore{dir: dir} }

func (s *MinuteStore) file(ts int64) string {
	return filepath.Join(s.dir, "metrics-"+time.Unix(ts, 0).UTC().Format("20060102")+".jsonl")
}

func (s *MinuteStore) Append(m metric.Metric) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.file(m.TS), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(m)
	_, err = f.Write(append(b, '\n'))
	return err
}

// Query 返回 [from, to] 内的分钟数据，按时间排序。
func (s *MinuteStore) Query(from, to int64) []metric.Metric {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []metric.Metric{}
	for day := time.Unix(from, 0).UTC().Truncate(24 * time.Hour); !day.After(time.Unix(to, 0).UTC()); day = day.Add(24 * time.Hour) {
		f, err := os.Open(s.file(day.Unix()))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var m metric.Metric
			if json.Unmarshal(sc.Bytes(), &m) == nil && m.TS >= from && m.TS <= to {
				out = append(out, m)
			}
		}
		f.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// Cleanup 删除超过保留期的文件。
func (s *MinuteStore) Cleanup(now time.Time) {
	cut := now.UTC().AddDate(0, 0, -historyDays).Format("20060102")
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "metrics-") && strings.HasSuffix(n, ".jsonl") && n[8:16] < cut {
			os.Remove(filepath.Join(s.dir, n))
		}
	}
}
