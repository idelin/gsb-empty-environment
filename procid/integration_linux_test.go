//go:build linux

package procid

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// 端到端：真实 socketpair 风格 Unix 连接 + SO_PEERCRED + 真实 pidfd +
// 真实 /proc，验证测试进程自身的完整绑定/认证链路。
func TestRealBindConnAndVerify(t *testing.T) {
	dir := t.TempDir()
	addr := filepath.Join(dir, "sock")

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	acceptErr := make(chan error, 1)
	serverConn := make(chan *net.UnixConn, 1)
	go func() {
		c, aerr := listener.AcceptUnix()
		acceptErr <- aerr
		if aerr == nil {
			serverConn <- c
		}
	}()

	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if err := <-acceptErr; err != nil {
		t.Fatalf("accept: %v", err)
	}
	conn := <-serverConn
	defer conn.Close()

	binder := &Binder{Proc: DefaultProcFS(), Opener: DefaultPidOpener()}
	binding, err := binder.BindConn(conn)
	if err != nil {
		t.Fatalf("BindConn: %v", err)
	}
	defer binding.Close()

	if binding.PID() != os.Getpid() {
		t.Fatalf("peer pid = %d, want %d", binding.PID(), os.Getpid())
	}
	if binding.UID() != uint32(os.Getuid()) {
		t.Fatalf("peer uid = %d, want %d", binding.UID(), os.Getuid())
	}
	if binding.Starttime() == 0 {
		t.Fatal("starttime must be non-zero")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if binding.Exe() != exe {
		t.Fatalf("exe = %q, want %q", binding.Exe(), exe)
	}

	if err := binding.Verify(); err != nil {
		t.Fatalf("Verify self: %v", err)
	}

	// 关闭后不得再放行认证。
	if err := binding.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := binding.Verify(); err == nil {
		t.Fatal("Verify after Close must fail")
	}
}

// 真实 procfs 解析：/proc/self/stat 必须被正确解析，PID 与自身一致。
func TestRealProcFSParseSelf(t *testing.T) {
	proc := DefaultProcFS()
	raw, err := proc.ReadStat(os.Getpid())
	if err != nil {
		t.Fatalf("ReadStat: %v", err)
	}
	info, err := ParseStat(raw)
	if err != nil {
		t.Fatalf("ParseStat: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Fatalf("stat pid = %d, want %d", info.PID, os.Getpid())
	}
	if info.Starttime == 0 {
		t.Fatal("self starttime should be non-zero")
	}

	exe, err := proc.ReadExe(os.Getpid())
	if err != nil {
		t.Fatalf("ReadExe: %v", err)
	}
	if IsExeDeleted(exe) {
		t.Fatalf("test binary exe reported deleted: %q", exe)
	}
}

// 真实 pidfd 生命周期：子进程退出后，句柄探活必须返回 not alive，
// 这是 PID 复用防护在生产环境成立的关键保证。
func TestRealPidFDDetectsExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sleep", "0.2")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}

	opener := DefaultPidOpener()
	fd, err := opener.OpenPidFD(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("OpenPidFD: %v", err)
	}
	defer fd.Close()

	if fd.PID() != cmd.Process.Pid {
		t.Fatalf("fd pid = %d, want %d", fd.PID(), cmd.Process.Pid)
	}
	alive, err := fd.Alive()
	if err != nil {
		t.Fatalf("Alive before exit: %v", err)
	}
	if !alive {
		t.Fatal("child should be alive before Wait")
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait child: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive, err = fd.Alive()
		if err != nil {
			t.Fatalf("Alive after exit: %v", err)
		}
		if !alive {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pidfd still reports alive after child exited")
}
