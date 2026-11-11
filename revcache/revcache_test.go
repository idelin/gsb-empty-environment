package revcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for deterministic expiry tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// fakeVerify counts invocations, replays scripted responses and can
// block an in-flight call until released to exercise coalescing.
type fakeVerify struct {
	calls atomic.Int32
	mu    sync.Mutex
	seq   []Response
	err   error

	started chan struct{}
	release chan struct{}
}

func newFakeVerify(responses ...Response) *fakeVerify {
	return &fakeVerify{
		seq:     append([]Response(nil), responses...),
		started: make(chan struct{}, 16),
	}
}

func (f *fakeVerify) verify(_ context.Context, _ Key) (Response, error) {
	f.calls.Add(1)
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Response{}, f.err
	}
	if len(f.seq) == 0 {
		return Response{}, errors.New("fakeVerify: no scripted response")
	}
	resp := f.seq[0]
	if len(f.seq) > 1 {
		f.seq = f.seq[1:]
	}
	return resp, nil
}

func (f *fakeVerify) callCount() int { return int(f.calls.Load()) }

func (f *fakeVerify) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

var base = time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)

func good(this time.Time, ttl time.Duration) Response {
	return Response{Status: StatusGood, ThisUpdate: this, NextUpdate: this.Add(ttl)}
}

func revoked(this time.Time, ttl time.Duration) Response {
	return Response{Status: StatusRevoked, ThisUpdate: this, NextUpdate: this.Add(ttl)}
}

// Same serial number under two issuers and two responders must stay
// isolated: the original bug reused one issuer's conclusion for the
// other.
func TestKeyBindingSameSerialDifferentCA(t *testing.T) {
	clk := newFakeClock(base)
	v := newFakeVerify(
		good(base, time.Hour),
		revoked(base, time.Hour),
	)
	c := New(v.verify, clk)

	keyA := Key{IssuerKeyDigest: "sha256:ca-a", Serial: "4242", Responder: "http://ocsp/ca-a"}
	keyB := Key{IssuerKeyDigest: "sha256:ca-b", Serial: "4242", Responder: "http://ocsp/ca-a"}
	keyC := Key{IssuerKeyDigest: "sha256:ca-a", Serial: "4242", Responder: "http://ocsp/ca-b"}

	gotA, err := c.Get(context.Background(), keyA)
	if err != nil || gotA.Status != StatusGood {
		t.Fatalf("keyA Get = %+v, %v; want good, nil", gotA, err)
	}
	gotB, err := c.Get(context.Background(), keyB)
	if err != nil || gotB.Status != StatusRevoked {
		t.Fatalf("keyB Get = %+v, %v; want revoked, nil", gotB, err)
	}
	if v.callCount() != 2 {
		t.Fatalf("verifier calls = %d, want 2", v.callCount())
	}
	// Re-reading either key hits its own cache and proves no cross-key reuse.
	if got, _ := c.Get(context.Background(), keyA); got.Status != StatusGood {
		t.Fatalf("keyA reread = %v, want good", got.Status)
	}
	if got, _ := c.Get(context.Background(), keyB); got.Status != StatusRevoked {
		t.Fatalf("keyB reread = %v, want revoked", got.Status)
	}
	if v.callCount() != 2 {
		t.Fatalf("verifier calls after reread = %d, want 2", v.callCount())
	}
	if _, err := c.Get(context.Background(), keyC); err != nil {
		t.Fatalf("different responder key: %v", err)
	}
	if c.Len() != 3 {
		t.Fatalf("cache Len = %d, want 3", c.Len())
	}
}

func TestValidityWindowAndControlledClock(t *testing.T) {
	clk := newFakeClock(base)
	v := newFakeVerify(good(base, time.Hour))
	c := New(v.verify, clk)

	key := Key{IssuerKeyDigest: "digest", Serial: "7", Responder: "r"}
	got, err := c.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	clk.advance(59 * time.Minute)
	if !validAt(got, clk.Now()) {
		t.Fatal("entry should still be valid one minute before nextUpdate")
	}
	if cached, err := c.Get(context.Background(), key); err != nil || cached != got {
		t.Fatalf("fresh window Get = %+v, %v; want cached entry", cached, err)
	}

	// Half-open interval: nextUpdate itself is expiry, forcing a refresh.
	clk.advance(time.Minute)
	v.mu.Lock()
	v.seq = []Response{revoked(base.Add(2*time.Hour), time.Hour)}
	v.mu.Unlock()
	got2, err := c.Get(context.Background(), key)
	if err != nil || got2.Status != StatusRevoked {
		t.Fatalf("post-expiry Get = %+v, %v; want revoked refresh", got2, err)
	}

	// A response whose validity window lies in the future is not valid yet.
	future := good(base.Add(24*time.Hour), time.Hour)
	if validAt(future, clk.Now()) {
		t.Fatal("future-dated response must not be valid before its thisUpdate")
	}
}

func TestUnknownAndMalformedNeverCached(t *testing.T) {
	clk := newFakeClock(base)
	boom := errors.New("signature verification failed")
	v := newFakeVerify(
		Response{Status: StatusUnknown},
		Response{Status: StatusGood}, // zero timestamps
		good(base, time.Hour),
	)
	c := New(v.verify, clk)
	key := Key{IssuerKeyDigest: "d", Serial: "1", Responder: "r"}

	if _, err := c.Get(context.Background(), key); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown Get err = %v, want ErrUnknown", err)
	}
	if _, err := c.Get(context.Background(), key); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("malformed Get err = %v, want ErrInvalidResponse", err)
	}
	if got, err := c.Get(context.Background(), key); err != nil || got.Status != StatusGood {
		t.Fatalf("valid Get = %+v, %v", got, err)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want only the valid entry cached", c.Len())
	}

	// Verification errors do not evict a still-valid cached conclusion.
	v.setError(boom)
	clk.advance(time.Minute)
	if got, err := c.Get(context.Background(), key); err != nil || got.Status != StatusGood {
		t.Fatalf("error while valid: %+v, %v", got, err)
	}

	// Once the entry expires the error must surface instead of being
	// treated as a valid conclusion.
	clk.advance(time.Hour)
	if _, err := c.Get(context.Background(), key); !errors.Is(err, boom) {
		t.Fatalf("post-expiry error Get = %v, want %v", err, boom)
	}
}

func TestCoalesceWithIndependentCancellation(t *testing.T) {
	clk := newFakeClock(base)
	v := newFakeVerify(good(base, time.Hour))
	v.release = make(chan struct{})
	c := New(v.verify, clk)
	key := Key{IssuerKeyDigest: "d", Serial: "9", Responder: "r"}

	var wg sync.WaitGroup
	results := make(chan error, 3)

	// Waiter 1 starts the single refresh and cancels before it settles.
	ctx1, cancel1 := context.WithCancel(context.Background())
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := c.Get(ctx1, key)
		results <- err
	}()
	<-v.started

	// Waiters 2 and 3 coalesce onto the same in-flight call.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Get(context.Background(), key)
			results <- err
		}()
	}

	// Give the joiners time to register, then cancel only waiter 1.
	time.Sleep(20 * time.Millisecond)
	cancel1()
	if err := <-results; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter err = %v, want context.Canceled", err)
	}

	// Settling the shared refresh must still unblock the other waiters.
	close(v.release)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("surviving waiter err = %v, want nil", err)
		}
	}
	if v.callCount() != 1 {
		t.Fatalf("verifier calls = %d, want exactly 1 coalesced call", v.callCount())
	}

	// An already-canceled context must not start or join a refresh.
	ctxDone, cancelDone := context.WithCancel(context.Background())
	cancelDone()
	if _, err := c.Get(ctxDone, Key{IssuerKeyDigest: "d", Serial: "other", Responder: "r"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Get err = %v, want context.Canceled", err)
	}
	if v.callCount() != 1 {
		t.Fatalf("verifier calls after pre-cancel = %d, want 1", v.callCount())
	}
}

func TestOutOfOrderRevocationProtection(t *testing.T) {
	clk := newFakeClock(base)
	// The verifier is replaced per phase below; start with a placeholder.
	c := New(func(context.Context, Key) (Response, error) {
		return Response{}, errors.New("unexpected verify call")
	}, clk)
	key := Key{IssuerKeyDigest: "d", Serial: "5", Responder: "r"}

	// Phase 1: a revoked conclusion valid for an hour is cached.
	v1 := newFakeVerify(revoked(base, time.Hour))
	c.verify = v1.verify
	if got, err := c.Get(context.Background(), key); err != nil || got.Status != StatusRevoked {
		t.Fatalf("phase1 = %+v, %v", got, err)
	}

	// Phase 2: an out-of-order good response with an older thisUpdate
	// must not overwrite the still-valid revocation. Get keeps serving
	// the revoked conclusion.
	v2 := newFakeVerify(good(base.Add(-time.Hour), 30*time.Minute))
	c.verify = v2.verify
	clk.advance(time.Minute)
	if got, err := c.Get(context.Background(), key); err != nil || got.Status != StatusRevoked {
		t.Fatalf("phase2 = %+v, %v; want retained revoked", got, err)
	}

	// After expiry, an older good conclusion is allowed in because the
	// revocation no longer has any protective validity.
	clk.advance(time.Hour)
	v3 := newFakeVerify(good(base.Add(-time.Hour), 3*time.Hour))
	c.verify = v3.verify
	if got, err := c.Get(context.Background(), key); err != nil || got.Status != StatusGood {
		t.Fatalf("phase3 = %+v, %v; want good after revocation expiry", got, err)
	}

	// A newer good response (thisUpdate at or after the revoked one) is
	// accepted even while a revocation is still valid; responders may
	// legitimately update a conclusion.
	v4 := newFakeVerify(revoked(base.Add(3*time.Hour), time.Hour))
	c.verify = v4.verify
	clk.advance(3 * time.Hour)
	if _, err := c.Get(context.Background(), key); err != nil {
		t.Fatalf("re-cache revoked: %v", err)
	}
	v5 := newFakeVerify(good(base.Add(3*time.Hour), time.Hour))
	c.verify = v5.verify
	clk.advance(time.Minute)
	if got, err := c.Get(context.Background(), key); err != nil || got.Status != StatusGood {
		t.Fatalf("newer good = %+v, %v; want accepted", got, err)
	}
}

func TestShouldStoreRuleTable(t *testing.T) {
	now := base
	cases := []struct {
		name     string
		prev     Response
		havePrev bool
		fresh    Response
		want     bool
	}{
		{
			name:     "first response always stored",
			havePrev: false,
			fresh:    good(now, time.Hour),
			want:     true,
		},
		{
			name:     "older good blocked by valid revoked",
			prev:     revoked(now, time.Hour),
			havePrev: true,
			fresh:    good(now.Add(-time.Minute), time.Hour),
			want:     false,
		},
		{
			name:     "equal thisUpdate good allowed",
			prev:     revoked(now, time.Hour),
			havePrev: true,
			fresh:    good(now, time.Hour),
			want:     true,
		},
		{
			name:     "newer good allowed over valid revoked",
			prev:     revoked(now, time.Hour),
			havePrev: true,
			fresh:    good(now.Add(time.Minute), time.Hour),
			want:     true,
		},
		{
			name:     "older good allowed once revoked expired",
			prev:     revoked(now.Add(-2*time.Hour), time.Hour),
			havePrev: true,
			fresh:    good(now.Add(-time.Hour), 2*time.Hour),
			want:     true,
		},
		{
			name:     "revoked always replaces good",
			prev:     good(now, time.Hour),
			havePrev: true,
			fresh:    revoked(now.Add(-time.Hour), 2*time.Hour),
			want:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldStore(tc.prev, tc.havePrev, tc.fresh, now); got != tc.want {
				t.Fatalf("shouldStore = %v, want %v", got, tc.want)
			}
		})
	}
}
