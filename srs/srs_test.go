package srs

import (
	"strings"
	"testing"
)

func TestOverlappingMatches(t *testing.T) {
	sys := &System{
		Rules:  []Rule{{Name: "shrink", Left: "aa", Right: "b"}},
		MaxLen: 10,
	}
	succs := sys.Successors(State{Str: "aaa"})
	if len(succs) != 2 {
		t.Fatalf("expected 2 successors for overlapping matches, got %d", len(succs))
	}
	if succs[0].State.Str != "ba" || succs[0].Step.Pos != 0 {
		t.Errorf("succ[0] = %+v, want ba@0", succs[0])
	}
	if succs[1].State.Str != "ab" || succs[1].Step.Pos != 1 {
		t.Errorf("succ[1] = %+v, want ab@1", succs[1])
	}
}

func TestEmptyRightSide(t *testing.T) {
	sys := &System{
		Rules:  []Rule{{Name: "del", Left: "a", Right: ""}},
		MaxLen: 10,
	}
	succs := sys.Successors(State{Str: "aa"})
	if len(succs) != 2 || succs[0].State.Str != "a" || succs[1].State.Str != "a" {
		t.Fatalf("deletion successors = %+v", succs)
	}
	sr, err := NewSearcher(sys, State{Str: "aa"})
	if err != nil {
		t.Fatal(err)
	}
	res := sr.Search(1000)
	if res.Kind != NoCycle {
		t.Fatalf("kind = %v, want NoCycle", res.Kind)
	}
	if res.Longest != 2 {
		t.Errorf("longest = %d, want 2 (aa -> a -> \"\")", res.Longest)
	}
	if err := sys.VerifyDerivation(State{Str: "aa"}, res.LongestPath); err != nil {
		t.Errorf("longest path does not replay: %v", err)
	}
	end, _ := sys.Replay(State{Str: "aa"}, res.LongestPath)
	if end.Str != "" {
		t.Errorf("longest path ends at %q, want empty string", end.Str)
	}
}

func TestOverLengthDiscarded(t *testing.T) {
	sys := &System{
		Rules:  []Rule{{Name: "grow", Left: "a", Right: "bb"}},
		MaxLen: 1,
	}
	if got := sys.Successors(State{Str: "a"}); len(got) != 0 {
		t.Fatalf("expected over-length successor to be discarded, got %+v", got)
	}
	sys.MaxLen = 2
	if got := sys.Successors(State{Str: "a"}); len(got) != 1 || got[0].State.Str != "bb" {
		t.Fatalf("expected bb within bound, got %+v", got)
	}
}

func TestSimpleCycleWithEntry(t *testing.T) {
	sys := &System{
		Rules: []Rule{
			{Name: "enter", Left: "x", Right: "a"},
			{Name: "ab", Left: "a", Right: "b"},
			{Name: "ba", Left: "b", Right: "a"},
		},
		MaxLen: 5,
	}
	sr, err := NewSearcher(sys, State{Str: "x"})
	if err != nil {
		t.Fatal(err)
	}
	res := sr.Search(1000)
	if res.Kind != Cycle {
		t.Fatalf("kind = %v, want Cycle", res.Kind)
	}
	if len(res.Entry) != 1 || res.Entry[0].Rule != 0 {
		t.Errorf("entry = %+v, want single step via rule 0", res.Entry)
	}
	if len(res.Cycle) != 2 {
		t.Errorf("cycle length = %d, want 2", len(res.Cycle))
	}
	if err := sys.VerifyCycle(State{Str: "x"}, res.Entry, res.Cycle); err != nil {
		t.Errorf("evidence does not verify: %v", err)
	}
}

func TestSelfLoopCycle(t *testing.T) {
	sys := &System{
		Rules:  []Rule{{Name: "id", Left: "a", Right: "a"}},
		MaxLen: 5,
	}
	sr, _ := NewSearcher(sys, State{Str: "a"})
	res := sr.Search(100)
	if res.Kind != Cycle || len(res.Entry) != 0 || len(res.Cycle) != 1 {
		t.Fatalf("res = %+v, want self-loop cycle", res)
	}
	if err := sys.VerifyCycle(State{Str: "a"}, res.Entry, res.Cycle); err != nil {
		t.Errorf("evidence does not verify: %v", err)
	}
}

func TestControlStatePreventsCycle(t *testing.T) {
	// The string never changes, but the control state moves 0 -> 1 and
	// no rule leads back: a repeated string alone is not a cycle.
	sys := &System{
		Rules:  []Rule{{Name: "flip", Left: "a", Right: "a", From: 0, To: 1}},
		MaxLen: 5,
	}
	sr, _ := NewSearcher(sys, State{Str: "a", Ctrl: 0})
	res := sr.Search(100)
	if res.Kind != NoCycle || res.Longest != 1 {
		t.Fatalf("res = %+v, want NoCycle longest=1", res)
	}

	// Adding the return edge makes (string, control) truly cyclic.
	sys.Rules = append(sys.Rules, Rule{Name: "back", Left: "a", Right: "a", From: 1, To: 0})
	sr, _ = NewSearcher(sys, State{Str: "a", Ctrl: 0})
	res = sr.Search(100)
	if res.Kind != Cycle || len(res.Cycle) != 2 {
		t.Fatalf("res = %+v, want 2-step control cycle", res)
	}
	if err := sys.VerifyCycle(State{Str: "a"}, res.Entry, res.Cycle); err != nil {
		t.Errorf("evidence does not verify: %v", err)
	}
}

func TestOneShotResourcePreventsCycle(t *testing.T) {
	// String and control state are invariant; only the resource
	// drains. The full-state search must report NoCycle.
	sys := &System{
		Rules:  []Rule{{Name: "spend", Left: "a", Right: "a", Need: []int{1}}},
		MaxLen: 5,
		NumRes: 1,
	}
	start := State{Str: "a", Res: []int{2}}
	sr, _ := NewSearcher(sys, start)
	res := sr.Search(100)
	if res.Kind != NoCycle {
		t.Fatalf("kind = %v, want NoCycle (resource depletes)", res.Kind)
	}
	if res.Longest != 2 {
		t.Errorf("longest = %d, want 2 (two units of resource)", res.Longest)
	}
	if res.States != 3 {
		t.Errorf("states = %d, want 3 (res 2, 1, 0)", res.States)
	}

	// A rule that regenerates what it consumes is truly cyclic.
	sys.Rules[0].Gain = []int{1}
	sr, _ = NewSearcher(sys, start)
	res = sr.Search(100)
	if res.Kind != Cycle || len(res.Cycle) != 1 {
		t.Fatalf("res = %+v, want 1-step resource-balanced cycle", res)
	}
	if err := sys.VerifyCycle(start, res.Entry, res.Cycle); err != nil {
		t.Errorf("evidence does not verify: %v", err)
	}
}

func TestReplayRejectsResourceReuse(t *testing.T) {
	// Fabricated "cycle" evidence that secretly spends the same
	// one-shot resource twice must be rejected by replay.
	sys := &System{
		Rules:  []Rule{{Name: "spend", Left: "a", Right: "a", Need: []int{1}}},
		MaxLen: 5,
		NumRes: 1,
	}
	start := State{Str: "a", Res: []int{1}}
	fake := []Step{{Rule: 0, Pos: 0}, {Rule: 0, Pos: 0}}
	err := sys.VerifyCycle(start, nil, fake)
	if err == nil {
		t.Fatal("expected resource-reuse evidence to be rejected")
	}
	if !strings.Contains(err.Error(), "one-shot") {
		t.Errorf("error should mention one-shot resources: %v", err)
	}
	// A single step does not close the cycle either: resources differ.
	err = sys.VerifyCycle(start, nil, fake[:1])
	if err == nil {
		t.Fatal("expected non-closing evidence to be rejected")
	}
}

func TestBudgetPauseResume(t *testing.T) {
	sys := &System{
		Rules: []Rule{
			{Name: "ab", Left: "a", Right: "b"},
			{Name: "bc", Left: "b", Right: "c"},
			{Name: "ca", Left: "c", Right: "a"},
		},
		MaxLen: 5,
	}
	start := State{Str: "a"}

	full, _ := NewSearcher(sys, start)
	want := full.Search(1000)
	if want.Kind != Cycle {
		t.Fatalf("sanity: want Cycle, got %+v", want)
	}

	// Drip-feed the search one node at a time.
	sr, _ := NewSearcher(sys, start)
	res := sr.Search(0)
	if res.Kind != Unknown {
		t.Fatalf("zero budget with open frontier: kind = %v, want Unknown", res.Kind)
	}
	for i := 0; i < 100; i++ {
		res = sr.Search(1)
		if res.Kind != Unknown {
			break
		}
	}
	if res.Kind != want.Kind {
		t.Fatalf("resumed search kind = %v, want %v", res.Kind, want.Kind)
	}
	if len(res.Entry) != len(want.Entry) || len(res.Cycle) != len(want.Cycle) {
		t.Errorf("resumed evidence %+v differs from one-shot %+v", res, want)
	}
	if err := sys.VerifyCycle(start, res.Entry, res.Cycle); err != nil {
		t.Errorf("resumed evidence does not verify: %v", err)
	}
	// A finished searcher caches its result.
	if again := sr.Search(0); again.Kind != Cycle {
		t.Errorf("cached result kind = %v, want Cycle", again.Kind)
	}
}

func TestShortestEntryPath(t *testing.T) {
	// Two routes to the a<->b cycle: a long one and a short one.
	// The reported entry must be the shortest.
	sys := &System{
		Rules: []Rule{
			{Name: "long1", Left: "s", Right: "t"},
			{Name: "long2", Left: "t", Right: "u"},
			{Name: "long3", Left: "u", Right: "a"},
			{Name: "short", Left: "s", Right: "a"},
			{Name: "ab", Left: "a", Right: "b"},
			{Name: "ba", Left: "b", Right: "a"},
		},
		MaxLen: 5,
	}
	sr, _ := NewSearcher(sys, State{Str: "s"})
	res := sr.Search(1000)
	if res.Kind != Cycle {
		t.Fatalf("kind = %v, want Cycle", res.Kind)
	}
	if len(res.Entry) != 1 || res.Entry[0].Rule != 3 {
		t.Errorf("entry = %+v, want the 1-step short route (rule 3)", res.Entry)
	}
	if err := sys.VerifyCycle(State{Str: "s"}, res.Entry, res.Cycle); err != nil {
		t.Errorf("evidence does not verify: %v", err)
	}
}

func TestLongestDerivationBranching(t *testing.T) {
	sys := &System{
		Rules: []Rule{
			{Name: "ab", Left: "a", Right: "b"},
			{Name: "ac", Left: "a", Right: "c"},
			{Name: "bd", Left: "b", Right: "d"},
			{Name: "cd", Left: "c", Right: "d"},
			{Name: "de", Left: "d", Right: "e"},
		},
		MaxLen: 5,
	}
	sr, _ := NewSearcher(sys, State{Str: "a"})
	res := sr.Search(1000)
	if res.Kind != NoCycle {
		t.Fatalf("kind = %v, want NoCycle", res.Kind)
	}
	if res.Longest != 3 {
		t.Errorf("longest = %d, want 3 (a -> b|c -> d -> e)", res.Longest)
	}
	if len(res.LongestPath) != 3 {
		t.Fatalf("longest path = %+v, want 3 steps", res.LongestPath)
	}
	if err := sys.VerifyDerivation(State{Str: "a"}, res.LongestPath); err != nil {
		t.Errorf("longest path does not replay: %v", err)
	}
}

func TestOverlapAndEmptyRightInFullGraph(t *testing.T) {
	// "aa" -> "" on "aaa": both overlapping matches lead to the same
	// state "a" (a diamond, not a cycle). The full reachable graph
	// must not confuse re-discovery with a cycle.
	sys := &System{
		Rules:  []Rule{{Name: "delaa", Left: "aa", Right: ""}},
		MaxLen: 5,
	}
	sr, _ := NewSearcher(sys, State{Str: "aaa"})
	res := sr.Search(1000)
	if res.Kind != NoCycle {
		t.Fatalf("kind = %v, want NoCycle (diamond is not a cycle)", res.Kind)
	}
	if res.States != 2 {
		t.Errorf("states = %d, want 2 (aaa, a)", res.States)
	}
	if res.Longest != 1 {
		t.Errorf("longest = %d, want 1", res.Longest)
	}
}

func TestValidation(t *testing.T) {
	sys := &System{Rules: []Rule{{Name: "bad", Left: ""}}, MaxLen: 1}
	if _, err := NewSearcher(sys, State{Str: "a"}); err == nil {
		t.Fatal("expected empty-pattern rule to be rejected")
	}
}
