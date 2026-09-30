package main

import "testing"

// Parallel branches must overlap on the wall clock: the critical path takes
// the max, while total work accumulates both branches.
func TestCriticalPathParallelNoDoubleCount(t *testing.T) {
	tr := NewTrace()
	a := tr.Add(Event{Tick: 0, Kind: KindSendStart, Packet: 0})
	b := tr.Add(Event{Tick: 5, Kind: KindRecv, Packet: 0, Parents: []int{a}})
	c := tr.Add(Event{Tick: 8, Kind: KindRecv, Packet: 0, Parents: []int{a}})
	d := tr.Add(Event{Tick: 8, Kind: KindDeliver, Packet: 0, Parents: []int{b, c}})

	cp, path := CriticalPath(tr)
	if cp != 8 {
		t.Fatalf("critical path = %d, want 8 (parallel branches must not sum to 13)", cp)
	}
	if len(path) != 3 || path[0] != a || path[2] != d {
		t.Fatalf("witness path = %v, want a 3-event path from %d to %d", path, a, d)
	}
	if path[1] != b && path[1] != c {
		t.Fatalf("witness path = %v, middle must be %d or %d", path, b, c)
	}
	if work := TotalWork(tr); work != 16 {
		t.Fatalf("total work = %d, want 16 (5+8+3+0)", work)
	}
	best, paths, ok := EnumerateCriticalPath(tr, 1000)
	if !ok || best != cp {
		t.Fatalf("enumerate = (%d, ok=%v), want match with DP %d", best, ok, cp)
	}
	if paths != 2 {
		t.Fatalf("enumerated %d paths, want 2", paths)
	}
}

// Contradictory timestamps are reported with the original edge as evidence
// and clamped (not silently zero-filled away).
func TestConflictEvidence(t *testing.T) {
	tr := NewTrace()
	e1 := tr.Add(Event{Tick: 10, Kind: KindSendStart, Packet: 0})
	e2 := tr.Add(Event{Tick: 5, Kind: KindRecv, Packet: 0, Parents: []int{e1}})

	conflicts, _ := CheckTrace(tr)
	if len(conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1", len(conflicts))
	}
	c := conflicts[0]
	if c.FromID != e1 || c.ToID != e2 || c.FromTick != 10 || c.ToTick != 5 {
		t.Fatalf("conflict evidence = %+v, want edge %d@10 -> %d@5", c, e1, e2)
	}
	if cp, _ := CriticalPath(tr); cp != 0 {
		t.Fatalf("critical path = %d, want 0 after clamping negative edge", cp)
	}
}

// A parent reference to an absent event is reported as missing.
func TestMissingParentRef(t *testing.T) {
	tr := NewTrace()
	e := tr.Add(Event{Tick: 3, Kind: KindRecv, Packet: 0, Parents: []int{999}})
	_, missing := CheckTrace(tr)
	if len(missing) != 1 || missing[0].EventID != e || missing[0].ParentID != 999 {
		t.Fatalf("missing = %+v, want event %d referencing 999", missing, e)
	}
}

// Exhaustive enumeration agrees with the DP on a larger deterministic DAG,
// and total work always upper-bounds the critical path.
func TestEnumerateMatchesDP(t *testing.T) {
	tr := NewTrace()
	ids := make([]int, 0, 40)
	state := uint32(12345)
	rand := func(n int64) int64 {
		state = state*1664525 + 1013904223
		return int64(state >> 16 % uint32(n))
	}
	for i := 0; i < 40; i++ {
		tick := int64(i)*3 + rand(4)
		var parents []int
		if i > 0 {
			for k := 0; k < 1+int(rand(3)); k++ {
				parents = append(parents, ids[rand(int64(len(ids)))])
			}
		}
		ids = append(ids, tr.Add(Event{Tick: tick, Kind: Kind(i % 10), Packet: i % 4, Parents: parents}))
	}
	cp, _ := CriticalPath(tr)
	best, _, ok := EnumerateCriticalPath(tr, 1_000_000)
	if !ok {
		t.Fatal("enumeration bailed out on a small DAG")
	}
	if best != cp {
		t.Fatalf("enumerate=%d != DP=%d", best, cp)
	}
	if work := TotalWork(tr); work < cp {
		t.Fatalf("total work %d < critical path %d", work, cp)
	}
}
