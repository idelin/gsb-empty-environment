package netsec

import (
	"errors"
	"testing"
)

// fakeDriver mirrors the engine state in its own map so tests can detect any
// divergence between the policy bookkeeping and the driver side.
type fakeDriver struct {
	pages      map[VirtPage]Mapping
	failAt     int // fail the Nth driver call (0-based); -1 disables
	permanent  bool
	calls      int
	failErr    error
	mapCalls   int
	unmapCalls int
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{pages: make(map[VirtPage]Mapping), failAt: -1, failErr: errors.New("simulated driver fault")}
}

func (f *fakeDriver) maybeFail() error {
	if f.failAt >= 0 && f.calls == f.failAt {
		if !f.permanent {
			f.failAt = -1
		}
		return f.failErr
	}
	f.calls++
	return nil
}

func (f *fakeDriver) Map(v VirtPage, p PhysPage, perm Perm) error {
	if err := f.maybeFail(); err != nil {
		return err
	}
	f.pages[v] = Mapping{Virt: v, Phys: p, Perm: perm}
	f.mapCalls++
	return nil
}

func (f *fakeDriver) Unmap(v VirtPage) error {
	if err := f.maybeFail(); err != nil {
		return err
	}
	delete(f.pages, v)
	f.unmapCalls++
	return nil
}

func (f *fakeDriver) Reprotect(v VirtPage, perm Perm) error {
	if err := f.maybeFail(); err != nil {
		return err
	}
	m := f.pages[v]
	m.Perm = perm
	f.pages[v] = m
	return nil
}

func (f *fakeDriver) state() *State {
	s := NewState()
	for v, m := range f.pages {
		s.Mappings[v] = m
	}
	return s
}

// allStates enumerates every mapping state over nv virtual and np physical
// pages for the given permission set, including unmapped pages.
func allStates(nv, np int, perms []Perm) []*State {
	type cell struct {
		phys PhysPage
		perm Perm
	}
	options := make([]cell, 0, 1+np*len(perms))
	options = append(options, cell{}) // unmapped sentinel: phys 0, perm 0
	for p := 0; p < np; p++ {
		for _, perm := range perms {
			options = append(options, cell{phys: PhysPage(p + 1), perm: perm})
		}
	}

	var states []*State
	chosen := make([]cell, nv)
	var rec func(i int)
	rec = func(i int) {
		if i == nv {
			s := NewState()
			for v, c := range chosen {
				if c.perm != 0 {
					vp := VirtPage(v + 1)
					s.Mappings[vp] = Mapping{Virt: vp, Phys: c.phys, Perm: c.perm}
				}
			}
			states = append(states, s)
			return
		}
		for _, c := range options {
			chosen[i] = c
			rec(i + 1)
		}
	}
	rec(0)
	return states
}

// referenceViolation independently recomputes the W^X invariant.
func referenceViolation(s *State) bool {
	type flags struct{ w, x bool }
	phys := make(map[PhysPage]flags)
	for _, m := range s.Mappings {
		f := phys[m.Phys]
		if m.Perm&PermWrite != 0 {
			f.w = true
		}
		if m.Perm&PermExec != 0 {
			f.x = true
		}
		phys[m.Phys] = f
	}
	for _, f := range phys {
		if f.w && f.x {
			return true
		}
	}
	return false
}

// txnBetween synthesizes a transaction transforming from into to.
func txnBetween(from, to *State) Txn {
	var txn Txn
	handled := make(map[VirtPage]bool)
	for v, cur := range from.Mappings {
		next, ok := to.Mappings[v]
		switch {
		case !ok:
			txn.Ops = append(txn.Ops, Op{Kind: OpUnmap, Virt: v})
		case cur.Phys != next.Phys:
			txn.Ops = append(txn.Ops,
				Op{Kind: OpUnmap, Virt: v},
				Op{Kind: OpMap, Virt: v, Phys: next.Phys, Perm: next.Perm},
			)
		case cur.Perm != next.Perm:
			txn.Ops = append(txn.Ops, Op{Kind: OpReprotect, Virt: v, Perm: next.Perm})
		}
		handled[v] = true
	}
	for v, next := range to.Mappings {
		if !handled[v] {
			txn.Ops = append(txn.Ops, Op{Kind: OpMap, Virt: v, Phys: next.Phys, Perm: next.Perm})
		}
	}
	return txn
}

// replayPlan applies plan steps to a private state and checks the invariant
// after every prefix.
func replayPlan(t *testing.T, start *State, plan *Plan) {
	t.Helper()
	s := start.Clone()
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("start state violates invariant: %v", err)
	}
	for _, step := range plan.Steps {
		op := step.Op
		switch op.Kind {
		case OpMap:
			s.Mappings[op.Virt] = Mapping{Virt: op.Virt, Phys: op.Phys, Perm: op.Perm}
		case OpUnmap:
			delete(s.Mappings, op.Virt)
		case OpReprotect:
			m := s.Mappings[op.Virt]
			m.Perm = op.Perm
			s.Mappings[op.Virt] = m
		}
		if err := s.CheckInvariant(); err != nil {
			t.Fatalf("invariant broken after %s: %v", step, err)
		}
	}
	if !s.Equal(plan.To) {
		t.Fatalf("plan replay reached %v, want %v", s.Mappings, plan.To.Mappings)
	}
}

func TestInvariantClassification(t *testing.T) {
	perms := []Perm{PermRead, PermRead | PermWrite, PermRead | PermExec, PermRead | PermWrite | PermExec}
	for _, s := range allStates(3, 2, perms) {
		got := s.CheckInvariant() != nil
		want := referenceViolation(s)
		if got != want {
			t.Fatalf("invariant mismatch for %v: got %v want %v", s.Mappings, got, want)
		}
	}
}

func TestExhaustivePlans(t *testing.T) {
	// 3 virtual pages, 2 physical pages, R / RW / RX -> 7^3 = 343 states.
	perms := []Perm{PermRead, PermRead | PermWrite, PermRead | PermExec}
	states := allStates(3, 2, perms)
	var safe []*State
	for _, s := range states {
		if s.CheckInvariant() == nil {
			safe = append(safe, s)
		}
	}

	pairs := 0
	for _, from := range safe {
		for _, to := range safe {
			pairs++
			txn := txnBetween(from, to)
			plan, err := PlanTxn(from, txn)
			if err != nil {
				t.Fatalf("PlanTxn %v -> %v: %v", from.Mappings, to.Mappings, err)
			}
			// Downgrades must all precede upgrades.
			seenUpgrade := false
			for _, st := range plan.Steps {
				if st.Phase == PhaseUpgrade {
					seenUpgrade = true
				}
				if seenUpgrade && st.Phase == PhaseDowngrade {
					t.Fatalf("downgrade after upgrade in plan %v -> %v: %s", from.Mappings, to.Mappings, st)
				}
			}
			replayPlan(t, from, plan)

			// Reversing the transaction must produce the inverse plan.
			back, err := PlanTxn(to, txnBetween(to, from))
			if err != nil {
				t.Fatalf("reverse PlanTxn: %v", err)
			}
			replayPlan(t, to, back)
			if !back.To.Equal(from) {
				t.Fatalf("reverse plan target mismatch")
			}
		}
	}
	t.Logf("exhausted %d safe-state pairs (%d total states)", pairs, len(states))
}

func TestExhaustivePartialFailure(t *testing.T) {
	perms := []Perm{PermRead, PermRead | PermWrite, PermRead | PermExec}
	states := allStates(3, 2, perms)
	var safe []*State
	for _, s := range states {
		if s.CheckInvariant() == nil {
			safe = append(safe, s)
		}
	}

	cases := 0
	for _, from := range safe {
		for _, to := range safe {
			txn := txnBetween(from, to)
			basePlan, err := PlanTxn(from, txn)
			if err != nil {
				t.Fatalf("PlanTxn: %v", err)
			}
			// Inject a failure at every possible step, including one past the
			// end (fully successful run).
			for failAt := 0; failAt <= len(basePlan.Steps); failAt++ {
				cases++
				drv := newFakeDriver()
				for v, m := range from.Mappings {
					drv.pages[v] = m
				}
				eng, err := NewEngineWithState(drv, from)
				if err != nil {
					t.Fatalf("NewEngineWithState: %v", err)
				}
				if failAt < len(basePlan.Steps) {
					drv.failAt = failAt
				}

				res, err := eng.Apply(txn)
				if err != nil {
					t.Fatalf("Apply: %v", err)
				}
				// Engine and driver must always agree and stay invariant-safe.
				if !eng.State().Equal(drv.state()) {
					t.Fatalf("engine/driver divergence after failure at %d", failAt)
				}
				if err := eng.State().CheckInvariant(); err != nil {
					t.Fatalf("post-fail invariant: %v", err)
				}

				if failAt == len(basePlan.Steps) {
					if res.Failure != nil {
						t.Fatalf("expected clean completion")
					}
					if job := eng.PendingJob(); job != nil && job.Pending() {
						t.Fatalf("unexpected pending job on completion")
					}
					if !eng.State().Equal(to) {
						t.Fatalf("final state mismatch")
					}
					continue
				}

				job := eng.PendingJob()
				if job == nil || !job.Pending() {
					t.Fatalf("expected pending job after failure at %d", failAt)
				}
				if !errors.Is(res.Failure, drv.failErr) {
					t.Fatalf("failure cause not propagated: %v", res.Failure)
				}
				// Both recovery plans must be invariant-safe prefixes.
				replayPlan(t, eng.State(), job.ResumePlan)
				replayPlan(t, eng.State(), job.RollbackPlan)

				// Path A: resume reaches the target and stays convergent.
				if r, err := eng.Resume(); err != nil || r.Failure != nil {
					t.Fatalf("resume: res=%v err=%v", r, err)
				}
				if !eng.State().Equal(to) || !eng.State().Equal(drv.state()) {
					t.Fatalf("resume did not converge to target")
				}

				// Path B (fresh engine): rollback reaches the origin.
				drv2 := newFakeDriver()
				for v, m := range from.Mappings {
					drv2.pages[v] = m
				}
				eng2, _ := NewEngineWithState(drv2, from)
				drv2.failAt = failAt
				if _, err := eng2.Apply(txn); err != nil {
					t.Fatalf("Apply B: %v", err)
				}
				if r, err := eng2.Rollback(); err != nil || r.Failure != nil {
					t.Fatalf("rollback: res=%v err=%v", r, err)
				}
				if !eng2.State().Equal(from) || !eng2.State().Equal(drv2.state()) {
					t.Fatalf("rollback did not converge to origin")
				}
			}
		}
	}
	t.Logf("exhausted %d failure-injection cases", cases)
}
