package idem

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testParams = map[string]any{"to": "alice", "amount": 100}

// countingEffect increments the "count" business key; the number of times
// it is *committed* is the exactly-once signal.
func countingEffect(delta int) Effect {
	return func(kv KV) error {
		raw, _ := kv.Get("count")
		n, _ := strconv.Atoi(raw)
		kv.Set("count", strconv.Itoa(n+delta))
		return nil
	}
}

func effectCount(t *testing.T, s *MemoryStore) int {
	t.Helper()
	v, _, err := s.Effect("count")
	if err != nil {
		t.Fatalf("read effect: %v", err)
	}
	n, _ := strconv.Atoi(v)
	return n
}

// runStep retries a store-touching step across a simulated crash: restart
// from durable state and try again, which is exactly what a real client
// does after losing a receipt.
func runStep(store *MemoryStore, afterCrash func(), step func() error) error {
	for {
		err := step()
		if errors.Is(err, ErrCrash) {
			store.Restart()
			if afterCrash != nil {
				afterCrash()
			}
			continue
		}
		return err
	}
}

// TestExactlyOnce_CutPointEnumeration crashes the process at every
// persistence cut point of a single execution, both before and after the
// write lands, and verifies the business effect is committed exactly once
// and the result is always recoverable.
func TestExactlyOnce_CutPointEnumeration(t *testing.T) {
	const lease = 20 * time.Millisecond

	total := func() int {
		store := NewMemoryStore()
		exec := NewExecutor(store, time.Hour, lease, WithPollInterval(time.Millisecond))
		_, err := exec.Do(context.Background(), "id-1", testParams,
			func() (Effect, []byte, error) { return countingEffect(1), []byte("R"), nil })
		if err != nil {
			t.Fatalf("baseline run: %v", err)
		}
		return store.Cuts()
	}()

	for cut := 1; cut <= total; cut++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("cut=%d/after=%v", cut, after), func(t *testing.T) {
				store := NewMemoryStore()
				exec := NewExecutor(store, time.Hour, lease, WithPollInterval(time.Millisecond))
				store.ArmCrash(cut, after)

				var result []byte
				err := runStep(store, nil, func() error {
					var err error
					result, err = exec.Do(context.Background(), "id-1", testParams,
						func() (Effect, []byte, error) { return countingEffect(1), []byte("R"), nil })
					return err
				})
				if err != nil {
					t.Fatalf("Do: %v", err)
				}
				if string(result) != "R" {
					t.Fatalf("result = %q, want R", result)
				}
				if got := effectCount(t, store); got != 1 {
					t.Fatalf("effect committed %d times, want exactly 1", got)
				}
				rec, found, err := store.Get("id-1")
				if err != nil || !found {
					t.Fatalf("record missing: found=%v err=%v", found, err)
				}
				if rec.State != StateCompleted {
					t.Fatalf("state = %v, want completed", rec.State)
				}
			})
		}
	}
}

// TestLifecycle_CutPointEnumeration enumerates cut points across the whole
// lifecycle — execute, expire, sweep — and verifies the tombstone left
// behind rejects replay while the effect stays committed exactly once.
func TestLifecycle_CutPointEnumeration(t *testing.T) {
	const (
		ttl   = time.Hour
		lease = time.Minute
	)

	scenario := func(t *testing.T, store *MemoryStore, exec *Executor, now *time.Time) {
		ctx := context.Background()
		advance := func() { *now = now.Add(2 * lease) } // restart takes time: leases lapse

		err := runStep(store, advance, func() error {
			_, err := exec.Do(ctx, "id-1", testParams,
				func() (Effect, []byte, error) { return countingEffect(1), []byte("R"), nil })
			return err
		})
		if err != nil {
			t.Fatalf("execute: %v", err)
		}

		*now = now.Add(2 * ttl) // let the result expire
		var swept int
		err = runStep(store, advance, func() error {
			var err error
			swept, err = exec.Sweep()
			return err
		})
		if err != nil {
			t.Fatalf("sweep: %v", err)
		}
		// A crash-after on the sweep cut point may have already swept the
		// record before its receipt was lost, so a retry legitimately
		// reports 0. The final tombstone assertion below is the invariant.
		if swept > 1 {
			t.Fatalf("swept = %d, want at most 1", swept)
		}

		if _, err := exec.Do(ctx, "id-1", testParams,
			func() (Effect, []byte, error) { return countingEffect(1), []byte("R"), nil }); !errors.Is(err, ErrExpired) {
			t.Fatalf("replay same params: err = %v, want ErrExpired", err)
		}
		if _, err := exec.Do(ctx, "id-1", map[string]any{"to": "bob", "amount": 5},
			func() (Effect, []byte, error) { return countingEffect(1), []byte("R"), nil }); !errors.Is(err, ErrFingerprintMismatch) {
			t.Fatalf("replay different params: err = %v, want ErrFingerprintMismatch", err)
		}
	}

	// Baseline pass to count the persistence cut points of the scenario.
	total := func() int {
		store := NewMemoryStore()
		now := time.Unix(1_700_000_000, 0)
		exec := NewExecutor(store, ttl, lease, WithNow(func() time.Time { return now }))
		scenario(t, store, exec, &now)
		return store.Cuts()
	}()

	for cut := 1; cut <= total; cut++ {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("cut=%d/after=%v", cut, after), func(t *testing.T) {
				store := NewMemoryStore()
				now := time.Unix(1_700_000_000, 0)
				exec := NewExecutor(store, ttl, lease, WithNow(func() time.Time { return now }))
				store.ArmCrash(cut, after)

				scenario(t, store, exec, &now)

				if got := effectCount(t, store); got != 1 {
					t.Fatalf("effect committed %d times, want exactly 1", got)
				}
				rec, found, err := store.Get("id-1")
				if err != nil || !found {
					t.Fatalf("record missing: found=%v err=%v", found, err)
				}
				if rec.State != StateTombstone {
					t.Fatalf("state = %v, want tombstone", rec.State)
				}
				if rec.Result != nil {
					t.Fatalf("tombstone still holds result %q", rec.Result)
				}
			})
		}
	}
}

// TestConcurrentDuplicates hammers one id from many goroutines: exactly one
// execution happens, every duplicate with equal params receives the cached
// result, and every duplicate with different params is rejected.
func TestConcurrentDuplicates(t *testing.T) {
	store := NewMemoryStore()
	exec := NewExecutor(store, time.Hour, 50*time.Millisecond, WithPollInterval(time.Millisecond))
	ctx := context.Background()

	const workers = 64
	var fnCalls atomic.Int64
	var wg sync.WaitGroup
	errs := make([]error, workers)
	results := make([][]byte, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			params := testParams
			if i%2 == 1 {
				params = map[string]any{"to": "mallory", "amount": 999}
			}
			res, err := exec.Do(ctx, "id-c", params, func() (Effect, []byte, error) {
				fnCalls.Add(1)
				return countingEffect(1), []byte("R"), nil
			})
			errs[i] = err
			results[i] = res
		}(i)
	}
	wg.Wait()

	// The id may be claimed by either param variant; whichever fingerprint
	// won, its duplicates converge and the others are rejected.
	rec, _, err := store.Get("id-c")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	winFP, _ := Fingerprint(testParams)
	winnerIsHonest := rec.Fingerprint == winFP

	for i := 0; i < workers; i++ {
		honest := i%2 == 0
		if honest == winnerIsHonest {
			if errs[i] != nil || string(results[i]) != "R" {
				t.Fatalf("worker %d: res=%q err=%v, want cached R", i, results[i], errs[i])
			}
		} else if !errors.Is(errs[i], ErrFingerprintMismatch) {
			t.Fatalf("worker %d: err=%v, want ErrFingerprintMismatch", i, errs[i])
		}
	}
	if got := fnCalls.Load(); got != 1 {
		t.Fatalf("fn executed %d times, want 1", got)
	}
	if got := effectCount(t, store); got != 1 {
		t.Fatalf("effect committed %d times, want exactly 1", got)
	}
}

// TestExpiryTombstone covers the cleanup path without crashes: results are
// served until the TTL, sweeping leaves a non-replayable tombstone, and the
// tombstone still detects parameter mismatches.
func TestExpiryTombstone(t *testing.T) {
	store := NewMemoryStore()
	now := time.Unix(1_700_000_000, 0)
	exec := NewExecutor(store, time.Hour, time.Minute, WithNow(func() time.Time { return now }))
	ctx := context.Background()

	res, err := exec.Do(ctx, "id-1", testParams,
		func() (Effect, []byte, error) { return countingEffect(1), []byte("R"), nil })
	if err != nil || string(res) != "R" {
		t.Fatalf("execute: res=%q err=%v", res, err)
	}

	// Duplicate within TTL is served from cache.
	if res, err := exec.Do(ctx, "id-1", testParams, nil); err != nil || string(res) != "R" {
		t.Fatalf("cached: res=%q err=%v", res, err)
	}

	// Not yet expired: sweep keeps the record completed.
	now = now.Add(30 * time.Minute)
	if n, err := exec.Sweep(); err != nil || n != 0 {
		t.Fatalf("early sweep: n=%d err=%v", n, err)
	}

	now = now.Add(time.Hour)
	if n, err := exec.Sweep(); err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}

	if _, err := exec.Do(ctx, "id-1", testParams,
		func() (Effect, []byte, error) { return countingEffect(1), []byte("R"), nil }); !errors.Is(err, ErrExpired) {
		t.Fatalf("replay after expiry: err=%v, want ErrExpired", err)
	}
	if _, err := exec.Do(ctx, "id-1", map[string]any{"to": "bob"},
		func() (Effect, []byte, error) { return countingEffect(1), []byte("R"), nil }); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("mismatch after expiry: err=%v, want ErrFingerprintMismatch", err)
	}
	if got := effectCount(t, store); got != 1 {
		t.Fatalf("effect committed %d times, want exactly 1", got)
	}
}

// TestLostClaimCannotCommit checks fencing: an executor whose lease lapsed
// and whose claim was taken over cannot commit its (stale) effect.
func TestLostClaimCannotCommit(t *testing.T) {
	store := NewMemoryStore()
	now := time.Unix(1_700_000_000, 0)

	fp, _ := Fingerprint(testParams)
	if _, _, err := store.Claim("id-1", fp, "stale-token", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// Lease lapses; another executor takes over and commits.
	now = now.Add(2 * time.Minute)
	if _, claimed, err := store.Claim("id-1", fp, "fresh-token", now, now.Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("takeover: claimed=%v err=%v", claimed, err)
	}
	if err := store.Commit("id-1", "fresh-token", countingEffect(1), []byte("R"), now.Add(time.Hour)); err != nil {
		t.Fatalf("fresh commit: %v", err)
	}
	// The stale executor wakes up and tries to commit: rejected.
	if err := store.Commit("id-1", "stale-token", countingEffect(1), []byte("R"), now.Add(time.Hour)); !errors.Is(err, ErrLostClaim) {
		t.Fatalf("stale commit: err=%v, want ErrLostClaim", err)
	}
	if got := effectCount(t, store); got != 1 {
		t.Fatalf("effect committed %d times, want exactly 1", got)
	}
}
