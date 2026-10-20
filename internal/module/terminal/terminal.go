// Package terminal 管理终端会话。会话独立于 WebSocket 存在：浏览器断开后保留一段时间，
// 重新连接时回放最近 1 MB 输出。
package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/apple2333usa/sshdesktop/internal/audit"
	"github.com/apple2333usa/sshdesktop/internal/node"
	"github.com/apple2333usa/sshdesktop/internal/store"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

const replayLimit = 1 << 20

var containerID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type Session struct {
	ID        string    `json:"id"`
	Node      string    `json:"node"`
	UserID    uint      `json:"-"`
	Username  string    `json:"username"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	Attached  int       `json:"attached"`
	Exited    bool      `json:"exited"`
	ExitCode  int       `json:"exitCode"`

	pty      node.PTY
	ip       string
	mu       sync.Mutex
	buf      []byte
	subs     map[chan []byte]struct{}
	detached time.Time
	done     chan struct{}
}

type Service struct {
	nodes *node.Registry
	st    *store.Store
	audit *audit.Recorder

	mu       sync.Mutex
	sessions map[string]*Session

	// AllowContainer 判断当前账号能否进入容器终端（需要 docker:manage），失败时已输出响应。
	AllowContainer func(c *gin.Context) bool
}

func New(nodes *node.Registry, st *store.Store, rec *audit.Recorder) *Service {
	return &Service{nodes: nodes, st: st, audit: rec, sessions: map[string]*Session{}}
}

func (s *Service) Register(r *gin.RouterGroup, use gin.HandlerFunc) {
	r.GET("/terminals", use, s.list)
	r.POST("/terminals", use, s.create)
	r.DELETE("/terminals/:sid", use, s.close)
	r.GET("/terminals/:sid/ws", use, s.attach)
}

func (s *Service) get(c *gin.Context) *Session {
	s.mu.Lock()
	sess := s.sessions[c.Param("sid")]
	s.mu.Unlock()
	// 只能接入自己的会话
	if sess == nil || sess.UserID != api.User(c).UserID || sess.Node != c.Param("node") {
		api.Fail(c, api.Err(404, api.CodeSessionGone, "终端会话不存在或已结束"))
		return nil
	}
	return sess
}

func (s *Service) list(c *gin.Context) {
	uid := api.User(c).UserID
	out := []*Session{}
	s.mu.Lock()
	for _, sess := range s.sessions {
		if sess.UserID == uid && sess.Node == c.Param("node") {
			out = append(out, sess)
		}
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	api.OK(c, out)
}

func (s *Service) create(c *gin.Context) {
	var req struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
		Dir  string `json:"dir"`
		// Container 不为空时进入该容器（docker exec），仅本机
		Container string `json:"container"`
	}
	if !api.Bind(c, &req) {
		return
	}
	n, err := s.nodes.Get(c.Param("node"))
	if err != nil {
		api.Fail(c, api.NotFound("节点不存在"))
		return
	}
	opt := node.ShellOptions{Size: node.Size{Cols: req.Cols, Rows: req.Rows}, Dir: req.Dir}
	title := "终端"
	if req.Container != "" {
		if n.ID() != node.LocalID || !containerID.MatchString(req.Container) {
			api.Fail(c, api.BadRequest("只能进入本机的容器"))
			return
		}
		if s.AllowContainer == nil || !s.AllowContainer(c) {
			return
		}
		// 优先 bash，没有时用 sh
		opt.Command = []string{"docker", "exec", "-it", "-e", "TERM=xterm-256color", req.Container,
			"sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"}
		title = "容器 " + req.Container
	}
	p, err := n.Shell(context.Background(), opt)
	if err != nil {
		api.Fail(c, err)
		return
	}
	b := make([]byte, 8)
	rand.Read(b)
	u := api.User(c)
	sess := &Session{
		ID: hex.EncodeToString(b), Node: n.ID(), UserID: u.UserID, Username: u.Username,
		Title: title, CreatedAt: time.Now(), pty: p, ip: api.IP(c),
		subs: map[chan []byte]struct{}{}, detached: time.Now(), done: make(chan struct{}),
	}
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
	e := audit.FromContext(c, "terminal", "open")
	e.Target, e.Success = "会话 "+sess.ID, true
	if req.Container != "" {
		e.Target += "（容器 " + req.Container + "）"
	}
	s.audit.Write(e)
	go s.pump(sess)
	api.OK(c, sess)
}

// pump 持续读取终端输出，写入回放缓冲并分发给已连接的客户端。
func (s *Service) pump(sess *Session) {
	buf := make([]byte, 32<<10)
	for {
		n, err := sess.pty.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			sess.mu.Lock()
			sess.buf = append(sess.buf, chunk...)
			if len(sess.buf) > replayLimit {
				sess.buf = append(sess.buf[:0:0], sess.buf[len(sess.buf)-replayLimit:]...)
			}
			for ch := range sess.subs {
				select {
				case ch <- chunk:
				default: // 客户端太慢时丢弃，刷新后可从回放缓冲恢复
				}
			}
			sess.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	code, _ := sess.pty.Wait()
	sess.mu.Lock()
	sess.Exited, sess.ExitCode = true, code
	sess.mu.Unlock()
	close(sess.done)
	s.remove(sess, "exit")
}

func (s *Service) remove(sess *Session, reason string) {
	s.mu.Lock()
	_, ok := s.sessions[sess.ID]
	delete(s.sessions, sess.ID)
	s.mu.Unlock()
	if !ok {
		return
	}
	sess.pty.Close()
	s.audit.Write(store.AuditLog{
		At: time.Now(), UserID: sess.UserID, Username: sess.Username, IP: sess.ip, Node: sess.Node,
		Module: "terminal", Action: "close", Success: true,
		Target: fmt.Sprintf("会话 %s（%s，时长 %s）", sess.ID, reason, time.Since(sess.CreatedAt).Round(time.Second)),
	})
}

func (s *Service) close(c *gin.Context) {
	sess := s.get(c)
	if sess == nil {
		return
	}
	s.remove(sess, "closed")
	api.OK(c, nil)
}

// Reap 结束断开时间超过保留期的会话，由后台定时调用。
func (s *Service) Reap() {
	keep := time.Duration(s.st.Runtime().TerminalKeepMinutes) * time.Minute
	var stale []*Session
	s.mu.Lock()
	for _, sess := range s.sessions {
		sess.mu.Lock()
		if sess.Attached == 0 && time.Since(sess.detached) > keep {
			stale = append(stale, sess)
		}
		sess.mu.Unlock()
	}
	s.mu.Unlock()
	for _, sess := range stale {
		s.remove(sess, "timeout")
	}
}

// CloseUser 结束某账号的全部终端（账号被停用或删除时）。
func (s *Service) CloseUser(userID uint) {
	var list []*Session
	s.mu.Lock()
	for _, sess := range s.sessions {
		if sess.UserID == userID {
			list = append(list, sess)
		}
	}
	s.mu.Unlock()
	for _, sess := range list {
		s.remove(sess, "revoked")
	}
}

type control struct {
	Type  string `json:"type"`
	Cols  uint16 `json:"cols"`
	Rows  uint16 `json:"rows"`
	Title string `json:"title"`
}

// attach：二进制帧传输终端数据；文本帧为 JSON 控制消息（resize、title）。
func (s *Service) attach(c *gin.Context) {
	sess := s.get(c)
	if sess == nil {
		return
	}
	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	out := make(chan []byte, 256)
	sess.mu.Lock()
	replay := append([]byte(nil), sess.buf...)
	sess.subs[out] = struct{}{}
	sess.Attached++
	sess.mu.Unlock()
	defer func() {
		sess.mu.Lock()
		delete(sess.subs, out)
		sess.Attached--
		sess.detached = time.Now()
		sess.mu.Unlock()
	}()

	if len(replay) > 0 {
		if err := conn.Write(ctx, websocket.MessageBinary, replay); err != nil {
			return
		}
	}

	go func() {
		defer cancel()
		for {
			typ, b, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := sess.pty.Write(b); err != nil {
					return
				}
				continue
			}
			var m control
			if json.Unmarshal(b, &m) != nil {
				continue
			}
			switch m.Type {
			case "resize":
				if m.Cols > 0 && m.Rows > 0 {
					sess.pty.Resize(m.Cols, m.Rows)
				}
			case "title":
				sess.mu.Lock()
				sess.Title = m.Title
				sess.mu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case b := <-out:
			if err := conn.Write(ctx, websocket.MessageBinary, b); err != nil {
				return
			}
		case <-sess.done:
			// 把剩余输出发完再通知退出
			for len(out) > 0 {
				conn.Write(ctx, websocket.MessageBinary, <-out)
			}
			msg, _ := json.Marshal(map[string]any{"type": "exit", "code": sess.ExitCode})
			conn.Write(ctx, websocket.MessageText, msg)
			conn.Close(websocket.StatusNormalClosure, "exit")
			return
		}
	}
}
