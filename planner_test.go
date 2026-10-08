package topo

import (
	"errors"
	"reflect"
	"testing"
)

func mustPlanner(t *testing.T, n int, edges []Edge) *Planner {
	t.Helper()
	p, err := NewPlanner(n, edges)
	if err != nil {
		t.Fatalf("NewPlanner: %v", err)
	}
	return p
}

func TestPlannerDeterministicPlan(t *testing.T) {
	p := mustPlanner(t, 4, []Edge{{3, 2}, {0, 1}})
	if got, want := p.Plan(), []int{0, 1, 3, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
}

func TestExecuteFreezesPrefix(t *testing.T) {
	p := mustPlanner(t, 5, []Edge{{0, 1}, {1, 2}})
	done, err := p.Execute(2) // executes 0, 1
	if err != nil || !reflect.DeepEqual(done, []int{0, 1}) {
		t.Fatalf("Execute = %v, %v", done, err)
	}
	// New constraint among pending tasks only reorders the future.
	if err := p.Apply(Tx{Add: []Edge{{4, 2}}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := p.Executed(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("executed prefix moved: %v", got)
	}
	if got, want := p.Plan(), []int{3, 4, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	if err := p.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestContradictionDirectEvidence(t *testing.T) {
	p := mustPlanner(t, 4, nil)
	if _, err := p.Execute(2); err != nil { // executed 0, 1
		t.Fatal(err)
	}
	// 1->0 would reorder the frozen past.
	err := p.Apply(Tx{Add: []Edge{{1, 0}}})
	var ce *ContradictionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want ContradictionError", err)
	}
	if !reflect.DeepEqual(ce.Chain, []int{1, 0, 1}) {
		t.Fatalf("chain = %v, want [1 0 1]", ce.Chain)
	}
	// Pending task ordered before an executed one is also a contradiction.
	err = p.Apply(Tx{Add: []Edge{{3, 0}}})
	if !errors.As(err, &ce) || !reflect.DeepEqual(ce.Chain, []int{3, 0, 3}) {
		t.Fatalf("chain = %v (err %v), want [3 0 3]", ce.Chain, err)
	}
	// State untouched after rejections.
	if got, want := p.Plan(), []int{2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
}

func TestContradictionShortestChain(t *testing.T) {
	// Existing: 2->3->4 and 2->5->6->4. Adding 4->2 must surface the
	// shorter loop 4->2->3->4, not merely list remaining tasks.
	p := mustPlanner(t, 7, []Edge{{2, 3}, {3, 4}, {2, 5}, {5, 6}, {6, 4}})
	chain := p.CheckAdd(Edge{4, 2})
	if !reflect.DeepEqual(chain, []int{4, 2, 3, 4}) {
		t.Fatalf("chain = %v, want [4 2 3 4]", chain)
	}
}

func TestContradictionThroughExecutionEvidence(t *testing.T) {
	// Executed 0; constraint 2->1 exists. Adding 1->2 closes a loop
	// through execution evidence (1 executed before pending 2).
	p := mustPlanner(t, 3, []Edge{{2, 1}})
	if _, err := p.Execute(1); err != nil { // executes 0
		t.Fatal(err)
	}
	if _, err := p.Execute(1); err != nil { // executes 1
		t.Fatal(err)
	}
	err := p.Apply(Tx{Add: []Edge{{1, 2}}})
	var ce *ContradictionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want ContradictionError", err)
	}
	if !reflect.DeepEqual(ce.Chain, []int{1, 2, 1}) {
		t.Fatalf("chain = %v, want [1 2 1]", ce.Chain)
	}
}

func TestAtomicTransactionAllOrNothing(t *testing.T) {
	p := mustPlanner(t, 5, []Edge{{0, 1}})
	if _, err := p.Execute(1); err != nil { // executes 0
		t.Fatal(err)
	}
	planBefore := p.Plan()
	// Bundle: delete task 4, drop constraint 0->1, add valid 3->2, plus
	// 1->0 which contradicts the executed prefix -> whole tx rejected.
	err := p.Apply(Tx{
		Delete: []int{4},
		Remove: []Edge{{0, 1}},
		Add:    []Edge{{3, 2}, {1, 0}},
	})
	var ce *ContradictionError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want ContradictionError", err)
	}
	// Nothing from the bundle may leak through.
	if got := p.Plan(); !reflect.DeepEqual(got, planBefore) {
		t.Fatalf("plan changed to %v after failed tx", got)
	}
	if p.deleted[4] {
		t.Fatal("task 4 deleted despite failed tx")
	}
	if _, ok := p.edges[Edge{0, 1}]; !ok {
		t.Fatal("constraint 0->1 removed despite failed tx")
	}
	if _, ok := p.edges[Edge{3, 2}]; ok {
		t.Fatal("constraint 3->2 added despite failed tx")
	}
}

func TestAtomicTransactionCommit(t *testing.T) {
	p := mustPlanner(t, 5, []Edge{{0, 1}, {4, 3}})
	if err := p.Apply(Tx{
		Delete: []int{4}, // also drops constraint 4->3
		Remove: []Edge{{0, 1}},
		Add:    []Edge{{1, 0}, {3, 2}},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, want := p.Plan(), []int{1, 0, 3, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	if err := p.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestDeleteExecutedTaskRejected(t *testing.T) {
	p := mustPlanner(t, 3, nil)
	if _, err := p.Execute(1); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(Tx{Delete: []int{0}}); err == nil {
		t.Fatal("deleting executed task 0 must fail")
	}
	if got := p.Plan(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("plan = %v after rejected delete", got)
	}
}

func TestCycleInUpdateRejected(t *testing.T) {
	p := mustPlanner(t, 4, []Edge{{0, 1}})
	err := p.Apply(Tx{Add: []Edge{{1, 2}, {2, 1}}})
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err = %v, want CycleError", err)
	}
	assertRealCycle(t, 4, []Edge{{0, 1}, {1, 2}, {2, 1}}, Result{Cycle: cyc.Cycle})
	if got, want := p.Plan(), []int{0, 1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plan = %v after rejected cyclic tx", got)
	}
}

func TestRecoverRoundTrip(t *testing.T) {
	p := mustPlanner(t, 6, []Edge{{0, 2}, {1, 2}, {2, 3}})
	if _, err := p.Execute(2); err != nil { // executes 0, 1
		t.Fatal(err)
	}
	if err := p.Apply(Tx{Add: []Edge{{3, 4}}, Delete: []int{5}}); err != nil {
		t.Fatal(err)
	}
	q, err := Recover(p.Snapshot())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if !reflect.DeepEqual(q.Executed(), p.Executed()) || !reflect.DeepEqual(q.Plan(), p.Plan()) {
		t.Fatalf("recovered = %v/%v, want %v/%v", q.Executed(), q.Plan(), p.Executed(), p.Plan())
	}
	if err := q.Verify(); err != nil {
		t.Fatalf("Verify after recover: %v", err)
	}
}

func TestRecoverRejectsCorruptedPrefix(t *testing.T) {
	// Snapshot claims 1 executed before 0, but constraint 0->1 says
	// otherwise: recovery must refuse instead of resetting progress.
	_, err := Recover(Snapshot{N: 3, Executed: []int{1, 0}, Edges: []Edge{{0, 1}}})
	if err == nil {
		t.Fatal("corrupted executed prefix must fail recovery")
	}
	// Deleted-but-executed task is also corrupt.
	_, err = Recover(Snapshot{N: 3, Executed: []int{0}, Deleted: []int{0}})
	if err == nil {
		t.Fatal("executed+deleted task must fail recovery")
	}
	// Cyclic surviving constraints must fail recovery.
	_, err = Recover(Snapshot{N: 2, Edges: []Edge{{0, 1}, {1, 0}}})
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err = %v, want CycleError", err)
	}
}

func TestNewPlannerCycle(t *testing.T) {
	_, err := NewPlanner(3, []Edge{{0, 1}, {1, 2}, {2, 0}})
	var cyc *CycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("err = %v, want CycleError", err)
	}
	if len(cyc.Cycle) != 4 {
		t.Fatalf("cycle = %v, want a 3-task loop", cyc.Cycle)
	}
}
