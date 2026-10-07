package mq

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// flakyPersister fails Apply while armed, without persisting anything.
type flakyPersister struct {
	inner *MemPersister
	mu    sync.Mutex
	err   error
}

func newFlakyPersister() *flakyPersister { return &flakyPersister{inner: NewMemPersister()} }

func (f *flakyPersister) arm(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *flakyPersister) heal()         { f.arm(nil) }

func (f *flakyPersister) Apply(batch []Record) error {
	f.mu.Lock()
	err := f.err
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.inner.Apply(batch)
}

func (f *flakyPersister) Replay() ([]Record, error) { return f.inner.Replay() }

var errPersist = errors.New("disk on fire")

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newTestQueue(t *testing.T, p Persister, clock Clock, vis time.Duration) *Queue {
	t.Helper()
	q, err := Open(p, WithClock(clock), WithVisibilityTimeout(vis))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return q
}

func mustTake(t *testing.T, q *Queue) (Message, Receipt) {
	t.Helper()
	m, r, err := q.Take()
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	return m, r
}

func takeBody(t *testing.T, q *Queue) string {
	t.Helper()
	m, _ := mustTake(t, q)
	return string(m.Body)
}

func TestFIFOVisibleOrder(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	q := newTestQueue(t, NewMemPersister(), clock.Now, time.Minute)

	if _, err := q.Send([]byte("a"), []byte("b"), []byte("c")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for _, want := range []string{"a", "b", "c"} {
		if got := takeBody(t, q); got != want {
			t.Fatalf("order: got %q want %q", got, want)
		}
	}
	if _, _, err := q.Take(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("expected ErrEmpty, got %v", err)
	}
}

func TestClaimDoesNotDelete(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	p := NewMemPersister()
	q := newTestQueue(t, p, clock.Now, time.Minute)

	if _, err := q.Send([]byte("a")); err != nil {
		t.Fatal(err)
	}
	mustTake(t, q)
	if got := q.InFlight(); got != 1 {
		t.Fatalf("InFlight = %d, want 1", got)
	}
	// Restart from the same log: the un-acked message must survive.
	q2 := newTestQueue(t, p, clock.Now, time.Minute)
	clock.Advance(2 * time.Minute) // let the claim expire
	if got := takeBody(t, q2); got != "a" {
		t.Fatalf("after restart got %q, want %q", got, "a")
	}
}

func TestRejectRequeuesToTail(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	q := newTestQueue(t, NewMemPersister(), clock.Now, time.Minute)

	if _, err := q.Send([]byte("a"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	_, ra := mustTake(t, q) // claim a
	if err := q.Reject(ra); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if got := takeBody(t, q); got != "b" {
		t.Fatalf("got %q, want b first", got)
	}
	if got := takeBody(t, q); got != "a" {
		t.Fatalf("got %q, want a requeued at tail", got)
	}
}

func TestConfirmDeletesAndDuplicateConfirmFails(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	p := NewMemPersister()
	q := newTestQueue(t, p, clock.Now, time.Minute)

	if _, err := q.Send([]byte("a")); err != nil {
		t.Fatal(err)
	}
	_, r := mustTake(t, q)
	if err := q.Confirm(r); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if err := q.Confirm(r); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("duplicate confirm: got %v, want ErrStaleReceipt", err)
	}
	// The delete is durable: nothing comes back after restart + timeout.
	q2 := newTestQueue(t, p, clock.Now, time.Minute)
	clock.Advance(2 * time.Minute)
	if _, _, err := q2.Take(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("acked message resurrected: %v", err)
	}
}

func TestTimeoutRedeliveryInvalidatesOldReceipt(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	q := newTestQueue(t, NewMemPersister(), clock.Now, 10*time.Second)

	if _, err := q.Send([]byte("a")); err != nil {
		t.Fatal(err)
	}
	_, old := mustTake(t, q)
	clock.Advance(11 * time.Second) // visibility expires

	_, renewed := mustTake(t, q) // redelivered with a new generation
	if old == renewed {
		t.Fatal("redelivery must issue a fresh receipt")
	}
	if err := q.Confirm(old); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("old receipt confirm: got %v, want ErrStaleReceipt", err)
	}
	if got := q.InFlight(); got != 1 {
		t.Fatalf("stale confirm must not delete; InFlight = %d, want 1", got)
	}
	if err := q.Reject(old); !errors.Is(err, ErrStaleReceipt) {
		t.Fatalf("old receipt reject: got %v, want ErrStaleReceipt", err)
	}
	if err := q.Confirm(renewed); err != nil {
		t.Fatalf("confirm new receipt: %v", err)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("Len = %d after ack, want 0", got)
	}
}

func TestRedeliveryOrderOldestDeadlineFirst(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	q := newTestQueue(t, NewMemPersister(), clock.Now, 10*time.Second)

	if _, err := q.Send([]byte("a"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	mustTake(t, q) // a, deadline t+10
	clock.Advance(time.Second)
	mustTake(t, q) // b, deadline t+11
	clock.Advance(11 * time.Second)

	if got := takeBody(t, q); got != "a" {
		t.Fatalf("got %q, want a (older deadline) first", got)
	}
	if got := takeBody(t, q); got != "b" {
		t.Fatalf("got %q, want b second", got)
	}
}

func TestTransactionalSendAllOrNothing(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	p := newFlakyPersister()
	q := newTestQueue(t, p, clock.Now, time.Minute)

	p.arm(errPersist)
	if _, err := q.Send([]byte("a"), []byte("b"), []byte("c")); !errors.Is(err, errPersist) {
		t.Fatalf("Send: got %v, want wrapped errPersist", err)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("partial send visible: Len = %d, want 0", got)
	}
	// Nothing reached the log either: a restarted queue stays empty.
	q2 := newTestQueue(t, p, clock.Now, time.Minute)
	if got := q2.Len(); got != 0 {
		t.Fatalf("partial send persisted: Len = %d, want 0", got)
	}

	p.heal()
	if _, err := q.Send([]byte("a"), []byte("b"), []byte("c")); err != nil {
		t.Fatalf("Send after heal: %v", err)
	}
	for _, want := range []string{"a", "b", "c"} {
		if got := takeBody(t, q); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func TestTakePersistFailureKeepsMessageVisible(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	p := newFlakyPersister()
	q := newTestQueue(t, p, clock.Now, time.Minute)

	if _, err := q.Send([]byte("a")); err != nil {
		t.Fatal(err)
	}
	p.arm(errPersist)
	if _, _, err := q.Take(); !errors.Is(err, errPersist) {
		t.Fatalf("Take: got %v, want wrapped errPersist", err)
	}
	if got := q.Len(); got != 1 {
		t.Fatalf("Len = %d, want 1 (claim must not be lost on persist failure)", got)
	}
	p.heal()
	if got := takeBody(t, q); got != "a" {
		t.Fatalf("got %q, want a", got)
	}
}

func TestConfirmPersistFailureKeepsInFlight(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	p := newFlakyPersister()
	q := newTestQueue(t, p, clock.Now, time.Minute)

	if _, err := q.Send([]byte("a")); err != nil {
		t.Fatal(err)
	}
	_, r := mustTake(t, q)
	p.arm(errPersist)
	if err := q.Confirm(r); !errors.Is(err, errPersist) {
		t.Fatalf("Confirm: got %v, want wrapped errPersist", err)
	}
	if got := q.InFlight(); got != 1 {
		t.Fatalf("InFlight = %d, want 1 (failed ack must not delete)", got)
	}
	p.heal()
	if err := q.Confirm(r); err != nil {
		t.Fatalf("retry Confirm: %v", err)
	}
	if got := q.InFlight(); got != 0 {
		t.Fatalf("InFlight = %d after retry, want 0", got)
	}
}

func TestRejectPersistFailureKeepsInFlight(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	p := newFlakyPersister()
	q := newTestQueue(t, p, clock.Now, time.Minute)

	if _, err := q.Send([]byte("a")); err != nil {
		t.Fatal(err)
	}
	_, r := mustTake(t, q)
	p.arm(errPersist)
	if err := q.Reject(r); !errors.Is(err, errPersist) {
		t.Fatalf("Reject: got %v, want wrapped errPersist", err)
	}
	if got := q.InFlight(); got != 1 {
		t.Fatalf("InFlight = %d, want 1", got)
	}
	p.heal()
	if err := q.Reject(r); err != nil {
		t.Fatalf("retry Reject: %v", err)
	}
	if got := takeBody(t, q); got != "a" {
		t.Fatalf("got %q, want a", got)
	}
}

func TestRecoverMixedState(t *testing.T) {
	clock := &manualClock{now: time.Unix(1000, 0)}
	p := NewMemPersister()
	q := newTestQueue(t, p, clock.Now, 10*time.Second)

	if _, err := q.Send([]byte("gone"), []byte("stuck"), []byte("ready")); err != nil {
		t.Fatal(err)
	}
	_, rg := mustTake(t, q)
	if err := q.Confirm(rg); err != nil {
		t.Fatal(err)
	}
	mustTake(t, q) // "stuck" claimed, will expire

	// Restart; let the in-flight claim expire.
	q2 := newTestQueue(t, p, clock.Now, 10*time.Second)
	clock.Advance(11 * time.Second)
	if got := takeBody(t, q2); got != "ready" {
		t.Fatalf("got %q, want ready", got)
	}
	if got := takeBody(t, q2); got != "stuck" {
		t.Fatalf("got %q, want stuck redelivered at tail", got)
	}
	if _, _, err := q2.Take(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("got %v, want ErrEmpty", err)
	}
}

func TestFilePersisterRoundTripAndCorruptTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wal.log")
	clock := &manualClock{now: time.Unix(1000, 0)}

	fp, err := OpenFilePersister(path)
	if err != nil {
		t.Fatal(err)
	}
	q := newTestQueue(t, fp, clock.Now, time.Minute)
	if _, err := q.Send([]byte("a"), []byte("b")); err != nil {
		t.Fatal(err)
	}
	_, r := mustTake(t, q)
	if err := q.Confirm(r); err != nil {
		t.Fatal(err)
	}
	if err := fp.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: only "b" remains.
	fp2, err := OpenFilePersister(path)
	if err != nil {
		t.Fatal(err)
	}
	q2 := newTestQueue(t, fp2, clock.Now, time.Minute)
	if got := q2.Len(); got != 1 {
		t.Fatalf("after reopen Len = %d, want 1 (only b remains)", got)
	}

	// Simulate a torn write, then replay: the corrupt tail is dropped.
	fp2.Close()
	f, err := filepath.Glob(path)
	if err != nil || len(f) != 1 {
		t.Fatalf("glob: %v %v", f, err)
	}
	appendBytes(t, path, []byte{0xde, 0xad, 0xbe, 0xef, 'x'})
	fp3, err := OpenFilePersister(path)
	if err != nil {
		t.Fatal(err)
	}
	recs, err := fp3.Replay()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 4 { // enqueue a, enqueue b, inflight a, ack a
		t.Fatalf("Replay returned %d records, want 4 (corrupt tail dropped)", len(recs))
	}
	fp3.Close()
}

func appendBytes(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
}
