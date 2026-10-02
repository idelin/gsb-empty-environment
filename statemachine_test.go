package sm

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

var testCfg = Config{NumPartitions: 2, MaxPending: 64, ReorderWindow: 64}

// keyForPartition finds a key that hashes into partition p.
func keyForPartition(s *StateMachine, p int, prefix string) string {
	for i := 0; ; i++ {
		k := fmt.Sprintf("%s-%d", prefix, i)
		if s.PartitionOf(k) == p {
			return k
		}
	}
}

// shuffled returns a deterministic permutation of events for the seed.
func shuffled(events []Event, seed int64) []Event {
	out := make([]Event, len(events))
	copy(out, events)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func mustReplay(t *testing.T, s *StateMachine, events []Event) {
	t.Helper()
	if err := s.Replay(events); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

// Acceptance boundary 1: T1 and T2 run concurrently across 2 partitions
// with interleaved prepare/commit; injecting out-of-order network packets
// must yield the same final key-value state on every replica.
func TestOutOfOrderDeterminism(t *testing.T) {
	probe := New(testCfg)
	kA := keyForPartition(probe, 0, "a") // partition 0, contested key
	kB := keyForPartition(probe, 1, "b") // partition 1, T1 only
	kC := keyForPartition(probe, 1, "c") // partition 1, T2 only

	// Interleaved two-phase events: T1 and T2 both write kA (conflict),
	// each touches both partitions' keys across the log.
	log := []Event{
		{LSN: 1, TxnID: "T1", Kind: KindPrepare, Writes: map[string]string{kA: "v1", kB: "v1"}},
		{LSN: 2, TxnID: "T2", Kind: KindPrepare, Writes: map[string]string{kA: "v2", kC: "v2"}},
		{LSN: 3, TxnID: "T1", Kind: KindCommit},
		{LSN: 4, TxnID: "T2", Kind: KindCommit},
	}

	// Reference replica: in-order delivery.
	ref := New(testCfg)
	mustReplay(t, ref, log)

	// Deterministic tie-break: T2 committed later, wins kA.
	want := map[string]string{kA: "v2", kB: "v1", kC: "v2"}
	for k, v := range want {
		if got, _ := ref.Get(k); got != v {
			t.Fatalf("key %s = %q, want %q", k, got, v)
		}
	}

	// Replicas receiving shuffled packets must converge to the same state
	// and emit the same audit evidence.
	for seed := int64(0); seed < 50; seed++ {
		replica := New(testCfg)
		mustReplay(t, replica, shuffled(log, seed))
		if replica.State() != ref.State() {
			t.Fatalf("seed %d: divergent state:\ngot:\n%s\nwant:\n%s", seed, replica.State(), ref.State())
		}
		if !reflect.DeepEqual(replica.Evidence(), ref.Evidence()) {
			t.Fatalf("seed %d: divergent evidence: %+v vs %+v", seed, replica.Evidence(), ref.Evidence())
		}
	}
}

// Pending write-write conflicts must produce auditable tie-break evidence.
func TestPendingConflictEvidence(t *testing.T) {
	probe := New(testCfg)
	kA := keyForPartition(probe, 0, "a")

	s := New(testCfg)
	mustReplay(t, s, []Event{
		{LSN: 1, TxnID: "T1", Kind: KindPrepare, Writes: map[string]string{kA: "v1"}},
		{LSN: 2, TxnID: "T2", Kind: KindPrepare, Writes: map[string]string{kA: "v2"}},
		{LSN: 3, TxnID: "T1", Kind: KindCommit},
		{LSN: 4, TxnID: "T2", Kind: KindCommit},
	})

	ev := s.Evidence()
	if len(ev) != 1 {
		t.Fatalf("want exactly 1 evidence record, got %d: %+v", len(ev), ev)
	}
	e := ev[0]
	if e.Key != kA || e.WinnerTxn != "T2" || e.LoserTxn != "T1" ||
		e.WinnerLSN != 4 || e.LoserLSN != 3 || e.Rule != RuleCommitLSNOrder || e.Value != "v2" {
		t.Fatalf("unexpected evidence: %+v", e)
	}
	// Evidence must be JSON-auditable.
	if _, err := json.Marshal(e); err != nil {
		t.Fatalf("evidence not serializable: %v", err)
	}
	if got, _ := s.Get(kA); got != "v2" {
		t.Fatalf("key %s = %q, want v2", kA, got)
	}
}

// A committed txn must wait for a conflicting still-prepared txn
// (dependency graph ordering), not apply past it.
func TestCommitWaitsForPendingConflict(t *testing.T) {
	probe := New(testCfg)
	kA := keyForPartition(probe, 0, "a")

	s := New(testCfg)
	mustReplay(t, s, []Event{
		{LSN: 1, TxnID: "T1", Kind: KindPrepare, Writes: map[string]string{kA: "v1"}},
		{LSN: 2, TxnID: "T2", Kind: KindPrepare, Writes: map[string]string{kA: "v2"}},
		{LSN: 3, TxnID: "T1", Kind: KindCommit},
	})
	if _, ok := s.Get(kA); ok {
		t.Fatal("T1 applied while conflicting T2 still pending")
	}
	if s.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", s.Pending())
	}
	// Aborting T2 unblocks T1.
	mustReplay(t, s, []Event{{LSN: 4, TxnID: "T2", Kind: KindAbort}})
	if got, _ := s.Get(kA); got != "v1" {
		t.Fatalf("key %s = %q, want v1 after T2 abort", kA, got)
	}
	if len(s.Evidence()) != 0 {
		t.Fatalf("no conflict resolved, want no evidence, got %+v", s.Evidence())
	}
}

// Acceptance boundary 2: after a snapshot, replaying the remaining log
// suffix must reproduce the exact state of a full in-order run.
func TestSnapshotReplayConsistency(t *testing.T) {
	probe := New(testCfg)
	kA := keyForPartition(probe, 0, "a")
	kB := keyForPartition(probe, 1, "b")
	kC := keyForPartition(probe, 0, "c")

	log := []Event{
		{LSN: 1, TxnID: "T1", Kind: KindPrepare, Writes: map[string]string{kA: "1", kB: "1"}},
		{LSN: 2, TxnID: "T2", Kind: KindPrepare, Writes: map[string]string{kA: "2", kC: "2"}},
		{LSN: 3, TxnID: "T1", Kind: KindCommit},
		{LSN: 4, TxnID: "T2", Kind: KindCommit},
		{LSN: 5, TxnID: "T3", Kind: KindPrepare, Writes: map[string]string{kB: "3"}},
		{LSN: 6, TxnID: "T3", Kind: KindCommit},
		{LSN: 7, TxnID: "T4", Kind: KindPrepare, Writes: map[string]string{kC: "4"}},
		{LSN: 8, TxnID: "T4", Kind: KindAbort},
	}

	ref := New(testCfg)
	mustReplay(t, ref, log)

	// Cut at every possible slice point, restore, replay the suffix
	// (shuffled, to simulate out-of-order redelivery after crash).
	for cut := 0; cut <= len(log); cut++ {
		pre := New(testCfg)
		mustReplay(t, pre, log[:cut])
		snap := pre.Snapshot()
		pre.Compact(snap.ExpectLSN) // bound log memory at the slice point

		recovered := Restore(snap, Config{MaxPending: 64, ReorderWindow: 64})
		mustReplay(t, recovered, shuffled(log[cut:], int64(cut)))

		if recovered.State() != ref.State() {
			t.Fatalf("cut=%d: divergent state after recovery:\ngot:\n%s\nwant:\n%s",
				cut, recovered.State(), ref.State())
		}
		if !reflect.DeepEqual(recovered.Evidence(), ref.Evidence()) {
			t.Fatalf("cut=%d: divergent evidence: %+v vs %+v",
				cut, recovered.Evidence(), ref.Evidence())
		}
	}
}

// A snapshot taken while a transaction is mid-two-phase-commit must
// resume exactly after restore.
func TestSnapshotWithInFlightTxn(t *testing.T) {
	probe := New(testCfg)
	kA := keyForPartition(probe, 0, "a")

	log := []Event{
		{LSN: 1, TxnID: "T1", Kind: KindPrepare, Writes: map[string]string{kA: "v1"}},
		{LSN: 2, TxnID: "T2", Kind: KindPrepare, Writes: map[string]string{kA: "v2"}},
		{LSN: 3, TxnID: "T1", Kind: KindCommit}, // blocked by pending T2
		{LSN: 4, TxnID: "T2", Kind: KindCommit},
	}

	pre := New(testCfg)
	mustReplay(t, pre, log[:3]) // snapshot mid-flight: T1 committed-but-blocked, T2 prepared
	snap := pre.Snapshot()

	recovered := Restore(snap, Config{})
	mustReplay(t, recovered, shuffled(log[3:], 7))

	ref := New(testCfg)
	mustReplay(t, ref, log)
	if recovered.State() != ref.State() {
		t.Fatalf("divergent state:\ngot:\n%s\nwant:\n%s", recovered.State(), ref.State())
	}
	if !reflect.DeepEqual(recovered.Evidence(), ref.Evidence()) {
		t.Fatalf("divergent evidence: %+v vs %+v", recovered.Evidence(), ref.Evidence())
	}
}

// Memory bounds: pending-txn limit and reorder-window limit must be
// enforced with backpressure errors instead of unbounded growth.
func TestBoundedMemory(t *testing.T) {
	probe := New(testCfg)
	kA := keyForPartition(probe, 0, "a")
	kB := keyForPartition(probe, 1, "b")

	s := New(Config{NumPartitions: 2, MaxPending: 1, ReorderWindow: 2})
	if err := s.Deliver(Event{LSN: 1, TxnID: "T1", Kind: KindPrepare, Writes: map[string]string{kA: "1"}}); err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	err := s.Deliver(Event{LSN: 2, TxnID: "T2", Kind: KindPrepare, Writes: map[string]string{kB: "2"}})
	if err != ErrPendingLimit {
		t.Fatalf("want ErrPendingLimit, got %v", err)
	}

	// Reorder window: buffer two gapped packets, the third overflows.
	r := New(Config{NumPartitions: 2, MaxPending: 8, ReorderWindow: 2})
	if err := r.Deliver(Event{LSN: 3, TxnID: "T3", Kind: KindCommit}); err != nil {
		t.Fatalf("buffer LSN 3: %v", err)
	}
	if err := r.Deliver(Event{LSN: 4, TxnID: "T4", Kind: KindCommit}); err != nil {
		t.Fatalf("buffer LSN 4: %v", err)
	}
	if err := r.Deliver(Event{LSN: 5, TxnID: "T5", Kind: KindCommit}); err != ErrReorderWindowFull {
		t.Fatalf("want ErrReorderWindowFull, got %v", err)
	}

	// Log compaction bounds log memory after a snapshot.
	c := New(testCfg)
	mustReplay(t, c, []Event{
		{LSN: 1, TxnID: "T1", Kind: KindPrepare, Writes: map[string]string{kA: "1"}},
		{LSN: 2, TxnID: "T1", Kind: KindCommit},
	})
	snap := c.Snapshot()
	c.Compact(snap.ExpectLSN)
	if len(c.Log()) != 0 {
		t.Fatalf("log not compacted, %d entries remain", len(c.Log()))
	}
}

// Duplicate redelivery of applied events must be idempotent.
func TestIdempotentRedelivery(t *testing.T) {
	probe := New(testCfg)
	kA := keyForPartition(probe, 0, "a")

	s := New(testCfg)
	e1 := Event{LSN: 1, TxnID: "T1", Kind: KindPrepare, Writes: map[string]string{kA: "v1"}}
	e2 := Event{LSN: 2, TxnID: "T1", Kind: KindCommit}
	mustReplay(t, s, []Event{e1, e2, e1, e2, e1}) // re-deliver applied LSNs
	if got, _ := s.Get(kA); got != "v1" {
		t.Fatalf("key %s = %q, want v1", kA, got)
	}
	if s.Pending() != 0 {
		t.Fatalf("pending = %d, want 0", s.Pending())
	}
}
