// Package cronexpr 解析标准 5 段 cron 表达式（分 时 日 月 周），支持 *、范围、步长、列表、
// 月份和星期的英文缩写，以及 @hourly / @daily / @weekly / @monthly / @yearly / @reboot。
// 只用于校验和计算下次执行时间；实际调度由系统 cron 负责。
package cronexpr

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	minute, hour, dom, month, dow uint64
	domStar, dowStar              bool
	Reboot                        bool
}

var macros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dowNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// Parse 解析表达式。
func Parse(expr string) (*Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "@reboot" {
		return &Schedule{Reboot: true}, nil
	}
	if m, ok := macros[strings.ToLower(expr)]; ok {
		expr = m
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron 表达式应为 5 段（分 时 日 月 周）：%q", expr)
	}
	s := &Schedule{}
	var err error
	if s.minute, err = field(f[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("分钟：%w", err)
	}
	if s.hour, err = field(f[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("小时：%w", err)
	}
	if s.dom, err = field(f[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("日期：%w", err)
	}
	if s.month, err = field(f[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("月份：%w", err)
	}
	if s.dow, err = field(f[4], 0, 7, dowNames); err != nil {
		return nil, fmt.Errorf("星期：%w", err)
	}
	if s.dow&(1<<7) != 0 { // 7 也表示星期日
		s.dow |= 1
	}
	s.domStar = f[2] == "*" || strings.HasPrefix(f[2], "*/")
	s.dowStar = f[4] == "*" || strings.HasPrefix(f[4], "*/")
	return s, nil
}

func value(s string, names map[string]int) (int, error) {
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	return strconv.Atoi(s)
}

func field(s string, lo, hi int, names map[string]int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(s, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("步长无效：%q", part)
			}
			step, part = n, part[:i]
		}
		start, end := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			x, err1 := value(a, names)
			y, err2 := value(b, names)
			if err1 != nil || err2 != nil {
				return 0, fmt.Errorf("范围无效：%q", part)
			}
			start, end = x, y
		default:
			x, err := value(part, names)
			if err != nil {
				return 0, fmt.Errorf("取值无效：%q", part)
			}
			start, end = x, x
			if step > 1 { // 形如 5/10：从 5 开始每 10
				end = hi
			}
		}
		if start < lo || end > hi || start > end {
			return 0, fmt.Errorf("取值超出范围 %d-%d：%q", lo, hi, part)
		}
		for v := start; v <= end; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

func has(bits uint64, v int) bool { return bits&(1<<uint(v)) != 0 }

// dayMatch 与 Vixie cron 一致：日和星期都不是 * 时，满足其一即可。
func (s *Schedule) dayMatch(t time.Time) bool {
	d, w := has(s.dom, t.Day()), has(s.dow, int(t.Weekday()))
	if s.domStar || s.dowStar {
		return d && w
	}
	return d || w
}

// Next 返回 after 之后的下一次执行时间；@reboot 或五年内不会执行时返回零值。
func (s *Schedule) Next(after time.Time) time.Time {
	if s.Reboot {
		return time.Time{}
	}
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(5, 0, 0)
	for t.Before(limit) {
		if !has(s.month, int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.dayMatch(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !has(s.hour, t.Hour()) {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !has(s.minute, t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}
