package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	sshdesktop "github.com/apple2333usa/sshdesktop"
	"github.com/apple2333usa/sshdesktop/internal/auth"
	"github.com/apple2333usa/sshdesktop/internal/rbac"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func infoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "显示版本、端口、访问方式、数据目录",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			status := "未知"
			if out, err := exec.Command("systemctl", "is-active", "sshdesktop").Output(); err == nil || len(out) > 0 {
				status = strings.TrimSpace(string(out))
			}
			fmt.Printf("版本：      %s\n", sshdesktop.Version())
			fmt.Printf("服务状态：  %s\n", status)
			fmt.Printf("配置文件：  %s\n", cfg.Path())
			fmt.Printf("数据目录：  %s\n", cfg.DataDir)
			fmt.Printf("日志目录：  %s\n", cfg.LogDir)
			fmt.Printf("监听地址：  %s\n", cfg.ListenAddr())
			fmt.Println()
			if cfg.Public() {
				fmt.Printf("访问地址：  https://%s:%d\n", cfg.Domain, cfg.Port)
			} else {
				fmt.Println("访问方式：  面板只接受本机连接，请在你的电脑上建立 SSH 隧道：")
				fmt.Printf("            ssh -N -L %d:127.0.0.1:%d -p %s root@%s\n", cfg.Port, cfg.Port, sshPort(), publicIP())
				fmt.Printf("            然后在浏览器打开 http://127.0.0.1:%d\n", cfg.Port)
				fmt.Println("            配置域名和 HTTPS 后才会对公网开放。")
			}
			return nil
		},
	}
}

// sshPort 返回 sshd 实际监听的端口，取不到时按默认 22。
func sshPort() string {
	if out, err := exec.Command("sshd", "-T").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[0] == "port" {
				return f[1]
			}
		}
	}
	return "22"
}

// publicIP 返回本机第一个公网 IPv4；云服务器网卡上通常只有内网地址，此时给占位符。
func publicIP() string {
	cgnat := net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := n.IP.To4()
		if ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip) {
			return ip.String()
		}
	}
	return "<服务器IP>"
}

// readPassword 交互读取密码（不回显）；非终端时从标准输入读一行。
func readPassword(prompt string, confirm bool) (string, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if confirm {
		fmt.Fprint(os.Stderr, "再次输入：")
		b2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(b) != string(b2) {
			return "", errors.New("两次输入的密码不一致")
		}
	}
	return string(b), nil
}

func userCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "账号管理（创建、重置密码、列表）"}

	var password, roleName string
	var random bool
	add := &cobra.Command{
		Use:   "add <用户名>",
		Short: "创建账号（默认为管理员）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()
			if err := rbac.Seed(st.DB); err != nil {
				return err
			}
			var role store.Role
			if err := st.DB.Where("name = ?", roleName).First(&role).Error; err != nil {
				return fmt.Errorf("角色 %q 不存在", roleName)
			}
			pw, err := choosePassword(password, random)
			if err != nil {
				return err
			}
			if _, err := auth.CreateUser(st.DB, args[0], pw, role.ID); err != nil {
				return err
			}
			fmt.Printf("已创建账号 %s（角色：%s）\n", args[0], role.Name)
			if random {
				fmt.Printf("密码：%s\n", pw)
			}
			return nil
		},
	}
	add.Flags().StringVar(&password, "password", "", "密码（不填则交互输入）")
	add.Flags().BoolVar(&random, "random-password", false, "生成随机密码并打印")
	add.Flags().StringVar(&roleName, "role", rbac.AdminRoleName, "角色名")

	var resetPw string
	var resetRandom bool
	reset := &cobra.Command{
		Use:   "reset-password <用户名>",
		Short: "重置密码（忘记管理员密码时使用），并注销该账号所有会话",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()
			var u store.User
			if err := st.DB.Where("username = ?", args[0]).First(&u).Error; err != nil {
				return fmt.Errorf("账号 %q 不存在", args[0])
			}
			pw, err := choosePassword(resetPw, resetRandom)
			if err != nil {
				return err
			}
			if err := auth.New(st).SetPassword(u.ID, pw, 0); err != nil {
				return err
			}
			// 同时解除停用，方便管理员自救
			st.DB.Model(&u).Update("disabled", false)
			fmt.Printf("已重置 %s 的密码\n", u.Username)
			if resetRandom {
				fmt.Printf("新密码：%s\n", pw)
			}
			return nil
		},
	}
	reset.Flags().StringVar(&resetPw, "password", "", "新密码（不填则交互输入）")
	reset.Flags().BoolVar(&resetRandom, "random-password", false, "生成随机密码并打印")

	list := &cobra.Command{
		Use:   "list",
		Short: "列出账号",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()
			var users []store.User
			st.DB.Preload("Role").Order("id").Find(&users)
			fmt.Printf("%-4s %-20s %-10s %-6s %s\n", "ID", "用户名", "角色", "状态", "最后登录")
			for _, u := range users {
				role, state, last := "", "启用", "-"
				if u.Role != nil {
					role = u.Role.Name
				}
				if u.Disabled {
					state = "停用"
				}
				if u.LastLoginAt != nil {
					last = u.LastLoginAt.Local().Format("2006-01-02 15:04") + " " + u.LastLoginIP
				}
				fmt.Printf("%-4d %-20s %-10s %-6s %s\n", u.ID, u.Username, role, state, last)
			}
			return nil
		},
	}
	cmd.AddCommand(add, reset, list)
	return cmd
}

func choosePassword(given string, random bool) (string, error) {
	switch {
	case random:
		return auth.RandomPassword(16), nil
	case given != "":
		return given, auth.CheckPasswordStrength(given)
	}
	pw, err := readPassword("密码（至少 8 位）：", true)
	if err != nil {
		return "", err
	}
	return pw, auth.CheckPasswordStrength(pw)
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "查看或修改启动配置（修改后需重启服务生效）"}
	show := &cobra.Command{
		Use:   "show",
		Short: "显示全部配置",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			for _, k := range []string{"port", "domain", "https", "https.cert", "https.key", "log-level", "data-dir", "log-dir"} {
				v, _ := cfg.Get(k)
				fmt.Printf("%-11s %s\n", k, v)
			}
			return nil
		},
	}
	get := &cobra.Command{
		Use:   "get <配置项>",
		Short: "读取配置项",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			v, err := cfg.Get(args[0])
			if err != nil {
				return err
			}
			fmt.Println(v)
			return nil
		},
	}
	set := &cobra.Command{
		Use:   "set <配置项> <值>",
		Short: "修改配置项，例如 sshdesktop config set port 33333",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if err := cfg.Set(args[0], args[1]); err != nil {
				return err
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Println("已保存。执行 systemctl restart sshdesktop 后生效。")
			return nil
		},
	}
	unset := &cobra.Command{
		Use:   "unset <配置项>",
		Short: "恢复默认值；unset https 关闭 HTTPS，回到仅本机访问（证书出问题时自救）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// 配置校验失败（例如 HTTPS 配置不完整）时也要能自救，所以只在解析失败时退出
			cfg, err := loadConfig()
			if cfg == nil {
				return err
			}
			if err := cfg.Unset(args[0]); err != nil {
				return err
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Println("已保存。执行 systemctl restart sshdesktop 后生效。")
			return nil
		},
	}
	cmd.AddCommand(show, get, set, unset)
	return cmd
}
