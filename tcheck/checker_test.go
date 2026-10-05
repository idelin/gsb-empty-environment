package tcheck

import "testing"

func mustLoad(t *testing.T, doc string) *Machine {
	t.Helper()
	m, err := Load([]byte(doc))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

// A request that is always answered, plus a danger state reachable only
// via an authorized state: both properties hold.
func TestBothPropertiesHold(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "idle",
	  "states": [
	    {"name": "idle",    "props": []},
	    {"name": "pending", "props": ["request"]},
	    {"name": "served",  "props": ["response"]},
	    {"name": "auth",    "props": ["authorized"]},
	    {"name": "hazard",  "props": ["danger"]}
	  ],
	  "transitions": [
	    ["idle", "pending"],
	    ["pending", "served"],
	    ["served", "idle"],
	    ["idle", "auth"],
	    ["auth", "hazard"],
	    ["hazard", "idle"]
	  ]
	}`)
	if r := m.CheckSafety("danger", "authorized"); !r.OK {
		t.Fatalf("safety should hold, got %v", r)
	}
	if r := m.CheckLiveness("request", "response"); !r.OK {
		t.Fatalf("liveness should hold, got %v", r)
	}
}

// A reachable cycle containing a request and no response is a
// permanent failure; the witness must be prefix + cycle.
func TestLivenessViolationHasLassoWitness(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "idle",
	  "states": [
	    {"name": "idle",    "props": []},
	    {"name": "pending", "props": ["request"]},
	    {"name": "spin_a",  "props": []},
	    {"name": "spin_b",  "props": []}
	  ],
	  "transitions": [
	    ["idle", "pending"],
	    ["pending", "spin_a"],
	    ["spin_a", "spin_b"],
	    ["spin_b", "spin_a"]
	  ]
	}`)
	r := m.CheckLiveness("request", "response")
	if r.OK {
		t.Fatal("liveness should be violated")
	}
	if len(r.Prefix) == 0 || r.Prefix[0] != "idle" {
		t.Fatalf("prefix must start at initial state, got %v", r.Prefix)
	}
	if last := r.Prefix[len(r.Prefix)-1]; last != r.Cycle[0] {
		t.Fatalf("prefix %v must end where cycle %v begins", r.Prefix, r.Cycle)
	}
	if first, last := r.Cycle[0], r.Cycle[len(r.Cycle)-1]; first != last {
		t.Fatalf("cycle must be closed, got %v", r.Cycle)
	}
	for _, s := range r.Cycle[:len(r.Cycle)-1] {
		if m.Has(s, "response") {
			t.Fatalf("cycle %v must not contain a response state", r.Cycle)
		}
	}
}

// A finite path that ends in a deadlocked request state is NOT a
// permanent failure: there is no infinite execution to blame.
func TestFiniteUnresponsivePathIsNotFailure(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "idle",
	  "states": [
	    {"name": "idle",    "props": []},
	    {"name": "pending", "props": ["request"]},
	    {"name": "stuck",   "props": []}
	  ],
	  "transitions": [
	    ["idle", "pending"],
	    ["pending", "stuck"]
	  ]
	}`)
	if r := m.CheckLiveness("request", "response"); !r.OK {
		t.Fatalf("deadlock must not count as liveness failure, got %v", r)
	}
}

// A self-loop on a request state is a genuine permanent failure.
func TestSelfLoopOnRequestIsFailure(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "q",
	  "states": [
	    {"name": "q", "props": ["request"]}
	  ],
	  "transitions": [["q", "q"]]
	}`)
	r := m.CheckLiveness("request", "response")
	if r.OK {
		t.Fatal("self-loop on request should violate liveness")
	}
	if got := r.Cycle.String(); got != "q -> q" {
		t.Fatalf("unexpected cycle %q", got)
	}
}

// A cycle that passes through a response state answers the request.
func TestCycleThroughResponseIsFine(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "pending",
	  "states": [
	    {"name": "pending", "props": ["request"]},
	    {"name": "served",  "props": ["response"]}
	  ],
	  "transitions": [
	    ["pending", "served"],
	    ["served", "pending"]
	  ]
	}`)
	if r := m.CheckLiveness("request", "response"); !r.OK {
		t.Fatalf("liveness should hold, got %v", r)
	}
}

// Unreachable states must not influence either verdict: an unreachable
// request-state cycle and an unreachable unauthorized danger entry are
// both ignored.
func TestUnreachableStatesAreIgnored(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "idle",
	  "states": [
	    {"name": "idle",     "props": []},
	    {"name": "ghost_q",  "props": ["request"]},
	    {"name": "ghost_d",  "props": ["danger"]},
	    {"name": "ghost_in", "props": []}
	  ],
	  "transitions": [
	    ["idle", "idle"],
	    ["ghost_q", "ghost_q"],
	    ["ghost_in", "ghost_d"]
	  ]
	}`)
	if r := m.CheckLiveness("request", "response"); !r.OK {
		t.Fatalf("unreachable request cycle must be ignored, got %v", r)
	}
	if r := m.CheckSafety("danger", "authorized"); !r.OK {
		t.Fatalf("unreachable danger entry must be ignored, got %v", r)
	}
}

// Entering danger from a non-authorized state violates safety and
// yields a shortest witness ending in the offending transition.
func TestSafetyViolationWitness(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "idle",
	  "states": [
	    {"name": "idle",   "props": []},
	    {"name": "hazard", "props": ["danger"]},
	    {"name": "auth",   "props": ["authorized"]}
	  ],
	  "transitions": [
	    ["idle", "hazard"],
	    ["idle", "auth"],
	    ["auth", "hazard"],
	    ["hazard", "idle"]
	  ]
	}`)
	r := m.CheckSafety("danger", "authorized")
	if r.OK {
		t.Fatal("safety should be violated")
	}
	if r.Entered != "hazard" {
		t.Fatalf("expected entered=hazard, got %q", r.Entered)
	}
	if got := r.Witness.String(); got != "idle -> hazard" {
		t.Fatalf("unexpected witness %q", got)
	}
}

// A danger state that is itself authorized is acceptable.
func TestDangerStateMayAuthorizeItself(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "idle",
	  "states": [
	    {"name": "idle",   "props": []},
	    {"name": "hazard", "props": ["danger", "authorized"]}
	  ],
	  "transitions": [["idle", "hazard"], ["hazard", "idle"]]
	}`)
	if r := m.CheckSafety("danger", "authorized"); !r.OK {
		t.Fatalf("self-authorized danger state should be fine, got %v", r)
	}
}

// Starting directly in an unauthorized danger state is a violation.
func TestInitialDangerStateViolates(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "hazard",
	  "states": [{"name": "hazard", "props": ["danger"]}],
	  "transitions": []
	}`)
	r := m.CheckSafety("danger", "authorized")
	if r.OK {
		t.Fatal("initial unauthorized danger state should violate safety")
	}
	if got := r.Witness.String(); got != "hazard" {
		t.Fatalf("unexpected witness %q", got)
	}
}

// A request reachable only after the response-free region is left
// behind is still checked: request on a cycle without response fails
// even if other requests in the system are answered.
func TestSecondRequestKindMayFail(t *testing.T) {
	m := mustLoad(t, `{
	  "initial": "a",
	  "states": [
	    {"name": "a", "props": ["request"]},
	    {"name": "b", "props": ["response"]},
	    {"name": "c", "props": ["request"]},
	    {"name": "d", "props": []}
	  ],
	  "transitions": [
	    ["a", "b"],
	    ["b", "a"],
	    ["b", "c"],
	    ["c", "d"],
	    ["d", "c"]
	  ]
	}`)
	r := m.CheckLiveness("request", "response")
	if r.OK {
		t.Fatal("request on the c<->d cycle is never answered")
	}
	if r.Cycle[0] != "c" && r.Cycle[0] != "d" {
		t.Fatalf("cycle should be within {c,d}, got %v", r.Cycle)
	}
}

// Invalid input is rejected.
func TestLoadErrors(t *testing.T) {
	if _, err := Load([]byte(`{"initial": "x", "states": [], "transitions": []}`)); err == nil {
		t.Fatal("undeclared initial state should be rejected")
	}
	if _, err := Load([]byte(`{
	  "initial": "a",
	  "states": [{"name": "a", "props": []}],
	  "transitions": [["a", "b"]]
	}`)); err == nil {
		t.Fatal("transition to undeclared state should be rejected")
	}
}
