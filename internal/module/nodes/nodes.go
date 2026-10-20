// Package nodes 是节点管理模块：添加、修改、删除远程节点，确认主机指纹，一键安装 agent。
// 凭据用主密钥加密后入库；节点在面板启动时载入注册表并在后台保持连接。
package nodes

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/realtime"
	"github.com/apple2333usa/sshdesktop/internal/secret"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/apple2333usa/sshdesktop/internal/task"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

type Deps struct {
	Store   *store.Store
	Nodes   *node.Registry
	Hub     *realtime.Hub
	Tasks   *task.Manager
	Box     *secret.Box
	Confirm func(c *gin.Context) bool
	Audit   func(module, action string) gin.HandlerFunc
	Version string
	// CacheDir 缓存从 GitHub 下载的 agent（开发构建没有内置 agent 时）。
	CacheDir string
}

type Service struct {
	d Deps
	// OnAdded 在远程节点加入注册表（新建、修改、重新确认指纹）后调用；OnRemoved 在删除后调用。
	OnAdded   func(n *node.SSH)
	OnRemoved func(id string)
	// OnAgentChanged 在安装或卸载 agent 后调用，用于重新探测监控来源。
	OnAgentChanged func(id string)
}

func New(d Deps) *Service { return &Service{d: d} }

type Guards struct {
	Login, Manage gin.HandlerFunc
}

func (s *Service) Register(r *gin.RouterGroup, g Guards) {
	a := s.d.Audit
	r.GET("/nodes", g.Login, s.list)
	r.POST("/nodes/scan", g.Manage, s.scan)
	r.POST("/nodes/test", g.Manage, s.test)
	r.POST("/nodes", g.Manage, a("nodes", "create"), s.create)
	r.PUT("/nodes/order", g.Manage, s.order)
	r.PUT("/nodes/:node", g.Manage, a("nodes", "update"), s.update)
	r.DELETE("/nodes/:node", g.Manage, a("nodes", "delete"), s.remove)
	r.POST("/nodes/:node/reconnect", g.Manage, s.reconnect)
	r.POST("/nodes/:node/hostkey", g.Manage, a("nodes", "hostkey"), s.acceptHostKey)
	r.POST("/nodes/:node/agent/install", g.Manage, a("nodes", "agent-install"), s.agentInstall)
	r.POST("/nodes/:node/agent/uninstall", g.Manage, a("nodes", "agent-uninstall"), s.agentUninstall)
}

// Load 在面板启动时调用：保证本机节点存在，并载入全部远程节点。
func (s *Service) Load() error {
	db := s.d.Store.DB
	var local store.Node
	if err := db.First(&local, "id = ?", node.LocalID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		name, _ := os.Hostname()
		if name == "" {
			name = "本机"
		}
		local = store.Node{ID: node.LocalID, Kind: string(node.KindLocal), Name: name, TrafficResetDay: 1}
		if err := db.Create(&local).Error; err != nil {
			return err
		}
	}
	var rows []store.Node
	db.Where("kind = ?", string(node.KindSSH)).Find(&rows)
	for _, row := range rows {
		s.start(row)
	}
	return nil
}

// start 用数据库中的配置创建远程节点并放入注册表（替换旧连接）。
func (s *Service) start(row store.Node) *node.SSH {
	cfg, err := s.sshConfig(row)
	if err != nil {
		cfg.Err = err.Error()
	}
	n := node.NewSSH(cfg, s.onStatus)
	s.d.Nodes.Put(n)
	if s.OnAdded != nil {
		s.OnAdded(n)
	}
	return n
}

func (s *Service) onStatus(id string, st node.Status) {
	s.d.Hub.Publish("node-status", gin.H{"id": id, "status": st})
	if st.State != node.StateOnline && st.State != node.StateConnecting {
		slog.Info("节点状态变化", "node", id, "state", st.State, "error", st.Error)
	}
}

func (s *Service) sshConfig(row store.Node) (node.SSHConfig, error) {
	return SSHConfig(s.d.Store, s.d.Box, row)
}

// SSHConfig 解密凭据并组装连接配置（命令行执行定时备份时也用它连接 SFTP 目标）。
func SSHConfig(st *store.Store, box *secret.Box, row store.Node) (node.SSHConfig, error) {
	cfg := node.SSHConfig{ID: row.ID, Host: row.Host, Port: row.Port, User: row.Username}
	var cred store.NodeCredential
	if err := st.DB.First(&cred, "node_id = ?", row.ID).Error; err != nil {
		return cfg, errors.New("凭据不存在，请重新填写")
	}
	sec, err := box.Open(cred.Secret)
	if err != nil {
		return cfg, err
	}
	if row.AuthType == "key" {
		cfg.Key = sec
		if cfg.KeyPass, err = box.Open(cred.Passphrase); err != nil {
			return cfg, err
		}
	} else {
		cfg.Password = string(sec)
	}
	var hk store.NodeHostKey
	if err := st.DB.First(&hk, "node_id = ?", row.ID).Error; err != nil {
		return cfg, errors.New("主机密钥未确认")
	}
	if cfg.HostKey, err = parseKey(hk.Key); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func parseKey(b64 string) (ssh.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("主机密钥格式错误")
	}
	k, err := ssh.ParsePublicKey(raw)
	if err != nil {
		return nil, errors.New("主机密钥格式错误")
	}
	return k, nil
}

/* ---------- 列表 ---------- */

type HostKeyView struct {
	Type        string    `json:"type"`
	Fingerprint string    `json:"fingerprint"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

type View struct {
	store.Node
	Status  node.Status  `json:"status"`
	HostKey *HostKeyView `json:"hostKey,omitempty"`
}

func (s *Service) views() []View {
	var rows []store.Node
	s.d.Store.DB.Find(&rows)
	var keys []store.NodeHostKey
	s.d.Store.DB.Find(&keys)
	km := map[string]store.NodeHostKey{}
	for _, k := range keys {
		km[k.NodeID] = k
	}
	out := make([]View, 0, len(rows))
	for _, r := range rows {
		v := View{Node: r}
		if r.ID == node.LocalID {
			v.Status = node.Status{State: node.StateOnline}
		} else if n, ok := s.d.Nodes.SSH(r.ID); ok {
			v.Status = n.Status()
		} else {
			v.Status = node.Status{State: node.StateOffline}
		}
		if k, ok := km[r.ID]; ok {
			v.HostKey = &HostKeyView{Type: k.KeyType, Fingerprint: k.Fingerprint, ConfirmedAt: k.ConfirmedAt}
		}
		out = append(out, v)
	}
	// 本机在最前，其余按排序值和创建时间
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.ID == node.LocalID) != (b.ID == node.LocalID) {
			return a.ID == node.LocalID
		}
		if a.SortOrder != b.SortOrder {
			return a.SortOrder < b.SortOrder
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return out
}

func (s *Service) list(c *gin.Context) { api.OK(c, s.views()) }

/* ---------- 主机指纹 ---------- */

type scanResult struct {
	KeyType     string `json:"keyType"`
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

func scanKey(ctx context.Context, host string, port int) (*scanResult, error) {
	k, err := node.ScanHostKey(ctx, host, port)
	if err != nil {
		return nil, friendly(err)
	}
	return &scanResult{KeyType: k.Type(), Key: base64.StdEncoding.EncodeToString(k.Marshal()), Fingerprint: ssh.FingerprintSHA256(k)}, nil
}

func friendly(err error) error {
	var ne net.Error
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return api.BadRequest("连接被拒绝：请检查地址和 SSH 端口")
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return api.BadRequest("连接超时：请检查地址、端口和防火墙")
	case strings.Contains(msg, "no such host"):
		return api.BadRequest("无法解析主机名")
	}
	return api.BadRequest("连接失败：" + msg)
}

func (s *Service) scan(c *gin.Context) {
	var req struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if !api.Bind(c, &req) {
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	if req.Port == 0 {
		req.Port = 22
	}
	if err := validAddr(req.Host, req.Port); err != nil {
		api.Fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	r, err := scanKey(ctx, req.Host, req.Port)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, r)
}

/* ---------- 新建与修改 ---------- */

type nodeReq struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthType   string `json:"authType"` // key / password
	Password   string `json:"password"`
	PrivateKey string `json:"privateKey"`
	Passphrase string `json:"passphrase"`
	// HostKey 是用户确认过的主机密钥（scan 返回的 key）。
	HostKey string `json:"hostKey"`
	Color   string `json:"color"`
}

var (
	hostRe  = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]_]+$`)
	userRe  = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._\-@]*$`)
	colorRe = regexp.MustCompile(`^(#[0-9a-fA-F]{6})?$`)
)

func validAddr(host string, port int) error {
	if host == "" || len(host) > 255 || !hostRe.MatchString(host) {
		return api.BadRequest("地址无效")
	}
	if port < 1 || port > 65535 {
		return api.BadRequest("端口无效")
	}
	return nil
}

func (r *nodeReq) normalize() error {
	r.Name, r.Host, r.Username = strings.TrimSpace(r.Name), strings.TrimSpace(r.Host), strings.TrimSpace(r.Username)
	if r.Port == 0 {
		r.Port = 22
	}
	if r.Name == "" {
		r.Name = r.Host
	}
	if len([]rune(r.Name)) > 64 {
		return api.BadRequest("名称不能超过 64 个字符")
	}
	if err := validAddr(r.Host, r.Port); err != nil {
		return err
	}
	if r.Username == "" || len(r.Username) > 64 || !userRe.MatchString(r.Username) {
		return api.BadRequest("用户名无效")
	}
	if r.AuthType != "key" && r.AuthType != "password" {
		return api.BadRequest("认证方式只能是私钥或密码")
	}
	if !colorRe.MatchString(r.Color) {
		return api.BadRequest("颜色格式错误")
	}
	return nil
}

func (r *nodeReq) config(id string, key ssh.PublicKey) node.SSHConfig {
	cfg := node.SSHConfig{ID: id, Host: r.Host, Port: r.Port, User: r.Username, HostKey: key}
	if r.AuthType == "key" {
		cfg.Key, cfg.KeyPass = []byte(r.PrivateKey), []byte(r.Passphrase)
	} else {
		cfg.Password = r.Password
	}
	return cfg
}

func newID() string {
	b := make([]byte, 5)
	rand.Read(b)
	return "n" + hex.EncodeToString(b)
}

// verify 用确认过的主机密钥连接一次，验证凭据。
func verify(c *gin.Context, cfg node.SSHConfig) error {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if err := node.TestConnection(ctx, cfg); err != nil {
		var hk *node.HostKeyMismatchError
		if errors.As(err, &hk) {
			return api.Conflict(api.CodeHostKeyChanged, err.Error())
		}
		return api.BadRequest(err.Error())
	}
	return nil
}

// test 只验证连接，不保存（添加向导的“测试连接”）。
func (s *Service) test(c *gin.Context) {
	var req nodeReq
	if !api.Bind(c, &req) {
		return
	}
	if err := req.normalize(); err != nil {
		api.Fail(c, err)
		return
	}
	key, err := parseKey(req.HostKey)
	if err != nil {
		api.Fail(c, api.BadRequest("请先确认主机指纹"))
		return
	}
	if err := verify(c, req.config("test", key)); err != nil {
		api.Fail(c, err)
		return
	}
	api.OK(c, nil)
}

func (s *Service) saveCredential(tx *gorm.DB, id string, req *nodeReq) error {
	cred := store.NodeCredential{NodeID: id}
	if req.AuthType == "key" {
		cred.Secret, cred.Passphrase = s.d.Box.Seal([]byte(req.PrivateKey)), s.d.Box.Seal([]byte(req.Passphrase))
	} else {
		cred.Secret = s.d.Box.Seal([]byte(req.Password))
	}
	return tx.Save(&cred).Error
}

func hostKeyRow(id string, key ssh.PublicKey) store.NodeHostKey {
	return store.NodeHostKey{
		NodeID: id, KeyType: key.Type(), Key: base64.StdEncoding.EncodeToString(key.Marshal()),
		Fingerprint: ssh.FingerprintSHA256(key), ConfirmedAt: time.Now(),
	}
}

func (s *Service) create(c *gin.Context) {
	var req nodeReq
	if !api.Bind(c, &req) {
		return
	}
	if err := req.normalize(); err != nil {
		api.Fail(c, err)
		return
	}
	if req.AuthType == "key" && strings.TrimSpace(req.PrivateKey) == "" || req.AuthType == "password" && req.Password == "" {
		api.Fail(c, api.BadRequest("请填写私钥或密码"))
		return
	}
	api.Target(c, fmt.Sprintf("%s（%s@%s:%d）", req.Name, req.Username, req.Host, req.Port))
	key, err := parseKey(req.HostKey)
	if err != nil {
		api.Fail(c, api.BadRequest("请先确认主机指纹"))
		return
	}
	id := newID()
	if err := verify(c, req.config(id, key)); err != nil {
		api.Fail(c, err)
		return
	}
	var maxOrder int
	s.d.Store.DB.Model(&store.Node{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)
	row := store.Node{
		ID: id, Kind: string(node.KindSSH), Name: req.Name, Host: req.Host, Port: req.Port,
		Username: req.Username, AuthType: req.AuthType, Color: req.Color, SortOrder: maxOrder + 1, TrafficResetDay: 1,
	}
	err = s.d.Store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if err := s.saveCredential(tx, id, &req); err != nil {
			return err
		}
		hk := hostKeyRow(id, key)
		return tx.Create(&hk).Error
	})
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.SetNode(c, id)
	s.start(row)
	api.OK(c, s.view(id))
}

func (s *Service) view(id string) *View {
	for _, v := range s.views() {
		if v.ID == id {
			return &v
		}
	}
	return nil
}

func (s *Service) row(c *gin.Context) (*store.Node, bool) {
	var row store.Node
	if err := s.d.Store.DB.First(&row, "id = ?", c.Param("node")).Error; err != nil {
		api.Fail(c, api.NotFound("节点不存在"))
		return nil, false
	}
	return &row, true
}

// update 修改节点。本机只能改名称和颜色；远程节点的地址、端口或用户名变化时需要重新确认指纹并验证连接，
// 凭据留空表示不修改。
func (s *Service) update(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	var req nodeReq
	if !api.Bind(c, &req) {
		return
	}
	api.Target(c, row.Name)
	if !colorRe.MatchString(req.Color) {
		api.Fail(c, api.BadRequest("颜色格式错误"))
		return
	}
	if row.Kind == string(node.KindLocal) {
		name := strings.TrimSpace(req.Name)
		if name == "" || len([]rune(name)) > 64 {
			api.Fail(c, api.BadRequest("名称无效"))
			return
		}
		s.d.Store.DB.Model(row).Updates(map[string]any{"name": name, "color": req.Color})
		api.OK(c, s.view(row.ID))
		return
	}
	if err := req.normalize(); err != nil {
		api.Fail(c, err)
		return
	}
	addrChanged := req.Host != row.Host || req.Port != row.Port || req.Username != row.Username
	credChanged := req.AuthType != row.AuthType || req.Password != "" || strings.TrimSpace(req.PrivateKey) != ""
	if req.AuthType != row.AuthType && (req.AuthType == "key" && strings.TrimSpace(req.PrivateKey) == "" || req.AuthType == "password" && req.Password == "") {
		api.Fail(c, api.BadRequest("更换认证方式时请填写新的私钥或密码"))
		return
	}
	old, err := s.sshConfig(*row)
	var key ssh.PublicKey
	switch {
	case req.HostKey != "":
		if key, err = parseKey(req.HostKey); err != nil {
			api.Fail(c, err)
			return
		}
	case req.Host != row.Host || req.Port != row.Port:
		api.Fail(c, api.BadRequest("地址或端口已修改，请重新确认主机指纹"))
		return
	default:
		key = old.HostKey
	}
	if addrChanged || credChanged || req.HostKey != "" {
		cfg := req.config(row.ID, key)
		if !credChanged {
			// 沿用已保存的凭据
			cfg.Key, cfg.KeyPass, cfg.Password = old.Key, old.KeyPass, old.Password
			if err != nil {
				api.Fail(c, api.BadRequest("已保存的凭据无法使用（"+err.Error()+"），请重新填写"))
				return
			}
		}
		if err := verify(c, cfg); err != nil {
			api.Fail(c, err)
			return
		}
	}
	err = s.d.Store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(row).Updates(map[string]any{
			"name": req.Name, "host": req.Host, "port": req.Port, "username": req.Username,
			"auth_type": req.AuthType, "color": req.Color,
		}).Error; err != nil {
			return err
		}
		if credChanged {
			if err := s.saveCredential(tx, row.ID, &req); err != nil {
				return err
			}
		}
		if req.HostKey != "" {
			hk := hostKeyRow(row.ID, key)
			return tx.Save(&hk).Error
		}
		return nil
	})
	if err != nil {
		api.Fail(c, err)
		return
	}
	if addrChanged || credChanged || req.HostKey != "" {
		s.d.Store.DB.First(row, "id = ?", row.ID)
		s.start(*row)
	}
	api.OK(c, s.view(row.ID))
}

func (s *Service) order(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if !api.Bind(c, &req) {
		return
	}
	for i, id := range req.IDs {
		s.d.Store.DB.Model(&store.Node{}).Where("id = ? AND kind = ?", id, string(node.KindSSH)).Update("sort_order", i+1)
	}
	api.OK(c, nil)
}

// remove 删除节点（二级危险操作）：断开连接，删除凭据、指纹、监控历史和流量统计。
func (s *Service) remove(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	if row.ID == node.LocalID {
		api.Fail(c, api.BadRequest("本机节点不能删除"))
		return
	}
	api.Target(c, fmt.Sprintf("%s（%s@%s:%d）", row.Name, row.Username, row.Host, row.Port))
	if !s.d.Confirm(c) {
		return
	}
	id := row.ID
	err := s.d.Store.DB.Transaction(func(tx *gorm.DB) error {
		for _, m := range []any{&store.NodeCredential{}, &store.NodeHostKey{}} {
			if err := tx.Where("node_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		for _, m := range []any{&store.Metric1m{}, &store.Metric15m{}, &store.Metric1h{}, &store.TrafficDaily{}} {
			if err := tx.Where("node_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&store.Node{}, "id = ?", id).Error
	})
	if err != nil {
		api.Fail(c, err)
		return
	}
	s.d.Nodes.Remove(id)
	if s.OnRemoved != nil {
		s.OnRemoved(id)
	}
	s.d.Hub.Publish("node-status", gin.H{"id": id, "removed": true})
	api.OK(c, nil)
}

func (s *Service) reconnect(c *gin.Context) {
	n, ok := s.d.Nodes.SSH(c.Param("node"))
	if !ok {
		api.Fail(c, api.BadRequest("只有远程节点可以重新连接"))
		return
	}
	n.Reconnect()
	api.OK(c, nil)
}

// acceptHostKey 在主机密钥变化后重新确认（二级危险操作）。客户端先调用 scan 取得新密钥并展示给用户。
func (s *Service) acceptHostKey(c *gin.Context) {
	row, ok := s.row(c)
	if !ok {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if !api.Bind(c, &req) {
		return
	}
	key, err := parseKey(req.Key)
	if err != nil {
		api.Fail(c, err)
		return
	}
	api.Target(c, fmt.Sprintf("%s 新指纹 %s", row.Name, ssh.FingerprintSHA256(key)))
	if !s.d.Confirm(c) {
		return
	}
	cfg, err := s.sshConfig(*row)
	if err != nil && cfg.HostKey == nil && !strings.Contains(err.Error(), "主机密钥") {
		api.Fail(c, api.BadRequest(err.Error()))
		return
	}
	cfg.HostKey = key
	if err := verify(c, cfg); err != nil {
		api.Fail(c, err)
		return
	}
	hk := hostKeyRow(row.ID, key)
	if err := s.d.Store.DB.Save(&hk).Error; err != nil {
		api.Fail(c, err)
		return
	}
	s.start(*row)
	api.OK(c, s.view(row.ID))
}
