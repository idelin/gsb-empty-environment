package netsec

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func okParams() Params {
	return Params{SCost: 1024, TCost: 1, Delta: 3}
}

func noopHash(buf []byte) error { return nil }

func TestDeltaTooSmall(t *testing.T) {
	l := NewLimiter(MaxRequestBytes)
	p := okParams()
	p.Delta = 2

	err := l.Hash(context.Background(), "tenant-a", p, noopHash)
	if !errors.Is(err, ErrDeltaTooSmall) {
		t.Fatalf("delta=2: want ErrDeltaTooSmall, got %v", err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("rejected request must not reserve quota, used=%d", got)
	}

	p.Delta = 1
	if err := p.Validate(); !errors.Is(err, ErrDeltaTooSmall) {
		t.Fatalf("delta=1: want ErrDeltaTooSmall, got %v", err)
	}
}

func TestSingleRequestMemoryLimit(t *testing.T) {
	l := NewLimiter(1 << 30)

	atLimit := Params{SCost: MaxRequestBytes / BlockSize, TCost: 1, Delta: 3}
	if err := l.Hash(context.Background(), "tenant-a", atLimit, noopHash); err != nil {
		t.Fatalf("exactly 64 MiB should pass, got %v", err)
	}

	over := Params{SCost: MaxRequestBytes/BlockSize + 1, TCost: 1, Delta: 3}
	err := l.Hash(context.Background(), "tenant-a", over, noopHash)
	if !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("64 MiB + 1 block: want ErrMemoryLimit, got %v", err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("rejected request must not reserve quota, used=%d", got)
	}
}

func TestTwoTenantsCompeteForQuota(t *testing.T) {
	unit := int64(1024 * BlockSize)
	l := NewLimiter(3 * unit) // room for three 1024-block requests total

	p := okParams() // 1024 blocks = one unit
	started := make(chan struct{}, 2)
	finish := make(chan struct{})
	blocking := func(buf []byte) error {
		started <- struct{}{}
		<-finish
		return nil
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = l.Hash(context.Background(), "tenant-a", p, blocking) }()
	go func() { defer wg.Done(); _ = l.Hash(context.Background(), "tenant-a", p, blocking) }()
	<-started
	<-started

	// Two units held by tenant-a; tenant-b can take one but not two more.
	if err := l.Hash(context.Background(), "tenant-b", p, noopHash); err != nil {
		t.Fatalf("tenant-b first request should fit, got %v", err)
	}
	res, err := l.Reserve("tenant-b", unit)
	if err != nil {
		t.Fatalf("tenant-b direct reserve should fit, got %v", err)
	}
	if err := l.Hash(context.Background(), "tenant-b", p, noopHash); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("tenant-b over quota: want ErrQuotaExhausted, got %v", err)
	}
	res.Release()

	close(finish)
	wg.Wait()
	if got := l.Used(); got != 0 {
		t.Fatalf("all reservations released, used=%d", got)
	}
	if got := l.TenantUsed("tenant-a"); got != 0 {
		t.Fatalf("tenant-a usage should be 0, got %d", got)
	}
}

func TestPanicReleasesReservation(t *testing.T) {
	l := NewLimiter(MaxRequestBytes)
	p := okParams()

	err := l.Hash(context.Background(), "tenant-a", p, func(buf []byte) error {
		if got := l.Used(); got != p.MemoryBytes() {
			t.Errorf("during hashing used=%d, want %d", got, p.MemoryBytes())
		}
		panic("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want panic-wrapping error, got %v", err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("panic must release reservation, used=%d", got)
	}
}

func TestBufferLengthMismatch(t *testing.T) {
	l := NewLimiter(MaxRequestBytes)
	l.alloc = func(n int) []byte { return make([]byte, n-BlockSize) } // one block short

	called := false
	err := l.Hash(context.Background(), "tenant-a", okParams(), func(buf []byte) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrBufferLength) {
		t.Fatalf("want ErrBufferLength, got %v", err)
	}
	if called {
		t.Fatal("hash callback must not run with a mis-sized buffer")
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("mis-sized buffer must release reservation, used=%d", got)
	}
}

func TestTimeoutReleasesReservation(t *testing.T) {
	l := NewLimiter(MaxRequestBytes)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // let the deadline pass

	err := l.Hash(ctx, "tenant-a", okParams(), noopHash)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("timeout must release reservation, used=%d", got)
	}
}

func TestHappyPathBufferSize(t *testing.T) {
	l := NewLimiter(MaxRequestBytes)
	p := okParams()
	err := l.Hash(context.Background(), "tenant-a", p, func(buf []byte) error {
		if want := p.SCost * BlockSize; len(buf) != want {
			t.Errorf("callback buffer len=%d, want %d", len(buf), want)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("happy path failed: %v", err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("completed hash must release reservation, used=%d", got)
	}
}
