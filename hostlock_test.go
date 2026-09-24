package hostlock

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProber is an injectable process table: pids present in the map are
// alive with the given start signature.
type fakeProber struct {
	mu    sync.Mutex
	procs map[int]string
}

func newFakeProber() *fakeProber {
	return &fakeProber{procs: map[int]string{os.Getpid(): "bootA:100"}}
}

func (f *fakeProber) Probe(pid int) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sig, ok := f.procs[pid]
	return ok, sig
}

func (f *fakeProber) kill(pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.procs, pid)
}

func (f *fakeProber) reuse(pid int, sig string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.procs[pid] = sig
}

type fixture struct {
	lock  *Lock
	clock *FakeClock
	proc  *fakeProber
	hooks *Hooks
}

func newFixture(t *testing.T, lease time.Duration) *fixture {
	t.Helper()
	clock := NewFakeClock(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	proc := newFakeProber()
	hooks := &Hooks{}
	l, err := New(Options{
		Dir:    t.TempDir(),
		Name:   "res",
		Lease:  lease,
		Clock:  clock,
		Prober: proc,
		Hooks:  hooks,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &fixture{lock: l, clock: clock, proc: proc, hooks: hooks}
}

func TestAcquireInspectRelease(t *testing.T) {
	f := newFixture(t, time.Minute)

	h, err := f.lock.TryAcquire()
	if err != nil {
		t.Fatalf("TryAcquire: %v", err)
	}
	if h.Token() != 1 {
		t.Fatalf("first token = %d, want 1", h.Token())
	}

	rec, status, err := f.lock.Inspect()
	if err != nil || status != StatusLive {
		t.Fatalf("Inspect = %v, %v; want live", status, err)
	}
	if rec.PID != os.Getpid() || rec.Token != 1 || rec.Nonce == "" {
		t.Fatalf("bad owner record: %+v", rec)
	}

	if err := h.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, status, _ := f.lock.Inspect(); status != StatusFree {
		t.Fatalf("status after release = %v, want free", status)
	}
	if err := h.Release(); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("second Release = %v, want ErrNotHeld", err)
	}
}

func TestContentionReturnsHeld(t *testing.T) {
	f := newFixture(t, time.Minute)
	if _, err := f.lock.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	other, err := New(Options{Dir: f.lock.dir, Name: "res", Lease: time.Minute, Clock: f.clock, Prober: f.proc})
	if err != nil {
		t.Fatal(err)
	}
	_, err = other.TryAcquire()
	var held *HeldError
	if !errors.As(err, &held) || !errors.Is(err, ErrHeld) {
		t.Fatalf("TryAcquire = %v, want HeldError", err)
	}
	if held.Owner.Token != 1 || held.Owner.PID != os.Getpid() {
		t.Fatalf("HeldError.Owner = %+v", held.Owner)
	}
}

func TestStaleTakeoverAndOldHolderRejected(t *testing.T) {
	f := newFixture(t, time.Minute)

	old, err := f.lock.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(2 * time.Minute) // lease lapses

	if _, status, _ := f.lock.Inspect(); status != StatusStale {
		t.Fatalf("status = %v, want stale", status)
	}

	// A "new process" (new client) takes over the stale lock.
	other, _ := New(Options{Dir: f.lock.dir, Name: "res", Lease: time.Minute, Clock: f.clock, Prober: f.proc})
	h2, err := other.TryAcquire()
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if h2.Token() != 2 {
		t.Fatalf("takeover token = %d, want 2 (monotonic)", h2.Token())
	}

	// The old holder wakes up: it can neither release nor renew.
	if err := old.Release(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("old Release = %v, want ErrNotOwner", err)
	}
	if err := old.Renew(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("old Renew = %v, want ErrNotOwner", err)
	}
	// The new owner's record must be untouched.
	if rec, status, _ := other.Inspect(); status != StatusLive || rec.Token != 2 {
		t.Fatalf("after old-holder attempts: status=%v rec=%+v", status, rec)
	}
}

func TestOldTokenCannotCommit(t *testing.T) {
	f := newFixture(t, time.Minute)
	gate := &Checker{}

	h1, _ := f.lock.TryAcquire()
	if err := gate.Check(h1.Token()); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(2 * time.Minute)

	other, _ := New(Options{Dir: f.lock.dir, Name: "res", Lease: time.Minute, Clock: f.clock, Prober: f.proc})
	h2, err := other.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	if err := gate.Check(h2.Token()); err != nil {
		t.Fatal(err)
	}

	// The expired holder recovers and tries to commit with its old token.
	if err := gate.Check(h1.Token()); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("old token commit = %v, want ErrStaleToken", err)
	}
}

func TestLostThenStaleStatus(t *testing.T) {
	f := newFixture(t, time.Minute)
	if _, err := f.lock.TryAcquire(); err != nil {
		t.Fatal(err)
	}

	f.proc.kill(os.Getpid()) // simulate crash: lease still valid
	if _, status, _ := f.lock.Inspect(); status != StatusLost {
		t.Fatalf("status = %v, want lost (lease valid, process gone)", status)
	}

	// While lost-but-unexpired, nobody can take the lock.
	if _, err := f.lock.TryAcquire(); !errors.Is(err, ErrHeld) {
		t.Fatalf("TryAcquire during lost = %v, want ErrHeld", err)
	}

	f.clock.Advance(2 * time.Minute)
	if _, status, _ := f.lock.Inspect(); status != StatusStale {
		t.Fatalf("status = %v, want stale after expiry", status)
	}
}

func TestPidReuseNotMisidentified(t *testing.T) {
	f := newFixture(t, time.Minute)
	h, err := f.lock.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}

	// Owner dies and the OS reuses its PID for an unrelated process.
	f.proc.reuse(os.Getpid(), "bootA:999")

	// Status must not claim the lock is live.
	if _, status, _ := f.lock.Inspect(); status != StatusLost {
		t.Fatalf("status with reused pid = %v, want lost", status)
	}

	// A process running under the reused PID cannot forge ownership:
	// it does not know the nonce, and a hand-made handle is rejected.
	forged := &Handle{lock: f.lock, nonce: "guessed-by-pid-recycler", token: h.Token()}
	if err := forged.Release(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("forged Release = %v, want ErrNotOwner", err)
	}
	if _, status, _ := f.lock.Inspect(); status == StatusFree {
		t.Fatal("forged release deleted the owner record")
	}
}

func TestReleaseOthersLockRejected(t *testing.T) {
	f := newFixture(t, time.Minute)
	if _, err := f.lock.TryAcquire(); err != nil {
		t.Fatal(err)
	}
	// Another client with a handle that does not match the record.
	stranger := &Handle{lock: f.lock, nonce: "not-the-nonce", token: 42}
	if err := stranger.Release(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("stranger Release = %v, want ErrNotOwner", err)
	}
	if err := stranger.Renew(); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("stranger Renew = %v, want ErrNotOwner", err)
	}
	if _, status, _ := f.lock.Inspect(); status != StatusLive {
		t.Fatalf("status = %v, want still live", status)
	}
}

func TestRenewExtendsAndRefusesAfterExpiry(t *testing.T) {
	f := newFixture(t, time.Minute)
	h, _ := f.lock.TryAcquire()

	f.clock.Advance(50 * time.Second) // still within the 1m lease
	if err := h.Renew(); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	rec, _, _ := f.lock.Inspect()
	want := f.clock.Now().Add(time.Minute)
	if !rec.ExpiresAt.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", rec.ExpiresAt, want)
	}
	if rec.Token != 1 {
		t.Fatalf("renew must keep the token, got %d", rec.Token)
	}

	f.clock.Advance(2 * time.Minute) // lease lapses without renewal
	if err := h.Renew(); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("Renew after expiry = %v, want ErrLeaseExpired", err)
	}
}

func TestFaultInjectionCrashBetweenTokenAndOwner(t *testing.T) {
	f := newFixture(t, time.Minute)
	f.hooks.FailBeforeOwnerWrite = func() error { return errors.New("simulated crash") }

	if _, err := f.lock.TryAcquire(); err == nil {
		t.Fatal("TryAcquire should fail via injected fault")
	}
	if _, status, _ := f.lock.Inspect(); status != StatusFree {
		t.Fatalf("status = %v, want free (no owner record written)", status)
	}
	// The token was persisted before the crash: monotonicity is preserved.
	data, err := os.ReadFile(filepath.Join(f.lock.dir, "res.token"))
	if err != nil || string(data) != "1\n" {
		t.Fatalf("token file = %q, %v; want \"1\\n\"", data, err)
	}
	f.hooks.FailBeforeOwnerWrite = nil
	h, err := f.lock.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	if h.Token() != 2 {
		t.Fatalf("token after crash = %d, want 2 (never reused)", h.Token())
	}
}

func TestFaultInjectionFsyncFailure(t *testing.T) {
	f := newFixture(t, time.Minute)
	f.hooks.FailBeforeFsync = func(path string) error { return errors.New("disk offline") }

	if _, err := f.lock.TryAcquire(); err == nil {
		t.Fatal("TryAcquire should fail via injected fsync fault")
	}
	if _, status, _ := f.lock.Inspect(); status != StatusFree {
		t.Fatalf("status = %v, want free", status)
	}
	// No temp files may be left behind.
	entries, _ := os.ReadDir(f.lock.dir)
	for _, e := range entries {
		if e.Name() != "res.mutex" {
			t.Errorf("unexpected file left behind: %s", e.Name())
		}
	}
}

func TestTokenMonotonicAcrossReleaseAndClients(t *testing.T) {
	f := newFixture(t, time.Minute)
	h1, _ := f.lock.TryAcquire()
	if err := h1.Release(); err != nil {
		t.Fatal(err)
	}
	// A fresh client instance (e.g. after process restart) continues the sequence.
	other, _ := New(Options{Dir: f.lock.dir, Name: "res", Lease: time.Minute, Clock: f.clock, Prober: f.proc})
	h2, err := other.TryAcquire()
	if err != nil {
		t.Fatal(err)
	}
	if h2.Token() != 2 {
		t.Fatalf("token = %d, want 2: release must not reset the token", h2.Token())
	}
}

func TestConcurrentTryAcquire(t *testing.T) {
	f := newFixture(t, 5*time.Second)
	var held atomic.Bool
	var violations atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				h, err := f.lock.TryAcquire()
				if errors.Is(err, ErrHeld) {
					continue
				}
				if err != nil {
					t.Errorf("TryAcquire: %v", err)
					return
				}
				if !held.CompareAndSwap(false, true) {
					violations.Add(1)
				}
				time.Sleep(time.Millisecond)
				held.Store(false)
				if err := h.Release(); err != nil {
					t.Errorf("Release: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if violations.Load() != 0 {
		t.Fatalf("%d mutual-exclusion violations", violations.Load())
	}
}

func TestInvalidOptions(t *testing.T) {
	if _, err := New(Options{Name: "x", Lease: time.Second}); err == nil {
		t.Error("missing Dir accepted")
	}
	if _, err := New(Options{Dir: t.TempDir(), Name: "../evil", Lease: time.Second}); err == nil {
		t.Error("path-traversal name accepted")
	}
	if _, err := New(Options{Dir: t.TempDir(), Name: "x"}); err == nil {
		t.Error("zero lease accepted")
	}
}
