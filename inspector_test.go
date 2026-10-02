package multipaxos

import (
	"strings"
	"testing"
)

// cleanSnapshots builds n identical, fully-converged snapshots.
func cleanSnapshots(n, slots int) []Snapshot {
	out := make([]Snapshot, n)
	for i := 0; i < n; i++ {
		s := Snapshot{
			ID:          i + 1,
			Promised:    Ballot{N: 7, Node: 1},
			Log:         make(map[int]Entry),
			Committed:   make(map[int]bool),
			CommitIndex: slots,
		}
		for slot := 1; slot <= slots; slot++ {
			s.Log[slot] = Entry{Slot: slot, Value: "v", Ballot: Ballot{N: 7, Node: 1}}
			s.Committed[slot] = true
		}
		out[i] = s
	}
	return out
}

func hasKind(vs []Violation, k Kind) bool {
	for _, v := range vs {
		if v.Kind == k {
			return true
		}
	}
	return false
}

func TestInspectorClean(t *testing.T) {
	if vs := NewInspector().Inspect(nil); len(vs) != 0 {
		t.Fatalf("empty cluster: %v", vs)
	}
	if vs := NewInspector().Inspect(cleanSnapshots(1, 0)); len(vs) != 0 {
		t.Fatalf("single empty node: %v", vs)
	}
	if vs := NewInspector().Inspect(cleanSnapshots(5, 10)); len(vs) != 0 {
		t.Fatalf("converged cluster: %v", vs)
	}
}

func TestInspectorCommittedDivergence(t *testing.T) {
	snaps := cleanSnapshots(3, 3)
	e := snaps[1].Log[2]
	e.Value = "different"
	snaps[1].Log[2] = e
	vs := NewInspector().Inspect(snaps)
	if !hasKind(vs, KindCommittedDivergence) {
		t.Fatalf("divergence not detected: %v", vs)
	}
}

func TestInspectorCommitGap(t *testing.T) {
	snaps := cleanSnapshots(3, 3)
	// Hole: committed slot 3 without slot 2.
	delete(snaps[0].Committed, 2)
	delete(snaps[0].Log, 2)
	vs := NewInspector().Inspect(snaps)
	if !hasKind(vs, KindCommitGap) {
		t.Fatalf("commit gap not detected: %v", vs)
	}
	// Inconsistent commit index.
	snaps = cleanSnapshots(3, 3)
	snaps[0].CommitIndex = 2
	vs = NewInspector().Inspect(snaps)
	if !hasKind(vs, KindCommitGap) {
		t.Fatalf("commitIndex mismatch not detected: %v", vs)
	}
	// Committed slot with no log entry.
	snaps = cleanSnapshots(3, 3)
	delete(snaps[0].Log, 2)
	vs = NewInspector().Inspect(snaps)
	if !hasKind(vs, KindCommitGap) {
		t.Fatalf("missing committed entry not detected: %v", vs)
	}
}

func TestInspectorDanglingAndFutureRefs(t *testing.T) {
	snaps := cleanSnapshots(3, 3)
	for i := range snaps {
		e := snaps[i].Log[3]
		e.Refs = []int{2} // slot 2 is committed -> fine
		snaps[i].Log[3] = e
	}
	if vs := NewInspector().Inspect(snaps); len(vs) != 0 {
		t.Fatalf("valid refs flagged: %v", vs)
	}
	// Dangling: slot 2 exists in the log but is not committed, and the
	// committed entry at slot 3 references it.
	for i := range snaps {
		snaps[i].Committed[2] = false
		delete(snaps[i].Committed, 2)
		e := snaps[i].Log[3]
		e.Refs = []int{2}
		snaps[i].Log[3] = e
		snaps[i].CommitIndex = 1
		delete(snaps[i].Committed, 3)
		delete(snaps[i].Log, 3)
		snaps[i].Log[2] = Entry{Slot: 2, Value: "pending", Ballot: Ballot{N: 7, Node: 1}}
		snaps[i].Log[3] = Entry{Slot: 3, Value: "v", Refs: []int{2}, Ballot: Ballot{N: 7, Node: 1}}
		snaps[i].Committed[3] = true
	}
	vs := NewInspector().Inspect(snaps)
	if !hasKind(vs, KindDanglingRef) {
		t.Fatalf("dangling ref not detected: %v", vs)
	}
	// Future/self reference.
	snaps = cleanSnapshots(3, 3)
	for i := range snaps {
		e := snaps[i].Log[2]
		e.Refs = []int{3}
		snaps[i].Log[2] = e
	}
	vs = NewInspector().Inspect(snaps)
	if !hasKind(vs, KindFutureRef) {
		t.Fatalf("future ref not detected: %v", vs)
	}
}

func TestInspectorRefCycle(t *testing.T) {
	snaps := cleanSnapshots(3, 4)
	for i := range snaps {
		e2 := snaps[i].Log[2]
		e2.Refs = []int{4}
		snaps[i].Log[2] = e2
		e4 := snaps[i].Log[4]
		e4.Refs = []int{2}
		snaps[i].Log[4] = e4
	}
	vs := NewInspector().Inspect(snaps)
	if !hasKind(vs, KindRefCycle) {
		t.Fatalf("ref cycle not detected: %v", vs)
	}
	for _, v := range vs {
		if v.Kind == KindRefCycle && !strings.Contains(v.Detail, "->") {
			t.Fatalf("cycle detail missing path: %q", v.Detail)
		}
	}
}

func TestInspectorBallotAnomaly(t *testing.T) {
	snaps := cleanSnapshots(3, 3)
	e := snaps[0].Log[2]
	e.Ballot = Ballot{N: 99, Node: 2} // exceeds promised (7,1)
	snaps[0].Log[2] = e
	vs := NewInspector().Inspect(snaps)
	if !hasKind(vs, KindBallotAnomaly) {
		t.Fatalf("ballot anomaly not detected: %v", vs)
	}
}

func TestInspectorMinorityCommit(t *testing.T) {
	snaps := cleanSnapshots(5, 3)
	// Only 2 of 5 nodes store the committed value of slot 2.
	for _, i := range []int{0, 1, 2} {
		e := snaps[i].Log[2]
		e.Value = "other"
		snaps[i].Log[2] = e
		snaps[i].Committed[2] = false
		delete(snaps[i].Committed, 2)
		delete(snaps[i].Log, 2)
		snaps[i].CommitIndex = 1
		delete(snaps[i].Committed, 3)
		delete(snaps[i].Log, 3)
	}
	vs := NewInspector().Inspect(snaps)
	if !hasKind(vs, KindMinorityCommit) {
		t.Fatalf("minority commit not detected: %v", vs)
	}
}

func TestInspectorDataSkew(t *testing.T) {
	snaps := cleanSnapshots(5, 10)
	// Node 5 lags badly.
	for slot := 1; slot <= 10; slot++ {
		delete(snaps[4].Log, slot)
		delete(snaps[4].Committed, slot)
	}
	snaps[4].CommitIndex = 0
	vs := NewInspector().Inspect(snaps)
	if !hasKind(vs, KindDataSkew) {
		t.Fatalf("data skew not detected: %v", vs)
	}
	// With a generous tolerance the same state passes.
	vs = (Inspector{SkewTolerance: 2.0}).Inspect(snaps)
	if hasKind(vs, KindDataSkew) {
		t.Fatalf("tolerance not honored: %v", vs)
	}
}

func TestInspectorDeterministicOrder(t *testing.T) {
	snaps := cleanSnapshots(3, 3)
	e := snaps[1].Log[2]
	e.Value = "different"
	snaps[1].Log[2] = e
	snaps[0].CommitIndex = 1
	a := NewInspector().Inspect(snaps)
	b := NewInspector().Inspect(snaps)
	if len(a) != len(b) {
		t.Fatal("non-deterministic violation count")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic order: %v vs %v", a, b)
		}
	}
}
