package pool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeRaw is a test connection counting closes and toggling health.
type fakeRaw struct {
	id       int
	healthy  int32
	closes   int32
	closeErr error
}

func (f *fakeRaw) Healthy() bool { return atomic.LoadInt32(&f.healthy) == 1 }

func (f *fakeRaw) Close() error {
	atomic.AddInt32(&f.closes, 1)
	return f.closeErr
}

func (f *fakeRaw) setHealthy(ok bool) {
	if ok {
		atomic.StoreInt32(&f.healthy, 1)
	} else {
		atomic.StoreInt32(&f.healthy, 0)
	}
}

// fakeFactory counts created connections and records every fakeRaw.
type fakeFactory struct {
	mu       sync.Mutex
	created  int
	conns    []*fakeRaw
	closeErr error
}

func (f *fakeFactory) make(ctx context.Context) (RawConn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	c := &fakeRaw{id: f.created, healthy: 1, closeErr: f.closeErr}
	f.conns = append(f.conns, c)
	return c, nil
}

func (f *fakeFactory) numCreated() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

func (f *fakeFactory) conn(i int) *fakeRaw {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns[i]
}

// borrowCounter counts successful borrows.
type borrowCounter struct{ n int32 }

func (b *borrowCounter) get(t *testing.T, p *Pool) *Conn {
	t.Helper()
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	atomic.AddInt32(&b.n, 1)
	return c
}

func (b *borrowCounter) count() int32 { return atomic.LoadInt32(&b.n) }

func closesOf(c *Conn) int32 { return atomic.LoadInt32(&c.raw.(*fakeRaw).closes) }

func TestReuseIdleConnection(t *testing.T) {
	f := &fakeFactory{}
	p := New(f.make, Config{MaxOpen: 2})
	defer p.Close()
	var bc borrowCounter

	c1 := bc.get(t, p)
	if err := c1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c2 := bc.get(t, p)
	if c2.Raw() != c1.Raw() {
		t.Fatalf("expected idle connection to be reused")
	}
	if got := f.numCreated(); got != 1 {
		t.Fatalf("created = %d, want 1", got)
	}
	if got := bc.count(); got != 2 {
		t.Fatalf("borrows = %d, want 2", got)
	}
	if got := closesOf(c1); got != 0 {
		t.Fatalf("closes = %d, want 0 while pooled", got)
	}
}

func TestRotateDrainsIdleAndRejectsOldReturns(t *testing.T) {
	f := &fakeFactory{}
	p := New(f.make, Config{MaxOpen: 4})
	var bc borrowCounter

	borrowed := bc.get(t, p) // stays borrowed across rotation
	idle := bc.get(t, p)
	idle.Close()

	p.Rotate(nil)

	if got := closesOf(idle); got != 1 {
		t.Fatalf("idle old-gen closes = %d, want 1 after rotation", got)
	}
	if got := closesOf(borrowed); got != 0 {
		t.Fatalf("borrowed old-gen closes = %d, want 0 (still in flight)", got)
	}

	c := bc.get(t, p)
	if c.Raw() == borrowed.Raw() || c.Raw() == idle.Raw() {
		t.Fatalf("new generation must not hand out old connections")
	}
	if got := f.numCreated(); got != 3 {
		t.Fatalf("created = %d, want 3", got)
	}

	// Old-generation return must be closed, not repooled.
	if err := borrowed.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := closesOf(borrowed); got != 1 {
		t.Fatalf("borrowed old-gen closes = %d, want 1 on return", got)
	}

	c.Close()
	again := bc.get(t, p)
	if again.Raw() != c.Raw() {
		t.Fatalf("new-gen idle connection should be reused")
	}
	if got := f.numCreated(); got != 3 {
		t.Fatalf("created = %d, want 3 (old conn must not re-enter pool)", got)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("pool Close: %v", err)
	}
	if got := closesOf(borrowed); got != 1 {
		t.Fatalf("borrowed old-gen closes = %d, want exactly 1", got)
	}
}

func TestBorrowedConnClosedExactlyOnce(t *testing.T) {
	f := &fakeFactory{}
	p := New(f.make, Config{MaxOpen: 1})
	var bc borrowCounter

	c := bc.get(t, p)
	p.Rotate(nil) // drains idle only; c is borrowed
	if err := p.Close(); err != nil {
		t.Fatalf("pool Close: %v", err)
	}
	if got := closesOf(c); got != 0 {
		t.Fatalf("closes = %d, want 0 while borrowed", got)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("conn Close: %v", err)
	}
	c.Close() // idempotent
	if got := closesOf(c); got != 1 {
		t.Fatalf("closes = %d, want exactly 1", got)
	}
	if _, err := p.Get(context.Background()); err != ErrPoolClosed {
		t.Fatalf("Get after close = %v, want ErrPoolClosed", err)
	}
}

func TestUnhealthyConnInvalidated(t *testing.T) {
	f := &fakeFactory{}
	p := New(f.make, Config{MaxOpen: 2})
	defer p.Close()
	var bc borrowCounter

	// Unhealthy on borrow: discarded, replaced by a fresh connection.
	c1 := bc.get(t, p)
	c1.Close()
	c1.Raw().(*fakeRaw).setHealthy(false)
	c2 := bc.get(t, p)
	if c2.Raw() == c1.Raw() {
		t.Fatalf("unhealthy idle connection must not be handed out")
	}
	if got := closesOf(c1); got != 1 {
		t.Fatalf("closes = %d, want 1 for invalidated conn", got)
	}

	// Unhealthy on return: closed immediately instead of pooled.
	c2.Raw().(*fakeRaw).setHealthy(false)
	c2.Close()
	if got := closesOf(c2); got != 1 {
		t.Fatalf("closes = %d, want 1 for unhealthy return", got)
	}
	c3 := bc.get(t, p)
	if c3.Raw() == c2.Raw() {
		t.Fatalf("unhealthy returned connection must not be repooled")
	}
	if got := f.numCreated(); got != 3 {
		t.Fatalf("created = %d, want 3", got)
	}
}

func TestGetTimeoutWhenExhausted(t *testing.T) {
	f := &fakeFactory{}
	p := New(f.make, Config{MaxOpen: 1})
	defer p.Close()
	var bc borrowCounter

	held := bc.get(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Get(ctx); err != context.DeadlineExceeded {
		t.Fatalf("Get err = %v, want DeadlineExceeded", err)
	}
	if got := f.numCreated(); got != 1 {
		t.Fatalf("created = %d, want 1", got)
	}

	// Returning the connection unblocks a waiter.
	done := make(chan *Conn, 1)
	go func() {
		c, err := p.Get(context.Background())
		if err != nil {
			t.Errorf("Get: %v", err)
		}
		done <- c
	}()
	time.Sleep(20 * time.Millisecond)
	held.Close()
	select {
	case c := <-done:
		if c.Raw() != held.Raw() {
			t.Fatalf("waiter should receive the returned connection")
		}
		c.Close()
	case <-time.After(time.Second):
		t.Fatalf("waiter still blocked after return")
	}
}

func TestCloseFailureStillFreesSlot(t *testing.T) {
	closeErr := errors.New("boom")
	f := &fakeFactory{closeErr: closeErr}
	p := New(f.make, Config{MaxOpen: 1})
	defer p.Close()
	var bc borrowCounter

	c := bc.get(t, p)
	c.Raw().(*fakeRaw).setHealthy(false)
	if err := c.Close(); err != closeErr {
		t.Fatalf("Close err = %v, want %v", err, closeErr)
	}
	if got := closesOf(c); got != 1 {
		t.Fatalf("closes = %d, want 1", got)
	}
	// Slot must be released despite the close failure.
	c2 := bc.get(t, p)
	if c2.Raw() == c.Raw() {
		t.Fatalf("failed-close connection must not be repooled")
	}
	if got := f.numCreated(); got != 2 {
		t.Fatalf("created = %d, want 2", got)
	}
	c2.Close()

	// Rotation also tolerates close failures on drained idle conns.
	p.Rotate(nil)
	if got := closesOf(c2); got != 1 {
		t.Fatalf("drained closes = %d, want 1", got)
	}
	c3 := bc.get(t, p)
	if got := f.numCreated(); got != 3 {
		t.Fatalf("created = %d, want 3", got)
	}
	c3.Close()
}

func TestFactoryErrorReleasesSlot(t *testing.T) {
	f := &fakeFactory{}
	fail := int32(1)
	factory := func(ctx context.Context) (RawConn, error) {
		if atomic.CompareAndSwapInt32(&fail, 1, 0) {
			return nil, errors.New("dial failed")
		}
		return f.make(ctx)
	}
	p := New(factory, Config{MaxOpen: 1})
	defer p.Close()

	if _, err := p.Get(context.Background()); err == nil {
		t.Fatalf("expected factory error")
	}
	c, err := p.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after factory error: %v", err)
	}
	c.Close()
}
