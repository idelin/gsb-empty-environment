package wrapstore

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

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

func TestWrapLookupUnwrapLifecycle(t *testing.T) {
	store := New()
	secret := []byte("database-master-password")
	creationPath := "vault/services/billing/2026/rotations/42"

	token, gotPath, err := store.Wrap(secret, creationPath)
	if err != nil {
		t.Fatalf("Wrap failed: %v", err)
	}
	if token == "" {
		t.Fatal("Wrap returned an empty token")
	}
	if gotPath != creationPath {
		t.Fatalf("Wrap creation path = %q, want %q", gotPath, creationPath)
	}

	for i := 0; i < 3; i++ {
		path, err := store.Lookup(token)
		if err != nil {
			t.Fatalf("Lookup #%d failed: %v", i, err)
		}
		if path != creationPath {
			t.Fatalf("Lookup #%d path = %q, want %q", i, path, creationPath)
		}
	}

	gotSecret, unwrapPath, err := store.Unwrap(token)
	if err != nil {
		t.Fatalf("Unwrap failed: %v", err)
	}
	if !bytes.Equal(gotSecret, secret) {
		t.Fatalf("Unwrap secret = %q, want %q", gotSecret, secret)
	}
	if unwrapPath != creationPath {
		t.Fatalf("Unwrap path = %q, want %q", unwrapPath, creationPath)
	}

	gotSecret[0] = 'X'
	if secret[0] == 'X' {
		t.Fatal("returned secret shares storage with the store")
	}
}

func TestUnwrapIsAtomicUnderConcurrency(t *testing.T) {
	store := New()
	secret := []byte("concurrent-credential")
	creationPath := "vault/region/a/path/7"
	token, _, err := store.Wrap(secret, creationPath)
	if err != nil {
		t.Fatalf("Wrap failed: %v", err)
	}

	const contenders = 64
	var start sync.WaitGroup
	var done sync.WaitGroup
	results := make(chan result, contenders)
	start.Add(1)

	for i := 0; i < contenders; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			gotSecret, gotPath, err := store.Unwrap(token)
			results <- result{secret: gotSecret, path: gotPath, err: err}
		}()
	}

	start.Done()
	done.Wait()
	close(results)

	winners := 0
	intercepted := 0
	for res := range results {
		if res.err == nil {
			winners++
			if !bytes.Equal(res.secret, secret) {
				t.Fatalf("winner secret = %q, want %q", res.secret, secret)
			}
			if res.path != creationPath {
				t.Fatalf("winner path = %q, want %q", res.path, creationPath)
			}
			continue
		}
		if errors.Is(res.err, ErrSuspectedInterception) {
			intercepted++
			continue
		}
		t.Fatalf("unexpected Unwrap error: %v", res.err)
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}
	if intercepted != contenders-1 {
		t.Fatalf("interception errors = %d, want %d", intercepted, contenders-1)
	}
}

type result struct {
	secret []byte
	path   string
	err    error
}

func TestLookupThenRacedUnwrapReportsInterception(t *testing.T) {
	store := New()
	secret := []byte("handover-package")
	expectedPath := "vault/handover/recipient/alice"
	token, _, err := store.Wrap(secret, expectedPath)
	if err != nil {
		t.Fatalf("Wrap failed: %v", err)
	}

	path, err := store.Lookup(token)
	if err != nil {
		t.Fatalf("recipient Lookup failed: %v", err)
	}
	if path != expectedPath {
		t.Fatalf("Lookup path = %q, want %q", path, expectedPath)
	}

	if stolen, _, err := store.Unwrap(token); err != nil || !bytes.Equal(stolen, secret) {
		t.Fatalf("attacker Unwrap failed: secret=%q err=%v", stolen, err)
	}

	_, _, err = store.Unwrap(token)
	if !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("recipient Unwrap error = %v, want ErrSuspectedInterception", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("consumed token must not be reported as plain not found")
	}

	if _, err := store.Lookup(token); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("Lookup after interception error = %v, want ErrSuspectedInterception", err)
	}
}

func TestCreationPathReplacementDetectedBeforeUnwrap(t *testing.T) {
	store := New()
	expectedPath := "vault/recipients/alice/envelope/9"

	genuine, _, err := store.Wrap([]byte("genuine-secret"), expectedPath)
	if err != nil {
		t.Fatalf("Wrap genuine failed: %v", err)
	}
	decoy, _, err := store.Wrap([]byte("attacker-secret"), "vault/recipients/mallory/envelope/9")
	if err != nil {
		t.Fatalf("Wrap decoy failed: %v", err)
	}

	presented := decoy
	path, err := store.Lookup(presented)
	if err != nil {
		t.Fatalf("Lookup decoy failed: %v", err)
	}
	if path == expectedPath {
		t.Fatal("decoy creation path unexpectedly matches the expected path")
	}
	if path != "vault/recipients/mallory/envelope/9" {
		t.Fatalf("decoy creation path = %q", path)
	}

	// The recipient rejects the replaced wrapping instead of unfolding it; the
	// genuine token remains untouched and usable.
	if _, _, err := store.Unwrap(genuine); err != nil {
		t.Fatalf("genuine token should remain usable, got %v", err)
	}

	if _, err := store.Lookup("never-issued-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token Lookup error = %v, want ErrNotFound", err)
	}
	if _, _, err := store.Unwrap("never-issued-token"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown token Unwrap error = %v, want ErrNotFound", err)
	}
}

func TestRewrapRevokesOldTokenAndPreservesPayload(t *testing.T) {
	store := New()
	secret := []byte("rotating-wrapping")
	creationPath := "vault/rotations/path/1"
	oldToken, _, err := store.Wrap(secret, creationPath)
	if err != nil {
		t.Fatalf("Wrap failed: %v", err)
	}

	newToken, path, err := store.Rewrap(oldToken)
	if err != nil {
		t.Fatalf("Rewrap failed: %v", err)
	}
	if newToken == "" || newToken == oldToken {
		t.Fatalf("Rewrap token = %q, want a fresh distinct token", newToken)
	}
	if path != creationPath {
		t.Fatalf("Rewrap path = %q, want %q", path, creationPath)
	}

	if _, err := store.Lookup(oldToken); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("old token Lookup error = %v, want ErrSuspectedInterception", err)
	}
	if _, _, err := store.Unwrap(oldToken); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("old token Unwrap error = %v, want ErrSuspectedInterception", err)
	}

	gotSecret, gotPath, err := store.Unwrap(newToken)
	if err != nil {
		t.Fatalf("new token Unwrap failed: %v", err)
	}
	if !bytes.Equal(gotSecret, secret) {
		t.Fatalf("new token secret = %q, want %q", gotSecret, secret)
	}
	if gotPath != creationPath {
		t.Fatalf("new token path = %q, want %q", gotPath, creationPath)
	}

	if _, _, err := store.Rewrap(oldToken); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("Rewrap revoked token error = %v, want ErrSuspectedInterception", err)
	}
	if _, _, err := store.Rewrap("unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Rewrap unknown token error = %v, want ErrNotFound", err)
	}
}

func TestTokenInvalidFromExpirySecond(t *testing.T) {
	clock := newFakeClock()
	store := New(WithClock(clock.now), WithTTL(2*time.Second))
	secret := []byte("time-bomb-secret")
	creationPath := "vault/expiry/path/3"
	token, _, err := store.Wrap(secret, creationPath)
	if err != nil {
		t.Fatalf("Wrap failed: %v", err)
	}

	clock.advance(time.Second)
	if path, err := store.Lookup(token); err != nil || path != creationPath {
		t.Fatalf("token at second 1: path=%q err=%v", path, err)
	}

	clock.advance(time.Second)
	if _, err := store.Lookup(token); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("Lookup at expiry second error = %v, want ErrSuspectedInterception", err)
	}
	if _, _, err := store.Unwrap(token); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("Unwrap at expiry second error = %v, want ErrSuspectedInterception", err)
	}

	clock.advance(time.Hour)
	if _, _, err := store.Unwrap(token); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("Unwrap long after expiry error = %v, want ErrSuspectedInterception", err)
	}
}

func TestRewrapRenewsExpiryWindow(t *testing.T) {
	clock := newFakeClock()
	store := New(WithClock(clock.now), WithTTL(2*time.Second))
	token, _, err := store.Wrap([]byte("renewed"), "vault/renewal/path/4")
	if err != nil {
		t.Fatalf("Wrap failed: %v", err)
	}

	clock.advance(time.Second)
	newToken, _, err := store.Rewrap(token)
	if err != nil {
		t.Fatalf("Rewrap at second 1 failed: %v", err)
	}

	clock.advance(time.Second)
	if _, _, err := store.Unwrap(token); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("old token at original expiry error = %v", err)
	}
	if _, err := store.Lookup(newToken); err != nil {
		t.Fatalf("rewrapped token at original expiry: %v", err)
	}

	clock.advance(time.Second)
	if _, _, err := store.Unwrap(newToken); !errors.Is(err, ErrSuspectedInterception) {
		t.Fatalf("new token after its own expiry error = %v", err)
	}
}
