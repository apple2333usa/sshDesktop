package agent

import (
	"net"
	"os"
	"strconv"
	"syscall"
)

// systemd 传入的 socket 从 fd 3 开始（sd_listen_fds 约定）。
const listenFDsStart = 3

// ActivationListener 返回 systemd socket 激活传入的监听 socket；不是由 socket 激活启动时返回 nil。
// 独立 agent 用它接收 sshdesktop-agent.socket 以 root 创建的 socket：动态用户自己建的
// RuntimeDirectory 在新版 systemd 下位于只有 root 能进入的 /run/private 中，非 root 的 SSH 账号连不上。
func ActivationListener() (net.Listener, error) {
	return activationListener(listenFDsStart)
}

func activationListener(fd int) (net.Listener, error) {
	pid, fds := os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_FDNAMES")
	if pid != strconv.Itoa(os.Getpid()) {
		return nil, nil
	}
	if n, err := strconv.Atoi(fds); err != nil || n < 1 {
		return nil, nil
	}
	syscall.CloseOnExec(fd)
	f := os.NewFile(uintptr(fd), "agent.sock")
	defer f.Close()
	return net.FileListener(f)
}
