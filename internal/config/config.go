// Package config 读写启动配置文件 /etc/sshdesktop/config.yaml。
// 运行期可调的选项（会话超时、封禁策略等）存放在数据库 settings 表，不在这里。
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultPath = "/etc/sshdesktop/config.yaml"

type HTTPS struct {
	Enabled bool   `yaml:"enabled"`
	Cert    string `yaml:"cert,omitempty"`
	Key     string `yaml:"key,omitempty"`
}

type Config struct {
	Port     int    `yaml:"port"`
	Domain   string `yaml:"domain,omitempty"`
	HTTPS    HTTPS  `yaml:"https"`
	DataDir  string `yaml:"dataDir"`
	LogDir   string `yaml:"logDir"`
	LogLevel string `yaml:"logLevel"`
	// Listen 仅用于开发调试：覆盖自动选择的监听地址。生产环境留空。
	Listen string `yaml:"listen,omitempty"`
	// AgentSocket 是面板自带 agent 接口的 Unix socket，留空为 /run/sshdesktop/agent.sock，"-" 表示不提供。
	AgentSocket string `yaml:"agentSocket,omitempty"`

	path string `yaml:"-"`
}

func Default() *Config {
	return &Config{
		Port:     33333,
		DataDir:  "/var/lib/sshdesktop",
		LogDir:   "/var/log/sshdesktop",
		LogLevel: "info",
	}
}

// Load 读取配置；文件不存在时返回默认值（并记住路径，便于之后保存）。
func Load(path string) (*Config, error) {
	c := Default()
	c.path = path
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	return c, c.Validate()
}

func (c *Config) Path() string { return c.path }

func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("端口 %d 无效", c.Port)
	}
	if c.HTTPS.Enabled {
		if c.Domain == "" {
			return errors.New("启用 HTTPS 时必须配置域名")
		}
		if c.HTTPS.Cert == "" || c.HTTPS.Key == "" {
			return errors.New("启用 HTTPS 时必须配置证书和私钥路径")
		}
	}
	if c.DataDir == "" {
		return errors.New("dataDir 不能为空")
	}
	return nil
}

// Public 表示面板是否对公网开放（配置了域名和 HTTPS）。
func (c *Config) Public() bool { return c.HTTPS.Enabled && c.Domain != "" }

// ListenAddr 按访问控制规则返回监听地址：未配置 HTTPS 时只监听本机。
func (c *Config) ListenAddr() string {
	if c.Listen != "" {
		return c.Listen
	}
	if c.Public() {
		return fmt.Sprintf("0.0.0.0:%d", c.Port)
	}
	return fmt.Sprintf("127.0.0.1:%d", c.Port)
}

func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("配置文件路径未知")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Keys 是 `sshdesktop config get/set` 支持的键。
var Keys = []string{"port", "domain", "https", "https.cert", "https.key", "log-level", "data-dir", "log-dir", "agent-socket"}

func (c *Config) Get(key string) (string, error) {
	switch key {
	case "port":
		return strconv.Itoa(c.Port), nil
	case "domain":
		return c.Domain, nil
	case "https":
		return strconv.FormatBool(c.HTTPS.Enabled), nil
	case "https.cert":
		return c.HTTPS.Cert, nil
	case "https.key":
		return c.HTTPS.Key, nil
	case "log-level":
		return c.LogLevel, nil
	case "data-dir":
		return c.DataDir, nil
	case "log-dir":
		return c.LogDir, nil
	case "agent-socket":
		return c.AgentSocket, nil
	}
	return "", fmt.Errorf("未知配置项 %q，可用：%s", key, strings.Join(Keys, ", "))
}

func (c *Config) Set(key, value string) error {
	switch key {
	case "port":
		p, err := strconv.Atoi(value)
		if err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("端口 %q 无效", value)
		}
		c.Port = p
	case "domain":
		c.Domain = value
	case "https":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("https 只能是 true 或 false")
		}
		c.HTTPS.Enabled = b
	case "https.cert":
		c.HTTPS.Cert = value
	case "https.key":
		c.HTTPS.Key = value
	case "log-level":
		switch value {
		case "debug", "info", "warn", "error":
			c.LogLevel = value
		default:
			return fmt.Errorf("日志级别只能是 debug、info、warn、error")
		}
	case "data-dir":
		c.DataDir = value
	case "log-dir":
		c.LogDir = value
	case "agent-socket":
		c.AgentSocket = value
	default:
		return fmt.Errorf("未知配置项 %q，可用：%s", key, strings.Join(Keys, ", "))
	}
	return nil
}

// Unset 恢复某项为默认值；`unset https` 关闭 HTTPS，回到仅本机访问。
func (c *Config) Unset(key string) error {
	d := Default()
	switch key {
	case "https":
		c.HTTPS = HTTPS{}
	case "domain":
		c.Domain = ""
	case "port":
		c.Port = d.Port
	case "log-level":
		c.LogLevel = d.LogLevel
	case "agent-socket":
		c.AgentSocket = ""
	default:
		return fmt.Errorf("配置项 %q 不支持 unset", key)
	}
	return nil
}
