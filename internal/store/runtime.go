package store

// Runtime 是可在面板设置中修改的运行期选项。
type Runtime struct {
	SessionTimeoutHours int    `json:"sessionTimeoutHours"`
	BanMaxFailures      int    `json:"banMaxFailures"`
	BanWindowMinutes    int    `json:"banWindowMinutes"`
	BanMinutes          int    `json:"banMinutes"`
	TrustedProxy        bool   `json:"trustedProxy"`
	DefaultAppearance   string `json:"defaultAppearance"` // auto / desktop / admin
	TerminalKeepMinutes int    `json:"terminalKeepMinutes"`
	TrashRetentionDays  int    `json:"trashRetentionDays"`
	AuditRetentionDays  int    `json:"auditRetentionDays"`
}

func DefaultRuntime() Runtime {
	return Runtime{
		SessionTimeoutHours: 12,
		BanMaxFailures:      5,
		BanWindowMinutes:    10,
		BanMinutes:          30,
		DefaultAppearance:   "auto",
		TerminalKeepMinutes: 30,
		TrashRetentionDays:  30,
		AuditRetentionDays:  180,
	}
}

func (r *Runtime) Normalize() {
	d := DefaultRuntime()
	clamp := func(v *int, def, min, max int) {
		if *v == 0 {
			*v = def
		}
		if *v < min {
			*v = min
		}
		if *v > max {
			*v = max
		}
	}
	clamp(&r.SessionTimeoutHours, d.SessionTimeoutHours, 1, 24*30)
	clamp(&r.BanMaxFailures, d.BanMaxFailures, 1, 100)
	clamp(&r.BanWindowMinutes, d.BanWindowMinutes, 1, 24*60)
	clamp(&r.BanMinutes, d.BanMinutes, 1, 30*24*60)
	clamp(&r.TerminalKeepMinutes, d.TerminalKeepMinutes, 1, 24*60)
	clamp(&r.TrashRetentionDays, d.TrashRetentionDays, 1, 3650)
	clamp(&r.AuditRetentionDays, d.AuditRetentionDays, 7, 3650)
	switch r.DefaultAppearance {
	case "auto", "desktop", "admin":
	default:
		r.DefaultAppearance = d.DefaultAppearance
	}
}

const runtimeKey = "runtime"

func (s *Store) Runtime() Runtime {
	r := DefaultRuntime()
	s.GetSetting(runtimeKey, &r)
	r.Normalize()
	return r
}

func (s *Store) SetRuntime(r Runtime) error {
	r.Normalize()
	return s.SetSetting(runtimeKey, r)
}
