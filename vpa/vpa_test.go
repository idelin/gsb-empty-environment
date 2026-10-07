package vpa

import (
	"reflect"
	"testing"
)

const (
	testMaxLen   = 20
	testMaxDepth = 8
)

func TestValidateComplete(t *testing.T) {
	for _, a := range []*Automaton{ProtocolA(), ProtocolB()} {
		if err := a.Validate(); err != nil {
			t.Fatalf("%s invalid: %v", a.Name, err)
		}
	}
}

func TestShortestDistinguishingWord(t *testing.T) {
	res, err := FindDistinguishing(ProtocolA(), ProtocolB(), testMaxLen, testMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found {
		t.Fatal("expected a distinguishing word")
	}
	want := []int{0, 1, 0, 2} // c0 c1 c0 r0
	if !reflect.DeepEqual(res.Word, want) {
		t.Fatalf("word = %v (%s), want %v", res.Word, ProtocolA().WordString(res.Word), want)
	}
	if !res.Accept1 || res.Accept2 {
		t.Fatalf("verdicts A=%v B=%v, want A=true B=false", res.Accept1, res.Accept2)
	}
	if len(res.Trace1) != 4 || len(res.Trace2) != 4 {
		t.Fatalf("traces not recorded step by step: %d, %d", len(res.Trace1), len(res.Trace2))
	}
}

func TestIdenticalAutomataHaveNoDifference(t *testing.T) {
	res, err := FindDistinguishing(ProtocolA(), ProtocolA(), testMaxLen, testMaxDepth)
	if err != nil {
		t.Fatal(err)
	}
	if res.Found {
		t.Fatalf("identical automata, but found %v", res.Word)
	}
}

func TestEmptyStackReturnRule(t *testing.T) {
	r := ProtocolA().Run([]int{2}, testMaxDepth) // stray close "r0"
	if r.OutOfRange {
		t.Fatal("single return must stay in range")
	}
	if r.Accepted || r.FinalState != 2 {
		t.Fatalf("stray close: accepted=%v final=q%d, want rejected in q2", r.Accepted, r.FinalState)
	}
	if len(r.Steps) != 1 || r.Steps[0].Note == "" {
		t.Fatalf("empty-stack rule not traced: %+v", r.Steps)
	}
}

func TestDepthOverflowIsOutOfRangeNotRejection(t *testing.T) {
	deep := make([]int, testMaxDepth+1) // c0 x 9: depth 9 > 8
	r := ProtocolA().Run(deep, testMaxDepth)
	if !r.OutOfRange {
		t.Fatal("expected out-of-range, not a normal verdict")
	}
	if r.OutOfRangeAt != testMaxDepth || len(r.Steps) != testMaxDepth {
		t.Fatalf("stopped at %d with %d steps, want stop at %d", r.OutOfRangeAt, len(r.Steps), testMaxDepth)
	}
	if r.Accepted {
		t.Fatal("out-of-range run must not report an acceptance verdict")
	}
}

func TestBruteForceConfirmsMinimality(t *testing.T) {
	a, b := ProtocolA(), ProtocolB()
	// No word of length < 4 may distinguish.
	if _, _, _, found, checked := BruteForce(a, b, 3, testMaxDepth); found {
		t.Fatalf("shorter distinguishing word exists (checked %d words)", checked)
	}
	// At length 4 the brute force must find one, of the same length.
	w, wa, wb, found, _ := BruteForce(a, b, 4, testMaxDepth)
	if !found || len(w) != 4 {
		t.Fatalf("brute force found=%v word=%v, want length-4 word", found, w)
	}
	if !wa || wb {
		t.Fatalf("brute-force verdicts A=%v B=%v, want true/false", wa, wb)
	}
}

// TestSimulateMatchesRun cross-checks the independent simulator against
// the traced Run on every word up to length 5 (small alphabet, exhaustive).
func TestSimulateMatchesRun(t *testing.T) {
	a := ProtocolA()
	n := len(a.Kinds)
	total := 0
	var rec func(w []int, depth int)
	rec = func(w []int, depth int) {
		acc, inRange := Simulate(a, w, testMaxDepth)
		r := a.Run(w, testMaxDepth)
		if !inRange || r.OutOfRange {
			t.Fatalf("word %v unexpectedly out of range", w)
		}
		if acc != r.Accepted {
			t.Fatalf("word %v: Simulate=%v Run=%v", w, acc, r.Accepted)
		}
		total++
		if len(w) == 5 {
			return
		}
		for s := 0; s < n; s++ {
			nd := depth
			switch a.Kinds[s] {
			case Call:
				nd = depth + 1
				if nd > testMaxDepth {
					continue
				}
			case Return:
				if depth > 0 {
					nd = depth - 1
				}
			}
			rec(append(append([]int{}, w...), s), nd)
		}
	}
	rec(nil, 0)
	if total != 1+5+25+125+625+3125 {
		t.Fatalf("checked %d words, want %d", total, 1+5+25+125+625+3125)
	}
}

// TestDepthIsWordLevelProperty verifies both automata always have equal
// stack height, so depth pruning is automaton-independent.
func TestDepthIsWordLevelProperty(t *testing.T) {
	a, b := ProtocolA(), ProtocolB()
	n := len(a.Kinds)
	var rec func(w []int, depth int)
	rec = func(w []int, depth int) {
		if len(w) == 6 {
			return
		}
		ra, rb := a.Run(w, testMaxDepth), b.Run(w, testMaxDepth)
		if len(ra.FinalStack) != len(rb.FinalStack) {
			t.Fatalf("word %v: depths %d vs %d", w, len(ra.FinalStack), len(rb.FinalStack))
		}
		for s := 0; s < n; s++ {
			nd := depth
			switch a.Kinds[s] {
			case Call:
				nd = depth + 1
				if nd > testMaxDepth {
					continue
				}
			case Return:
				if depth > 0 {
					nd = depth - 1
				}
			}
			rec(append(append([]int{}, w...), s), nd)
		}
	}
	rec(nil, 0)
}
