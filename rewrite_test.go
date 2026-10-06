package rewrite

import (
	"strings"
	"testing"
)

func mustSearch(t *testing.T, cfg Config, budget int) Result {
	t.Helper()
	res, err := Search(cfg, budget)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return res
}

func hasState(states []State, str string) bool {
	for _, s := range states {
		if s.Str == str {
			return true
		}
	}
	return false
}

// Overlapping matches must all be explored, and empty right-hand sides
// must shrink the string. Verified against the full reachable graph.
func TestOverlappingMatchesAndEmptyRHS(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "collapse", LHS: "aa", RHS: "b"},
			{Name: "drop", LHS: "a", RHS: ""},
		},
		Init:   State{Str: "aaa"},
		MaxLen: 3,
	}
	s, err := NewSearcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	res := s.Run(1000)
	if res.Status != NoCycle {
		t.Fatalf("status = %v, want NoCycle", res.Status)
	}
	// aaa -> aa -> a -> "" is the longest derivation.
	if res.Longest != 3 {
		t.Fatalf("Longest = %d, want 3", res.Longest)
	}
	states, edges := s.Snapshot()
	// Overlapping matches of "aa" in "aaa" at positions 0 and 1.
	if !hasState(states, "ba") || !hasState(states, "ab") {
		t.Fatalf("overlapping successors missing, states: %v", states)
	}
	// Empty RHS must be able to erase the string completely.
	if !hasState(states, "") {
		t.Fatalf("empty-string state missing, states: %v", states)
	}
	var pos0, pos1 bool
	for _, e := range edges {
		if states[e.From].Str == "aaa" && e.Step.Rule == 0 {
			if e.Step.Position == 0 {
				pos0 = true
			}
			if e.Step.Position == 1 {
				pos1 = true
			}
		}
	}
	if !pos0 || !pos1 {
		t.Fatalf("expected collapse edges at positions 0 and 1, edges: %v", edges)
	}
}

// A reachable cycle is reported with the shortest entry path, and the
// evidence replays.
func TestCycleShortestEntry(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "x2y", LHS: "x", RHS: "y"}, // 0
			{Name: "y2z", LHS: "y", RHS: "z"}, // 1
			{Name: "z2y", LHS: "z", RHS: "y"}, // 2
			{Name: "x2w", LHS: "x", RHS: "w"}, // 3
			{Name: "w2v", LHS: "w", RHS: "v"}, // 4
			{Name: "v2y", LHS: "v", RHS: "y"}, // 5
		},
		Init:   State{Str: "x"},
		MaxLen: 1,
	}
	res := mustSearch(t, cfg, 1000)
	if res.Status != FoundCycle {
		t.Fatalf("status = %v, want FoundCycle", res.Status)
	}
	// Entry via x->y (1 step), not via x->w->v->y (3 steps).
	if len(res.Entry) != 1 || res.Entry[0] != (Step{Rule: 0, Position: 0}) {
		t.Fatalf("Entry = %v, want [{0 0}]", res.Entry)
	}
	if len(res.Cycle) != 2 {
		t.Fatalf("Cycle = %v, want 2 steps", res.Cycle)
	}
	if err := Replay(cfg, res.Entry, res.Cycle); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

func TestSelfLoop(t *testing.T) {
	cfg := Config{
		Rules:  []Rule{{Name: "id", LHS: "a", RHS: "a"}},
		Init:   State{Str: "a"},
		MaxLen: 1,
	}
	res := mustSearch(t, cfg, 100)
	if res.Status != FoundCycle {
		t.Fatalf("status = %v, want FoundCycle", res.Status)
	}
	if len(res.Entry) != 0 || len(res.Cycle) != 1 {
		t.Fatalf("Entry=%v Cycle=%v, want empty entry and 1-step cycle", res.Entry, res.Cycle)
	}
	if err := Replay(cfg, res.Entry, res.Cycle); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

func TestNoCycleLongest(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "ab", LHS: "a", RHS: "b"},
			{Name: "bc", LHS: "b", RHS: "c"},
		},
		Init:   State{Str: "a"},
		MaxLen: 1,
	}
	res := mustSearch(t, cfg, 100)
	if res.Status != NoCycle || res.Longest != 2 {
		t.Fatalf("got %v longest=%d, want NoCycle longest=2", res.Status, res.Longest)
	}
}

// The strings alternate a <-> b, but the one-time resource is spent, so
// there is no cycle. A string-only check would wrongly report one.
func TestResourcesPreventStringCycle(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "ab", LHS: "a", RHS: "b", Delta: []int{-1}},
			{Name: "ba", LHS: "b", RHS: "a", Delta: []int{-1}},
		},
		Init:   State{Str: "a", Resources: []int{1}},
		MaxLen: 1,
	}
	res := mustSearch(t, cfg, 100)
	if res.Status != NoCycle || res.Longest != 1 {
		t.Fatalf("got %v longest=%d, want NoCycle longest=1", res.Status, res.Longest)
	}
}

// A cycle that consumes and restores a resource is genuine: the full
// state (string, control, resources) repeats.
func TestResourceCycle(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "ab", LHS: "a", RHS: "b", Delta: []int{-1}},
			{Name: "ba", LHS: "b", RHS: "a", Delta: []int{1}},
		},
		Init:        State{Str: "a", Resources: []int{1}},
		MaxLen:      1,
		MaxResource: []int{1},
	}
	res := mustSearch(t, cfg, 100)
	if res.Status != FoundCycle {
		t.Fatalf("status = %v, want FoundCycle", res.Status)
	}
	if len(res.Cycle) != 2 {
		t.Fatalf("Cycle = %v, want 2 steps", res.Cycle)
	}
	if err := Replay(cfg, res.Entry, res.Cycle); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

// Control states participate in the state identity and gate the rules.
func TestControlState(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "ab", LHS: "a", RHS: "b", From: 0, To: 1},
			{Name: "ba", LHS: "b", RHS: "a", From: 1, To: 0},
		},
		Init:   State{Str: "a", Ctl: 0},
		MaxLen: 1,
	}
	res := mustSearch(t, cfg, 100)
	if res.Status != FoundCycle {
		t.Fatalf("status = %v, want FoundCycle", res.Status)
	}
	if err := Replay(cfg, res.Entry, res.Cycle); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	// Same strings, but the way back needs control state 2: no cycle.
	cfg.Rules[1].From = 2
	res = mustSearch(t, cfg, 100)
	if res.Status != NoCycle || res.Longest != 1 {
		t.Fatalf("got %v longest=%d, want NoCycle longest=1", res.Status, res.Longest)
	}
}

// Pause/resume: small budgets return Unknown, and the accumulated search
// reaches the same final result as a single large-budget run.
func TestPauseResume(t *testing.T) {
	cfg := Config{
		Rules:  []Rule{{Name: "grow", LHS: "a", RHS: "aa"}},
		Init:   State{Str: "a"},
		MaxLen: 8,
	}
	oneShot := mustSearch(t, cfg, 1000)
	if oneShot.Status != NoCycle || oneShot.Longest != 7 {
		t.Fatalf("one-shot: got %v longest=%d, want NoCycle longest=7", oneShot.Status, oneShot.Longest)
	}

	s, err := NewSearcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var res Result
	for i := 0; i < 10; i++ {
		res = s.Run(2)
		if res.Status != Unknown {
			break
		}
	}
	if res.Status != oneShot.Status || res.Longest != oneShot.Longest {
		t.Fatalf("incremental: got %v longest=%d, want %v longest=%d",
			res.Status, res.Longest, oneShot.Status, oneShot.Longest)
	}
	if res.Explored != oneShot.Explored {
		t.Fatalf("explored = %d, want %d", res.Explored, oneShot.Explored)
	}
	// The result is cached and idempotent.
	if again := s.Run(0); again.Status != res.Status || again.Longest != res.Longest {
		t.Fatalf("cached run mismatch: %v vs %v", again, res)
	}
}

// Pause/resume over a cyclic system yields the same evidence as one shot.
func TestPauseResumeCycle(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "ab", LHS: "a", RHS: "b"},
			{Name: "ba", LHS: "b", RHS: "a"},
		},
		Init:   State{Str: "a"},
		MaxLen: 1,
	}
	s, err := NewSearcher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res := s.Run(1); res.Status != Unknown {
		t.Fatalf("first run: status = %v, want Unknown", res.Status)
	}
	res := s.Run(100)
	if res.Status != FoundCycle {
		t.Fatalf("status = %v, want FoundCycle", res.Status)
	}
	if err := Replay(cfg, res.Entry, res.Cycle); err != nil {
		t.Fatalf("Replay: %v", err)
	}
}

func TestBudgetExhaustedUnknown(t *testing.T) {
	cfg := Config{
		Rules:  []Rule{{Name: "grow", LHS: "a", RHS: "aa"}},
		Init:   State{Str: "a"},
		MaxLen: 8,
	}
	res := mustSearch(t, cfg, 0)
	if res.Status != Unknown {
		t.Fatalf("status = %v, want Unknown", res.Status)
	}
}

// Replay rejects evidence that only closes on the string while one-time
// resources are drained: the claimed cycle is not a real one.
func TestReplayRejectsResourceReuse(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "ab", LHS: "a", RHS: "b", Delta: []int{-1}},
			{Name: "ba", LHS: "b", RHS: "a"},
		},
		Init:   State{Str: "a", Resources: []int{1}},
		MaxLen: 1,
	}
	// The searcher agrees there is no cycle: (a,1) -> (b,0) -> (a,0),
	// and (a,0) is dead because rule ab needs a resource.
	res := mustSearch(t, cfg, 100)
	if res.Status != NoCycle || res.Longest != 2 {
		t.Fatalf("got %v longest=%d, want NoCycle longest=2", res.Status, res.Longest)
	}
	// Fabricated evidence: the string returns to "a" but resources don't.
	err := Replay(cfg, nil, []Step{{Rule: 0, Position: 0}, {Rule: 1, Position: 0}})
	if err == nil || !strings.Contains(err.Error(), "does not close") {
		t.Fatalf("Replay err = %v, want 'does not close'", err)
	}
}

func TestReplayRejectsBadEvidence(t *testing.T) {
	cfg := Config{
		Rules: []Rule{
			{Name: "ab", LHS: "a", RHS: "b"},
			{Name: "ba", LHS: "b", RHS: "a"},
		},
		Init:   State{Str: "a"},
		MaxLen: 1,
	}
	cases := []struct {
		name  string
		entry []Step
		cycle []Step
	}{
		{"wrong position", nil, []Step{{Rule: 0, Position: 1}}},
		{"bad rule index", nil, []Step{{Rule: 7, Position: 0}}},
		{"empty cycle", nil, nil},
		{"non-closing", nil, []Step{{Rule: 0, Position: 0}}},
	}
	for _, tc := range cases {
		if err := Replay(cfg, tc.entry, tc.cycle); err == nil {
			t.Fatalf("%s: Replay succeeded, want error", tc.name)
		}
	}

	// Control-state mismatch is caught too.
	ctlCfg := Config{
		Rules: []Rule{
			{Name: "ab", LHS: "a", RHS: "b", From: 0, To: 1},
			{Name: "ba", LHS: "b", RHS: "a", From: 1, To: 0},
		},
		Init:   State{Str: "a", Ctl: 0},
		MaxLen: 1,
	}
	if err := Replay(ctlCfg, nil, []Step{{Rule: 1, Position: 0}}); err == nil {
		t.Fatal("control-state mismatch: Replay succeeded, want error")
	}
}

func TestValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty LHS", Config{Rules: []Rule{{LHS: ""}}, MaxLen: 1}},
		{"init too long", Config{Init: State{Str: "aaa"}, MaxLen: 1}},
		{"unbounded production", Config{
			Rules: []Rule{{LHS: "a", RHS: "a", Delta: []int{1}}},
			Init:  State{Str: "a", Resources: []int{0}}, MaxLen: 1}},
		{"delta dimension", Config{
			Rules: []Rule{{LHS: "a", RHS: "a", Delta: []int{1, 2}}},
			Init:  State{Str: "a", Resources: []int{0}}, MaxLen: 1, MaxResource: []int{1}}},
	}
	for _, tc := range cases {
		if _, err := NewSearcher(tc.cfg); err == nil {
			t.Fatalf("%s: want validation error", tc.name)
		}
	}
}
