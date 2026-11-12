package netsec

import (
	"sync"
	"testing"
)

// RFC 4226 Appendix D fixed test vectors: secret "12345678901234567890"
// (ASCII), counters 0..9.
var rfc4226Secret = []byte("12345678901234567890")

var rfc4226Codes = []string{
	"755224", "287082", "359152", "969429", "338314",
	"254676", "287922", "162583", "399871", "520489",
}

func newTestVerifier(t *testing.T, lookAhead, resyncWindow uint64) (*Verifier, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore(0)
	v, err := NewVerifier(rfc4226Secret, store, Options{
		LookAhead:    lookAhead,
		ResyncWindow: resyncWindow,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v, store
}

func TestHOTP_RFC4226Vectors(t *testing.T) {
	for counter, want := range rfc4226Codes {
		if got := HOTP(rfc4226Secret, uint64(counter)); got != want {
			t.Errorf("HOTP(counter=%d) = %s, want %s", counter, got, want)
		}
	}
}

func TestVerify_WindowEnd(t *testing.T) {
	v, store := newTestVerifier(t, 3, 5)

	// Last counter inside the normal window [0,3) is 2.
	ok, err := v.Verify(rfc4226Codes[2])
	if err != nil || !ok {
		t.Fatalf("Verify(window end) = %v, %v; want true, nil", ok, err)
	}
	if got := v.Counter(); got != 3 {
		t.Fatalf("counter = %d, want 3", got)
	}
	if persisted, _ := store.Load(); persisted != 3 {
		t.Fatalf("persisted counter = %d, want 3", persisted)
	}
}

func TestVerify_BeyondWindowRejected(t *testing.T) {
	v, store := newTestVerifier(t, 3, 5)

	// Counter 3 is the first resync-window counter: outside the normal
	// window, so a single guess must not move anything.
	ok, err := v.Verify(rfc4226Codes[3])
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if ok {
		t.Fatal("Verify accepted a code outside the normal window")
	}
	if got := v.Counter(); got != 0 {
		t.Fatalf("counter = %d, want 0", got)
	}
	if persisted, _ := store.Load(); persisted != 0 {
		t.Fatalf("persisted counter = %d, want 0", persisted)
	}
}

func TestVerify_Concurrent(t *testing.T) {
	v, store := newTestVerifier(t, uint64(len(rfc4226Codes)), 5)

	var wg sync.WaitGroup
	const goroutines = 16
	var mu sync.Mutex
	succeeded := 0
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := v.Verify(rfc4226Codes[0])
			if err != nil {
				t.Errorf("Verify: %v", err)
				return
			}
			if ok {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	// Interleaved invalid guesses must never advance the counter.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := v.Verify("000001"); err != nil || ok {
				t.Errorf("Verify(invalid) = %v, %v; want false, nil", ok, err)
			}
		}()
	}
	wg.Wait()

	// Check-and-advance is atomic: exactly one concurrent attempt on the
	// same code wins, and the counter moves exactly once.
	if succeeded != 1 {
		t.Fatalf("concurrent successes = %d, want exactly 1", succeeded)
	}
	if got := v.Counter(); got != 1 {
		t.Fatalf("counter = %d, want 1", got)
	}
	if persisted, _ := store.Load(); persisted != 1 {
		t.Fatalf("persisted counter = %d, want 1", persisted)
	}
}

func TestResync_SuccessAdvancesToLatterPlusOne(t *testing.T) {
	v, store := newTestVerifier(t, 3, 5)

	// Resync window is [3, 8); pair (5, 6) must move the counter to 7.
	ok, err := v.Resync(rfc4226Codes[5], rfc4226Codes[6])
	if err != nil || !ok {
		t.Fatalf("Resync = %v, %v; want true, nil", ok, err)
	}
	if got := v.Counter(); got != 7 {
		t.Fatalf("counter = %d, want 7", got)
	}
	if persisted, _ := store.Load(); persisted != 7 {
		t.Fatalf("persisted counter = %d, want 7", persisted)
	}

	// Normal verification continues from the new counter.
	ok, err = v.Verify(rfc4226Codes[7])
	if err != nil || !ok {
		t.Fatalf("Verify after resync = %v, %v; want true, nil", ok, err)
	}
}

func TestResync_WrongSecondStepKeepsPersistentCounter(t *testing.T) {
	v, store := newTestVerifier(t, 3, 5)

	// Valid first step at counter 5, wrong second step: the attempt must
	// fail atomically and leave the persistent counter untouched.
	ok, err := v.Resync(rfc4226Codes[5], "000000")
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if ok {
		t.Fatal("Resync accepted a wrong second step")
	}
	if got := v.Counter(); got != 0 {
		t.Fatalf("counter = %d, want 0", got)
	}
	if persisted, _ := store.Load(); persisted != 0 {
		t.Fatalf("persisted counter = %d, want 0", persisted)
	}

	// No partial state is kept: presenting the correct second step in a
	// later attempt (paired with a wrong first) must still fail.
	ok, err = v.Resync("000000", rfc4226Codes[6])
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if ok {
		t.Fatal("Resync leaked state across attempts")
	}
	if persisted, _ := store.Load(); persisted != 0 {
		t.Fatalf("persisted counter = %d, want 0", persisted)
	}
}

func TestResync_SingleGuessCannotAdvance(t *testing.T) {
	v, store := newTestVerifier(t, 3, 5)

	// A single correct value reused as both steps is not a consecutive
	// pair and must not move the counter.
	ok, err := v.Resync(rfc4226Codes[5], rfc4226Codes[5])
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if ok {
		t.Fatal("Resync advanced on a single guessed value")
	}
	if persisted, _ := store.Load(); persisted != 0 {
		t.Fatalf("persisted counter = %d, want 0", persisted)
	}
}

func TestResync_PairOutsideSyncWindowRejected(t *testing.T) {
	v, store := newTestVerifier(t, 3, 5)

	// Pair (8, 9) starts past the resync window [3, 8).
	ok, err := v.Resync(rfc4226Codes[8], rfc4226Codes[9])
	if err != nil {
		t.Fatalf("Resync: %v", err)
	}
	if ok {
		t.Fatal("Resync accepted a pair outside the sync window")
	}
	if persisted, _ := store.Load(); persisted != 0 {
		t.Fatalf("persisted counter = %d, want 0", persisted)
	}
}
