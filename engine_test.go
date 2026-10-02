package kvsm

import (
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// buildAcceptanceLog builds the acceptance scenario: concurrent transactions
// T1 and T2 spanning partitions 0 and 1, with interleaved two-phase events,
// plus a non-conflicting T3. T2's commit is logged before T1's, forcing the
// dependency resolver to order them deterministically.
func buildAcceptanceLog() *Log {
	l := NewLog()
	l.Append(Event{Kind: KindPrepare, TxnID: "T1", Parts: []int{0, 1},
		Writes: map[int]map[string]string{0: {"a": "v1", "b": "v1"}, 1: {"x": "v1"}}})
	l.Append(Event{Kind: KindPrepare, TxnID: "T2", Parts: []int{0, 1},
		Writes: map[int]map[string]string{0: {"a": "v2"}, 1: {"x": "v2", "y": "v2"}}})
	l.Append(Event{Kind: KindCommit, TxnID: "T2", Parts: []int{0, 1}}) // logged first, must defer
	l.Append(Event{Kind: KindPrepare, TxnID: "T3", Parts: []int{1},
		Writes: map[int]map[string]string{1: {"z": "v3"}}})
	l.Append(Event{Kind: KindCommit, TxnID: "T1", Parts: []int{0, 1}})
	l.Append(Event{Kind: KindCommit, TxnID: "T3", Parts: []int{1}})
	return l
}

func replayInOrder(t *testing.T, events []Event) *Engine {
	t.Helper()
	e := NewEngine(2, 0, 0)
	if err := e.DeliverAll(events); err != nil {
		t.Fatalf("in-order replay: %v", err)
	}
	return e
}

// Acceptance boundary 1: with T1/T2 concurrent across 2 partitions and
// out-of-order packet injection, every delivery order converges to the same
// final key-value state.
func TestDeterministicUnderPacketReordering(t *testing.T) {
	events := buildAcceptanceLog().Events()
	ref := replayInOrder(t, events)

	want := map[string]string{
		"0/a": "v2", "0/b": "v1", // T2 wins a (later prepare), T1's b survives
		"1/x": "v2", "1/y": "v2", "1/z": "v3",
	}
	if got := ref.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("in-order state = %v, want %v", got, want)
	}

	for seed := int64(0); seed < 200; seed++ {
		shuffled := make([]Event, len(events))
		perm := rand.New(rand.NewSource(seed)).Perm(len(events))
		for i, j := range perm {
			shuffled[i] = events[j]
		}
		e := NewEngine(2, 0, 0)
		if err := e.DeliverAll(shuffled); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if got := e.State(); !reflect.DeepEqual(got, ref.State()) {
			t.Fatalf("seed %d: state %v != reference %v", seed, got, ref.State())
		}
	}
}

// The conflict between T1 and T2 must produce auditable tie-break evidence.
func TestConflictEvidenceIsAuditable(t *testing.T) {
	events := buildAcceptanceLog().Events()
	e := replayInOrder(t, events)

	var deferEv, resolveEv *Evidence
	for i, ev := range e.Evidence() {
		switch ev.Action {
		case "defer-commit":
			deferEv = &e.Evidence()[i]
		case "apply-after-resolve":
			resolveEv = &e.Evidence()[i]
		}
	}
	if deferEv == nil || resolveEv == nil {
		t.Fatalf("missing evidence, got %+v", e.Evidence())
	}
	if deferEv.Winner != "T2" || deferEv.Loser != "T1" {
		t.Fatalf("defer evidence winner/loser = %s/%s, want T2/T1", deferEv.Winner, deferEv.Loser)
	}
	if deferEv.Rule != "prepare-order-topo" || deferEv.WinnerSeq <= deferEv.LoserSeq {
		t.Fatalf("evidence not self-auditable: %+v", deferEv)
	}
	if resolveEv.Winner != "T2" || resolveEv.Loser != "T1" {
		t.Fatalf("resolve evidence winner/loser = %s/%s, want T2/T1", resolveEv.Winner, resolveEv.Loser)
	}
	// Evidence must be deterministic across replays.
	e2 := replayInOrder(t, events)
	if !reflect.DeepEqual(e.Evidence(), e2.Evidence()) {
		t.Fatalf("evidence not deterministic:\n%+v\n%+v", e.Evidence(), e2.Evidence())
	}
}

// Acceptance boundary 2: after a snapshot, replaying the remaining log
// yields state identical to a full replay.
func TestSnapshotSlicePointReplay(t *testing.T) {
	events := buildAcceptanceLog().Events()
	full := replayInOrder(t, events)

	const cut = 3 // snapshot after seq 3 (T2's commit is deferred there)
	head := NewEngine(2, 0, 0)
	if err := head.DeliverAll(events[:cut]); err != nil {
		t.Fatalf("head: %v", err)
	}
	snap := head.Snapshot()
	if snap.LastSeq != uint64(cut) {
		t.Fatalf("snapshot LastSeq = %d, want %d", snap.LastSeq, cut)
	}

	tail := NewEngine(2, 0, 0)
	_ = tail
	restored := Restore(snap, 0, 0)
	if err := restored.DeliverAll(events[cut:]); err != nil {
		t.Fatalf("tail replay: %v", err)
	}
	if got, want := restored.State(), full.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot+replay state %v != full replay %v", got, want)
	}
}

// Crash recovery: persist snapshot + log suffix to disk, "crash", restore in
// a fresh engine, replay the suffix, and require identical state.
func TestCrashRecoveryFromDisk(t *testing.T) {
	dir, err := ioutil.TempDir("", "kvsm")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	log := buildAcceptanceLog()
	events := log.Events()

	const cut = 4
	live := NewEngine(2, 0, 0)
	if err := live.DeliverAll(events[:cut]); err != nil {
		t.Fatalf("live: %v", err)
	}
	snapPath := filepath.Join(dir, "snapshot.json")
	logPath := filepath.Join(dir, "log.json")
	if err := SaveSnapshot(snapPath, live.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := log.Truncate(uint64(cut)); err != nil {
		t.Fatal(err)
	}
	if err := SaveLog(logPath, log.Events()); err != nil {
		t.Fatal(err)
	}
	// --- simulated crash: `live` is discarded ---

	snap, err := LoadSnapshot(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	suffix, err := LoadLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range suffix {
		if ev.Seq <= snap.LastSeq {
			t.Fatalf("log suffix contains pre-snapshot event %d", ev.Seq)
		}
	}
	recovered := Restore(snap, 0, 0)
	if err := recovered.DeliverAll(suffix); err != nil {
		t.Fatalf("recover replay: %v", err)
	}

	full := replayInOrder(t, events)
	if got, want := recovered.State(), full.State(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered state %v != full replay %v", got, want)
	}
}

// Memory stays bounded: the reorder buffer rejects events beyond its window
// and log truncation drops snapshotted entries.
func TestBoundedMemory(t *testing.T) {
	e := NewEngine(2, 4, 0)
	// Deliver seq 1..4 out of order to fill the buffer, then overflow it.
	if err := e.Deliver(Event{Seq: 5, Kind: KindCommit, TxnID: "X"}); err != nil {
		t.Fatalf("buffer should accept seq 5: %v", err)
	}
	for seq := uint64(6); seq <= 8; seq++ {
		if err := e.Deliver(Event{Seq: seq, Kind: KindCommit, TxnID: "X"}); err != nil {
			t.Fatalf("buffer should accept seq %d: %v", seq, err)
		}
	}
	if err := e.Deliver(Event{Seq: 9, Kind: KindCommit, TxnID: "X"}); err == nil {
		t.Fatal("expected reorder-buffer-full error")
	}
	if n := e.BufferedCount(); n > 4 {
		t.Fatalf("buffer holds %d events, bound is 4", n)
	}

	l := buildAcceptanceLog()
	if err := l.Truncate(4); err != nil {
		t.Fatal(err)
	}
	if l.Len() != 2 {
		t.Fatalf("log retains %d events after truncate, want 2", l.Len())
	}
	if err := l.Truncate(100); err == nil {
		t.Fatal("expected error truncating beyond last event")
	}
}

// An abort must resolve pending conflicts deterministically, with evidence.
func TestAbortResolvesConflict(t *testing.T) {
	l := NewLog()
	l.Append(Event{Kind: KindPrepare, TxnID: "T1", Parts: []int{0},
		Writes: map[int]map[string]string{0: {"k": "v1"}}})
	l.Append(Event{Kind: KindPrepare, TxnID: "T2", Parts: []int{0},
		Writes: map[int]map[string]string{0: {"k": "v2"}}})
	l.Append(Event{Kind: KindCommit, TxnID: "T2", Parts: []int{0}}) // defers on T1
	l.Append(Event{Kind: KindAbort, TxnID: "T1", Parts: []int{0}})  // unblocks T2

	e := NewEngine(2, 0, 0)
	if err := e.DeliverAll(l.Events()); err != nil {
		t.Fatal(err)
	}
	if v, ok := e.Get(0, "k"); !ok || v != "v2" {
		t.Fatalf("k = %q,%v; want v2,true", v, ok)
	}
	var sawAbortEvidence bool
	for _, ev := range e.Evidence() {
		if ev.Action == "abort-loser" && ev.Winner == "T2" && ev.Loser == "T1" {
			sawAbortEvidence = true
		}
	}
	if !sawAbortEvidence {
		t.Fatalf("missing abort-loser evidence: %+v", e.Evidence())
	}
	if e.PendingCount() != 0 {
		t.Fatalf("%d transactions still pending", e.PendingCount())
	}
}

// Duplicate delivery (at-least-once transport) must be idempotent.
func TestDuplicateDeliveryIdempotent(t *testing.T) {
	events := buildAcceptanceLog().Events()
	e := NewEngine(2, 0, 0)
	for _, ev := range events {
		if err := e.Deliver(ev); err != nil {
			t.Fatal(err)
		}
		if err := e.Deliver(ev); err != nil { // duplicate
			t.Fatal(err)
		}
	}
	ref := replayInOrder(t, events)
	if !reflect.DeepEqual(e.State(), ref.State()) {
		t.Fatalf("duplicate delivery changed state: %v != %v", e.State(), ref.State())
	}
}
