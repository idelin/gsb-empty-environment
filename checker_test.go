package main

import "testing"

type testState struct {
	labels []string
	next   []string
}

// build constructs a Machine from an adjacency description for tests.
func build(initial string, states map[string]testState, order []string) *Machine {
	m := &Machine{
		Initial: initial,
		Order:   order,
		Labels:  make(map[string]map[string]bool),
		Adj:     make(map[string][]string),
	}
	for _, name := range order {
		st := states[name]
		labels := make(map[string]bool)
		for _, l := range st.labels {
			labels[l] = true
		}
		m.Labels[name] = labels
		m.Adj[name] = append([]string{}, st.next...)
	}
	return m
}

func TestLivenessPassRespondsOnCycle(t *testing.T) {
	m := build("s0", map[string]testState{
		"s0": {nil, []string{"s1"}},
		"s1": {[]string{"request"}, []string{"s2"}},
		"s2": {[]string{"response"}, []string{"s0"}},
	}, []string{"s0", "s1", "s2"})
	if r := m.CheckLiveness("request", "response"); !r.OK {
		t.Fatalf("expected pass, got failure with prefix %v cycle %v", r.Prefix, r.Cycle)
	}
}

func TestLivenessFailLasso(t *testing.T) {
	m := build("s0", map[string]testState{
		"s0": {nil, []string{"s1"}},
		"s1": {[]string{"request"}, []string{"s2"}},
		"s2": {nil, []string{"s3"}},
		"s3": {nil, []string{"s2"}},
	}, []string{"s0", "s1", "s2", "s3"})
	r := m.CheckLiveness("request", "response")
	if r.OK {
		t.Fatal("expected liveness failure")
	}
	if r.ReqState != "s1" {
		t.Fatalf("expected request state s1, got %q", r.ReqState)
	}
	if len(r.Cycle) < 2 || r.Cycle[0] != r.Cycle[len(r.Cycle)-1] {
		t.Fatalf("cycle witness must be a closed loop, got %v", r.Cycle)
	}
	for _, s := range r.Cycle {
		if m.HasLabel(s, "response") {
			t.Fatalf("cycle %v contains a response state", r.Cycle)
		}
	}
	if len(r.Prefix) == 0 || r.Prefix[0] != "s0" {
		t.Fatalf("prefix must start at initial state, got %v", r.Prefix)
	}
}

func TestLivenessFiniteUnresponsivePathIsNotFailure(t *testing.T) {
	// Every finite path without a response can still be extended to one;
	// there is no response-free cycle, so the property must hold.
	m := build("s0", map[string]testState{
		"s0": {nil, []string{"s1"}},
		"s1": {[]string{"request"}, []string{"s2", "s4"}},
		"s2": {nil, []string{"s3"}},
		"s3": {[]string{"response"}, nil},
		"s4": {nil, []string{"s5"}},
		"s5": {nil, []string{"s6"}},
		"s6": {[]string{"response"}, nil},
	}, []string{"s0", "s1", "s2", "s3", "s4", "s5", "s6"})
	if r := m.CheckLiveness("request", "response"); !r.OK {
		t.Fatalf("finite unresponsive paths must not count as permanent failure, got prefix %v cycle %v", r.Prefix, r.Cycle)
	}
}

func TestLivenessUnreachableViolationIgnored(t *testing.T) {
	// s9/s10 form an unreachable response-free lasso with a pending request.
	m := build("s0", map[string]testState{
		"s0":  {nil, []string{"s1"}},
		"s1":  {[]string{"request"}, []string{"s2"}},
		"s2":  {[]string{"response"}, []string{"s0"}},
		"s9":  {[]string{"request"}, []string{"s10"}},
		"s10": {nil, []string{"s9"}},
	}, []string{"s0", "s1", "s2", "s9", "s10"})
	if r := m.CheckLiveness("request", "response"); !r.OK {
		t.Fatalf("unreachable states must not affect the verdict, got prefix %v cycle %v", r.Prefix, r.Cycle)
	}
}

func TestLivenessSelfLoopViolation(t *testing.T) {
	m := build("s0", map[string]testState{
		"s0": {nil, []string{"s1"}},
		"s1": {[]string{"request"}, []string{"s1"}},
	}, []string{"s0", "s1"})
	r := m.CheckLiveness("request", "response")
	if r.OK {
		t.Fatal("expected liveness failure on self-loop")
	}
	if len(r.Cycle) != 2 || r.Cycle[0] != "s1" || r.Cycle[1] != "s1" {
		t.Fatalf("expected self-loop cycle [s1 s1], got %v", r.Cycle)
	}
}

func TestSafetyFailDangerWithoutAuth(t *testing.T) {
	m := build("s0", map[string]testState{
		"s0": {nil, []string{"s1"}},
		"s1": {nil, []string{"s2"}},
		"s2": {[]string{"danger"}, nil},
	}, []string{"s0", "s1", "s2"})
	r := m.CheckSafety("authorized", "danger")
	if r.OK {
		t.Fatal("expected safety failure")
	}
	want := []string{"s0", "s1", "s2"}
	if len(r.Path) != len(want) {
		t.Fatalf("expected path %v, got %v", want, r.Path)
	}
	for i := range want {
		if r.Path[i] != want[i] {
			t.Fatalf("expected path %v, got %v", want, r.Path)
		}
	}
}

func TestSafetyPassAuthorizedFirst(t *testing.T) {
	m := build("s0", map[string]testState{
		"s0": {nil, []string{"s1"}},
		"s1": {[]string{"authorized"}, []string{"s2"}},
		"s2": {[]string{"danger"}, nil},
	}, []string{"s0", "s1", "s2"})
	if r := m.CheckSafety("authorized", "danger"); !r.OK {
		t.Fatalf("expected pass, got witness %v", r.Path)
	}
}

func TestSafetyFailAuthOnlyOnParallelBranch(t *testing.T) {
	// One branch authorizes before danger; the other does not.
	m := build("s0", map[string]testState{
		"s0": {nil, []string{"s1", "s3"}},
		"s1": {[]string{"authorized"}, []string{"s2"}},
		"s2": {[]string{"danger"}, nil},
		"s3": {nil, []string{"s4"}},
		"s4": {[]string{"danger"}, nil},
	}, []string{"s0", "s1", "s2", "s3", "s4"})
	r := m.CheckSafety("authorized", "danger")
	if r.OK {
		t.Fatal("expected safety failure via unauthorized branch")
	}
	last := r.Path[len(r.Path)-1]
	if last != "s4" {
		t.Fatalf("expected witness ending at s4, got %v", r.Path)
	}
	for _, s := range r.Path[:len(r.Path)-1] {
		if m.HasLabel(s, "authorized") {
			t.Fatalf("witness path %v passes through an authorized state", r.Path)
		}
	}
}

func TestSafetyUnreachableDangerIgnored(t *testing.T) {
	m := build("s0", map[string]testState{
		"s0":  {nil, []string{"s1"}},
		"s1":  {[]string{"authorized"}, []string{"s2"}},
		"s2":  {[]string{"danger"}, nil},
		"s9":  {nil, []string{"s10"}},
		"s10": {[]string{"danger"}, nil},
	}, []string{"s0", "s1", "s2", "s9", "s10"})
	if r := m.CheckSafety("authorized", "danger"); !r.OK {
		t.Fatalf("unreachable danger must not affect the verdict, got %v", r.Path)
	}
}

func TestSafetyDangerAtInitial(t *testing.T) {
	m := build("s0", map[string]testState{
		"s0": {[]string{"danger"}, nil},
	}, []string{"s0"})
	r := m.CheckSafety("authorized", "danger")
	if r.OK {
		t.Fatal("expected failure when initial state is danger")
	}
	if len(r.Path) != 1 || r.Path[0] != "s0" {
		t.Fatalf("expected witness [s0], got %v", r.Path)
	}
}
