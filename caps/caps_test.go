package caps

import "testing"

func names(acts []Action) []string {
	var out []string
	for _, a := range acts {
		out = append(out, a.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Chain: c0 -> c1 -> c2, BFS must find the shortest 3-step plan and the
// independent replay must confirm every step.
func TestShortestChain(t *testing.T) {
	p := Problem{
		Goal: Cap(2),
		Actions: []Action{
			{Name: "gain0", Add: Cap(0)},
			{Name: "gain1", Require: Cap(0), Add: Cap(1)},
			{Name: "gain2", Require: Cap(1), Add: Cap(2)},
			{Name: "noop", Add: 0},
		},
	}
	sol, ok := Solve(p)
	if !ok {
		t.Fatal("expected a plan")
	}
	want := []string{"gain0", "gain1", "gain2"}
	if !equalStrings(names(sol.Actions), want) {
		t.Fatalf("got plan %v, want %v", names(sol.Actions), want)
	}
	wantStates := []Set{0, Cap(0), Cap(0) | Cap(1), Cap(0) | Cap(1) | Cap(2)}
	if len(sol.States) != len(wantStates) {
		t.Fatalf("got %d states, want %d", len(sol.States), len(wantStates))
	}
	for i, s := range wantStates {
		if sol.States[i] != s {
			t.Fatalf("state %d: got %v, want %v", i, sol.States[i], s)
		}
	}
	if _, err := Replay(p, sol.Actions); err != nil {
		t.Fatalf("replay rejected BFS plan: %v", err)
	}
}

// Non-monotonic revocation: the goal is only reachable by gaining c0 and
// later losing it, because "unlock" requires c0 to be absent.
func TestNonMonotonicRevocation(t *testing.T) {
	p := Problem{
		Goal: Cap(2),
		Actions: []Action{
			{Name: "grantA", Add: Cap(0)},
			{Name: "grantB", Require: Cap(0), Add: Cap(1)},
			{Name: "revokeA", Require: Cap(1), Del: Cap(0)},
			{Name: "unlock", Require: Cap(1), Forbid: Cap(0), Add: Cap(2)},
		},
	}
	sol, ok := Solve(p)
	if !ok {
		t.Fatal("expected a plan")
	}
	want := []string{"grantA", "grantB", "revokeA", "unlock"}
	if !equalStrings(names(sol.Actions), want) {
		t.Fatalf("got plan %v, want %v", names(sol.Actions), want)
	}
	// The plan must not be monotonic: c0 is gained then revoked.
	if IsMonotonicGrowth(sol.States) {
		t.Fatalf("expected non-monotonic capability sets, got %v", sol.States)
	}
	if sol.States[3] != Cap(1) {
		t.Fatalf("after revocation got %v, want {c1}", sol.States[3])
	}
	replayed, err := Replay(p, sol.Actions)
	if err != nil {
		t.Fatalf("replay rejected BFS plan: %v", err)
	}
	for i := range sol.States {
		if replayed.States[i] != sol.States[i] {
			t.Fatalf("replay state %d differs: %v vs %v", i, replayed.States[i], sol.States[i])
		}
	}
}

// Replay must catch a plan that skips the revocation step (precondition
// Forbid violated), proving the verifier enforces non-monotonic deletes.
func TestReplayRejectsMissingRevocation(t *testing.T) {
	p := Problem{
		Goal: Cap(2),
		Actions: []Action{
			{Name: "grantA", Add: Cap(0)},
			{Name: "grantB", Require: Cap(0), Add: Cap(1)},
			{Name: "revokeA", Require: Cap(1), Del: Cap(0)},
			{Name: "unlock", Require: Cap(1), Forbid: Cap(0), Add: Cap(2)},
		},
	}
	bad := []Action{p.Actions[0], p.Actions[1], p.Actions[3]}
	if _, err := Replay(p, bad); err == nil {
		t.Fatal("replay accepted a plan that skips revocation")
	}
}

// Forbidden states force BFS around the direct route.
func TestForbiddenStateDetour(t *testing.T) {
	p := Problem{
		Goal:      Cap(1),
		Forbidden: []Set{Cap(0)},
		Actions: []Action{
			{Name: "direct", Add: Cap(0) | Cap(1)},       // passes through forbidden {c0}? no: single step
			{Name: "via0", Add: Cap(0)},                  // enters forbidden {c0}
			{Name: "via1", Require: Cap(0), Add: Cap(1)}, // would need forbidden state
			{Name: "detourA", Add: Cap(2)},
			{Name: "detourB", Require: Cap(2), Add: Cap(1)},
		},
	}
	sol, ok := Solve(p)
	if !ok {
		t.Fatal("expected a plan")
	}
	// Shortest legal plan is the single "direct" action (it never enters
	// the forbidden state {c0} exactly).
	if !equalStrings(names(sol.Actions), []string{"direct"}) {
		t.Fatalf("got plan %v, want [direct]", names(sol.Actions))
	}
	if _, err := Replay(p, sol.Actions); err != nil {
		t.Fatalf("replay rejected plan: %v", err)
	}

	// Forbid every state the direct route can land in: only the detour remains.
	p.Forbidden = append(p.Forbidden, Cap(0)|Cap(1), Cap(0)|Cap(1)|Cap(2))
	sol, ok = Solve(p)
	if !ok {
		t.Fatal("expected a detour plan")
	}
	if !equalStrings(names(sol.Actions), []string{"detourA", "detourB"}) {
		t.Fatalf("got plan %v, want [detourA detourB]", names(sol.Actions))
	}
	for _, s := range sol.States {
		for _, f := range p.Forbidden {
			if s == f {
				t.Fatalf("plan enters forbidden state %v", s)
			}
		}
	}
	if _, err := Replay(p, sol.Actions); err != nil {
		t.Fatalf("replay rejected detour plan: %v", err)
	}
}

// Goal already satisfied initially: the plan must be empty.
func TestInitiallySatisfied(t *testing.T) {
	p := Problem{
		Initial: Cap(0) | Cap(1),
		Goal:    Cap(1),
		Actions: []Action{{Name: "gain2", Add: Cap(2)}},
	}
	sol, ok := Solve(p)
	if !ok {
		t.Fatal("expected an (empty) plan")
	}
	if len(sol.Actions) != 0 {
		t.Fatalf("got %d actions, want 0", len(sol.Actions))
	}
	if len(sol.States) != 1 || sol.States[0] != p.Initial {
		t.Fatalf("got states %v, want [%v]", sol.States, p.Initial)
	}
	if _, err := Replay(p, sol.Actions); err != nil {
		t.Fatalf("replay rejected empty plan: %v", err)
	}
}

// Unsolvable: the goal capability is never granted.
func TestUnsolvable(t *testing.T) {
	p := Problem{
		Goal:    Cap(3),
		Actions: []Action{{Name: "gain0", Add: Cap(0)}},
	}
	if _, ok := Solve(p); ok {
		t.Fatal("expected no plan")
	}
}

// Forbidden initial state: no plan may exist.
func TestForbiddenInitial(t *testing.T) {
	p := Problem{
		Initial:   Cap(0),
		Goal:      Cap(1),
		Forbidden: []Set{Cap(0)},
		Actions:   []Action{{Name: "gain1", Add: Cap(1)}},
	}
	if _, ok := Solve(p); ok {
		t.Fatal("expected no plan from a forbidden initial state")
	}
}
