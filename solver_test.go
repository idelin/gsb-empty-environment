package snapshot

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func mustSolve(t *testing.T, p Problem) *Solution {
	t.Helper()
	s, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return s
}

func TestSimpleSplitRollback(t *testing.T) {
	s := mustSolve(t, Problem{
		Partitions: [][]string{{"A"}, {"A"}},
		Watermark:  []int{1, 0},
	})
	if !reflect.DeepEqual(s.Frontier, []int{0, 0}) {
		t.Fatalf("frontier = %v, want [0 0]", s.Frontier)
	}
	if len(s.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(s.Events))
	}
	ev := s.Events[0]
	if ev.Txn != "A" || ev.Partition != 0 || ev.FromFrontier != 1 || ev.ToFrontier != 0 {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Witness != (VersionID{Partition: 1, Index: 0}) || ev.WitnessCause != WatermarkCause {
		t.Fatalf("unexpected witness: %+v cause %d", ev.Witness, ev.WitnessCause)
	}
	steps, ok := s.ReasonChain(VersionID{Partition: 0, Index: 0})
	if !ok || len(steps) != 2 || steps[1].Cause != WatermarkCause {
		t.Fatalf("chain = %+v ok=%v", steps, ok)
	}
	if !s.IsConsistent() {
		t.Fatal("result is not consistent")
	}
}

func TestCascadeAcrossPartitions(t *testing.T) {
	s := mustSolve(t, Problem{
		Partitions: [][]string{
			{"A", "B"},
			{"A"},
			{"B"},
		},
		Watermark: []int{2, 0, 1},
	})
	if !reflect.DeepEqual(s.Frontier, []int{0, 0, 0}) {
		t.Fatalf("frontier = %v, want [0 0 0]", s.Frontier)
	}
	if len(s.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(s.Events))
	}
	// Chain for p2#0: rolled back because B split, because B@p0#1 was
	// excluded by A's rollback, because A@p1#0 is beyond the watermark.
	steps, ok := s.ReasonChain(VersionID{Partition: 2, Index: 0})
	if !ok {
		t.Fatal("no chain for p2#0")
	}
	if len(steps) != 3 {
		t.Fatalf("chain length = %d, want 3: %+v", len(steps), steps)
	}
	if steps[2].Cause != WatermarkCause || steps[2].Version != (VersionID{Partition: 1, Index: 0}) {
		t.Fatalf("chain root = %+v", steps[2])
	}
}

func TestCycleTerminates(t *testing.T) {
	// A and B form a cycle: A@p0#0 < B@p0#1 on p0, B@p1#0 < A@p1#1 on p1.
	s := mustSolve(t, Problem{
		Partitions: [][]string{
			{"A", "B"},
			{"B", "A"},
		},
		Watermark: []int{2, 1},
	})
	if !reflect.DeepEqual(s.Frontier, []int{0, 0}) {
		t.Fatalf("frontier = %v, want [0 0]", s.Frontier)
	}
	if !s.IsConsistent() {
		t.Fatal("result is not consistent")
	}
	// Every excluded version must have a finite chain ending at the watermark.
	for pt, vers := range [][]string{{"A", "B"}, {"B", "A"}} {
		for i := range vers {
			steps, ok := s.ReasonChain(VersionID{Partition: pt, Index: i})
			if !ok {
				t.Fatalf("p%d#%d should be excluded", pt, i)
			}
			if steps[len(steps)-1].Cause != WatermarkCause {
				t.Fatalf("chain for p%d#%d does not end at watermark", pt, i)
			}
		}
	}
}

func TestAlreadyConsistent(t *testing.T) {
	s := mustSolve(t, Problem{
		Partitions: [][]string{
			{"A"},
			{"A", "B"},
			{"B"},
		},
		Watermark: []int{1, 2, 1},
	})
	if !reflect.DeepEqual(s.Frontier, []int{1, 2, 1}) {
		t.Fatalf("frontier = %v, want [1 2 1]", s.Frontier)
	}
	if len(s.Events) != 0 {
		t.Fatalf("events = %v, want none", s.Events)
	}
	if _, ok := s.ReasonChain(VersionID{Partition: 0, Index: 0}); ok {
		t.Fatal("included version should have no chain")
	}
}

func TestSinglePartitionMultiVersionTxn(t *testing.T) {
	s := mustSolve(t, Problem{
		Partitions: [][]string{{"A", "A", "B"}},
		Watermark:  []int{1},
	})
	if !reflect.DeepEqual(s.Frontier, []int{0}) {
		t.Fatalf("frontier = %v, want [0]", s.Frontier)
	}
	if !s.IsConsistent() {
		t.Fatal("result is not consistent")
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := Solve(Problem{Partitions: [][]string{{"A"}}, Watermark: []int{1, 0}}); err == nil {
		t.Fatal("expected error for mismatched watermark")
	}
	if _, err := Solve(Problem{Partitions: [][]string{{"A"}}, Watermark: []int{2}}); err == nil {
		t.Fatal("expected error for out-of-range watermark")
	}
}

// bruteForce returns the componentwise maximum of all consistent cuts not
// exceeding the watermark, which is the greatest consistent cut because
// consistent cuts (order ideals) are closed under union.
func bruteForce(p Problem) []int {
	n := len(p.Partitions)
	best := make([]int, n)
	total := map[string]int{}
	for _, vers := range p.Partitions {
		for _, id := range vers {
			total[id]++
		}
	}
	cut := make([]int, n)
	var rec func(pt int)
	rec = func(pt int) {
		if pt == n {
			inc := map[string]int{}
			for q, vers := range p.Partitions {
				for i := 0; i < cut[q]; i++ {
					inc[vers[i]]++
				}
			}
			for id, c := range inc {
				if c != total[id] {
					return
				}
			}
			for q := range cut {
				if cut[q] > best[q] {
					best[q] = cut[q]
				}
			}
			return
		}
		for f := 0; f <= p.Watermark[pt]; f++ {
			cut[pt] = f
			rec(pt + 1)
		}
	}
	rec(0)
	return best
}

func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 3000; trial++ {
		np := 1 + rng.Intn(3)
		ntx := 1 + rng.Intn(4)
		p := Problem{
			Partitions: make([][]string, np),
			Watermark:  make([]int, np),
		}
		for pt := 0; pt < np; pt++ {
			length := rng.Intn(6)
			for i := 0; i < length; i++ {
				p.Partitions[pt] = append(p.Partitions[pt], fmt.Sprintf("T%d", rng.Intn(ntx)))
			}
			p.Watermark[pt] = rng.Intn(length + 1)
		}
		s := mustSolve(t, p)
		want := bruteForce(p)
		if !reflect.DeepEqual(s.Frontier, want) {
			t.Fatalf("trial %d: frontier = %v, brute force = %v, problem = %+v",
				trial, s.Frontier, want, p)
		}
		if !s.IsConsistent() {
			t.Fatalf("trial %d: inconsistent result, problem = %+v", trial, p)
		}
		for pt := range s.Frontier {
			if s.Frontier[pt] > p.Watermark[pt] {
				t.Fatalf("trial %d: frontier exceeds watermark", trial)
			}
		}
		// Validate every reason chain: links must match events, event IDs
		// must strictly decrease, and the root must be the watermark.
		for pt, vers := range p.Partitions {
			for i := range vers {
				v := VersionID{Partition: pt, Index: i}
				steps, excluded := s.ReasonChain(v)
				if excluded != !s.Included(v) {
					t.Fatalf("trial %d: chain/included mismatch for %v", trial, v)
				}
				if !excluded {
					continue
				}
				if steps[0].Version != v {
					t.Fatalf("trial %d: chain does not start at %v", trial, v)
				}
				prev := len(s.Events) + 1
				for j, st := range steps {
					if st.Cause == WatermarkCause {
						if j != len(steps)-1 {
							t.Fatalf("trial %d: watermark not at chain end", trial)
						}
						continue
					}
					if st.Cause >= prev {
						t.Fatalf("trial %d: event IDs not decreasing", trial)
					}
					prev = st.Cause
					if j+1 >= len(steps) {
						t.Fatalf("trial %d: chain does not reach watermark", trial)
					}
					if s.Events[st.Cause].Witness != steps[j+1].Version {
						t.Fatalf("trial %d: broken chain link at step %d", trial, j)
					}
				}
			}
		}
	}
}

func ExampleSolve() {
	s, _ := Solve(Problem{
		Partitions: [][]string{
			{"A", "B"},
			{"B", "A"},
		},
		Watermark: []int{2, 1},
	})
	fmt.Println("frontier:", s.Frontier)
	fmt.Println(s.Explain(VersionID{Partition: 1, Index: 0}))
	// Output:
	// frontier: [0 0]
	// p1#0 (txn B) excluded by rollback #1: txn B split, partition 1 frontier 1 -> 0 (witness p0#1)
	// p0#1 (txn B) excluded by rollback #0: txn A split, partition 0 frontier 2 -> 0 (witness p1#1)
	// p1#1 (txn A) excluded directly by the watermark
}
