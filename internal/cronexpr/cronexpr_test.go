package cronexpr

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	loc := time.Local
	base := time.Date(2026, 10, 20, 10, 30, 15, 0, loc) // 周二
	cases := []struct{ expr, want string }{
		{"* * * * *", "2026-10-20 10:31"},
		{"*/15 * * * *", "2026-10-20 10:45"},
		{"0 3 * * *", "2026-10-21 03:00"},
		{"@daily", "2026-10-21 00:00"},
		{"@hourly", "2026-10-20 11:00"},
		{"0 0 1 * *", "2026-11-01 00:00"},
		{"30 2 * * sun", "2026-10-25 02:30"},
		{"0 9 * * 1-5", "2026-10-21 09:00"},
		{"0 0 * * 7", "2026-10-25 00:00"},
		{"0 0 29 feb *", "2028-02-29 00:00"},
		// 日和周都限定时，任一满足即可（与 Vixie cron 一致）
		{"0 0 1 * 5", "2026-10-23 00:00"},
		{"5,10 1-2/1 * jan-dec *", "2026-10-21 01:05"},
	}
	for _, c := range cases {
		s, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("%s：%v", c.expr, err)
		}
		if got := s.Next(base).Format("2006-01-02 15:04"); got != c.want {
			t.Errorf("%s：下次 %s，期望 %s", c.expr, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, e := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *", "5-1 * * * *", "a * * * *", "@every 5m"} {
		if _, err := Parse(e); err == nil {
			t.Errorf("%q 应解析失败", e)
		}
	}
	s, err := Parse("@reboot")
	if err != nil || !s.Reboot || !s.Next(time.Now()).IsZero() {
		t.Fatal("@reboot 没有下次执行时间")
	}
}
