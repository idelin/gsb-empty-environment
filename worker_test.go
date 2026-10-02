package quota

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeMaster is an in-memory Heartbeater for unit-testing Worker.
type fakeMaster struct {
	mu    sync.Mutex
	calls int
	grant int64 // Additional returned per call
	err   error
	used  int64 // last reported Used
}

func (f *fakeMaster) Heartbeat(req HeartbeatRequest) (HeartbeatResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.used = req.Used
	if f.err != nil {
		return HeartbeatResponse{}, f.err
	}
	return HeartbeatResponse{
		Additional: f.grant,
		Remaining:  1 << 40, // effectively unlimited: no recall in these tests
		ExpiresAt:  time.Now().Add(time.Minute),
	}, nil
}

func (f *fakeMaster) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newRegisteredWorker(t *testing.T, f *fakeMaster, batch int64) *Worker {
	t.Helper()
	w := NewWorker("W", f, batch, 0.2)
	if err := w.Register(); err != nil {
		t.Fatalf("register: %v", err)
	}
	return w
}

// Local consumption must not touch the network until the watermark.
func TestLocalConsumptionWithoutRPC(t *testing.T) {
	f := &fakeMaster{grant: 1000}
	w := newRegisteredWorker(t, f, 500)
	callsAfterRegister := f.callCount()

	for i := 0; i < 700; i++ { // stays above the 20% watermark of 1000
		if !w.Allow() {
			t.Fatalf("Allow() = false at %d with local tokens left", i)
		}
	}
	w.Wait()
	if got := f.callCount(); got != callsAfterRegister {
		t.Fatalf("heartbeats during local consumption = %d, want %d", got, callsAfterRegister)
	}
}

// Hitting the low watermark fires exactly one asynchronous batched
// renewal that replenishes the local balance.
func TestAsyncBatchRenewal(t *testing.T) {
	f := &fakeMaster{grant: 1000}
	w := newRegisteredWorker(t, f, 500)
	for i := 0; i < 1000; i++ { // drain the whole initial grant
		w.Allow()
	}
	w.Wait()
	if got := f.callCount(); got < 2 {
		t.Fatalf("heartbeats = %d, want at least register + 1 renewal", got)
	}
	if s := w.Stats(); s.Tokens <= 0 {
		t.Fatalf("tokens after renewal = %d, want > 0", s.Tokens)
	}
	// Consumption resumes after the asynchronous renewal lands.
	if !w.Allow() {
		t.Fatal("Allow() = false after renewal replenished tokens")
	}
}

// A failed heartbeat must not lose the usage report.
func TestRenewFailureRollsBackUsage(t *testing.T) {
	f := &fakeMaster{grant: 1000}
	w := newRegisteredWorker(t, f, 500)
	for i := 0; i < 100; i++ {
		w.Allow()
	}
	f.err = errors.New("network down")
	w.triggerRenew()
	w.Wait()
	if s := w.Stats(); s.Used != 100 {
		t.Fatalf("unreported usage = %d, want 100 (rolled back)", s.Used)
	}
	f.err = nil
	w.triggerRenew()
	w.Wait()
	f.mu.Lock()
	used := f.used
	f.mu.Unlock()
	if used != 100 {
		t.Fatalf("master saw Used = %d, want 100 after retry", used)
	}
}

// Tokens die with the lease; Allow fails closed until a renewal lands.
func TestLeaseExpiryBlocksLocally(t *testing.T) {
	now := time.Now()
	cur := &now
	f := &fakeMaster{grant: 10}
	w := NewWorker("W", f, 10, 0.2)
	w.SetClock(func() time.Time { return *cur })
	if err := w.Register(); err != nil {
		t.Fatal(err)
	}
	*cur = now.Add(2 * time.Minute) // past the lease deadline
	if w.Allow() {
		t.Fatal("Allow() = true with an expired lease")
	}
	w.Wait()
	*cur = time.Now() // rejoin real time so the fresh lease is valid
	if !w.Allow() {
		t.Fatal("Allow() = false after post-expiry renewal")
	}
}
