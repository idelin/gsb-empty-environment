package leasecache

import (
	"testing"
	"time"
)

func sleepUntil(t time.Time) {
	if d := time.Until(t); d > 0 {
		time.Sleep(d)
	}
}

// 测试与演示在 PipeNetwork（进程内内存网络）上运行标准 HTTP/JSON 协议；
// 真实部署改用 TCP（Server.Start / Client.Start）即可，协议不变。
func startServer(t *testing.T, pn *PipeNetwork, ttl time.Duration) *Server {
	srv := NewServer(ttl)
	srv.Transport = pn.HTTPTransport()
	ln, err := pn.Listen("server")
	if err != nil {
		t.Fatalf("listen server: %v", err)
	}
	if err := srv.StartOn(ln); err != nil {
		t.Fatalf("start server: %v", err)
	}
	return srv
}

func startClient(t *testing.T, pn *PipeNetwork, id, serverURL string) *Client {
	c := NewClient(id, serverURL)
	c.Transport = pn.HTTPTransport()
	ln, err := pn.Listen(id)
	if err != nil {
		t.Fatalf("listen client %s: %v", id, err)
	}
	if err := c.StartOn(ln); err != nil {
		t.Fatalf("start client %s: %v", id, err)
	}
	return c
}

// 验收场景：客户端 1 获得文件 X 为期 5 秒的读租约；客户端 2 在第 2 秒
// 尝试修改 X；服务端通知客户端 1 撤销租约，1 失联，服务端安全阻塞至满
// 5 秒后才放行客户端 2 的写入。期间客户端 1 读本地缓存为零脏读。
func TestAcceptanceWriteBlockedUntilLeaseExpiry(t *testing.T) {
	pn := NewPipeNetwork()
	srv := startServer(t, pn, 5*time.Second)
	defer srv.Close()
	srv.SetFile("X", []byte("v1"))

	c1 := startClient(t, pn, "client-1", srv.URL())
	defer c1.Close()
	c2 := startClient(t, pn, "client-2", srv.URL())
	defer c2.Close()

	// t=0：客户端 1 获得 5 秒读租约
	data, version, err := c1.Read("X")
	if err != nil {
		t.Fatalf("c1 read: %v", err)
	}
	if string(data) != "v1" || version != 1 {
		t.Fatalf("c1 got %q v%d, want v1 v1", data, version)
	}
	grant := time.Now()

	// 客户端 1 失联：回调端口关闭，撤销通知无法送达
	c1.Disconnect()

	// t=2s：客户端 2 发起写入
	sleepUntil(grant.Add(2 * time.Second))
	type writeResult struct {
		version uint64
		elapsed time.Duration
		err     error
	}
	done := make(chan writeResult, 1)
	go func() {
		v, err := c2.Write("X", []byte("v2"))
		done <- writeResult{v, time.Since(grant), err}
	}()

	// t=3s：写入应仍被阻塞；客户端 1 读本地缓存必须仍是 v1（零脏读）
	sleepUntil(grant.Add(3 * time.Second))
	select {
	case r := <-done:
		t.Fatalf("write released too early at %v (lease valid until 5s)", r.elapsed)
	default:
	}
	cached, cv, err := c1.Read("X")
	if err != nil {
		t.Fatalf("c1 cached read: %v", err)
	}
	if string(cached) != "v1" || cv != 1 {
		t.Fatalf("dirty read: c1 got %q v%d during valid lease, want v1 v1", cached, cv)
	}
	if got := srv.Version("X"); got != 1 {
		t.Fatalf("server applied write during valid lease: version=%d", got)
	}

	// 写入必须在租约满 5 秒后才被放行
	r := <-done
	if r.err != nil {
		t.Fatalf("c2 write: %v", r.err)
	}
	if r.elapsed < 4800*time.Millisecond {
		t.Fatalf("write released before lease expiry: elapsed=%v", r.elapsed)
	}
	if r.elapsed > 7*time.Second {
		t.Fatalf("write released too late: elapsed=%v", r.elapsed)
	}
	if r.version != 2 {
		t.Fatalf("write version=%d, want 2", r.version)
	}

	// 写入生效后，新读必须看到 v2
	data, version, err = c2.Read("X")
	if err != nil {
		t.Fatalf("c2 read: %v", err)
	}
	if string(data) != "v2" || version != 2 {
		t.Fatalf("c2 got %q v%d, want v2 v2", data, version)
	}
}

// 客户端在线时，撤销回调成功，写入应提前放行（不必等满租约期）。
func TestWriteUnblockedEarlyByRevocation(t *testing.T) {
	pn := NewPipeNetwork()
	srv := startServer(t, pn, 5*time.Second)
	defer srv.Close()
	srv.SetFile("X", []byte("v1"))

	c1 := startClient(t, pn, "client-1", srv.URL())
	defer c1.Close()
	c2 := startClient(t, pn, "client-2", srv.URL())
	defer c2.Close()

	if _, _, err := c1.Read("X"); err != nil {
		t.Fatalf("c1 read: %v", err)
	}

	start := time.Now()
	if _, err := c2.Write("X", []byte("v2")); err != nil {
		t.Fatalf("c2 write: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("write not unblocked by revocation: elapsed=%v", elapsed)
	}

	// c1 的缓存已被撤销，重新读必须拿到 v2
	if c1.Cached("X") {
		t.Fatalf("c1 cache should have been invalidated by revocation")
	}
	data, _, err := c1.Read("X")
	if err != nil {
		t.Fatalf("c1 read: %v", err)
	}
	if string(data) != "v2" {
		t.Fatalf("c1 got %q, want v2", data)
	}
}

// 没有任何租约时，写入立即生效。
func TestWriteWithoutLeasesIsImmediate(t *testing.T) {
	pn := NewPipeNetwork()
	srv := startServer(t, pn, 5*time.Second)
	defer srv.Close()
	srv.SetFile("X", []byte("v1"))

	c := startClient(t, pn, "writer", srv.URL())
	defer c.Close()

	start := time.Now()
	v, err := c.Write("X", []byte("v2"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("write without leases should be immediate, took %v", elapsed)
	}
	if v != 2 {
		t.Fatalf("version=%d, want 2", v)
	}
}

// 租约自然过期后，写入不再被阻塞，客户端重新读会刷新缓存。
func TestExpiredLeaseDoesNotBlock(t *testing.T) {
	pn := NewPipeNetwork()
	srv := startServer(t, pn, 300*time.Millisecond)
	defer srv.Close()
	srv.SetFile("X", []byte("v1"))

	c1 := startClient(t, pn, "client-1", srv.URL())
	defer c1.Close()
	c2 := startClient(t, pn, "client-2", srv.URL())
	defer c2.Close()

	if _, _, err := c1.Read("X"); err != nil {
		t.Fatalf("c1 read: %v", err)
	}
	c1.Disconnect()
	time.Sleep(400 * time.Millisecond) // 租约已过期

	start := time.Now()
	if _, err := c2.Write("X", []byte("v2")); err != nil {
		t.Fatalf("c2 write: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expired lease should not block write, took %v", elapsed)
	}

	data, _, err := c2.Read("X")
	if err != nil {
		t.Fatalf("c2 read: %v", err)
	}
	if string(data) != "v2" {
		t.Fatalf("c2 got %q, want v2", data)
	}
}
