package lease

import (
	"bytes"
	"testing"
	"time"
)

// startServer 在共享的进程内传输上启动一个租约服务端，返回其地址。
func startServer(t *testing.T, tr *MemTransport, leaseDuration time.Duration) (string, *Server) {
	t.Helper()
	srv := NewServer(leaseDuration, tr)
	ln, err := tr.Listen()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), srv
}

func newClient(t *testing.T, tr *MemTransport, name, serverAddr string) *Client {
	t.Helper()
	c, err := NewClient(name, serverAddr, tr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// TestAcceptance 验收场景：
// 客户端 1 在 t=0 获得文件 X 为期 5 秒的读租约；
// 客户端 2 在 t=2s 尝试修改 X；
// 客户端 1 失联（撤销通知无法送达）；
// 服务端必须安全阻塞，直到 t=5s 租约自然过期后才放行写入。
func TestAcceptance(t *testing.T) {
	const leaseDuration = 5 * time.Second
	tr := NewMemTransport()
	addr, _ := startServer(t, tr, leaseDuration)

	c1 := newClient(t, tr, "client-1", addr)
	c2 := newClient(t, tr, "client-2", addr)

	start := time.Now()

	// t=0：客户端 1 读取 X，获得 5 秒读租约。
	content, hit, err := c1.Read("X")
	if err != nil {
		t.Fatalf("c1.Read: %v", err)
	}
	if hit {
		t.Fatal("first read should miss cache")
	}
	if string(content) != "" {
		t.Fatalf("expected empty initial content, got %q", content)
	}

	// 客户端 1 随后失联：撤销回调无法送达。
	c1.Disconnect()

	// t=2s：客户端 2 发起写入。
	time.Sleep(2*time.Second - time.Since(start))
	writeDone := make(chan time.Time, 1)
	go func() {
		if _, err := c2.Write("X", []byte("v2")); err != nil {
			t.Errorf("c2.Write: %v", err)
		}
		writeDone <- time.Now()
	}()

	// 在租约到期前（t=4.5s），写入绝不能生效。
	select {
	case done := <-writeDone:
		t.Fatalf("write completed at %v, before lease expiry (5s)", done.Sub(start))
	case <-time.After(4500*time.Millisecond - time.Since(start)):
	}

	// 写入应在 t=5s（租约过期）后不久放行。
	select {
	case done := <-writeDone:
		elapsed := done.Sub(start)
		if elapsed < leaseDuration {
			t.Fatalf("write completed at %v, before lease expiry", elapsed)
		}
		if elapsed > leaseDuration+2*time.Second {
			t.Fatalf("write completed too late at %v", elapsed)
		}
		t.Logf("write unblocked at %v (lease expired at 5s)", elapsed)
	case <-time.After(3 * time.Second):
		t.Fatal("write was not unblocked after lease expiry")
	}

	// 写入生效后，新读者必须读到新内容（零读脏的另一半）。
	c3 := newClient(t, tr, "client-3", addr)
	got, _, err := c3.Read("X")
	if err != nil {
		t.Fatalf("c3.Read: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("expected v2 after write, got %q", got)
	}
}

// TestRevocationFastPath 客户端在线时，服务端回调撤销租约，
// 写者应立即被放行，无需等待租约过期。
func TestRevocationFastPath(t *testing.T) {
	tr := NewMemTransport()
	addr, _ := startServer(t, tr, 10*time.Second) // 长租约：若走超时路径测试会失败
	c1 := newClient(t, tr, "client-1", addr)
	c2 := newClient(t, tr, "client-2", addr)

	if _, _, err := c1.Read("X"); err != nil {
		t.Fatalf("c1.Read: %v", err)
	}

	start := time.Now()
	if _, err := c2.Write("X", []byte("v2")); err != nil {
		t.Fatalf("c2.Write: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("write took %v; revocation fast path should be near-instant", elapsed)
	}

	// 客户端 1 的缓存应已被撤销，再次读必须回源拿到新内容。
	got, hit, err := c1.Read("X")
	if err != nil {
		t.Fatalf("c1.Read after revoke: %v", err)
	}
	if hit {
		t.Fatal("cache should have been invalidated by revocation")
	}
	if string(got) != "v2" {
		t.Fatalf("expected v2, got %q", got)
	}
}

// TestCacheHitWithinLease 租约有效期内重复读必须命中本地缓存，
// 不触发新的服务端读请求。
func TestCacheHitWithinLease(t *testing.T) {
	tr := NewMemTransport()
	addr, srv := startServer(t, tr, 2*time.Second)
	c1 := newClient(t, tr, "client-1", addr)
	c2 := newClient(t, tr, "client-2", addr)

	if _, err := c2.Write("X", []byte("v1")); err != nil {
		t.Fatalf("write: %v", err)
	}
	before := srv.ReadCount()
	for i := 0; i < 3; i++ {
		got, hit, err := c1.Read("X")
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if !bytes.Equal(got, []byte("v1")) {
			t.Fatalf("read %d: got %q", i, got)
		}
		if i > 0 && !hit {
			t.Fatalf("read %d should hit local cache", i)
		}
	}
	if got := srv.ReadCount() - before; got != 1 {
		t.Fatalf("server saw %d reads, want 1 (cache should absorb the rest)", got)
	}
}

// TestWriterBlocksNewLeases 写者等待期间，新的读租约请求被阻塞，
// 写入完成后读者直接读到新内容，保证写者等待时间有界。
func TestWriterBlocksNewLeases(t *testing.T) {
	tr := NewMemTransport()
	addr, _ := startServer(t, tr, 500*time.Millisecond)
	c1 := newClient(t, tr, "reader-old", addr)
	c2 := newClient(t, tr, "reader-new", addr)
	w := newClient(t, tr, "writer", addr)

	if _, _, err := c1.Read("X"); err != nil {
		t.Fatalf("c1.Read: %v", err)
	}
	c1.Disconnect() // 失联，写者必须等满租约

	writeDone := make(chan struct{})
	go func() {
		if _, err := w.Write("X", []byte("v2")); err != nil {
			t.Errorf("write: %v", err)
		}
		close(writeDone)
	}()

	// 写者已开始等待租约过期；此时新读者的 Read 应阻塞至写入完成，
	// 且最终读到的是新内容。
	time.Sleep(100 * time.Millisecond)
	got, _, err := c2.Read("X")
	if err != nil {
		t.Fatalf("c2.Read: %v", err)
	}
	<-writeDone
	if string(got) != "v2" {
		t.Fatalf("reader blocked behind writer should see v2, got %q", got)
	}
}

// TestLeaseExpiryRefreshesCache 租约自然过期后，客户端再次读会回源，
// 能读到此前已生效的写入。
func TestLeaseExpiryRefreshesCache(t *testing.T) {
	tr := NewMemTransport()
	addr, _ := startServer(t, tr, 300*time.Millisecond)
	c1 := newClient(t, tr, "client-1", addr)
	c2 := newClient(t, tr, "client-2", addr)

	if _, _, err := c1.Read("X"); err != nil {
		t.Fatalf("c1.Read: %v", err)
	}
	// 等租约自然过期，写者无需撤销即可直接写。
	time.Sleep(400 * time.Millisecond)
	if _, err := c2.Write("X", []byte("v2")); err != nil {
		t.Fatalf("c2.Write: %v", err)
	}
	got, hit, err := c1.Read("X")
	if err != nil {
		t.Fatalf("c1.Read: %v", err)
	}
	if hit {
		t.Fatal("expired lease must not serve from cache")
	}
	if string(got) != "v2" {
		t.Fatalf("expected v2, got %q", got)
	}
}
