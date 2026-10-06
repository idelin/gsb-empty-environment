package reqresp

import (
	"fmt"
	"sort"
	"strings"
)

// Verify independently replays a claimed counterexample against g and
// confirms it is genuine. It re-simulates the run step by step — checking
// that every step is an actual edge of g, tracking pending request ids
// from scratch — and then validates the failure condition:
//
//   - lasso (Cycle non-empty): the cycle returns to the exact product
//     state it started from (so it can repeat forever) and at least one
//     id in Owed stays pending at every point around the loop;
//   - finite (Cycle empty): the run ends in a terminal state of g with
//     at least one id in Owed still pending.
//
// Verify does not trust Violation.Owed: it recomputes the owed ids and
// requires Owed to be a non-empty subset of them.
func Verify(g *Graph, v *Violation) error {
	if v == nil {
		return fmt.Errorf("reqresp: nil violation")
	}
	if len(v.Owed) == 0 {
		return fmt.Errorf("reqresp: violation claims no owed ids")
	}
	if dup := duplicateID(v.Owed); dup != "" {
		return fmt.Errorf("reqresp: violation lists owed id %q twice", dup)
	}

	sim := &replay{g: g}
	state := g.Initial
	var pending []string
	var err error

	for i, s := range v.Prefix {
		if state, pending, err = sim.step(state, pending, s); err != nil {
			return fmt.Errorf("reqresp: prefix step %d: %w", i, err)
		}
	}

	if len(v.Cycle) == 0 {
		if !g.Terminal(state) {
			return fmt.Errorf("reqresp: finite counterexample ends in non-terminal state %q", state)
		}
		return checkOwed(pending, v.Owed, "terminal state")
	}

	// Lasso: remember the product state at the cycle entry.
	entryKey := state + "\x00" + strings.Join(pending, "\x01")
	owedHere := append([]string(nil), pending...)
	for i, s := range v.Cycle {
		if state, pending, err = sim.step(state, pending, s); err != nil {
			return fmt.Errorf("reqresp: cycle step %d: %w", i, err)
		}
		owedHere = intersectSorted(owedHere, pending)
	}
	backKey := state + "\x00" + strings.Join(pending, "\x01")
	if backKey != entryKey {
		return fmt.Errorf("reqresp: cycle does not return to its entry product state (not repeatable)")
	}
	return checkOwed(owedHere, v.Owed, "cycle")
}

func checkOwed(actual, claimed []string, where string) error {
	if len(actual) == 0 {
		return fmt.Errorf("reqresp: no response is owed on the %s: not a violation", where)
	}
	for _, id := range claimed {
		if !containsSorted(actual, id) {
			return fmt.Errorf("reqresp: claimed owed id %q is not pending throughout the %s", id, where)
		}
	}
	return nil
}

// replay is a standalone simulator used only by Verify.
type replay struct {
	g *Graph
}

// step checks that s is a real edge of g leaving state and applies its
// action to the pending set, enforcing the request/response discipline.
func (r *replay) step(state string, pending []string, s Step) (string, []string, error) {
	if s.From != state {
		return "", nil, fmt.Errorf("step starts at %q but replay is at %q", s.From, state)
	}
	found := false
	for _, e := range r.g.out[state] {
		if e.To == s.To && e.Action == s.Action {
			found = true
			break
		}
	}
	if !found {
		return "", nil, fmt.Errorf("no edge %s --%s--> %s in graph", s.From, s.Action, s.To)
	}
	switch s.Action.Kind {
	case Internal:
		return s.To, pending, nil
	case Request:
		if containsSorted(pending, s.Action.ID) {
			return "", nil, fmt.Errorf("request id %q already pending", s.Action.ID)
		}
		next := append(append([]string(nil), pending...), s.Action.ID)
		sort.Strings(next)
		return s.To, next, nil
	case Response:
		if !containsSorted(pending, s.Action.ID) {
			return "", nil, fmt.Errorf("response id %q not pending", s.Action.ID)
		}
		next := make([]string, 0, len(pending)-1)
		for _, id := range pending {
			if id != s.Action.ID {
				next = append(next, id)
			}
		}
		return s.To, next, nil
	}
	return "", nil, fmt.Errorf("unknown action kind %d", s.Action.Kind)
}

func containsSorted(xs []string, x string) bool {
	i := sort.SearchStrings(xs, x)
	return i < len(xs) && xs[i] == x
}

func intersectSorted(a, b []string) []string {
	var out []string
	for _, x := range a {
		if containsSorted(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func duplicateID(ids []string) string {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return id
		}
		seen[id] = true
	}
	return ""
}
