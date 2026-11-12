//go:build darwin || linux

package netsec

import (
	"net"
	"os"
	"syscall"
	"testing"
)

// unixPair 用 socketpair 建立一对已连接的 Unix 域套接字，
// 避免文件系统路径长度限制。
func unixPair(t *testing.T) (client, server *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	conns := make([]*net.UnixConn, 0, 2)
	for _, fd := range fds {
		f := os.NewFile(uintptr(fd), "sock")
		c, err := net.FileConn(f)
		f.Close()
		if err != nil {
			t.Fatalf("FileConn: %v", err)
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			t.Fatalf("conn is %T, want *net.UnixConn", c)
		}
		conns = append(conns, uc)
	}
	t.Cleanup(func() {
		conns[0].Close()
		conns[1].Close()
	})
	return conns[0], conns[1]
}

// 端到端：真实内核凭据（本进程 pid/uid、非零代次）与 SCM_RIGHTS
// 传入的句柄被绑定到同一消息；载荷自报身份被忽略。
func TestUnixTransportBindsKernelCreds(t *testing.T) {
	client, server := unixPair(t)

	// 传入一个真实文件句柄。
	f, err := os.CreateTemp("", "netsec-fd-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString("secret"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	// 载荷自称 root；内核凭据应将其覆盖。
	payload := []byte(`{"uid":0,"user":"root","op":"read"}`)
	oob := syscall.UnixRights(int(f.Fd()))
	if _, _, err := client.WriteMsgUnix(payload, oob, nil); err != nil {
		t.Fatalf("WriteMsgUnix: %v", err)
	}

	r := NewReceiver(NewUnixTransport(server))
	msg, err := r.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}

	if string(msg.Payload) != string(payload) {
		t.Fatalf("payload = %q, want %q", msg.Payload, payload)
	}
	if msg.Creds.PID != os.Getpid() {
		t.Fatalf("creds pid = %d, want %d", msg.Creds.PID, os.Getpid())
	}
	if msg.Creds.UID != uint32(os.Getuid()) {
		t.Fatalf("creds uid = %d, want %d", msg.Creds.UID, os.Getuid())
	}
	if msg.Creds.Generation == 0 {
		t.Fatal("creds generation = 0, want kernel-attached process generation")
	}
	if len(msg.FDs) != 1 {
		t.Fatalf("fds = %v, want 1 fd", msg.FDs)
	}

	// 收到的句柄可用：与发送方共享文件偏移，先归位再读。
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	buf := make([]byte, 6)
	if _, err := syscall.Read(msg.FDs[0], buf); err != nil {
		t.Fatalf("read received fd: %v", err)
	}
	if string(buf) != "secret" {
		t.Fatalf("read = %q, want %q", buf, "secret")
	}
	if err := msg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// 同一连接上连续两条消息内核身份不变，均通过校验。
func TestUnixTransportStableIdentity(t *testing.T) {
	client, server := unixPair(t)
	r := NewReceiver(NewUnixTransport(server))

	for _, body := range []string{"one", "two"} {
		if _, _, err := client.WriteMsgUnix([]byte(body), nil, nil); err != nil {
			t.Fatalf("WriteMsgUnix: %v", err)
		}
		msg, err := r.Recv()
		if err != nil {
			t.Fatalf("Recv %q: %v", body, err)
		}
		if string(msg.Payload) != body {
			t.Fatalf("payload = %q, want %q", msg.Payload, body)
		}
	}
}
