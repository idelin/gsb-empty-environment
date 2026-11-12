package netsec

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Canceled waits must not leave watcher goroutines behind, and the pool's
// reclaimer must terminate on Close.
func TestNoGoroutineLeak(t *testing.T) {
	z := NewPausableZeroer()
	pool, err := NewPool(32, 2, z)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}

	z.Pause()
	snapPg := make([]*PhysPage, 2)
	for i := range snapPg {
		pg, err := pool.TryAlloc()
		if err != nil {
			t.Fatalf("TryAlloc: %v", err)
		}
		snapPg[i] = pg
	}

	// Every waiter times out against a fully committed, paused pool.
	for i := 0; i < 10; i++ {
		allocCtx, allocCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		if _, err := pool.Alloc(allocCtx); err != context.DeadlineExceeded {
			t.Fatalf("Alloc = %v, want DeadlineExceeded", err)
		}
		allocCancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		if err := pool.WaitAvailable(waitCtx); err != context.DeadlineExceeded {
			t.Fatalf("WaitAvailable = %v, want DeadlineExceeded", err)
		}
		waitCancel()
	}

	for _, pg := range snapPg {
		pool.Release(pg)
	}
	z.Resume()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.WaitDrained(ctx); err != nil {
		t.Fatalf("WaitDrained: %v", err)
	}
	pool.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		if !strings.Contains(string(buf[:n]), "netsec/q0257.(*Pool)") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	t.Fatalf("pool goroutines still alive after Close:\n%s", buf[:n])
}
