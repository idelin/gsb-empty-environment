package digestauth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// controllableVerifier is a test double: acceptMode toggles acceptance, each
// invocation is recorded with the evidence presented, and an optional delay
// widens the race window for parallel tests.
type controllableVerifier struct {
	mu         sync.Mutex
	acceptMode bool
	calls      int32
	seen       []Evidence
	delay      time.Duration
}

func (v *controllableVerifier) Verify(_ context.Context, e Evidence) error {
	if v.delay > 0 {
		time.Sleep(v.delay)
	}
	atomic.AddInt32(&v.calls, 1)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.seen = append(v.seen, e)
	if !v.acceptMode {
		return errors.New("test verifier rejects signature")
	}
	return nil
}

func (v *controllableVerifier) callCount() int { return int(atomic.LoadInt32(&v.calls)) }

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func testState(t *testing.T) (*State, *controllableVerifier) {
	t.Helper()
	clock := newFakeClock()
	st := NewState([]byte("server-secret-key"),
		WithClock(clock.now), WithSweepInterval(0))
	t.Cleanup(st.Stop)
	return st, &controllableVerifier{acceptMode: true}
}

func testPolicy() Policy {
	return Policy{
		Realm:     "gateway@example.org",
		QOP:       "auth",
		Algorithm: "MD5",
		MaxCount:  50,
		Window:    8,
		TTL:       time.Minute,
	}
}

func issueNonce(t *testing.T, st *State) (string, Policy) {
	t.Helper()
	policy := testPolicy()
	nonce, err := st.Issue(policy)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return nonce, policy
}

func authReq(nonce string, policy Policy, nc uint32, cnonce string) Request {
	return Request{
		Policy:   policy,
		Username: "alice",
		Nonce:    nonce,
		CNonce:   cnonce,
		NC:       nc,
		Method:   "GET",
		URI:      "/api/target",
		BodySHA:  "d41d8cd98f00b204e9800998ecf8427e",
		Response: "client-response",
	}
}

func TestAuthenticateHappyPathAndReplay(t *testing.T) {
	st, verifier := testState(t)
	nonce, policy := issueNonce(t, st)

	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 1, "cn-1")); err != nil {
		t.Fatalf("first authenticate: %v", err)
	}
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 2, "cn-1")); err != nil {
		t.Fatalf("second authenticate: %v", err)
	}
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 2, "cn-1")); !errors.Is(err, ErrReplay) {
		t.Fatalf("duplicate nc = %v, want ErrReplay", err)
	}
	if got := verifier.callCount(); got != 3 {
		t.Fatalf("verifier calls = %d, want 3 (replay still verifies signature)", got)
	}

	first := verifier.seen[0]
	if first.Username != "alice" || first.Realm != policy.Realm ||
		first.Method != "GET" || first.URI != "/api/target" ||
		first.BodySHA == "" || first.Response != "client-response" {
		t.Fatalf("verifier evidence missing fields: %+v", first)
	}
}

func TestInvalidSignatureDoesNotBurnCounter(t *testing.T) {
	st, verifier := testState(t)
	nonce, policy := issueNonce(t, st)

	verifier.acceptMode = false
	req := authReq(nonce, policy, 1, "cn-1")
	if err := st.Authenticate(context.Background(), verifier, req); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("bad signature = %v, want ErrBadSignature", err)
	}

	verifier.acceptMode = true
	// nc=1 must still be available: the rejected request never registered it.
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 1, "cn-1")); err != nil {
		t.Fatalf("nc=1 after failed signature: %v", err)
	}
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 1, "cn-1")); !errors.Is(err, ErrReplay) {
		t.Fatalf("second nc=1 = %v, want ErrReplay", err)
	}
}

func TestOutOfOrderWindow(t *testing.T) {
	st, verifier := testState(t)
	nonce, policy := issueNonce(t, st)

	for _, nc := range []uint32{1, 4, 3, 2} {
		if err := st.Authenticate(context.Background(), verifier,
			authReq(nonce, policy, nc, "cn-1")); err != nil {
			t.Fatalf("out-of-order nc=%d: %v", nc, err)
		}
	}
}

func TestParallelDuplicateNC(t *testing.T) {
	st, verifier := testState(t)
	verifier.delay = 2 * time.Millisecond
	nonce, policy := issueNonce(t, st)

	const workers = 32
	var wg sync.WaitGroup
	var accepted, replayed int32
	start := make(chan struct{})
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := st.Authenticate(context.Background(), verifier,
				authReq(nonce, policy, 7, "cn-1"))
			switch {
			case err == nil:
				atomic.AddInt32(&accepted, 1)
			case errors.Is(err, ErrReplay):
				atomic.AddInt32(&replayed, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if accepted != 1 || replayed != workers-1 {
		t.Fatalf("accepted=%d replayed=%d, want 1 and %d",
			accepted, replayed, workers-1)
	}
	if verifier.callCount() != workers {
		t.Fatalf("verifier calls = %d, want %d", verifier.callCount(), workers)
	}
}

func TestParallelOutOfOrderAllAccepted(t *testing.T) {
	st, verifier := testState(t)
	verifier.delay = time.Millisecond
	policy := testPolicy()
	policy.Window = 64
	nonce, err := st.Issue(policy)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	start := make(chan struct{})
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(nc uint32) {
			defer wg.Done()
			<-start
			errs <- st.Authenticate(context.Background(), verifier,
				authReq(nonce, policy, nc, "cn-1"))
		}(uint32(i))
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("parallel unique nc failed: %v", err)
		}
	}
}

func TestCounterWrapRejectedAtMaxCount(t *testing.T) {
	st, verifier := testState(t)
	policy := testPolicy()
	policy.MaxCount = 5
	nonce, err := st.Issue(policy)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for nc := uint32(1); nc <= 5; nc++ {
		if err := st.Authenticate(context.Background(), verifier,
			authReq(nonce, policy, nc, "cn-1")); err != nil {
			t.Fatalf("nc=%d: %v", nc, err)
		}
	}
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 6, "cn-1")); !errors.Is(err, ErrCounterWrapped) {
		t.Fatalf("nc=6 = %v, want ErrCounterWrapped", err)
	}
	// The wrapped attempt never consumed a counter slot.
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 5, "cn-1")); !errors.Is(err, ErrReplay) {
		t.Fatalf("nc=5 after wrap = %v, want ErrReplay", err)
	}
}

func TestCNonceIsolatesCounters(t *testing.T) {
	st, verifier := testState(t)
	nonce, policy := issueNonce(t, st)

	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 1, "cn-A")); err != nil {
		t.Fatalf("cn-A: %v", err)
	}
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 1, "cn-B")); err != nil {
		t.Fatalf("same nc with different cnonce: %v", err)
	}
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 1, "cn-A")); !errors.Is(err, ErrReplay) {
		t.Fatalf("cn-A duplicate = %v, want ErrReplay", err)
	}
}

func TestNonceExpiryAndSweep(t *testing.T) {
	clock := newFakeClock()
	st := NewState([]byte("server-secret-key"),
		WithClock(clock.now), WithSweepInterval(0))
	t.Cleanup(st.Stop)
	verifier := &controllableVerifier{acceptMode: true}

	nonce, policy := issueNonce(t, st)
	clock.advance(policy.TTL)
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 1, "cn-1")); !errors.Is(err, ErrExpiredNonce) {
		t.Fatalf("expired nonce = %v, want ErrExpiredNonce", err)
	}

	st.mu.Lock()
	_, present := st.nonces[nonce]
	st.mu.Unlock()
	if present {
		t.Fatal("expired nonce entry was not removed on access")
	}

	other, _ := issueNonce(t, st)
	clock.advance(time.Hour)
	st.Sweep()
	st.mu.Lock()
	_, present = st.nonces[other]
	st.mu.Unlock()
	if present {
		t.Fatal("Sweep did not remove the expired nonce")
	}
}

func TestBackgroundSweepAndStop(t *testing.T) {
	clock := newFakeClock()
	st := NewState([]byte("server-secret-key"),
		WithClock(clock.now), WithSweepInterval(5*time.Millisecond))

	nonce, _ := issueNonce(t, st)
	clock.advance(time.Hour)
	deadline := time.After(time.Second)
	for {
		st.mu.Lock()
		_, present := st.nonces[nonce]
		st.mu.Unlock()
		if !present {
			break
		}
		select {
		case <-deadline:
			t.Fatal("background sweep did not remove expired nonce")
		case <-time.After(5 * time.Millisecond):
		}
	}
	st.Stop()
}

func TestPolicyMismatchAndForgedNonce(t *testing.T) {
	st, verifier := testState(t)
	nonce, policy := issueNonce(t, st)

	other := policy
	other.Realm = "other-realm"
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, other, 1, "cn-1")); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("policy mismatch = %v, want ErrPolicyMismatch", err)
	}

	forged := nonce[:len(nonce)-2] + "AA"
	if err := st.Authenticate(context.Background(), verifier,
		authReq(forged, policy, 1, "cn-1")); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("forged nonce = %v, want ErrInvalidNonce", err)
	}

	if err := st.Authenticate(context.Background(), verifier,
		authReq("not-a-nonce", policy, 1, "cn-1")); !errors.Is(err, ErrInvalidNonce) {
		t.Fatalf("garbage nonce = %v, want ErrInvalidNonce", err)
	}
}

func TestUnknownNonceAndInputValidation(t *testing.T) {
	clock := newFakeClock()
	st, verifier := testState(t)
	policy := testPolicy()

	// Same HMAC secret: the envelope verifies, but this State never issued it.
	foreign := NewState([]byte("server-secret-key"),
		WithClock(clock.now), WithSweepInterval(0))
	t.Cleanup(foreign.Stop)
	stranger, err := foreign.Issue(policy)
	if err != nil {
		t.Fatalf("foreign Issue: %v", err)
	}
	if err := st.Authenticate(context.Background(), verifier,
		authReq(stranger, policy, 1, "cn-1")); !errors.Is(err, ErrUnknownNonce) {
		t.Fatalf("foreign nonce = %v, want ErrUnknownNonce", err)
	}

	nonce, _ := issueNonce(t, st)
	if err := st.Authenticate(context.Background(), verifier,
		authReq(nonce, policy, 0, "cn-1")); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("nc=0 = %v, want ErrInvalidCount", err)
	}
	noCNonce := authReq(nonce, policy, 1, "")
	if err := st.Authenticate(context.Background(), verifier,
		noCNonce); !errors.Is(err, ErrMissingCNonce) {
		t.Fatalf("missing cnonce = %v, want ErrMissingCNonce", err)
	}
}
