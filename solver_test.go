package main

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, input string) []op {
	t.Helper()
	ops, err := parseOps(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return ops
}

func mustSolve(t *testing.T, input string) []answer {
	t.Helper()
	ops := mustParse(t, input)
	answers, err := solve(ops)
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if err := verifyAgainstRebuild(ops, answers); err != nil {
		t.Fatalf("verify: %v", err)
	}
	return answers
}

func TestBasicPathAndCut(t *testing.T) {
	answers := mustSolve(t, `
1 add 1 2
1 add 2 3
2 query 1 3
3 del 1 2
4 query 1 3
`)
	if len(answers) != 2 {
		t.Fatalf("want 2 answers, got %d", len(answers))
	}
	if !answers[0].connected || !reflect.DeepEqual(answers[0].path, []int{1, 2, 3}) {
		t.Errorf("answer 0: %+v", answers[0])
	}
	if answers[1].connected || !reflect.DeepEqual(answers[1].component, []int{1}) {
		t.Errorf("answer 1: %+v", answers[1])
	}
}

func TestSameTickMultipleOps(t *testing.T) {
	// All ops share tick 7; file order decides what each query sees.
	answers := mustSolve(t, `
7 add 1 2
7 query 1 2
7 del 1 2
7 query 1 2
7 add 1 2
7 query 1 2
`)
	want := []bool{true, false, true}
	if len(answers) != len(want) {
		t.Fatalf("want %d answers, got %d", len(want), len(answers))
	}
	for i, a := range answers {
		if a.connected != want[i] {
			t.Errorf("answer %d: connected=%v, want %v", i, a.connected, want[i])
		}
		if a.tick != 7 {
			t.Errorf("answer %d: tick=%d, want 7", i, a.tick)
		}
	}
}

func TestEdgeOpenedClosedRepeatedly(t *testing.T) {
	answers := mustSolve(t, `
1 add 1 2
2 query 1 2
3 del 1 2
4 query 1 2
5 add 1 2
6 query 1 2
7 del 1 2
8 add 1 2
9 query 1 2
`)
	want := []bool{true, false, true, true}
	if len(answers) != len(want) {
		t.Fatalf("want %d answers, got %d", len(want), len(answers))
	}
	for i, a := range answers {
		if a.connected != want[i] {
			t.Errorf("answer %d: connected=%v, want %v", i, a.connected, want[i])
		}
	}
}

func TestDuplicateEdgesRefCount(t *testing.T) {
	answers := mustSolve(t, `
1 add 1 2
1 add 1 2
2 query 1 2
3 del 1 2
4 query 1 2
5 del 1 2
6 query 1 2
`)
	want := []bool{true, true, false}
	if len(answers) != len(want) {
		t.Fatalf("want %d answers, got %d", len(want), len(answers))
	}
	for i, a := range answers {
		if a.connected != want[i] {
			t.Errorf("answer %d: connected=%v, want %v", i, a.connected, want[i])
		}
	}
}

func TestDelAbsentEdgeFails(t *testing.T) {
	cases := []string{
		"1 del 1 2\n",                       // never added
		"1 add 1 2\n2 del 1 2\n3 del 1 2\n", // removed, then removed again
		"1 add 1 2\n2 del 2 1\n3 del 1 2\n", // undirected: 2 1 == 1 2
	}
	for i, input := range cases {
		ops := mustParse(t, input)
		if _, err := solve(ops); err == nil {
			t.Errorf("case %d: expected error, got nil", i)
		} else if _, ok := err.(*OpError); !ok {
			t.Errorf("case %d: expected *OpError, got %T (%v)", i, err, err)
		}
	}
}

func TestAnswersMatchQueryTimeNotFinalGraph(t *testing.T) {
	// Final graph has edge 1-2, but the first query predates the add;
	// final graph lacks edge 3-4, but the second query predates the del.
	answers := mustSolve(t, `
1 query 1 2
2 add 1 2
3 add 3 4
4 query 3 4
5 del 3 4
`)
	if answers[0].connected {
		t.Errorf("early query must be answered against the empty graph, not the final one")
	}
	if !answers[1].connected {
		t.Errorf("query before del must see the edge even though the final graph lacks it")
	}
}

func TestCutCertificateIsRealCut(t *testing.T) {
	answers := mustSolve(t, `
1 add 1 2
1 add 2 3
1 add 4 5
2 query 1 4
`)
	a := answers[0]
	if a.connected {
		t.Fatalf("expected disconnected")
	}
	if !reflect.DeepEqual(a.component, []int{1, 2, 3}) {
		t.Fatalf("component = %v", a.component)
	}
	inCut := make(map[int]bool)
	for _, x := range a.component {
		inCut[x] = true
	}
	if inCut[a.v] {
		t.Errorf("v=%d must lie outside the cut", a.v)
	}
}

func TestSelfQuery(t *testing.T) {
	answers := mustSolve(t, "1 query 5 5\n")
	if !answers[0].connected || !reflect.DeepEqual(answers[0].path, []int{5}) {
		t.Errorf("self query: %+v", answers[0])
	}
}

// TestRandomizedVsRebuild fuzzes the segment-tree solver against an
// independent per-time rebuild on random streams with duplicate edges,
// repeated open/close cycles, and same-tick bursts.
func TestRandomizedVsRebuild(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		nVert := 1 + rng.Intn(8)
		nOps := 1 + rng.Intn(120)
		var sb strings.Builder
		count := make(map[edge]int)
		tick := 0
		for i := 0; i < nOps; i++ {
			tick += rng.Intn(3) // non-decreasing ticks, many collisions
			u, v := rng.Intn(nVert), rng.Intn(nVert)
			roll := rng.Intn(10)
			switch {
			case roll < 4:
				fmt.Fprintf(&sb, "%d add %d %d\n", tick, u, v)
				count[makeEdge(u, v)]++
			case roll < 7:
				// delete a present edge if any, else add
				var present []edge
				for e, c := range count {
					if c > 0 {
						present = append(present, e)
					}
				}
				if len(present) == 0 {
					fmt.Fprintf(&sb, "%d add %d %d\n", tick, u, v)
					count[makeEdge(u, v)]++
				} else {
					e := present[rng.Intn(len(present))]
					fmt.Fprintf(&sb, "%d del %d %d\n", tick, e[0], e[1])
					count[e]--
				}
			default:
				fmt.Fprintf(&sb, "%d query %d %d\n", tick, u, v)
			}
		}
		ops, err := parseOps(strings.NewReader(sb.String()))
		if err != nil {
			t.Fatalf("seed %d parse: %v", seed, err)
		}
		answers, err := solve(ops)
		if err != nil {
			t.Fatalf("seed %d solve: %v", seed, err)
		}
		if err := verifyAgainstRebuild(ops, answers); err != nil {
			t.Fatalf("seed %d mismatch:\ninput:\n%s\nerr: %v", seed, sb.String(), err)
		}
	}
}
