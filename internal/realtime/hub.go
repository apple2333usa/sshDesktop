// Package realtime 实现 /api/v1/ws：每个浏览器标签页一条 WebSocket，按频道订阅推送。
//
// 客户端发送 {"op":"sub","ch":"metrics:local"} 或 {"op":"unsub","ch":...}；
// 服务端推送 {"ch":"metrics:local","data":{...}}。
package realtime

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/apple2333usa/sshdesktop/internal/api"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	// Authorize 决定账号能否订阅某频道。
	Authorize func(p *api.Principal, ch string) bool
}

type client struct {
	user *api.Principal
	subs map[string]bool
	send chan []byte
	mu   sync.Mutex
}

type message struct {
	Ch   string `json:"ch"`
	Data any    `json:"data"`
}

func NewHub() *Hub { return &Hub{clients: map[*client]struct{}{}} }

// HasSubscribers 用于避免没人看时的无用计算。
func (h *Hub) HasSubscribers(ch string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.mu.Lock()
		ok := c.subs[ch]
		c.mu.Unlock()
		if ok {
			return true
		}
	}
	return false
}

// Publish 推送给订阅了该频道的所有连接。
func (h *Hub) Publish(ch string, data any) { h.publish(ch, data, 0) }

// PublishUser 只推送给某个账号的连接（无需订阅，例如通知）。
func (h *Hub) PublishUser(userID uint, ch string, data any) { h.publish(ch, data, userID) }

func (h *Hub) publish(ch string, data any, userID uint) {
	b, err := json.Marshal(message{Ch: ch, Data: data})
	if err != nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if userID != 0 {
			if c.user.UserID != userID {
				continue
			}
		} else {
			c.mu.Lock()
			ok := c.subs[ch]
			c.mu.Unlock()
			if !ok {
				continue
			}
		}
		select {
		case c.send <- b:
		default: // 慢客户端丢弃本条，避免阻塞
		}
	}
}

// DisconnectUser 关闭某账号的全部实时连接（会话被注销时）。
func (h *Hub) DisconnectUser(userID uint) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if c.user.UserID == userID {
			select {
			case c.send <- nil:
			default:
			}
		}
	}
}

func (h *Hub) Handle(c *gin.Context) {
	p := api.User(c)
	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	cl := &client{user: p, subs: map[string]bool{}, send: make(chan []byte, 64)}
	h.mu.Lock()
	h.clients[cl] = struct{}{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.clients, cl)
		h.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var m struct {
				Op string `json:"op"`
				Ch string `json:"ch"`
			}
			if json.Unmarshal(b, &m) != nil || m.Ch == "" {
				continue
			}
			switch m.Op {
			case "sub":
				if h.Authorize != nil && !h.Authorize(p, m.Ch) {
					b, _ := json.Marshal(message{Ch: "error", Data: map[string]string{"ch": m.Ch, "message": "没有权限订阅"}})
					cl.send <- b
					continue
				}
				cl.mu.Lock()
				cl.subs[m.Ch] = true
				cl.mu.Unlock()
			case "unsub":
				cl.mu.Lock()
				delete(cl.subs, m.Ch)
				cl.mu.Unlock()
			case "ping":
				cl.send <- []byte(`{"ch":"pong"}`)
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-cl.send:
			if b == nil {
				conn.Close(websocket.StatusPolicyViolation, "session revoked")
				return
			}
			wctx, wc := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, b)
			wc()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pc := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			pc()
			if err != nil {
				slog.Debug("ws ping 失败", "err", err)
				return
			}
		}
	}
}

// ChannelNode 从 "metrics:local" 这类频道名中取出节点 ID。
func ChannelNode(ch string) (prefix, rest string) {
	prefix, rest, _ = strings.Cut(ch, ":")
	return
}
