package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	sshdesktop "github.com/apple2333usa/sshdesktop"
	"github.com/apple2333usa/sshdesktop/internal/config"
	"github.com/spf13/cobra"
)

const (
	repo       = "apple2333usa/sshDesktop"
	binaryPath = "/usr/local/bin/sshdesktop"
	unitPath   = "/etc/systemd/system/sshdesktop.service"
)

func assetName() string { return "sshdesktop-linux-" + runtime.GOARCH }

func httpGet(url string) (*http.Response, error) {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "sshdesktop/"+sshdesktop.Version())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("下载 %s 失败：HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

func latestTag() (string, error) {
	resp, err := httpGet("https://api.github.com/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", err
	}
	return r.TagName, nil
}

// download 下载新版本二进制和 SHA256SUMS 并校验，返回临时文件路径。
func download(tag, mirror string) (string, error) {
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/", repo, tag)
	if mirror != "" {
		base = strings.TrimSuffix(mirror, "/") + "/" + base
	}
	resp, err := httpGet(base + "SHA256SUMS")
	if err != nil {
		return "", err
	}
	want := ""
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == assetName() {
			want = f[0]
		}
	}
	resp.Body.Close()
	if want == "" {
		return "", fmt.Errorf("SHA256SUMS 中没有 %s", assetName())
	}
	resp, err = httpGet(base + assetName())
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(binaryPath), ".sshdesktop-upgrade-*")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("校验失败：期望 %s，实际 %s", want, got)
	}
	return tmp.Name(), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// healthCheck 等待新版本启动并通过 /api/v1/health 返回期望版本。
func healthCheck(cfg *config.Config, wantVersion string) error {
	scheme, host := "http", fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	client := &http.Client{Timeout: 3 * time.Second}
	if cfg.Public() {
		scheme = "https"
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: cfg.Domain}}
	}
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		req, _ := http.NewRequest(http.MethodGet, scheme+"://"+host+"/api/v1/health", nil)
		if cfg.Public() {
			req.Host = cfg.Domain
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		var r struct {
			Data struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		json.NewDecoder(resp.Body).Decode(&r)
		resp.Body.Close()
		if wantVersion == "" || r.Data.Version == wantVersion {
			return nil
		}
		lastErr = fmt.Errorf("运行中的版本是 %s，期望 %s", r.Data.Version, wantVersion)
	}
	return fmt.Errorf("健康检查失败：%v", lastErr)
}

func upgradeCmd() *cobra.Command {
	var tag, file, mirror string
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "升级到最新版本（下载 → 校验 → 备份 → 替换 → 重启 → 健康检查，失败自动回滚）",
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 {
				return errors.New("请以 root 运行")
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			newBin := file
			if newBin == "" {
				if tag == "" {
					if tag, err = latestTag(); err != nil {
						return fmt.Errorf("获取最新版本失败：%w", err)
					}
				}
				if strings.TrimPrefix(tag, "v") == sshdesktop.Version() {
					fmt.Println("已经是最新版本", sshdesktop.Version())
					return nil
				}
				fmt.Printf("下载 %s（%s）…\n", tag, assetName())
				if newBin, err = download(tag, mirror); err != nil {
					return err
				}
				defer os.Remove(newBin)
			}
			out, err := exec.Command(newBin, "version").Output()
			if err != nil {
				return fmt.Errorf("新版本无法运行：%w", err)
			}
			newVersion := strings.TrimSpace(string(out))

			backupDir := filepath.Join(cfg.DataDir, "upgrade-backup")
			os.MkdirAll(backupDir, 0o700)
			stamp := time.Now().Format("20060102-150405")
			oldBin := filepath.Join(backupDir, "sshdesktop-"+sshdesktop.Version()+"-"+stamp)
			if err := copyFile(binaryPath, oldBin, 0o755); err != nil {
				return fmt.Errorf("备份旧版本失败：%w", err)
			}
			if st, err := openStore(cfg); err == nil {
				dbBackup := filepath.Join(backupDir, "sshdesktop-"+stamp+".db")
				if err := st.DB.Exec("VACUUM INTO ?", dbBackup).Error; err != nil {
					fmt.Println("警告：数据库备份失败：", err)
				}
				st.Close()
			}
			fmt.Printf("已备份旧版本到 %s\n", backupDir)

			install := func(src string) error {
				tmp := binaryPath + ".new"
				if err := copyFile(src, tmp, 0o755); err != nil {
					return err
				}
				return os.Rename(tmp, binaryPath)
			}
			if err := install(newBin); err != nil {
				return fmt.Errorf("替换二进制失败：%w", err)
			}
			exec.Command("systemctl", "restart", "sshdesktop").Run()
			fmt.Println("已重启，健康检查中…")
			if err := healthCheck(cfg, newVersion); err != nil {
				fmt.Println(err, "，正在回滚…")
				if rerr := install(oldBin); rerr != nil {
					return fmt.Errorf("回滚失败：%v（旧版本在 %s）", rerr, oldBin)
				}
				exec.Command("systemctl", "restart", "sshdesktop").Run()
				return errors.New("升级失败，已回滚到 " + sshdesktop.Version())
			}
			fmt.Printf("升级完成：%s → %s\n", sshdesktop.Version(), newVersion)
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "version", "", "目标版本标签，例如 v0.2.0（默认最新）")
	cmd.Flags().StringVar(&file, "file", "", "使用本地二进制文件升级（离线升级）")
	cmd.Flags().StringVar(&mirror, "mirror", "", "GitHub 下载加速前缀，例如 https://ghproxy.example.com")
	return cmd
}

func uninstallCmd() *cobra.Command {
	var purge, yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "卸载面板（默认保留数据；--purge 同时删除配置、数据和日志）",
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 {
				return errors.New("请以 root 运行")
			}
			cfg, _ := loadConfig()
			if cfg == nil {
				cfg = config.Default()
			}
			what := "面板程序和系统服务（保留配置与数据）"
			if purge {
				what = fmt.Sprintf("面板程序、系统服务，以及 %s、%s、%s", filepath.Dir(configPath), cfg.DataDir, cfg.LogDir)
			}
			if !yes {
				fmt.Printf("将删除%s。确认请输入 yes：", what)
				var answer string
				fmt.Scanln(&answer)
				if answer != "yes" {
					return errors.New("已取消")
				}
			}
			exec.Command("systemctl", "disable", "--now", "sshdesktop").Run()
			os.Remove(unitPath)
			exec.Command("systemctl", "daemon-reload").Run()
			if purge {
				os.RemoveAll(filepath.Dir(configPath))
				os.RemoveAll(cfg.DataDir)
				os.RemoveAll(cfg.LogDir)
			}
			os.Remove(binaryPath)
			fmt.Println("已卸载。")
			return nil
		},
	}
	cmd.Flags().BoolVar(&purge, "purge", false, "同时删除配置、数据和日志")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "不再确认")
	return cmd
}

