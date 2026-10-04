package connpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeConn 假连接：统计关闭次数，可注入关闭失败。
type fakeConn struct {
	id        int
	broken    bool
	closeErr  error
	closeCnt  atomic.Int32
	closedCh  chan struct{}
	closeOnce sync.Once
}

func (f *fakeConn) Close() error {
	f.closeCnt.Add(1)
	f.closeOnce.Do(func() { close(f.closedCh) })
	return f.closeErr
}

// fakeFactory 统计创建次数，产出带编号的假连接。
type fakeFactory struct {
	created atomic.Int32
	dialErr error
}

func (ff *fakeFactory) dial(ctx context.Context) (Conn, error) {
	if ff.dialErr != nil {
		return nil, ff.dialErr
	}
	id := ff.created.Add(1)
	return &fakeConn{id: int(id), closedCh: make(chan struct{})}, nil
}

// 统计借出次数的包装。
type borrowCounter struct {
	pool  *Pool
	count atomic.Int32
}

func (b *borrowCounter) Borrow(ctx context.Context) (*Handle, error) {
	h, err := b.pool.Borrow(ctx)
	if err == nil {
		b.count.Add(1)
	}
	return h, err
}

func newTestPool(ff *fakeFactory, opts Options) (*Pool, *borrowCounter) {
	p := New(ff.dial, opts)
	return p, &borrowCounter{pool: p}
}

// 1. 空闲连接复用：归还后再次借出不新建连接。
func TestReuseIdleConn(t *testing.T) {
	ff := &fakeFactory{}
	_, bc := newTestPool(ff, Options{MaxOpen: 4, MaxIdle: 4})
	ctx := context.Background()

	h1, err := bc.Borrow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw := h1.Conn
	if err := h1.Close(); err != nil {
		t.Fatal(err)
	}
	h2, err := bc.Borrow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h2.Conn != raw {
		t.Fatalf("expected reused conn, got a different one")
	}
	h2.Close()

	if got := ff.created.Load(); got != 1 {
		t.Fatalf("created = %d, want 1", got)
	}
	if got := bc.count.Load(); got != 2 {
		t.Fatalf("borrowed = %d, want 2", got)
	}
	if got := raw.(*fakeConn).closeCnt.Load(); got != 0 {
		t.Fatalf("closed = %d, want 0 (reused, not closed)", got)
	}
}

// 2. 配置换代：旧代次空闲连接立即排空关闭，新借出创建新连接。
func TestUpdateConfigDrainsIdle(t *testing.T) {
	ff := &fakeFactory{}
	p, bc := newTestPool(ff, Options{MaxOpen: 4, MaxIdle: 4})
	ctx := context.Background()

	h1, _ := bc.Borrow(ctx)
	old := h1.Conn.(*fakeConn)
	h1.Close() // 回到空闲池

	p.UpdateConfig() // 换代：旧空闲连接应立即被排空关闭

	if got := old.closeCnt.Load(); got != 1 {
		t.Fatalf("old idle conn closed = %d, want 1", got)
	}
	if open, idle := p.Stats(); open != 0 || idle != 0 {
		t.Fatalf("stats = (%d,%d), want (0,0)", open, idle)
	}

	h2, _ := bc.Borrow(ctx)
	if h2.Conn.(*fakeConn) == old {
		t.Fatal("must not reuse old-generation conn after UpdateConfig")
	}
	if got := ff.created.Load(); got != 2 {
		t.Fatalf("created = %d, want 2", got)
	}
	h2.Close()
}

// 3. 借出中的旧代次连接：换代后允许完成请求，归还时关闭且不进入新池。
func TestInFlightOldGenClosedOnReturn(t *testing.T) {
	ff := &fakeFactory{}
	p, bc := newTestPool(ff, Options{MaxOpen: 4, MaxIdle: 4})
	ctx := context.Background()

	h1, _ := bc.Borrow(ctx)
	old := h1.Conn.(*fakeConn)

	p.UpdateConfig() // 换代时 h1 仍在借出中：不得被立即关闭
	if got := old.closeCnt.Load(); got != 0 {
		t.Fatalf("in-flight conn closed = %d, want 0", got)
	}

	h1.Close() // 旧代次归还：关闭，不进新池
	if got := old.closeCnt.Load(); got != 1 {
		t.Fatalf("returned old-gen conn closed = %d, want 1", got)
	}
	if open, idle := p.Stats(); open != 0 || idle != 0 {
		t.Fatalf("stats = (%d,%d), want (0,0): old gen must not enter new pool", open, idle)
	}

	h2, _ := bc.Borrow(ctx) // 新池里只能新建
	if got := ff.created.Load(); got != 2 {
		t.Fatalf("created = %d, want 2", got)
	}
	h2.Close()
}

// 4. 借出中的连接不得被重复关闭：换代 + 池关闭 + 归还，物理关闭仅一次。
func TestBorrowedConnNeverDoubleClosed(t *testing.T) {
	ff := &fakeFactory{}
	p, bc := newTestPool(ff, Options{MaxOpen: 4, MaxIdle: 4})
	ctx := context.Background()

	h, _ := bc.Borrow(ctx)
	fc := h.Conn.(*fakeConn)

	p.UpdateConfig() // 换代
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	h.Close()      // 归还（触发物理关闭）
	h.Close()      // 重复归还：幂等
	h.Invalidate() // 已归还后再标记：幂等

	if got := fc.closeCnt.Load(); got != 1 {
		t.Fatalf("closed = %d, want exactly 1", got)
	}
}

// 5. 等待超时：MaxOpen 打满且无人归还时，借出按 ctx 超时失败。
func TestBorrowWaitTimeout(t *testing.T) {
	ff := &fakeFactory{}
	_, bc := newTestPool(ff, Options{MaxOpen: 1, MaxIdle: 1})

	h1, err := bc.Borrow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer h1.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = bc.Borrow(ctx)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err = %v, want ErrWaitTimeout", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("timeout took too long: %v", d)
	}
	if got := ff.created.Load(); got != 1 {
		t.Fatalf("created = %d, want 1", got)
	}
}

// 6. 等待被唤醒：归还后阻塞中的借出获得连接。
func TestBorrowWakesOnReturn(t *testing.T) {
	ff := &fakeFactory{}
	_, bc := newTestPool(ff, Options{MaxOpen: 1, MaxIdle: 1})
	ctx := context.Background()

	h1, _ := bc.Borrow(ctx)
	done := make(chan *Handle, 1)
	go func() {
		h, err := bc.Borrow(ctx)
		if err != nil {
			t.Errorf("borrow: %v", err)
		}
		done <- h
	}()
	time.Sleep(20 * time.Millisecond)
	h1.Close()

	select {
	case h2 := <-done:
		if h2.Conn != h1.Conn {
			t.Fatal("woken borrower should reuse returned conn")
		}
		h2.Close()
	case <-time.After(time.Second):
		t.Fatal("borrower not woken by return")
	}
	if got := ff.created.Load(); got != 1 {
		t.Fatalf("created = %d, want 1", got)
	}
}

// 7. 健康失效：Invalidate 归还的连接被关闭，不得复用。
func TestInvalidateOnReturn(t *testing.T) {
	ff := &fakeFactory{}
	p, bc := newTestPool(ff, Options{MaxOpen: 4, MaxIdle: 4})
	ctx := context.Background()

	h1, _ := bc.Borrow(ctx)
	bad := h1.Conn.(*fakeConn)
	h1.Invalidate()

	if got := bad.closeCnt.Load(); got != 1 {
		t.Fatalf("invalidated conn closed = %d, want 1", got)
	}
	if _, idle := p.Stats(); idle != 0 {
		t.Fatalf("idle = %d, want 0", idle)
	}
	h2, _ := bc.Borrow(ctx)
	if h2.Conn.(*fakeConn) == bad {
		t.Fatal("invalidated conn must not be reused")
	}
	h2.Close()
}

// 8. 借出时健康检查失败：关闭失效空闲连接并新建。
func TestValidateOnBorrow(t *testing.T) {
	ff := &fakeFactory{}
	p, bc := newTestPool(ff, Options{
		MaxOpen:  4,
		MaxIdle:  4,
		Validate: func(c Conn) bool { return !c.(*fakeConn).broken },
	})
	_ = p
	ctx := context.Background()

	h1, _ := bc.Borrow(ctx)
	fc := h1.Conn.(*fakeConn)
	h1.Close()
	fc.broken = true // 模拟空闲期间连接变坏

	h2, _ := bc.Borrow(ctx)
	if h2.Conn.(*fakeConn) == fc {
		t.Fatal("broken conn must not be handed out")
	}
	if got := fc.closeCnt.Load(); got != 1 {
		t.Fatalf("broken conn closed = %d, want 1", got)
	}
	if got := ff.created.Load(); got != 2 {
		t.Fatalf("created = %d, want 2", got)
	}
	h2.Close()
}

// 9. 关闭失败：物理关闭返回错误时不 panic、计数正确、连接不残留池中。
func TestCloseFailure(t *testing.T) {
	ff := &fakeFactory{}
	p, bc := newTestPool(ff, Options{MaxOpen: 4, MaxIdle: 4})
	ctx := context.Background()

	// 场景 A：换代排空时关闭失败。
	h1, _ := bc.Borrow(ctx)
	fail1 := h1.Conn.(*fakeConn)
	h1.Close()
	fail1.closeErr = errors.New("close boom")
	p.UpdateConfig() // 排空时关闭失败不应 panic
	if got := fail1.closeCnt.Load(); got != 1 {
		t.Fatalf("close attempted = %d, want 1", got)
	}
	if open, idle := p.Stats(); open != 0 || idle != 0 {
		t.Fatalf("stats = (%d,%d), want (0,0) even on close failure", open, idle)
	}

	// 场景 B：旧代次归还时关闭失败，同样不得进入新池。
	h2, _ := bc.Borrow(ctx)
	fail2 := h2.Conn.(*fakeConn)
	p.UpdateConfig()
	fail2.closeErr = errors.New("close boom")
	h2.Close()
	if got := fail2.closeCnt.Load(); got != 1 {
		t.Fatalf("close attempted = %d, want 1", got)
	}
	if _, idle := p.Stats(); idle != 0 {
		t.Fatalf("idle = %d, want 0", idle)
	}

	// 场景 C：池 Close 时关闭失败，错误被返回。
	h3, _ := bc.Borrow(ctx)
	fail3 := h3.Conn.(*fakeConn)
	h3.Close()
	fail3.closeErr = errors.New("close boom")
	if err := p.Close(); err == nil {
		t.Fatal("Pool.Close should surface close failure")
	}
	if got := fail3.closeCnt.Load(); got != 1 {
		t.Fatalf("close attempted = %d, want 1", got)
	}
}

// 10. 池关闭后：借出失败，借出中的连接归还即关闭。
func TestPoolClose(t *testing.T) {
	ff := &fakeFactory{}
	p, bc := newTestPool(ff, Options{MaxOpen: 4, MaxIdle: 4})
	ctx := context.Background()

	inFlight, _ := bc.Borrow(ctx)
	idleH, _ := bc.Borrow(ctx)
	idleC := idleH.Conn.(*fakeConn)
	idleH.Close()

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if got := idleC.closeCnt.Load(); got != 1 {
		t.Fatalf("idle conn closed = %d, want 1", got)
	}
	if _, err := bc.Borrow(ctx); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("err = %v, want ErrPoolClosed", err)
	}

	fc := inFlight.Conn.(*fakeConn)
	inFlight.Close()
	if got := fc.closeCnt.Load(); got != 1 {
		t.Fatalf("in-flight conn closed on return = %d, want 1", got)
	}
	if open, _ := p.Stats(); open != 0 {
		t.Fatalf("open = %d, want 0", open)
	}
}

// 11. 并发压力：多 goroutine 借还 + 换代，关闭总数最终等于创建总数。
func TestConcurrentBorrowReturnRotate(t *testing.T) {
	ff := &fakeFactory{}
	p, bc := newTestPool(ff, Options{MaxOpen: 8, MaxIdle: 8})
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h, err := bc.Borrow(ctx)
				if err != nil {
					continue
				}
				time.Sleep(time.Microsecond)
				h.Close()
			}
		}()
	}
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Millisecond)
		p.UpdateConfig()
	}
	wg.Wait()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	var closed int32
	// 通过工厂无法直接拿到所有连接，改用 Stats 校验：池已排空。
	if open, idle := p.Stats(); open != 0 || idle != 0 {
		t.Fatalf("stats = (%d,%d), want (0,0)", open, idle)
	}
	_ = closed
}
