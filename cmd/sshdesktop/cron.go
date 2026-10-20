package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	sshdesktop "github.com/apple2333usa/sshdesktop"
	"github.com/apple2333usa/sshdesktop/internal/backup"
	"github.com/apple2333usa/sshdesktop/internal/docker"
	"github.com/apple2333usa/sshdesktop/internal/module/cron"
	"github.com/apple2333usa/sshdesktop/internal/module/nodes"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/secret"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/vfs"
	"github.com/spf13/cobra"
)

// cronCmd 供系统 cron 调用：/etc/cron.d/sshdesktop 中的每一行都是 sshdesktop cron run <任务ID>。
func cronCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cron", Short: "计划任务（由系统 cron 调用）"}
	cmd.AddCommand(&cobra.Command{
		Use:   "run <任务ID>",
		Short: "执行一次面板中的计划任务",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			id, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return errors.New("任务 ID 必须是数字")
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()
			var job store.CronJob
			if err := st.DB.First(&job, id).Error; err != nil {
				return fmt.Errorf("任务 %d 不存在", id)
			}
			if !job.Enabled {
				return fmt.Errorf("任务 %d 已停用", id)
			}
			box, err := secret.Load(filepath.Join(cfg.DataDir, "secret.key"))
			if err != nil {
				return err
			}
			eng := &backup.Engine{
				Store: st, Box: box, DataDir: cfg.DataDir, ConfigPath: cfg.Path(), Version: sshdesktop.Version(),
				Docker: docker.New(""), Remote: dialFS(st, box),
			}
			r := &cron.Runner{Store: st, DataDir: cfg.DataDir, Backup: eng.RunPlan}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			run := r.Run(ctx, job, "cron", nil)
			if run.Status == "failed" {
				return fmt.Errorf("任务执行失败（退出码 %d）", run.ExitCode)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "sync",
		Short: "按面板中的任务重写 " + cron.CronFile,
		RunE: func(c *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()
			bin, _ := os.Executable()
			return cron.New(cron.Deps{Store: st, Bin: bin, Config: cfg.Path()}).Sync()
		},
	})
	return cmd
}

// dialFS 为 SFTP 备份目标单独建立一次 SSH 连接（命令行进程中没有面板的连接池）。
func dialFS(st *store.Store, box *secret.Box) func(context.Context, string) (vfs.FS, func(), error) {
	return func(ctx context.Context, id string) (vfs.FS, func(), error) {
		var row store.Node
		if err := st.DB.First(&row, "id = ?", id).Error; err != nil {
			return nil, nil, errors.New("SFTP 备份目标所在的服务器不存在")
		}
		cfg, err := nodes.SSHConfig(st, box, row)
		if err != nil {
			return nil, nil, err
		}
		n := node.NewSSH(cfg, nil)
		if _, err := n.Client(ctx); err != nil {
			n.Close()
			return nil, nil, err
		}
		return n.FS(), n.Close, nil
	}
}
