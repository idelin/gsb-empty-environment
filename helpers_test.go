package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeVerifier models the upload backend: content must be Put before it can
// be verified, and Put records the digest that must match at verify time.
type fakeVerifier struct {
	mu       sync.Mutex
	content  map[string]string
	calls    []verifyCall
	mismatch map[string]bool
	anyOK    map[string]bool
}

type verifyCall struct {
	label  string
	digest string
}

func newFakeVerifier() *fakeVerifier {
	return &fakeVerifier{
		content:  make(map[string]string),
		mismatch: make(map[string]bool),
		anyOK:    make(map[string]bool),
	}
}

// AllowAnyDigest marks a label as valid for every non-empty digest, which
// models multiple valid uploads competing for one immutable label.
func (v *fakeVerifier) AllowAnyDigest(label string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.anyOK[label] = true
}

func (v *fakeVerifier) Put(label, digest string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.content[label] = digest
}

func (v *fakeVerifier) Remove(label string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.content, label)
}

func (v *fakeVerifier) FailMismatch(label string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.mismatch[label] = true
}

func (v *fakeVerifier) Verify(_ context.Context, label, digest string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls = append(v.calls, verifyCall{label: label, digest: digest})
	if v.mismatch[label] {
		return ErrContentMismatch
	}
	got, ok := v.content[label]
	if !ok {
		if v.anyOK[label] && digest != "" {
			return nil
		}
		return ErrContentNotFound
	}
	if got != digest {
		return ErrContentMismatch
	}
	return nil
}

func (v *fakeVerifier) callCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.calls)
}

// barrierVerifier allows only one Verify call at a time and blocks it until
// release is invoked, forcing concurrent first publishes to serialize inside
// the store transaction.
type barrierVerifier struct {
	inner  Verifier
	t      *testing.T
	mu     sync.Mutex
	inside bool
	enter  chan struct{}
	block  chan struct{}
}

func newBarrierVerifier(t *testing.T, inner Verifier) *barrierVerifier {
	return &barrierVerifier{
		inner: inner,
		t:     t,
		enter: make(chan struct{}, 1),
		block: make(chan struct{}),
	}
}

func (b *barrierVerifier) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-b.enter:
	case <-time.After(5 * time.Second):
		t.Fatal("verifier was never entered")
	}
}

func (b *barrierVerifier) release() { close(b.block) }

func (b *barrierVerifier) Verify(ctx context.Context, label, digest string) error {
	b.mu.Lock()
	if b.inside {
		b.mu.Unlock()
		b.t.Fatalf("verifier entered concurrently for label %q", label)
	}
	b.inside = true
	b.mu.Unlock()

	b.enter <- struct{}{}
	select {
	case <-b.block:
	case <-ctx.Done():
		b.mu.Lock()
		b.inside = false
		b.mu.Unlock()
		return ctx.Err()
	}

	b.mu.Lock()
	b.inside = false
	b.mu.Unlock()
	return b.inner.Verify(ctx, label, digest)
}

func newTestService(t *testing.T, ttl time.Duration) (*Service, *MemoryStore, *fakeVerifier, *fakeClock) {
	t.Helper()
	store := NewMemoryStore()
	verifier := newFakeVerifier()
	clock := newFakeClock()
	svc := NewService(store, verifier, WithClock(clock), WithTTL(ttl))
	return svc, store, verifier, clock
}

func mustRecord(t *testing.T, rec *LabelRecord, want State) *LabelRecord {
	t.Helper()
	if rec == nil {
		t.Fatal("expected record, got nil")
	}
	if rec.State != want {
		t.Fatalf("state = %s, want %s", rec.State, want)
	}
	return rec
}

func errIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error wrapping %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("error %v does not wrap %v", err, target)
	}
}
