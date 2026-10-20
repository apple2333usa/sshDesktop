// sshdesktop-agent 是只读的轻量监控程序：采集本机指标和流量，在 Unix socket 上提供给面板读取，不开任何网络端口。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/agent"
	"github.com/apple2333usa/sshdesktop/internal/metric"
)

// version 由构建时的 -ldflags "-X main.version=..." 注入。
var version = "dev"

type provider struct {
	col     *agent.Collector
	bc      *agent.Broadcaster
	store   *agent.MinuteStore
	traffic *agent.Traffic
}

func (p *provider) Info() agent.Info                         { return p.col.Info() }
func (p *provider) Subscribe() (<-chan agent.Sample, func()) { return p.bc.Subscribe() }
func (p *provider) History(from, to int64) []metric.Metric   { return p.store.Query(from, to) }
func (p *provider) Traffic(from, to int) []agent.TrafficRow  { return p.traffic.Rows(from, to) }

func main() {
	socket := flag.String("socket", agent.SocketPath, "Unix socket 路径")
	data := flag.String("data", "/var/lib/sshdesktop-agent", "数据目录（分钟级指标与流量统计）")
	showVersion := flag.Bool("version", false, "显示版本")
	flag.Parse()
	if *showVersion || flag.Arg(0) == "version" {
		fmt.Println(version)
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	trafficFile := filepath.Join(*data, "traffic.json")
	p := &provider{
		col: agent.NewCollector(), bc: agent.NewBroadcaster(),
		store: agent.NewMinuteStore(*data), traffic: agent.NewTraffic(),
	}
	if err := p.traffic.LoadFile(trafficFile); err != nil {
		slog.Warn("读取流量统计失败，从零开始", "err", err)
	}
	p.col.Traffic = p.traffic
	p.col.OnSample = p.bc.Publish
	p.col.OnMinute = func(m metric.Metric) {
		if err := p.store.Append(m); err != nil {
			slog.Warn("保存分钟指标失败", "err", err)
		}
		if err := p.traffic.SaveFile(trafficFile); err != nil {
			slog.Warn("保存流量统计失败", "err", err)
		}
		if m.TS%3600 == 0 {
			p.store.Cleanup(time.Now())
			p.traffic.Prune(time.Now(), 400)
		}
	}
	go p.col.Run(ctx)
	slog.Info("sshdesktop-agent 已启动", "version", version, "socket", *socket)
	if err := agent.Serve(ctx, *socket, p, version, false); err != nil {
		slog.Error("agent 接口启动失败", "err", err)
		os.Exit(1)
	}
	p.traffic.SaveFile(trafficFile)
}
