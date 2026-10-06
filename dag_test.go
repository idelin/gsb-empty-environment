package dagexec

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// buildDiamond creates the small DAG used across the tests:
//
//	A (root)   B (root)
//	 \         /
//	  C: all-success(A, B)   D: any-success(A, B)
//	   \                    /
//	    E: all-terminal(C, D)
func buildDiamond(t *testing.T) *Scheduler {
	t.Helper()
	s := New()
	s.Add("A", AllSuccess)
	s.Add("B", AllSuccess)
	s.Add("C", AllSuccess, "A", "B")
	s.Add("D", AnySuccess, "A", "B")
	s.Add("E", AllTerminal, "C", "D")
	return s
}

func mustStart(t *testing.T, s *Scheduler) []string {
	t.Helper()
	launched, err := s.Start()
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return launched
}

func mustComplete(t *testing.T, s *Scheduler, id string, outcome State) []string {
	t.Helper()
	launched, err := s.Complete(id, outcome)
	if err != nil {
		t.Fatalf("Complete(%s, %s): %v", id, outcome, err)
	}
	return launched
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// A failed parent must skip an all-success child immediately, before the
// other parent finishes, and must never leave anything waiting forever.
func TestImmediateSkipOnImpossibleCondition(t *testing.T) {
	s := buildDiamond(t)
	launched := mustStart(t, s)
	if !contains(launched, "A") || !contains(launched, "B") || len(launched) != 2 {
		t.Fatalf("roots launched = %v, want [A B]", launched)
	}

	launched = mustComplete(t, s, "A", Failed)
	if got := s.State("C"); got != Skipped {
		t.Fatalf("C state after A failed = %s, want skipped (immediate skip)", got)
	}
	if got := s.State("D"); got != Pending {
		t.Fatalf("D state after A failed = %s, want pending (B may still succeed)", got)
	}
	if got := s.State("E"); got != Pending {
		t.Fatalf("E state after A failed = %s, want pending (D not terminal)", got)
	}
	if len(launched) != 0 {
		t.Fatalf("launched after A failed = %v, want none", launched)
	}

	launched = mustComplete(t, s, "B", Succeeded)
	if !contains(launched, "D") || len(launched) != 1 {
		t.Fatalf("launched after B succeeded = %v, want [D]", launched)
	}
	if got := s.State("E"); got != Pending {
		t.Fatalf("E state while D running = %s, want pending", got)
	}

	launched = mustComplete(t, s, "D", Succeeded)
	if !contains(launched, "E") || len(launched) != 1 {
		t.Fatalf("launched after D succeeded = %v, want [E]", launched)
	}
	mustComplete(t, s, "E", Succeeded)

	for _, id := range s.IDs() {
		if st := s.State(id); !st.Terminal() {
			t.Errorf("task %s stuck in non-terminal state %s", id, st)
		}
	}
}

// Failed and cancelled parents must be recorded as distinct causes, and the
// reason chain must explain every skip/launch.
func TestReasonChainDistinguishesFailedAndCancelled(t *testing.T) {
	s := buildDiamond(t)
	mustStart(t, s)
	mustComplete(t, s, "A", Failed)
	// B cancelled: C (all-success) already skipped; D (any-success) now has
	// no chance of a success and must be skipped by this same event; E
	// (all-terminal) must launch immediately because C and D are terminal.
	launched := mustComplete(t, s, "B", Cancelled)
	if !contains(launched, "E") || len(launched) != 1 {
		t.Fatalf("launched after B cancelled = %v, want [E]", launched)
	}
	if got := s.State("D"); got != Skipped {
		t.Fatalf("D state = %s, want skipped", got)
	}

	rc := s.Reason("C")
	if rc.Decision != Skip || rc.Cond != AllSuccess {
		t.Fatalf("C reason = %v, want skip [all-success]", rc)
	}
	// Canonical causes: every parent that did not succeed, independent of
	// the order in which A and B terminated.
	wantC := []Cause{{Parent: "A", State: Failed}, {Parent: "B", State: Cancelled}}
	if fmt.Sprint(rc.Causes) != fmt.Sprint(wantC) {
		t.Fatalf("C causes = %v, want %v", rc.Causes, wantC)
	}

	rd := s.Reason("D")
	if rd.Decision != Skip || rd.Cond != AnySuccess {
		t.Fatalf("D reason = %v, want skip [any-success]", rd)
	}
	wantD := []Cause{{Parent: "A", State: Failed}, {Parent: "B", State: Cancelled}}
	if fmt.Sprint(rd.Causes) != fmt.Sprint(wantD) {
		t.Fatalf("D causes = %v, want %v (failed and cancelled recorded separately)", rd.Causes, wantD)
	}

	re := s.Reason("E")
	if re.Decision != Launch || re.Cond != AllTerminal {
		t.Fatalf("E reason = %v, want launch [all-terminal]", re)
	}
	wantE := []Cause{{Parent: "C", State: Skipped}, {Parent: "D", State: Skipped}}
	if fmt.Sprint(re.Causes) != fmt.Sprint(wantE) {
		t.Fatalf("E causes = %v, want %v", re.Causes, wantE)
	}

	chain := s.Explain("E")
	for _, frag := range []string{
		"E: running (launch [all-terminal]: C=skipped, D=skipped)",
		"C: skipped (skip [all-success]: A=failed, B=cancelled)",
		"A: failed",
		"D: skipped (skip [any-success]: A=failed, B=cancelled)",
		"B: cancelled",
	} {
		if !strings.Contains(chain, frag) {
			t.Errorf("reason chain missing %q:\n%s", frag, chain)
		}
	}
}

// Each task must be launched at most once and completed at most once.
func TestUniqueLaunchAndCompletion(t *testing.T) {
	s := buildDiamond(t)
	launched := mustStart(t, s)
	if len(launched) != 2 {
		t.Fatalf("roots launched = %v, want 2", launched)
	}
	if again, _ := s.Start(); len(again) != 0 {
		t.Fatalf("second Start launched %v, want none (unique launch)", again)
	}

	mustComplete(t, s, "A", Succeeded)
	if _, err := s.Complete("A", Failed); err == nil {
		t.Fatal("second Complete(A) succeeded, want error (unique completion)")
	}
	if _, err := s.Complete("C", Succeeded); err == nil {
		t.Fatal("Complete(C) while pending succeeded, want error")
	}
	if _, err := s.Complete("A", Skipped); err == nil {
		t.Fatal("Complete with Skipped outcome succeeded, want error")
	}
	if _, err := s.Complete("ZZZ", Succeeded); err == nil {
		t.Fatal("Complete of unknown task succeeded, want error")
	}

	mustComplete(t, s, "B", Succeeded)
	if got := s.State("C"); got != Running {
		t.Fatalf("C state = %s, want running (launched exactly once)", got)
	}
}

// outcomeKey identifies a run by the outcomes of the tasks that ran.
func outcomeKey(outcomes map[string]State) string {
	ids := make([]string, 0, len(outcomes))
	for id := range outcomes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "%s=%s;", id, outcomes[id])
	}
	return b.String()
}

// signature captures the full observable result of a run: final states and
// reason chains.
func signature(s *Scheduler) string {
	var b strings.Builder
	for _, id := range s.IDs() {
		fmt.Fprintf(&b, "%s=%s|%s\n", id, s.State(id), s.Reason(id))
	}
	return b.String()
}

// TestExhaustiveTerminalOrderings enumerates every possible order in which
// the tasks of the diamond DAG can reach a terminal state, with every
// possible outcome (succeeded/failed/cancelled) for every launched task.
// For every run it checks the model invariants, and across runs it checks
// confluence: the same outcome assignment always yields the same final
// states and reason chains, regardless of ordering.
func TestExhaustiveTerminalOrderings(t *testing.T) {
	terminalOutcomes := []State{Succeeded, Failed, Cancelled}
	results := map[string]string{}
	leaves := 0

	var walk func(s *Scheduler, running []string, outcomes map[string]State)
	walk = func(s *Scheduler, running []string, outcomes map[string]State) {
		if len(running) == 0 {
			leaves++
			checkRunInvariants(t, s, outcomes)
			key := outcomeKey(outcomes)
			sig := signature(s)
			if prev, ok := results[key]; ok && prev != sig {
				t.Errorf("non-confluent run for outcomes %s:\n--- first ---\n%s--- second ---\n%s", key, prev, sig)
			}
			results[key] = sig
			return
		}
		for i, id := range running {
			for _, outcome := range terminalOutcomes {
				s2 := s.Clone()
				launched, err := s2.Complete(id, outcome)
				if err != nil {
					t.Fatalf("Complete(%s, %s): %v", id, outcome, err)
				}
				next := make([]string, 0, len(running)-1+len(launched))
				next = append(next, running[:i]...)
				next = append(next, running[i+1:]...)
				next = append(next, launched...)
				o2 := make(map[string]State, len(outcomes)+1)
				for k, v := range outcomes {
					o2[k] = v
				}
				o2[id] = outcome
				walk(s2, next, o2)
			}
		}
	}

	s := buildDiamond(t)
	roots := mustStart(t, s)
	walk(s, roots, map[string]State{})

	t.Logf("enumerated %d terminal orderings, %d distinct outcome assignments", leaves, len(results))
	if leaves == 0 {
		t.Fatal("no runs enumerated")
	}
}

// checkRunInvariants verifies the model contract on one finished run.
func checkRunInvariants(t *testing.T, s *Scheduler, outcomes map[string]State) {
	t.Helper()
	for _, id := range s.IDs() {
		st := s.State(id)

		// No permanent waiting: everything reaches a terminal state.
		if !st.Terminal() {
			t.Errorf("run %s: task %s stuck in %s", outcomeKey(outcomes), id, st)
			continue
		}

		// Unique launch: a task ran iff it was launched exactly once
		// (recorded in outcomes by the driver), and a skipped task never ran.
		_, ran := outcomes[id]
		if st == Skipped && ran {
			t.Errorf("run %s: task %s skipped but was launched", outcomeKey(outcomes), id)
		}
		if st != Skipped && !ran {
			t.Errorf("run %s: task %s reached %s without being launched", outcomeKey(outcomes), id, st)
		}

		parents := s.Parents(id)
		if len(parents) == 0 {
			continue
		}

		// The recorded decision must match a re-evaluation over the final
		// parent states (terminal states never change, so this is exact).
		ps := make([]Cause, len(parents))
		for i, p := range parents {
			ps[i] = Cause{Parent: p, State: s.State(p)}
		}
		dec, causes := evaluate(s.Condition(id), ps)
		if st == Skipped && dec != Skip {
			t.Errorf("run %s: task %s skipped but final evaluation says %s", outcomeKey(outcomes), id, dec)
		}
		if ran && dec != Launch {
			t.Errorf("run %s: task %s launched but final evaluation says %s", outcomeKey(outcomes), id, dec)
		}

		// Reason chain: recorded causes must equal the canonical causes and
		// must match the parents' actual final states.
		r := s.Reason(id)
		if r.Decision != dec {
			t.Errorf("run %s: task %s reason decision %s, want %s", outcomeKey(outcomes), id, r.Decision, dec)
		}
		if fmt.Sprint(r.Causes) != fmt.Sprint(causes) {
			t.Errorf("run %s: task %s reason causes %v, want %v", outcomeKey(outcomes), id, r.Causes, causes)
		}
		for _, c := range r.Causes {
			if got := s.State(c.Parent); got != c.State {
				t.Errorf("run %s: task %s cause %s=%s, but parent is %s", outcomeKey(outcomes), id, c.Parent, c.State, got)
			}
		}
		if st == Skipped {
			for _, c := range r.Causes {
				if c.State == Succeeded {
					t.Errorf("run %s: skipped task %s has succeeded parent %s as cause", outcomeKey(outcomes), id, c.Parent)
				}
			}
		}
	}
}
