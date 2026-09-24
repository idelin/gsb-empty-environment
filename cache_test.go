package cache

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
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

func newNode(src *MapSource, l2 *L2, n *Notifier, extra ...func(*Options)) *Cache {
	opts := Options{Source: src, L2: l2, Notifier: n}
	for _, f := range extra {
		f(&opts)
	}
	return New(opts)
}

// A strong read must not serve an L1 entry that the version watermark
// proves to be stale; it revalidates via L2.
func TestStrongReadValidatesStaleL1(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	nodeA := newNode(src, l2, n)
	nodeB := newNode(src, l2, n)
	defer nodeA.Close()
	defer nodeB.Close()
	ctx := context.Background()

	if _, err := nodeA.Write(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if r, err := nodeA.Get(ctx, "k", Strong); err != nil || r.Value != "v1" {
		t.Fatalf("initial strong read: %+v, %v", r, err)
	}
	if _, err := nodeB.Write(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "nodeA watermark to reach 2", func() bool { return nodeA.Watermark("k") == 2 })

	r, err := nodeA.Get(ctx, "k", Strong)
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != "v2" || r.Version != 2 || r.Stale || r.Degraded {
		t.Fatalf("strong read served unvalidated data: %+v", r)
	}
	if r.From == "L1" {
		t.Fatalf("stale L1 entry was not revalidated: %+v", r)
	}
}

// An eventual read never waits for a delayed notification: it answers from
// L1 immediately, accepting that the value may already be superseded.
func TestEventualReadDoesNotWaitForNotification(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	nodeA := newNode(src, l2, n)
	nodeB := newNode(src, l2, n)
	defer nodeA.Close()
	defer nodeB.Close()
	ctx := context.Background()

	if _, err := nodeA.Write(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeA.Get(ctx, "k", Eventual); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	n.Delay = func() { <-release }
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = nodeB.Write(ctx, "k", "v2")
	}()
	time.Sleep(50 * time.Millisecond) // v2 in source+L2, notification in flight

	start := time.Now()
	r, err := nodeA.Get(ctx, "k", Eventual)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("eventual read waited %v for the notification", elapsed)
	}
	if r.Value != "v1" || r.From != "L1" {
		t.Fatalf("eventual read should answer from L1 without waiting: %+v", r)
	}
	close(release)
	<-writeDone
	n.Delay = nil
}

// Once the watermark proves L1 stale, an eventual read revalidates and
// converges to the new value.
func TestEventualReadConvergesAfterInvalidation(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	nodeA := newNode(src, l2, n)
	nodeB := newNode(src, l2, n)
	defer nodeA.Close()
	defer nodeB.Close()
	ctx := context.Background()

	if _, err := nodeA.Write(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeA.Get(ctx, "k", Eventual); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeB.Write(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "nodeA watermark to reach 2", func() bool { return nodeA.Watermark("k") == 2 })
	r, err := nodeA.Get(ctx, "k", Eventual)
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != "v2" || r.Stale {
		t.Fatalf("eventual read did not converge: %+v", r)
	}
}

// When the L2 update notification is delayed, a strong read waits (bounded
// by ctx) for the watermark; an eventual read returns per its declaration.
func TestStrongReadWaitsForDelayedNotification(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	nodeA := newNode(src, l2, n)
	nodeB := newNode(src, l2, n)
	defer nodeA.Close()
	defer nodeB.Close()
	ctx := context.Background()

	if _, err := nodeA.Write(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := nodeA.Get(ctx, "k", Strong); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	n.Delay = func() { <-release }
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = nodeB.Write(ctx, "k", "v2")
	}()
	time.Sleep(50 * time.Millisecond) // L2 already holds v2; notification in flight

	// Strong read with a short timeout must fail rather than serve v1.
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err := nodeA.Get(shortCtx, "k", Strong)
	cancel()
	if err == nil || !strings.Contains(err.Error(), "not observed") {
		t.Fatalf("strong read should wait then time out, got: %v", err)
	}

	// Strong read with a generous timeout succeeds once the notification lands.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	waitCtx, cancel2 := context.WithTimeout(ctx, 2*time.Second)
	r, err := nodeA.Get(waitCtx, "k", Strong)
	cancel2()
	if err != nil {
		t.Fatal(err)
	}
	if r.Value != "v2" || r.Stale || r.Degraded {
		t.Fatalf("strong read after notification: %+v", r)
	}
	<-writeDone
	n.Delay = nil
}

// Duplicate invalidation deliveries are idempotent no-ops.
func TestDuplicateInvalidationIdempotent(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	c := newNode(src, l2, n)
	defer c.Close()

	msg := Message{Src: "peer", Key: "k", Version: 3, Seq: 7}
	for i := 0; i < 3; i++ {
		if err := c.ApplyInvalidation(msg); err != nil {
			t.Fatal(err)
		}
	}
	s := c.Stats()
	if s.AppliedInvalidations != 1 || s.DuplicateDropped != 2 {
		t.Fatalf("duplicates not idempotent: %+v", s)
	}
	if c.Watermark("k") != 3 {
		t.Fatalf("watermark = %d, want 3", c.Watermark("k"))
	}
}

// Duplicates are deduped per publisher: the same Seq from a different
// publisher is a different message.
func TestDedupIsScopedPerPublisher(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	c := newNode(src, l2, n)
	defer c.Close()

	if err := c.ApplyInvalidation(Message{Src: "a", Key: "k", Version: 1, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyInvalidation(Message{Src: "b", Key: "k", Version: 2, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	if s := c.Stats(); s.AppliedInvalidations != 2 || s.DuplicateDropped != 0 {
		t.Fatalf("per-publisher dedup wrong: %+v", s)
	}
}

// Version rollback is rejected on the invalidation path and on reads.
func TestVersionRollbackRejected(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	c := newNode(src, l2, n)
	defer c.Close()
	ctx := context.Background()

	if _, err := c.Write(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := c.ApplyInvalidation(Message{Src: "peer", Key: "k", Version: 5, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	err := c.ApplyInvalidation(Message{Src: "peer", Key: "k", Version: 4, Seq: 2})
	if !errors.Is(err, ErrVersionRollback) {
		t.Fatalf("rollback invalidation: got %v", err)
	}
	if c.Watermark("k") != 5 {
		t.Fatalf("watermark moved backwards: %d", c.Watermark("k"))
	}
	// Source still holds version 1, far below the watermark: reads reject it.
	if _, err := c.Get(ctx, "k", Strong); !errors.Is(err, ErrVersionRollback) {
		t.Fatalf("rollback from source: got %v", err)
	}
	if s := c.Stats(); s.RollbacksRejected != 1 {
		t.Fatalf("rollback counter: %+v", s)
	}
}

// L2 failure follows explicit degradation rules and never fakes strong
// consistency.
func TestL2FailureDegradation(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	var events []string
	var mu sync.Mutex
	c := newNode(src, l2, n, func(o *Options) {
		o.OnEvent = func(msg string) { mu.Lock(); events = append(events, msg); mu.Unlock() }
	})
	defer c.Close()
	ctx := context.Background()

	if _, err := c.Write(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "k", Eventual); err != nil { // populate L1
		t.Fatal(err)
	}
	// Another writer bumps the source to v2; the notification is lost, so
	// the watermark stays at 1 and L1 still looks valid.
	if _, err := src.Store(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	l2.GetErr = func(string) error { return errors.New("L2 connection refused") }

	// Rule 1: source answers -> degraded read served from the source.
	r, err := c.Get(ctx, "k", Strong)
	if err != nil || !r.Degraded || r.From != "source" || r.Stale {
		t.Fatalf("rule 1: %+v, %v", r, err)
	}
	if r.Value != "v2" {
		t.Fatalf("rule 1: degraded read must come from the source, got %+v", r)
	}

	// Source also fails.
	src.LoadErr = func(string) error { return errors.New("source timeout") }

	// Rule 2: strong reads fail honestly.
	if _, err := c.Get(ctx, "k", Strong); !errors.Is(err, ErrL2Unavailable) {
		t.Fatalf("rule 2: strong read must fail with ErrL2Unavailable, got %v", err)
	}
	// Rule 3: eventual reads may serve declared-stale L1 data.
	// Watermark moves past the L1 copy (v2), so L1 is known-stale.
	if err := c.ApplyInvalidation(Message{Src: "peer", Key: "k", Version: 3, Seq: 1}); err != nil {
		t.Fatal(err)
	}
	r, err = c.Get(ctx, "k", Eventual)
	if err != nil || !r.Stale || !r.Degraded || r.Value != "v2" {
		t.Fatalf("rule 3: %+v, %v", r, err)
	}
	// No L1 entry at all: eventual reads fail too.
	if _, err := c.Get(ctx, "missing", Eventual); !errors.Is(err, ErrL2Unavailable) {
		t.Fatalf("rule 3 (miss): got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("degradation events were not reported via OnEvent")
	}
}

// The injected clock controls how long a validated L1 entry serves strong
// reads without touching L2.
func TestStrongTTLWithInjectedClock(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	var mu sync.Mutex
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	c := newNode(src, l2, n, func(o *Options) { o.Clock = clock; o.StrongTTL = time.Minute })
	defer c.Close()
	ctx := context.Background()

	if _, err := c.Write(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "watermark 1", func() bool { return c.Watermark("k") == 1 })

	advance(30 * time.Second) // within TTL
	r, err := c.Get(ctx, "k", Strong)
	if err != nil || r.From != "L1" {
		t.Fatalf("within TTL: %+v, %v", r, err)
	}
	if got := l2.Gets(); got != 0 {
		t.Fatalf("L2 hit %d times within TTL, want 0", got)
	}

	advance(2 * time.Minute) // TTL expired
	r, err = c.Get(ctx, "k", Strong)
	if err != nil || r.From != "L2" {
		t.Fatalf("after TTL: %+v, %v", r, err)
	}
	if got := l2.Gets(); got != 1 {
		t.Fatalf("L2 hit %d times after TTL, want 1", got)
	}
}

// Invalidations delivered through the bus are applied by the background
// loop, idempotently.
func TestInvalidationLoopViaBus(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	c := newNode(src, l2, n)
	defer c.Close()

	msg := Message{Src: "peer", Key: "k", Version: 9, Seq: 1}
	if err := n.Publish(msg); err != nil {
		t.Fatal(err)
	}
	if err := n.Publish(msg); err != nil { // duplicate delivery
		t.Fatal(err)
	}
	waitFor(t, "loop to apply one and drop one", func() bool {
		s := c.Stats()
		return s.AppliedInvalidations == 1 && s.DuplicateDropped == 1
	})
	if c.Watermark("k") != 9 {
		t.Fatalf("watermark = %d, want 9", c.Watermark("k"))
	}
}

// Write requires a writable source.
func TestWriteRejectedOnNonWritableSource(t *testing.T) {
	c := New(Options{Source: SourceFunc(func(context.Context, string) (string, uint64, error) {
		return "", 0, ErrMiss
	})})
	defer c.Close()
	if _, err := c.Write(context.Background(), "k", "v"); !errors.Is(err, ErrSourceNotWritable) {
		t.Fatalf("got %v", err)
	}
}

// Close releases the subscription and stops the loop; later ops fail.
func TestCloseReleasesResources(t *testing.T) {
	src, l2, n := NewMapSource(), NewL2(), NewNotifier()
	c := newNode(src, l2, n)
	if got := n.SubscriberCount(); got != 1 {
		t.Fatalf("subscribers before close = %d, want 1", got)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if got := n.SubscriberCount(); got != 0 {
		t.Fatalf("subscribers after close = %d, want 0", got)
	}
	if _, err := c.Get(context.Background(), "k", Eventual); !errors.Is(err, ErrClosed) {
		t.Fatalf("get after close: got %v", err)
	}
	if _, err := c.Write(context.Background(), "k", "v"); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: got %v", err)
	}
	if err := c.Close(); err != nil { // idempotent
		t.Fatalf("second close: %v", err)
	}
}
