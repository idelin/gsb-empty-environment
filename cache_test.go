package llcache

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func staticLoader(v Value) Loader {
	return func(context.Context, string) (Value, error) { return v, nil }
}

// A stale L1 entry is validated against invalidation notifications:
// once the notification for a newer version is applied, the next
// eventual read bypasses L1 and revalidates against L2.
func TestEventualReadValidatesInvalidation(t *testing.T) {
	l2 := NewL2()
	defer l2.Close()
	node := NewNode(l2, staticLoader(Value{Data: "v1", Version: 1}))
	defer node.Close()

	r, err := node.Get(context.Background(), "k", Eventual)
	if err != nil || r.Source != "loader" || r.Value.Version != 1 {
		t.Fatalf("prime: %+v err=%v", r, err)
	}
	if r, _ = node.Get(context.Background(), "k", Eventual); r.Source != "l1" {
		t.Fatalf("expected l1 hit, got %+v", r)
	}

	if err := l2.Update("k", Value{Data: "v2", Version: 2}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "invalidation applied", func() bool {
		return node.AppliedInvalidations() == 1
	})

	r, err = node.Get(context.Background(), "k", Eventual)
	if err != nil || r.Value.Data != "v2" || r.Source != "l2" {
		t.Fatalf("stale L1 not invalidated: %+v err=%v", r, err)
	}
}

// While the L2 update notification is delayed, an eventual read
// returns the previous version (declared behaviour), while a strong
// read revalidates and sees the new version immediately.
func TestNotifyDelayEventualReturnsOldStrongReturnsNew(t *testing.T) {
	l2 := NewL2(WithNotifyDelay(150 * time.Millisecond))
	defer l2.Close()
	node := NewNode(l2, staticLoader(Value{Data: "v1", Version: 1}))
	defer node.Close()

	if _, err := node.Get(context.Background(), "k", Eventual); err != nil {
		t.Fatal(err)
	}
	if err := l2.Update("k", Value{Data: "v2", Version: 2}); err != nil {
		t.Fatal(err)
	}

	r, err := node.Get(context.Background(), "k", Eventual)
	if err != nil || r.Value.Data != "v1" || r.Source != "l1" {
		t.Fatalf("eventual read during delay should serve declared stale value, got %+v err=%v", r, err)
	}
	r, err = node.Get(context.Background(), "k", Strong)
	if err != nil || r.Value.Data != "v2" || r.Source != "l2" {
		t.Fatalf("strong read must bypass notification delay, got %+v err=%v", r, err)
	}
}

// The alternative declared behaviour for a delayed notification:
// wait it out, then eventual reads observe the new version.
func TestNotifyDelayWaitedOut(t *testing.T) {
	l2 := NewL2(WithNotifyDelay(50 * time.Millisecond))
	defer l2.Close()
	node := NewNode(l2, staticLoader(Value{Data: "v1", Version: 1}))
	defer node.Close()

	if _, err := node.Get(context.Background(), "k", Eventual); err != nil {
		t.Fatal(err)
	}
	if err := l2.Update("k", Value{Data: "v2", Version: 2}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "delayed invalidation", func() bool {
		return node.AppliedInvalidations() == 1
	})
	r, err := node.Get(context.Background(), "k", Eventual)
	if err != nil || r.Value.Data != "v2" {
		t.Fatalf("after delay eventual read must converge, got %+v err=%v", r, err)
	}
}

// L2 failure degradation rules: eventual reads fall back to the
// loader, and if that also fails serve the L1 entry explicitly
// flagged Stale+Degraded. Strong reads fail with ErrUnavailable and
// never fake consistency.
func TestL2FailureDegradation(t *testing.T) {
	down := &atomic.Bool{}
	fail := func(op string) error {
		if down.Load() {
			return errors.New("injected L2 outage: " + op)
		}
		return nil
	}
	l2 := NewL2(WithL2FailHook(fail))
	defer l2.Close()

	loaderErr := errors.New("source unreachable")
	loaderCalls := &atomic.Int32{}
	loader := func(context.Context, string) (Value, error) {
		loaderCalls.Add(1)
		return Value{}, loaderErr
	}
	node := NewNode(l2, func(ctx context.Context, k string) (Value, error) {
		if down.Load() {
			return loader(ctx, k)
		}
		return Value{Data: "v1", Version: 1}, nil
	})
	defer node.Close()

	if _, err := node.Get(context.Background(), "k", Eventual); err != nil {
		t.Fatal(err)
	}
	// Commit v2 while L2 is healthy so the node's invalidation
	// watermark marks the cached v1 as superseded.
	if err := l2.Update("k", Value{Data: "v2", Version: 2}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "invalidation applied", func() bool {
		return node.AppliedInvalidations() == 1
	})
	down.Store(true)

	r, err := node.Get(context.Background(), "k", Eventual)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stale || !r.Degraded || r.Value.Data != "v1" {
		t.Fatalf("degraded read must be flagged, got %+v", r)
	}
	if _, err = node.Get(context.Background(), "k", Strong); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("strong read during outage must fail, got %v", err)
	}
}

// With L2 down but the loader healthy, eventual reads degrade to the
// data source and say so.
func TestL2FailureFallsBackToLoader(t *testing.T) {
	l2 := NewL2(WithL2FailHook(func(op string) error {
		return errors.New("injected L2 outage: " + op)
	}))
	defer l2.Close()
	node := NewNode(l2, staticLoader(Value{Data: "from-source", Version: 7}))
	defer node.Close()

	r, err := node.Get(context.Background(), "k", Eventual)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != "loader" || !r.Degraded || r.Value.Data != "from-source" {
		t.Fatalf("expected degraded loader fallback, got %+v", r)
	}
}

// Duplicate invalidation messages are absorbed idempotently: state
// changes exactly once.
func TestInvalidationIdempotent(t *testing.T) {
	l2 := NewL2()
	defer l2.Close()
	node := NewNode(l2, staticLoader(Value{Data: "v1", Version: 1}))
	defer node.Close()

	if _, err := node.Get(context.Background(), "k", Eventual); err != nil {
		t.Fatal(err)
	}
	msg := Invalidation{Key: "k", Version: 1, At: time.Now()}
	for i := 0; i < 3; i++ {
		node.applyInvalidation(msg)
	}
	if got := node.AppliedInvalidations(); got != 1 {
		t.Fatalf("duplicate invalidations must apply once, applied=%d", got)
	}
	// The invalidated entry must no longer be served as a fresh L1
	// hit; the read falls through to L2/loader instead.
	r, err := node.Get(context.Background(), "k", Eventual)
	if err != nil {
		t.Fatal(err)
	}
	if r.Source == "l1" {
		t.Fatalf("invalidated entry served as fresh: %+v", r)
	}
}

// Version rollback is rejected at L2: equal or lower versions are
// refused and the committed value is untouched.
func TestVersionRollbackRejected(t *testing.T) {
	l2 := NewL2()
	defer l2.Close()

	if err := l2.Update("k", Value{Data: "v5", Version: 5}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []uint64{4, 5} {
		if err := l2.Update("k", Value{Data: "rollback", Version: v}); !errors.Is(err, ErrVersionRollback) {
			t.Fatalf("version %d must be rejected, got %v", v, err)
		}
	}
	got, ok, err := l2.Get("k")
	if err != nil || !ok || got.Data != "v5" || got.Version != 5 {
		t.Fatalf("committed value changed after rollback attempt: %+v ok=%v err=%v", got, ok, err)
	}
	if wm, err := l2.Watermark(); err != nil || wm != 5 {
		t.Fatalf("watermark must stay monotonic, got %d err=%v", wm, err)
	}
}

// A failed L2 update is atomic: no version bump, no notification.
func TestUpdateFailureAtomic(t *testing.T) {
	fail := func(op string) error {
		if op == "l2.update" {
			return errors.New("injected update failure")
		}
		return nil
	}
	l2 := NewL2(WithL2FailHook(fail))
	defer l2.Close()
	node := NewNode(l2, staticLoader(Value{Data: "v1", Version: 1}))
	defer node.Close()

	if err := l2.Update("k", Value{Data: "v2", Version: 2}); err == nil {
		t.Fatal("expected injected failure")
	}
	if wm, err := l2.Watermark(); err != nil || wm != 0 {
		t.Fatalf("failed update must not move watermark, got %d", wm)
	}
	time.Sleep(20 * time.Millisecond)
	if got := node.AppliedInvalidations(); got != 0 {
		t.Fatalf("failed update must not notify, applied=%d", got)
	}
}

// Clock is injectable and stamps invalidations.
func TestClockInjectable(t *testing.T) {
	fixed := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	l2 := NewL2(WithL2Clock(func() time.Time { return fixed }))
	defer l2.Close()
	ch := l2.Subscribe(1)

	if err := l2.Update("k", Value{Data: "v1", Version: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-ch:
		if !msg.At.Equal(fixed) {
			t.Fatalf("invalidation not stamped by injected clock: %v", msg.At)
		}
	case <-time.After(time.Second):
		t.Fatal("no invalidation received")
	}
}

// Close releases every goroutine the cache created, including
// in-flight delayed deliveries.
func TestCloseReleasesGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()

	l2 := NewL2(WithNotifyDelay(30 * time.Millisecond))
	node := NewNode(l2, staticLoader(Value{Data: "v1", Version: 1}))
	if _, err := node.Get(context.Background(), "k", Eventual); err != nil {
		t.Fatal(err)
	}
	if err := l2.Update("k", Value{Data: "v2", Version: 2}); err != nil {
		t.Fatal(err)
	}
	node.Close()
	l2.Close()

	eventually(t, "goroutines released", func() bool {
		return runtime.NumGoroutine() <= before
	})
}
