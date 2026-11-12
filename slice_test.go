package netsec

import (
	"errors"
	"reflect"
	"testing"
)

// chainTrace builds: 1 -> 2 -> 3 -> 4(dangerous read of obj@8), with 5 as
// an unrelated node and 6 as a control dependency of 4.
func chainTrace() *Trace {
	return &Trace{
		Events: []Event{
			{Node: 1},
			{Node: 5},
			{Node: 2},
			{Node: 6},
			{Node: 3},
			{Node: 5},
			{Node: 4, Dangerous: true, Object: "buf", Offset: 8},
		},
		Deps: map[NodeID]Deps{
			2: {Data: []NodeID{1}},
			3: {Data: []NodeID{2}},
			4: {Data: []NodeID{3}, Control: []NodeID{6}},
		},
	}
}

func TestMinimizeChain(t *testing.T) {
	tr := chainTrace()
	s, err := tr.Minimize(6)
	if err != nil {
		t.Fatalf("Minimize: %v", err)
	}
	want := []NodeID{1, 2, 3, 4, 6}
	if !reflect.DeepEqual(s.Nodes, want) {
		t.Fatalf("got %v, want %v", s.Nodes, want)
	}
	if err := tr.Verify(s); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestReplayDropsDependents(t *testing.T) {
	tr := chainTrace()
	// Drop node 2: 3 and 4 must not fire even though they are kept.
	keep := map[NodeID]bool{1: true, 3: true, 4: true, 6: true}
	if got := tr.Replay(keep); len(got) != 0 {
		t.Fatalf("expected no dangerous events, got %v", got)
	}
}

func TestOtherCrashIsNotTheBug(t *testing.T) {
	tr := &Trace{
		Events: []Event{
			{Node: 1},
			{Node: 2, Dangerous: true, Object: "other", Offset: 0},
			{Node: 3, Dangerous: true, Object: "buf", Offset: 16},
		},
		Deps: map[NodeID]Deps{
			3: {Data: []NodeID{1}},
		},
	}
	s, err := tr.Minimize(2)
	if err != nil {
		t.Fatalf("Minimize: %v", err)
	}
	want := []NodeID{1, 3}
	if !reflect.DeepEqual(s.Nodes, want) {
		t.Fatalf("got %v, want %v", s.Nodes, want)
	}
	// A kept set that fires only the other crash must not count.
	if tr.ReplaysTarget(map[NodeID]bool{2: true}, 2) {
		t.Fatal("other crash accepted as the original bug")
	}
}

func TestSharedClosureExcludesUnrelated(t *testing.T) {
	tr := chainTrace()
	cands, err := tr.Candidates(6)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	want := []NodeID{1, 2, 3, 4, 6}
	if !reflect.DeepEqual(cands, want) {
		t.Fatalf("got %v, want %v", cands, want)
	}
}

func TestTooManyCandidates(t *testing.T) {
	tr := &Trace{Deps: map[NodeID]Deps{}}
	var deps []NodeID
	for i := NodeID(1); i <= MaxCandidates; i++ {
		tr.Events = append(tr.Events, Event{Node: i})
		deps = append(deps, i)
	}
	tr.Events = append(tr.Events, Event{Node: 99, Dangerous: true, Object: "x"})
	tr.Deps[99] = Deps{Data: deps}
	if _, err := tr.Candidates(len(tr.Events) - 1); !errors.Is(err, ErrTooManyCandidates) {
		t.Fatalf("got %v, want ErrTooManyCandidates", err)
	}
	if _, err := tr.Minimize(len(tr.Events) - 1); !errors.Is(err, ErrTooManyCandidates) {
		t.Fatalf("Minimize got %v, want ErrTooManyCandidates", err)
	}
}

func TestNotReproducible(t *testing.T) {
	// The target depends on node 1, but node 1 depends on itself (a
	// cycle), so it can never fire and the read is not reproducible.
	tr := &Trace{
		Events: []Event{
			{Node: 1},
			{Node: 2, Dangerous: true, Object: "buf"},
		},
		Deps: map[NodeID]Deps{
			1: {Data: []NodeID{1}},
			2: {Data: []NodeID{1}},
		},
	}
	if _, err := tr.Minimize(1); !errors.Is(err, ErrNotReproducible) {
		t.Fatalf("got %v, want ErrNotReproducible", err)
	}
}

func TestNoTarget(t *testing.T) {
	tr := chainTrace()
	if _, err := tr.Minimize(0); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("got %v, want ErrNoTarget", err)
	}
	if _, err := tr.Minimize(-1); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("got %v, want ErrNoTarget", err)
	}
	if _, err := tr.Minimize(100); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("got %v, want ErrNoTarget", err)
	}
}

func TestExternalDepsAreInputs(t *testing.T) {
	// Node 1 depends on node 42, which never appears in the trace: it is
	// treated as an always-available external input.
	tr := &Trace{
		Events: []Event{
			{Node: 1},
			{Node: 2, Dangerous: true, Object: "buf", Offset: 4},
		},
		Deps: map[NodeID]Deps{
			1: {Data: []NodeID{42}},
			2: {Data: []NodeID{1}},
		},
	}
	s, err := tr.Minimize(1)
	if err != nil {
		t.Fatalf("Minimize: %v", err)
	}
	want := []NodeID{1, 2}
	if !reflect.DeepEqual(s.Nodes, want) {
		t.Fatalf("got %v, want %v", s.Nodes, want)
	}
}

func TestMinimalityByExhaustion(t *testing.T) {
	// Diamond: 4 needs {2,3}, 2 and 3 both need 1, plus noise nodes 8,9.
	tr := &Trace{
		Events: []Event{
			{Node: 8},
			{Node: 1},
			{Node: 9},
			{Node: 2},
			{Node: 3},
			{Node: 4, Dangerous: true, Object: "heap", Offset: 32},
		},
		Deps: map[NodeID]Deps{
			2: {Data: []NodeID{1}},
			3: {Data: []NodeID{1}},
			4: {Data: []NodeID{2, 3}},
		},
	}
	s, err := tr.Minimize(5)
	if err != nil {
		t.Fatalf("Minimize: %v", err)
	}
	want := []NodeID{1, 2, 3, 4}
	if !reflect.DeepEqual(s.Nodes, want) {
		t.Fatalf("got %v, want %v", s.Nodes, want)
	}
	// Brute-force proof: no subset of the closure smaller than the result
	// replays the target.
	cands, err := tr.Candidates(5)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	for mask := 0; mask < 1<<uint(len(cands)); mask++ {
		keep := map[NodeID]bool{}
		size := 0
		for i, n := range cands {
			if mask&(1<<uint(i)) != 0 {
				keep[n] = true
				size++
			}
		}
		if size < len(s.Nodes) && tr.ReplaysTarget(keep, 5) {
			t.Fatalf("smaller subset %v replays target; slice %v not minimal", keep, s.Nodes)
		}
	}
}

func TestVerifyRejectsRedundant(t *testing.T) {
	tr := chainTrace()
	bad := &Slice{Target: 6, Nodes: []NodeID{1, 2, 3, 4, 5, 6}}
	if err := tr.Verify(bad); err == nil {
		t.Fatal("Verify accepted a non-minimal slice")
	}
	broken := &Slice{Target: 6, Nodes: []NodeID{1, 2, 4, 6}}
	if err := tr.Verify(broken); err == nil {
		t.Fatal("Verify accepted a slice that does not replay the target")
	}
}
