package monitor

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/metric"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"gorm.io/gorm/clause"
)

// remote 是一个远程节点的监控来源。有 agent 接口时经 SSH 的 direct-streamlocal 通道读取实时流、
// 补齐离线期间的分钟历史并同步流量统计；没有时在 SSH 连接上运行采集脚本（每 5 秒一次）。
type remote struct {
	s      *Service
	n      *node.SSH
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.RWMutex
	ring    []agent.Sample
	info    *agent.Info
	source  string
	version string
	err     string

	minute    []metric.Metric
	curMinute int64
}

// ringSeconds：内存中保留最近 10 分钟的实时数据。
const ringSeconds = 600

// agentRecheck：无 agent 模式下每隔一段时间检查一次对方是否装上了 agent。
var agentRecheck = 10 * time.Minute

func newRemote(s *Service, n *node.SSH) *remote {
	ctx, cancel := context.WithCancel(context.Background())
	return &remote{s: s, n: n, ctx: ctx, cancel: cancel}
}

func (r *remote) stop() { r.cancel() }

func (r *remote) setErr(err error) {
	r.mu.Lock()
	if err == nil {
		r.err = ""
	} else {
		r.err = err.Error()
	}
	r.mu.Unlock()
}

func (r *remote) setSource(src, version string, info agent.Info) {
	r.mu.Lock()
	r.source, r.version, r.info = src, version, &info
	r.mu.Unlock()
}

func (r *remote) overview() Overview {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o := Overview{ID: r.n.ID(), Source: r.source, AgentVersion: r.version, Info: r.info, Error: r.err}
	if len(r.ring) > 0 {
		sm := r.ring[len(r.ring)-1]
		o.Sample = &sm
	}
	return o
}

func (r *remote) recent(seconds int) []agent.Sample {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cut := time.Now().Unix() - int64(seconds)
	out := []agent.Sample{}
	for _, s := range r.ring {
		if s.TS > cut {
			out = append(out, s)
		}
	}
	return out
}

func (r *remote) onSample(sm agent.Sample) {
	sm.NodeID = r.n.ID()
	r.mu.Lock()
	r.ring = append(r.ring, sm)
	cut := sm.TS - ringSeconds
	i := 0
	for i < len(r.ring) && r.ring[i].TS <= cut {
		i++
	}
	if i > 0 {
		r.ring = append(r.ring[:0:0], r.ring[i:]...)
	}
	// 按分钟求平均写入历史
	m := sm.TS / 60
	var done []metric.Metric
	var doneMinute int64
	if m != r.curMinute {
		if len(r.minute) > 0 && r.curMinute != 0 {
			done, doneMinute = r.minute, r.curMinute
		}
		r.minute, r.curMinute = nil, m
	}
	r.minute = append(r.minute, sm.Metric)
	r.mu.Unlock()
	if done != nil {
		avg := agent.Average(done)
		avg.TS = doneMinute * 60
		r.s.saveMinute(r.n.ID(), avg)
	}
	r.s.publish(r.n.ID(), sm)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (r *remote) run() {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("远程监控异常", "node", r.n.ID(), "panic", p)
		}
	}()
	for r.ctx.Err() == nil {
		if _, err := r.n.Client(r.ctx); err != nil {
			r.setErr(err)
			if !sleepCtx(r.ctx, 5*time.Second) {
				return
			}
			continue
		}
		err := r.once(r.ctx)
		if r.ctx.Err() != nil {
			return
		}
		r.setErr(err)
		if err != nil {
			slog.Debug("远程监控中断", "node", r.n.ID(), "err", err)
		}
		if !sleepCtx(r.ctx, 3*time.Second) {
			return
		}
	}
}

func (r *remote) once(ctx context.Context) error {
	cl := r.n.AgentClient()
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	info, err := cl.Info(pctx)
	cancel()
	if err == nil {
		src := "agent"
		if info.Panel {
			src = "panel"
		}
		r.setSource(src, info.AgentVersion, info.Info)
		r.setErr(nil)
		r.backfill(ctx, cl)
		sctx, stop := context.WithCancel(ctx)
		defer stop()
		go r.trafficLoop(sctx, cl)
		return cl.Stream(sctx, r.onSample)
	}
	return r.sshLoop(ctx)
}

// backfill 从 agent 取回面板离线期间的分钟数据（最多 7 天）。
func (r *remote) backfill(ctx context.Context, cl *agent.Client) {
	id := r.n.ID()
	now := time.Now().Unix()
	var last int64
	r.s.st.DB.Model(&store.Metric1m{}).Where("node_id = ?", id).Select("COALESCE(MAX(ts), 0)").Scan(&last)
	from := max(last+60, now-7*86400)
	if now-from < 180 {
		return
	}
	hctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	rows, err := cl.History(hctx, from, now-60)
	if err != nil || len(rows) == 0 {
		return
	}
	list := make([]store.Metric1m, 0, len(rows))
	for _, m := range rows {
		m.NodeID = id
		list = append(list, store.Metric1m{Metric: m})
	}
	if err := r.s.st.DB.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&list, 500).Error; err != nil {
		slog.Warn("补齐历史失败", "node", id, "err", err)
		return
	}
	r.s.rollupRange(id, rows[0].TS, now-60)
	slog.Info("已从 agent 补齐历史", "node", id, "points", len(rows))
}

// trafficLoop 每 5 分钟从 agent 同步一次按天流量。首次取 400 天，之后只取最近几天。
func (r *remote) trafficLoop(ctx context.Context, cl *agent.Client) {
	days := trafficKeepDays
	for {
		now := time.Now()
		tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		rows, err := cl.Traffic(tctx, agent.DayOf(now.AddDate(0, 0, -days)), agent.DayOf(now))
		cancel()
		if err == nil {
			r.s.saveTraffic(r.n.ID(), rows)
			days = 3
		}
		if !sleepCtx(ctx, 5*time.Minute) {
			return
		}
	}
}

// sshLoop 是无 agent 模式：读取主机信息后在 SSH 会话里运行采集循环。
func (r *remote) sshLoop(ctx context.Context) error {
	var buf strings.Builder
	ictx, cancel := context.WithTimeout(ctx, 20*time.Second)
	_, err := r.n.RunScript(ictx, "sh -c "+node.ShellQuote(agent.InfoScript), &limitWriter{b: &buf, max: 64 << 10})
	cancel()
	if err != nil {
		return err
	}
	r.setSource("ssh", "", agent.ParseInfo(buf.String()))
	r.setErr(nil)

	sctx, stop := context.WithCancel(ctx)
	defer stop()
	out, closeFn, err := r.n.Start(sctx, "sh -c "+node.ShellQuote(agent.SampleScript))
	if err != nil {
		return err
	}
	defer closeFn()
	switched := make(chan struct{})
	go func() {
		// 定期检查对方是否装上了 agent，装上后切换过去
		for sleepCtx(sctx, agentRecheck) {
			pctx, cancel := context.WithTimeout(sctx, 10*time.Second)
			_, err := r.n.AgentClient().Info(pctx)
			cancel()
			if err == nil {
				close(switched)
				stop()
				return
			}
		}
	}()
	var sampler agent.ProcSampler
	var block strings.Builder
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "@@END" {
			if sm := sampler.Parse(block.String()); sm != nil {
				r.onSample(*sm)
			}
			block.Reset()
			continue
		}
		if block.Len() < 4<<20 {
			block.WriteString(line)
			block.WriteByte('\n')
		}
	}
	select {
	case <-switched:
		return nil
	default:
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return errors.New("采集脚本已退出")
}

type limitWriter struct {
	b   *strings.Builder
	max int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if room := w.max - w.b.Len(); room > 0 {
		if len(p) > room {
			w.b.Write(p[:room])
		} else {
			w.b.Write(p)
		}
	}
	return len(p), nil
}
