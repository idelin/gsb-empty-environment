package idemexec

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

// countingEffect increments a durable counter and derives its result from
// it, so tests can prove the effect was applied exactly once and that any
// replayed result came from that single committed execution.
func countingEffect(tx Tx) ([]byte, error) {
	n := 0
	if v, ok := tx.EffectGet("counters", "execs"); ok {
		n, _ = strconv.Atoi(string(v))
	}
	n++
	tx.EffectPut("counters", "execs", []byte(strconv.Itoa(n)))
	return []byte("result-" + strconv.Itoa(n)), nil
}

func effectCount(t *testing.T, e *Executor) int {
	t.Helper()
	n := 0
	err := e.store.View(func(tx Tx) error {
		if v, ok := tx.EffectGet("counters", "execs"); ok {
			n, _ = strconv.Atoi(string(v))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDoCachesResult(t *testing.T) {
	e := NewExecutor(NewMemStore(nil), 0)
	r1, err := e.Do("k", []byte("p"), countingEffect)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e.Do("k", []byte("p"), countingEffect)
	if err != nil {
		t.Fatal(err)
	}
	if string(r1) != "result-1" || string(r2) != "result-1" {
		t.Fatalf("got %q then %q, want cached result-1 both times", r1, r2)
	}
	if n := effectCount(t, e); n != 1 {
		t.Fatalf("effect applied %d times, want exactly once", n)
	}
}

func TestParamsMismatchRejected(t *testing.T) {
	e := NewExecutor(NewMemStore(nil), 0)
	if _, err := e.Do("k", []byte("params-a"), countingEffect); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Do("k", []byte("params-b"), countingEffect); err != ErrParamsMismatch {
		t.Fatalf("got %v, want ErrParamsMismatch", err)
	}
	if n := effectCount(t, e); n != 1 {
		t.Fatalf("effect applied %d times, want exactly once", n)
	}
	// A different key with those same parameters is unaffected.
	if _, err := e.Do("other", []byte("params-b"), countingEffect); err != nil {
		t.Fatal(err)
	}
}

func TestFailedEffectNotRecorded(t *testing.T) {
	e := NewExecutor(NewMemStore(nil), 0)
	boom := errors.New("boom")
	if _, err := e.Do("k", []byte("p"), func(Tx) ([]byte, error) { return nil, boom }); err != boom {
		t.Fatalf("got %v, want boom", err)
	}
	if _, found, err := e.Lookup("k"); err != nil || found {
		t.Fatalf("failed execution left a record behind: found=%v err=%v", found, err)
	}
	// The key is still free: a retry executes and commits.
	r, err := e.Do("k", []byte("p"), countingEffect)
	if err != nil || string(r) != "result-1" {
		t.Fatalf("retry got %q, %v", r, err)
	}
}

func TestExpiryForbidsReplay(t *testing.T) {
	now := time.Unix(1000, 0)
	e := NewExecutor(NewMemStore(nil), time.Minute)
	e.SetClock(func() time.Time { return now })

	if _, err := e.Do("k", []byte("p"), countingEffect); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)

	if _, err := e.Do("k", []byte("p"), countingEffect); err != ErrTombstoned {
		t.Fatalf("got %v, want ErrTombstoned", err)
	}
	rec, found, err := e.Lookup("k")
	if err != nil || !found {
		t.Fatalf("tombstone missing: found=%v err=%v", found, err)
	}
	if rec.State != StateTombstone {
		t.Fatalf("state = %v, want StateTombstone", rec.State)
	}
	if rec.Result != nil {
		t.Fatalf("tombstone still holds result payload %q", rec.Result)
	}
	if _, err := e.Do("k", []byte("other"), countingEffect); err != ErrParamsMismatch {
		t.Fatalf("mismatched params on tombstone: got %v, want ErrParamsMismatch", err)
	}
	if n := effectCount(t, e); n != 1 {
		t.Fatalf("effect applied %d times, want exactly once", n)
	}
}

func TestSweepTombstonesExpiredKeepsFresh(t *testing.T) {
	now := time.Unix(1000, 0)
	e := NewExecutor(NewMemStore(nil), time.Minute)
	e.SetClock(func() time.Time { return now })

	if _, err := e.Do("old", []byte("p"), countingEffect); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := e.Do("fresh", []byte("p"), countingEffect); err != nil {
		t.Fatal(err)
	}

	swept, err := e.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if swept != 1 {
		t.Fatalf("swept %d records, want 1", swept)
	}
	rec, _, err := e.Lookup("old")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != StateTombstone || rec.Result != nil {
		t.Fatalf("old record not tombstoned cleanly: %+v", rec)
	}
	// The fresh record still replays its cached result.
	r, err := e.Do("fresh", []byte("p"), countingEffect)
	if err != nil || string(r) != "result-2" {
		t.Fatalf("fresh replay got %q, %v", r, err)
	}
	// Sweep is idempotent and never deletes tombstones.
	swept, err = e.Sweep()
	if err != nil || swept != 0 {
		t.Fatalf("second sweep: swept=%d err=%v, want 0", swept, err)
	}
	if _, found, _ := e.Lookup("old"); !found {
		t.Fatal("sweep deleted the tombstone")
	}
	if _, err := e.Do("old", []byte("p"), countingEffect); err != ErrTombstoned {
		t.Fatalf("replayed swept key: got %v, want ErrTombstoned", err)
	}
	if n := effectCount(t, e); n != 2 {
		t.Fatalf("effect applied %d times, want 2", n)
	}
}

func TestEmptyKeyRejected(t *testing.T) {
	e := NewExecutor(NewMemStore(nil), 0)
	if _, err := e.Do("", []byte("p"), countingEffect); err != ErrEmptyKey {
		t.Fatalf("got %v, want ErrEmptyKey", err)
	}
}
