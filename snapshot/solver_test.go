package snapshot

import (
	"math/rand"
	"reflect"
	"testing"
)

func cutOf(versions ...int64) []int64 { return versions }

func TestSimpleHalfTransactionRolledBack(t *testing.T) {
	// t1 commits in A and B; the watermark includes only the A commit.
	in := Input{
		Logs: [][]LogEntry{
			{{Version: 1, Txn: 1}},
			{{Version: 2, Txn: 1}},
		},
		Watermark: []int64{1, 1},
	}
	res := Solve(in)
	if !reflect.DeepEqual(res.Cut, cutOf(0, 0)) {
		t.Fatalf("cut = %v, want [0 0]", res.Cut)
	}
	if len(res.Rollbacks) != 1 || res.Rollbacks[0].Txn != 1 {
		t.Fatalf("rollbacks = %+v, want only txn 1", res.Rollbacks)
	}
	r := res.Rollbacks[0].Reason
	if r.Kind != Watermark || r.Partition != 1 || r.Version != 2 {
		t.Fatalf("reason = %+v, want watermark miss at partition 1 version 2", r)
	}
	chain := res.Chain(1)
	if len(chain) != 1 || chain[0].Kind != Watermark {
		t.Fatalf("chain = %+v, want single watermark root", chain)
	}
}

func TestCascadeRollbackChain(t *testing.T) {
	// t1 straddles the watermark; evicting it from A also evicts t2's
	// commit there, so t2 must roll back too.
	in := Input{
		Logs: [][]LogEntry{
			{{Version: 1, Txn: 1}, {Version: 2, Txn: 2}}, // A
			{{Version: 1, Txn: 1}},                       // B
			{{Version: 2, Txn: 2}},                       // C
		},
		Watermark: []int64{2, 0, 2},
	}
	res := Solve(in)
	if !reflect.DeepEqual(res.Cut, cutOf(0, 0, 0)) {
		t.Fatalf("cut = %v, want [0 0 0]", res.Cut)
	}
	if len(res.Rollbacks) != 2 {
		t.Fatalf("rollbacks = %+v, want txns 1 and 2", res.Rollbacks)
	}
	chain := res.Chain(2)
	want := []Reason{
		{Kind: Cascade, CauseTxn: 1, Partition: 0, Version: 2},
		{Kind: Watermark, CauseTxn: -1, Partition: 1, Version: 1},
	}
	if !reflect.DeepEqual(chain, want) {
		t.Fatalf("chain(2) = %+v, want %+v", chain, want)
	}
}

func TestCycleTerminates(t *testing.T) {
	// Association cycle: t1(A,B) -> t2(B,C) -> t3(C,A) -> t1...
	// The cascade travels around the whole cycle and must terminate.
	in := Input{
		Logs: [][]LogEntry{
			{{Version: 1, Txn: 3}, {Version: 2, Txn: 1}}, // A
			{{Version: 1, Txn: 1}, {Version: 2, Txn: 2}}, // B
			{{Version: 1, Txn: 2}, {Version: 2, Txn: 3}}, // C
		},
		Watermark: []int64{2, 2, 1},
	}
	res := Solve(in)
	if !reflect.DeepEqual(res.Cut, cutOf(0, 0, 0)) {
		t.Fatalf("cut = %v, want [0 0 0]", res.Cut)
	}
	if len(res.Rollbacks) != 3 {
		t.Fatalf("rollbacks = %+v, want txns 1, 2, 3", res.Rollbacks)
	}
	// Root cause is t3 straddling the watermark at C@2.
	chain3 := res.Chain(3)
	if len(chain3) != 1 || chain3[0].Kind != Watermark || chain3[0].Partition != 2 || chain3[0].Version != 2 {
		t.Fatalf("chain(3) = %+v, want watermark root at C@2", chain3)
	}
	// t1 was evicted by t3's rollback, t2 by t1's: the chain walks the
	// cycle backwards and stops at the root instead of looping forever.
	chain2 := res.Chain(2)
	if len(chain2) != 3 {
		t.Fatalf("chain(2) = %+v, want 3 hops around the cycle", chain2)
	}
	if chain2[0].CauseTxn != 1 || chain2[1].CauseTxn != 3 || chain2[2].Kind != Watermark {
		t.Fatalf("chain(2) = %+v, want t2<-t1<-t3<-watermark", chain2)
	}
}

func TestNoRollbackWhenWatermarkConsistent(t *testing.T) {
	in := Input{
		Logs: [][]LogEntry{
			{{Version: 1, Txn: 1}, {Version: 2, Txn: 2}},
			{{Version: 2, Txn: 1}},
		},
		Watermark: []int64{2, 2},
	}
	res := Solve(in)
	if !reflect.DeepEqual(res.Cut, cutOf(2, 2)) {
		t.Fatalf("cut = %v, want [2 2]", res.Cut)
	}
	if len(res.Rollbacks) != 0 {
		t.Fatalf("rollbacks = %+v, want none", res.Rollbacks)
	}
	if res.Chain(1) != nil {
		t.Fatalf("chain(1) should be nil for an included txn")
	}
}

func TestWatermarkBelowEverything(t *testing.T) {
	in := Input{
		Logs: [][]LogEntry{
			{{Version: 5, Txn: 1}},
			{{Version: 7, Txn: 1}},
		},
		Watermark: []int64{1, 1},
	}
	res := Solve(in)
	if !reflect.DeepEqual(res.Cut, cutOf(0, 0)) {
		t.Fatalf("cut = %v, want [0 0]", res.Cut)
	}
	if len(res.Rollbacks) != 0 {
		t.Fatalf("rollbacks = %+v, want none (nothing was ever included)", res.Rollbacks)
	}
}

// --- brute-force oracle for randomized testing ---

// consistent reports whether the cut (per-partition last included index)
// includes every transaction wholly or not at all.
func consistent(in Input, commits map[int][][2]int, cut []int) bool {
	included := map[int]int{}
	for p := range in.Logs {
		for i := 0; i <= cut[p] && i < len(in.Logs[p]); i++ {
			included[in.Logs[p][i].Txn]++
		}
	}
	for txn, total := range commits {
		if n := included[txn]; n != 0 && n != len(total) {
			return false
		}
	}
	return true
}

// bruteForce enumerates all consistent cuts not later than the watermark and
// returns their componentwise maximum (consistent cuts are closed under
// union, so the maximum is itself a consistent cut — the greatest one).
func bruteForce(in Input) []int64 {
	parts := len(in.Logs)
	commits := map[int][][2]int{}
	for p := range in.Logs {
		for i, e := range in.Logs[p] {
			commits[e.Txn] = append(commits[e.Txn], [2]int{p, i})
		}
	}
	hi := make([]int, parts)
	for p := range in.Logs {
		hi[p] = -1
		for i, e := range in.Logs[p] {
			if e.Version <= in.Watermark[p] {
				hi[p] = i
			}
		}
	}
	best := make([]int, parts)
	for p := range best {
		best[p] = -1
	}
	cur := make([]int, parts)
	var rec func(p int)
	rec = func(p int) {
		if p == parts {
			if !consistent(in, commits, cur) {
				return
			}
			for i := range best {
				if cur[i] > best[i] {
					best[i] = cur[i]
				}
			}
			return
		}
		for i := -1; i <= hi[p]; i++ {
			cur[p] = i
			rec(p + 1)
		}
	}
	rec(0)
	out := make([]int64, parts)
	for p := range out {
		if best[p] >= 0 {
			out[p] = in.Logs[p][best[p]].Version
		}
	}
	return out
}

func TestMatchesBruteForceOnRandomInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 500; iter++ {
		parts := 2 + rng.Intn(2) // 2-3 partitions
		in := Input{Logs: make([][]LogEntry, parts), Watermark: make([]int64, parts)}
		for p := 0; p < parts; p++ {
			n := rng.Intn(5) // 0-4 entries
			v := int64(0)
			for i := 0; i < n; i++ {
				v += 1 + int64(rng.Intn(3))
				in.Logs[p] = append(in.Logs[p], LogEntry{Version: v, Txn: 1 + rng.Intn(5)})
			}
			in.Watermark[p] = int64(rng.Intn(14))
		}
		got := Solve(in)
		want := bruteForce(in)
		if !reflect.DeepEqual(got.Cut, want) {
			t.Fatalf("iter %d: cut = %v, brute force = %v, input = %+v", iter, got.Cut, want, in)
		}
		// The result must itself be consistent and within the watermark.
		commits := map[int][][2]int{}
		for p := range in.Logs {
			for i, e := range in.Logs[p] {
				commits[e.Txn] = append(commits[e.Txn], [2]int{p, i})
			}
		}
		cutIdx := make([]int, parts)
		for p := range in.Logs {
			cutIdx[p] = -1
			for i, e := range in.Logs[p] {
				if e.Version <= got.Cut[p] {
					cutIdx[p] = i
				}
			}
			if got.Cut[p] > in.Watermark[p] {
				t.Fatalf("iter %d: cut %v exceeds watermark %v", iter, got.Cut, in.Watermark)
			}
		}
		if !consistent(in, commits, cutIdx) {
			t.Fatalf("iter %d: cut %v is not consistent, input = %+v", iter, got.Cut, in)
		}
	}
}
