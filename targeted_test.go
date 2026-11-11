package netsec

import (
	"errors"
	"testing"
)

func TestRepeatedFailuresThenResume(t *testing.T) {
	start := NewState()
	start.Mappings[1] = Mapping{Virt: 1, Phys: 1, Perm: PermRead | PermExec}
	start.Mappings[2] = Mapping{Virt: 2, Phys: 2, Perm: PermRead}

	drv := newFakeDriver()
	drv.pages = start.Clone().Mappings
	eng, err := NewEngineWithState(drv, start)
	if err != nil {
		t.Fatalf("NewEngineWithState: %v", err)
	}

	txn := Txn{Ops: []Op{
		{Kind: OpReprotect, Virt: 2, Perm: PermRead | PermWrite},
		{Kind: OpReprotect, Virt: 1, Perm: PermRead},
	}}
	plan, err := PlanTxn(start, txn)
	if err != nil {
		t.Fatalf("PlanTxn: %v", err)
	}
	// The X-removing downgrade must precede the W-adding upgrade.
	if plan.Steps[0].Phase != PhaseDowngrade || plan.Steps[1].Phase != PhaseUpgrade {
		t.Fatalf("expected downgrade-first ordering: %v", plan.Steps)
	}

	drv.permanent = true
	drv.failAt = 0
	res, err := eng.Apply(txn)
	if err != nil || res.Failure == nil {
		t.Fatalf("expected failure, got res=%v err=%v", res, err)
	}
	for i := 0; i < 2; i++ {
		res, err = eng.Resume()
		if err != nil || res.Failure == nil {
			t.Fatalf("retry %d: expected persistent failure, got res=%v err=%v", i, res, err)
		}
		if !eng.State().Equal(start) || !eng.State().Equal(drv.state()) {
			t.Fatalf("state drifted while fault persists")
		}
		if err := eng.State().CheckInvariant(); err != nil {
			t.Fatalf("invariant broken during retries: %v", err)
		}
	}

	// Clear the fault; resume now completes and the writable alias appears
	// only after the executable alias was downgraded.
	drv.permanent = false
	drv.failAt = -1
	if res, err := eng.Resume(); err != nil || res.Failure != nil {
		t.Fatalf("final resume: res=%v err=%v", res, err)
	}
	want := start.Clone()
	want.Mappings[1] = Mapping{Virt: 1, Phys: 1, Perm: PermRead}
	want.Mappings[2] = Mapping{Virt: 2, Phys: 2, Perm: PermRead | PermWrite}
	if !eng.State().Equal(want) || !eng.State().Equal(drv.state()) {
		t.Fatalf("post-resume state mismatch")
	}
}

func TestInvalidTransactionsRejected(t *testing.T) {
	s := NewState()
	s.Mappings[1] = Mapping{Virt: 1, Phys: 1, Perm: PermRead | PermExec}
	s.Mappings[2] = Mapping{Virt: 2, Phys: 1, Perm: PermRead}

	cases := []struct {
		name string
		txn  Txn
	}{
		{"writable alias of executable phys", Txn{Ops: []Op{
			{Kind: OpReprotect, Virt: 2, Perm: PermRead | PermWrite},
		}}},
		{"map onto used virt", Txn{Ops: []Op{
			{Kind: OpMap, Virt: 1, Phys: 2, Perm: PermRead},
		}}},
		{"unmap absent virt", Txn{Ops: []Op{
			{Kind: OpUnmap, Virt: 9},
		}}},
		{"reprotect absent virt", Txn{Ops: []Op{
			{Kind: OpReprotect, Virt: 9, Perm: PermRead},
		}}},
		{"zero-perm map", Txn{Ops: []Op{
			{Kind: OpMap, Virt: 3, Phys: 1, Perm: 0},
		}}},
		{"zero-perm reprotect", Txn{Ops: []Op{
			{Kind: OpReprotect, Virt: 1, Perm: 0},
		}}},
		{"duplicate virt ops", Txn{Ops: []Op{
			{Kind: OpReprotect, Virt: 1, Perm: PermRead},
			{Kind: OpReprotect, Virt: 1, Perm: PermRead | PermExec},
		}}},
		{"single rwx mapping", Txn{Ops: []Op{
			{Kind: OpMap, Virt: 3, Phys: 2, Perm: PermRead | PermWrite | PermExec},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := PlanTxn(s, tc.txn); err == nil {
				t.Fatalf("expected planning error for %s", tc.name)
			}
		})
	}

	// A pre-existing violating starting state must also be rejected.
	bad := NewState()
	bad.Mappings[1] = Mapping{Virt: 1, Phys: 1, Perm: PermRead | PermExec}
	bad.Mappings[2] = Mapping{Virt: 2, Phys: 1, Perm: PermRead | PermWrite}
	if err := bad.CheckInvariant(); err == nil {
		t.Fatalf("expected invariant violation")
	}
	if _, err := NewEngineWithState(newFakeDriver(), bad); err == nil {
		t.Fatalf("expected engine construction to reject violating state")
	}
}

func TestRemapAcrossPhysicalPages(t *testing.T) {
	from := NewState()
	from.Mappings[1] = Mapping{Virt: 1, Phys: 1, Perm: PermRead | PermExec}
	to := NewState()
	to.Mappings[1] = Mapping{Virt: 1, Phys: 2, Perm: PermRead | PermWrite}

	plan, err := PlanTxn(from, txnBetween(from, to))
	if err != nil {
		t.Fatalf("PlanTxn: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("expected unmap+map, got %v", plan.Steps)
	}
	if plan.Steps[0].Op.Kind != OpUnmap || plan.Steps[0].Phase != PhaseDowngrade {
		t.Fatalf("first step must be downgrade unmap: %v", plan.Steps[0])
	}
	if plan.Steps[1].Op.Kind != OpMap || plan.Steps[1].Phase != PhaseUpgrade {
		t.Fatalf("second step must be upgrade map: %v", plan.Steps[1])
	}
	replayPlan(t, from, plan)

	// Failure after the unmap: rollback must re-create the original alias.
	drv := newFakeDriver()
	drv.pages = from.Clone().Mappings
	eng, _ := NewEngineWithState(drv, from)
	drv.failAt = 1
	res, _ := eng.Apply(txnBetween(from, to))
	if res.Failure == nil {
		t.Fatalf("expected failure on second step")
	}
	rb := eng.PendingJob().RollbackPlan
	replayPlan(t, eng.State(), rb)
	if r, err := eng.Rollback(); err != nil || r.Failure != nil {
		t.Fatalf("rollback: res=%v err=%v", r, err)
	}
	if !eng.State().Equal(from) {
		t.Fatalf("rollback state mismatch: %v", eng.State().Mappings)
	}
}

func TestAliasSummary(t *testing.T) {
	s := NewState()
	s.Mappings[1] = Mapping{Virt: 1, Phys: 1, Perm: PermRead | PermExec}
	s.Mappings[2] = Mapping{Virt: 2, Phys: 1, Perm: PermRead}
	s.Mappings[3] = Mapping{Virt: 3, Phys: 2, Perm: PermRead | PermWrite}

	sums := s.Summarize()
	if len(sums) != 2 || sums[0].Phys != 1 || sums[1].Phys != 2 {
		t.Fatalf("unexpected summaries: %+v", sums)
	}
	if len(sums[0].Aliases) != 2 || len(sums[0].Exec) != 1 || len(sums[0].Writable) != 0 {
		t.Fatalf("phys 1 summary wrong: %+v", sums[0])
	}
	if len(sums[1].Writable) != 1 || sums[1].Writable[0] != 3 {
		t.Fatalf("phys 2 summary wrong: %+v", sums[1])
	}
	if err := s.CheckInvariant(); err != nil {
		t.Fatalf("valid state flagged: %v", err)
	}

	// Adding a writable alias to phys 1 must be reported.
	m := s.Mappings[2]
	m.Perm = PermRead | PermWrite
	s.Mappings[2] = m
	err := s.CheckInvariant()
	if err == nil {
		t.Fatalf("expected violation")
	}
	var v Violation
	if !errors.As(err, &v) {
		t.Fatalf("expected Violation in error chain, got %T", err)
	}
	if v.Phys != 1 {
		t.Fatalf("violation names wrong physical page: %+v", v)
	}
}

func TestPendingJobBlocksNewTransaction(t *testing.T) {
	start := NewState()
	start.Mappings[1] = Mapping{Virt: 1, Phys: 1, Perm: PermRead}
	drv := newFakeDriver()
	drv.pages = start.Clone().Mappings
	eng, _ := NewEngineWithState(drv, start)

	drv.failAt = 0
	res, _ := eng.Apply(Txn{Ops: []Op{{Kind: OpReprotect, Virt: 1, Perm: PermRead | PermWrite}}})
	if res.Failure == nil {
		t.Fatalf("expected failure")
	}
	if _, err := eng.Apply(Txn{Ops: []Op{{Kind: OpUnmap, Virt: 1}}}); err == nil {
		t.Fatalf("expected Apply to be blocked while job pending")
	}
	if _, err := eng.Resume(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if job := eng.PendingJob(); job != nil && job.Pending() {
		t.Fatalf("job should be cleared after resume")
	}
	// After recovery the next transaction is accepted.
	if _, err := eng.Apply(Txn{Ops: []Op{{Kind: OpUnmap, Virt: 1}}}); err != nil {
		t.Fatalf("post-recovery Apply: %v", err)
	}
}

func TestFailureDuringRecovery(t *testing.T) {
	start := NewState()
	start.Mappings[1] = Mapping{Virt: 1, Phys: 1, Perm: PermRead}
	start.Mappings[2] = Mapping{Virt: 2, Phys: 2, Perm: PermRead}

	txn := Txn{Ops: []Op{
		{Kind: OpReprotect, Virt: 1, Perm: PermRead | PermWrite},
		{Kind: OpMap, Virt: 3, Phys: 2, Perm: PermRead | PermExec},
	}}
	plan, err := PlanTxn(start, txn)
	if err != nil {
		t.Fatalf("PlanTxn: %v", err)
	}
	if len(plan.Steps) < 2 {
		t.Fatalf("expected multi-step plan: %v", plan.Steps)
	}

	// First failure at step 0, then another failure while resuming.
	drv := newFakeDriver()
	drv.pages = start.Clone().Mappings
	eng, _ := NewEngineWithState(drv, start)
	drv.failAt = 0
	res, _ := eng.Apply(txn)
	if res.Failure == nil {
		t.Fatalf("expected first failure")
	}
	intermediate := eng.State().Clone()

	// Fail again on the first resumed step.
	drv.failAt = drv.calls
	res, err = eng.Resume()
	if err != nil || res.Failure == nil {
		t.Fatalf("expected second failure, got res=%v err=%v", res, err)
	}
	if !eng.State().Equal(intermediate) || !eng.State().Equal(drv.state()) {
		t.Fatalf("state changed on failed resume step")
	}
	replayPlan(t, eng.State(), eng.PendingJob().ResumePlan)
	replayPlan(t, eng.State(), eng.PendingJob().RollbackPlan)

	// Rollback after the second failure still reaches the original state.
	drv.failAt = -1
	if r, err := eng.Rollback(); err != nil || r.Failure != nil {
		t.Fatalf("rollback: res=%v err=%v", r, err)
	}
	if !eng.State().Equal(start) || !eng.State().Equal(drv.state()) {
		t.Fatalf("rollback after repeated failures did not reach origin")
	}
}
