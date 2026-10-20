// Package sshtest 提供测试用的进程内 SSH 服务器：密码与公钥认证、exec、带 PTY 的 shell、
// SFTP 子系统、direct-streamlocal（Unix socket 转发）和 keepalive，命令在本机用 sh 执行。
// 用于在没有 sshd 的环境（CI）里对远程节点相关代码做集成测试。
package sshtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"testing"

	"github.com/creack/pty"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

const (
	User     = "test"
	Password = "secret"
)

type Server struct {
	Addr    string
	Host    string
	Port    int
	HostKey ssh.PublicKey
	// ClientKey 是被授权的客户端私钥（PEM），用于测试私钥认证。
	ClientKey []byte

	ln     net.Listener
	signer ssh.Signer
	wg     sync.WaitGroup
	mu     sync.Mutex
	conns  []net.Conn
}

// Start 启动服务器，测试结束时自动关闭。
func Start(t testing.TB) *Server {
	t.Helper()
	_, hostPriv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	clientPub, clientPriv, _ := ed25519.GenerateKey(rand.Reader)
	pemBlock, err := ssh.MarshalPrivateKey(clientPriv, "")
	if err != nil {
		t.Fatal(err)
	}
	authorized, _ := ssh.NewPublicKey(clientPub)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	s := &Server{
		Addr: ln.Addr().String(), Host: host, Port: p, HostKey: signer.PublicKey(),
		ClientKey: pem.EncodeToMemory(pemBlock), ln: ln, signer: signer,
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if c.User() == User && string(pw) == Password {
				return nil, nil
			}
			return nil, errors.New("密码错误")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == User && string(k.Marshal()) == string(authorized.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("未授权的密钥")
		},
	}
	cfg.AddHostKey(signer)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go s.serve(c, cfg)
		}
	}()
	t.Cleanup(s.Close)
	return s
}

// Close 关闭监听和全部连接。
func (s *Server) Close() {
	s.ln.Close()
	s.DropConnections()
	s.wg.Wait()
}

// DropConnections 断开当前所有连接（用于测试断线重连）。
func (s *Server) DropConnections() {
	s.mu.Lock()
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
	s.mu.Unlock()
}

func (s *Server) serve(c net.Conn, cfg *ssh.ServerConfig) {
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	defer sc.Close()
	go func() {
		for r := range reqs {
			if r.WantReply {
				r.Reply(r.Type == "keepalive@openssh.com", nil)
			}
		}
	}()
	for nc := range chans {
		switch nc.ChannelType() {
		case "session":
			ch, creqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go session(ch, creqs)
		case "direct-streamlocal@openssh.com":
			go streamlocal(nc)
		default:
			nc.Reject(ssh.UnknownChannelType, "不支持")
		}
	}
}

func streamlocal(nc ssh.NewChannel) {
	var msg struct {
		Path      string
		Reserved0 string
		Reserved1 uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &msg); err != nil {
		nc.Reject(ssh.ConnectionFailed, "参数错误")
		return
	}
	conn, err := net.Dial("unix", msg.Path)
	if err != nil {
		nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		conn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		io.Copy(ch, conn)
		ch.CloseWrite()
		ch.Close()
	}()
	io.Copy(conn, ch)
	conn.Close()
}

func exitStatus(ch ssh.Channel, err error) {
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
		if code < 0 {
			code = 255
		}
	} else if err != nil {
		code = 255
	}
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(code))
	ch.SendRequest("exit-status", false, b)
}

func session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	var ptyReq *struct {
		Term          string
		Cols, Rows    uint32
		Width, Height uint32
		Modes         string
	}
	var cmd *exec.Cmd
	var tty *os.File
	done := make(chan struct{})
	start := func(c *exec.Cmd) bool {
		c.Env = append(os.Environ(), "HOME="+os.Getenv("HOME"))
		if ptyReq != nil {
			f, err := pty.StartWithSize(c, &pty.Winsize{Cols: uint16(ptyReq.Cols), Rows: uint16(ptyReq.Rows)})
			if err != nil {
				return false
			}
			tty = f
			go func() { io.Copy(f, ch) }()
			go func() {
				io.Copy(ch, f)
				err := c.Wait()
				exitStatus(ch, err)
				close(done)
				ch.Close()
			}()
			return true
		}
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		stdin, _ := c.StdinPipe()
		c.Stdout, c.Stderr = ch, ch.Stderr()
		if err := c.Start(); err != nil {
			return false
		}
		go func() {
			io.Copy(stdin, ch)
			stdin.Close()
		}()
		go func() {
			err := c.Wait()
			exitStatus(ch, err)
			close(done)
			ch.Close()
		}()
		return true
	}
	for r := range reqs {
		ok := false
		switch r.Type {
		case "pty-req":
			ptyReq = &struct {
				Term          string
				Cols, Rows    uint32
				Width, Height uint32
				Modes         string
			}{}
			ok = ssh.Unmarshal(r.Payload, ptyReq) == nil
		case "env":
			ok = true
		case "exec":
			var m struct{ Command string }
			if ssh.Unmarshal(r.Payload, &m) == nil && cmd == nil {
				cmd = exec.Command("sh", "-c", m.Command)
				ok = start(cmd)
			}
		case "shell":
			if cmd == nil {
				cmd = exec.Command("sh")
				ok = start(cmd)
			}
		case "subsystem":
			var m struct{ Name string }
			if ssh.Unmarshal(r.Payload, &m) == nil && m.Name == "sftp" && cmd == nil {
				ok = true
				r.Reply(true, nil)
				srv, err := sftp.NewServer(ch)
				if err == nil {
					srv.Serve()
					srv.Close()
				}
				return
			}
		case "window-change":
			var m struct{ Cols, Rows, W, H uint32 }
			if tty != nil && ssh.Unmarshal(r.Payload, &m) == nil {
				pty.Setsize(tty, &pty.Winsize{Cols: uint16(m.Cols), Rows: uint16(m.Rows)})
			}
			ok = true
		case "signal":
			if cmd != nil && cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				cmd.Process.Kill()
			}
			ok = true
		}
		if r.WantReply {
			r.Reply(ok, nil)
		}
	}
	if cmd != nil && cmd.Process != nil {
		select {
		case <-done:
		default:
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			cmd.Process.Kill()
		}
	}
}
