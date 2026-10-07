package vpa

import (
	"strings"
	"testing"
)

func TestRunSemantics(t *testing.T) {
	a, _ := ExamplePair()

	// Empty-stack return rule: stray close is tolerated, not a crash.
	if r := a.Run([]string{"close"}, 8); r.Outcome != Accept {
		t.Fatalf("stray close: got %s, want accept (empty-stack rule)", r.Outcome)
	}
	// Happy path with nesting.
	if r := a.Run([]string{"open", "open", "exec", "close"}, 8); r.Outcome != Accept {
		t.Fatalf("nested happy path: got %s, want accept", r.Outcome)
	}
	// Ends inside a request: state 1 is not final.
	if r := a.Run([]string{"open", "close", "open"}, 8); r.Outcome != Reject {
		t.Fatalf("open close open: got %s, want reject", r.Outcome)
	}
	// exec outside a request: no transition -> reject.
	if r := a.Run([]string{"exec"}, 8); r.Outcome != Reject {
		t.Fatalf("exec in idle: got %s, want reject", r.Outcome)
	}
	// Depth limit is out-of-range, NOT rejection.
	deep := make([]string, 9)
	for i := range deep {
		deep[i] = "open"
	}
	r := a.Run(deep, 8)
	if r.Outcome != OutOfRange {
		t.Fatalf("9 opens at depth limit 8: got %s, want out-of-range", r.Outcome)
	}
	if r.ExceededAt != 9 {
		t.Fatalf("ExceededAt = %d, want 9", r.ExceededAt)
	}
	// Same word fits with a higher limit and is then rejected (not final).
	if r := a.Run(deep, 9); r.Outcome != Reject {
		t.Fatalf("9 opens at depth limit 9: got %s, want reject", r.Outcome)
	}
}

func TestShortestDistinguishingWord(t *testing.T) {
	a, b := ExamplePair()
	res, err := ShortestDistinguishingWord(a, b, 20, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found {
		t.Fatal("no distinguishing word found, want one of length 4")
	}
	want := []string{"open", "exec", "exec", "close"}
	if strings.Join(res.Word, " ") != strings.Join(want, " ") {
		t.Fatalf("word = %v, want %v", res.Word, want)
	}
	if res.RunA.Outcome != Accept || res.RunB.Outcome != Reject {
		t.Fatalf("outcomes: A=%s B=%s, want accept/reject", res.RunA.Outcome, res.RunB.Outcome)
	}
	if ok, msg := ConfirmMinimal(a, b, res.Word, 8); !ok {
		t.Fatalf("ConfirmMinimal: %s", msg)
	}
}

func TestConfirmMinimalRejectsBadClaims(t *testing.T) {
	a, b := ExamplePair()
	// A word that does not distinguish at all.
	if ok, _ := ConfirmMinimal(a, b, []string{"open", "close"}, 8); ok {
		t.Fatal("open close wrongly confirmed as minimal distinguisher")
	}
	// A distinguishing but non-minimal word.
	longer := []string{"close", "open", "exec", "exec", "close"}
	if a.Run(longer, 8).Outcome != Accept || b.Run(longer, 8).Outcome != Reject {
		t.Fatal("test premise broken: longer word should distinguish")
	}
	if ok, _ := ConfirmMinimal(a, b, longer, 8); ok {
		t.Fatal("non-minimal word wrongly confirmed as minimal")
	}
}

// TestExhaustiveCrossCheck compares, for every word up to length 9 over the
// small alphabet, the naive independent simulator against VPA.Run for both
// automata, and the brute-force first-difference length against the BFS.
func TestExhaustiveCrossCheck(t *testing.T) {
	a, b := ExamplePair()
	const maxLen, maxDepth = 9, 8

	var pruned, checked int
	forEachWord(a, maxLen, maxDepth, func(w []string) {
		checked++
		for _, v := range []*VPA{a, b} {
			got := naiveOutcome(v, w, maxDepth)
			want := v.Run(w, maxDepth).Outcome
			if got != want {
				t.Fatalf("simulator mismatch on %v for %s: naive=%s run=%s", w, v.Name, got, want)
			}
		}
	}, &pruned)
	if checked == 0 || pruned == 0 {
		t.Fatalf("enumeration suspicious: checked=%d pruned=%d", checked, pruned)
	}

	rep := Exhaustive(a, b, maxLen, maxDepth)
	if rep.FirstDiffLen != 4 {
		t.Fatalf("brute force first difference at length %d, want 4", rep.FirstDiffLen)
	}
	res, err := ShortestDistinguishingWord(a, b, maxLen, maxDepth)
	if err != nil || !res.Found || len(res.Word) != rep.FirstDiffLen {
		t.Fatalf("BFS/brute-force disagree: bfs=%v brute=%d", res.Word, rep.FirstDiffLen)
	}
}

// TestDepthBoundIsNotEquivalence builds two automata that only disagree on a
// word needing stack depth 9. With depth bound 8 the search must report
// "not found within range" (never "equivalent"); with bound 9 it must find it.
func TestDepthBoundIsNotEquivalence(t *testing.T) {
	build := func(finalTop bool) *VPA {
		v := New("counter", 10, 0)
		v.AddSymbol("open", Call).AddSymbol("close", Return)
		for i := 0; i < 9; i++ {
			v.AddCall(i, "open", "S", i+1)
		}
		for i := 1; i <= 9; i++ {
			v.AddReturn(i, "close", "S", i-1)
		}
		v.AddFinal(0)
		if finalTop {
			v.AddFinal(9)
		}
		return v
	}
	a, b := build(true), build(false) // differ only on words reaching depth 9

	res, err := ShortestDistinguishingWord(a, b, 20, 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.Found {
		t.Fatalf("depth-8 search found %v, but the only difference needs depth 9", res.Word)
	}
	if res.PrunedDeep == 0 {
		t.Fatal("expected pruned deep words to be counted")
	}
	if got := res.Summary(); !strings.Contains(got, "NOT an equivalence proof") {
		t.Fatalf("summary must disclaim equivalence, got: %s", got)
	}

	res, err = ShortestDistinguishingWord(a, b, 20, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Found || len(res.Word) != 9 {
		t.Fatalf("depth-9 search: found=%v word=%v, want length 9", res.Found, res.Word)
	}
}

func TestIdenticalAutomataHaveNoDifference(t *testing.T) {
	a, b := ExamplePair()
	res, err := ShortestDistinguishingWord(a, a, 12, 8)
	if err != nil {
		t.Fatal(err)
	}
	if res.Found {
		t.Fatalf("identical automata: unexpected distinguishing word %v", res.Word)
	}
	rep := Exhaustive(b, b, 8, 8)
	if rep.FirstDiffLen != -1 {
		t.Fatalf("brute force found difference at length %d between identical automata", rep.FirstDiffLen)
	}
}
