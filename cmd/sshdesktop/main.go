// sshdesktop 面板主程序。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	sshdesktop "github.com/apple2333usa/sshdesktop"
	"github.com/apple2333usa/sshdesktop/internal/config"
	"github.com/apple2333usa/sshdesktop/internal/logx"
	"github.com/apple2333usa/sshdesktop/internal/server"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/spf13/cobra"
)

var configPath string

func main() {
	root := &cobra.Command{
		Use:           "sshdesktop",
		Short:         "sshDesktop：带 Windows 11 风格网页桌面的 Linux 服务器管理面板",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", config.DefaultPath, "配置文件路径")
	root.AddCommand(serverCmd(), infoCmd(), versionCmd(), userCmd(), configCmd(), upgradeCmd(), uninstallCmd())
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func loadConfig() (*config.Config, error) { return config.Load(configPath) }

func openStore(cfg *config.Config) (*store.Store, error) {
	return store.Open(filepath.Join(cfg.DataDir, "sshdesktop.db"))
}

func serverCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "运行面板服务（通常由 systemd 启动）",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			closer, err := logx.Setup(cfg.LogDir, cfg.LogLevel)
			if err != nil {
				return err
			}
			defer closer.Close()
			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()
			srv, err := server.New(cfg, st, sshdesktop.Version(), sshdesktop.WebFS())
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "显示版本号",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(sshdesktop.Version())
		},
	}
}
