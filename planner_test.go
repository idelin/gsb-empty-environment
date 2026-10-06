package tasksched

import (
	"strings"
	"testing"
)

func names(n int) []string {
	tasks := make([]string, n)
	for i := range tasks {
		tasks[i] = string(rune('a' + i))
	}
	return tasks
}

func TestPlanIsDeterministicAndExecutesInOrder(t *testing.T) {
	p := NewPlanner(names(4)) // a b c d
	if c := p.Apply(nil, []Edge{{0, 2}, {1, 2}}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	want := []int{0, 1, 2, 3}
	if plan := p.Plan(); !equalInts(plan.Order, want) {
		t.Fatalf("plan = %v, want %v", plan.Order, want)
	}
	for _, w := range want {
		got, ok := p.ExecuteNext()
		if !ok || got != w {
			t.Fatalf("ExecuteNext = %d,%v, want %d,true", got, ok, w)
		}
	}
	if _, ok := p.ExecuteNext(); ok {
		t.Fatal("ExecuteNext after completion should report false")
	}
	if !equalInts(p.Executed(), want) {
		t.Fatalf("executed = %v, want %v", p.Executed(), want)
	}
}

func TestNewConstraintCannotReorderThePast(t *testing.T) {
	p := NewPlanner(names(3)) // a b c
	if next, ok := p.ExecuteNext(); !ok || next != 0 {
		t.Fatalf("ExecuteNext = %d,%v", next, ok)
	}
	c := p.AddEdge(Edge{2, 0}) // c before a, but a already ran
	if c == nil || c.Kind != ConflictLocked {
		t.Fatalf("conflict = %v, want ConflictLocked", c)
	}
	if !equalInts(c.Chain, []int{0, 2}) {
		t.Fatalf("chain = %v, want [a c]", c.Chain)
	}
	// State untouched: progress kept, constraint rejected.
	if !equalInts(p.Executed(), []int{0}) {
		t.Fatalf("executed = %v, progress was reset", p.Executed())
	}
	if plan := p.Plan(); !equalInts(plan.Order, []int{0, 1, 2}) {
		t.Fatalf("plan = %v, rejected constraint leaked in", plan.Order)
	}
}

func TestLockedConflictChainCoversExecutedSegment(t *testing.T) {
	p := NewPlanner(names(4))  // a b c d
	p.ExecuteNext()            // a
	p.ExecuteNext()            // b
	p.ExecuteNext()            // c
	c := p.AddEdge(Edge{2, 0}) // c before a: both executed, a first
	if c == nil || c.Kind != ConflictLocked {
		t.Fatalf("conflict = %v, want ConflictLocked", c)
	}
	if !equalInts(c.Chain, []int{0, 1, 2}) {
		t.Fatalf("chain = %v, want executed segment [a b c]", c.Chain)
	}
}

func TestCycleConflictReturnsShortestChain(t *testing.T) {
	p := NewPlanner(names(4)) // a b c d
	if c := p.Apply(nil, []Edge{{0, 1}, {1, 2}, {0, 2}}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	c := p.AddEdge(Edge{2, 0}) // c before a closes a loop
	if c == nil || c.Kind != ConflictCycle {
		t.Fatalf("conflict = %v, want ConflictCycle", c)
	}
	// Shortest loop uses the direct a->c constraint, not a->b->c.
	if !equalInts(c.Chain, []int{0, 2}) {
		t.Fatalf("chain = %v, want shortest [a c]", c.Chain)
	}
	if !strings.Contains(c.Detail, "a -> c -> a") {
		t.Fatalf("detail = %q, want loop description", c.Detail)
	}
}

func TestTransactionIsAtomic(t *testing.T) {
	p := NewPlanner(names(5)) // a b c d e
	if c := p.AddEdge(Edge{1, 2}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	// Batch: delete d, add a->b (fine) and c->b (closes b->c->b loop).
	c := p.Apply([]int{3}, []Edge{{0, 1}, {2, 1}})
	if c == nil || c.Kind != ConflictCycle {
		t.Fatalf("conflict = %v, want ConflictCycle", c)
	}
	// Nothing committed: d alive, a->b absent.
	plan := p.Plan()
	if !equalInts(plan.Order, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("plan = %v, transaction partially applied", plan.Order)
	}
	// Good batch commits everything at once.
	if c := p.Apply([]int{3}, []Edge{{0, 1}}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	if plan := p.Plan(); !equalInts(plan.Order, []int{0, 1, 2, 4}) {
		t.Fatalf("plan = %v, want [a b c e]", plan.Order)
	}
}

func TestDeleteUnblocksAndRemovesConstraints(t *testing.T) {
	p := NewPlanner(names(3)) // a b c
	if c := p.AddEdge(Edge{0, 2}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	if c := p.DeleteTasks(0); c != nil {
		t.Fatalf("delete: %v", c)
	}
	if plan := p.Plan(); !equalInts(plan.Order, []int{1, 2}) {
		t.Fatalf("plan = %v, want [b c]", plan.Order)
	}
	if c := p.DeleteTasks(0); c == nil || c.Kind != ConflictInvalid {
		t.Fatalf("re-delete conflict = %v, want ConflictInvalid", c)
	}
}

func TestExecutedTaskCannotBeDeleted(t *testing.T) {
	p := NewPlanner(names(2))
	p.ExecuteNext() // a
	c := p.DeleteTasks(0)
	if c == nil || c.Kind != ConflictExecutedDelete {
		t.Fatalf("conflict = %v, want ConflictExecutedDelete", c)
	}
	if !equalInts(p.Executed(), []int{0}) {
		t.Fatalf("executed = %v, progress lost", p.Executed())
	}
}

func TestVerifyAcceptsConsistentRecovery(t *testing.T) {
	p := NewPlanner(names(4)) // a b c d
	if c := p.Apply(nil, []Edge{{0, 2}, {1, 2}}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	p.ExecuteNext() // a
	p.ExecuteNext() // b
	if c := p.AddEdge(Edge{2, 3}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	q := Restore(p.Snapshot())
	if c := q.Verify(); c != nil {
		t.Fatalf("verify restored state: %v", c)
	}
	if plan := q.Plan(); !equalInts(plan.Order, []int{0, 1, 2, 3}) {
		t.Fatalf("restored plan = %v", plan.Order)
	}
}

func TestVerifyRejectsTamperedExecutionEvidence(t *testing.T) {
	p := NewPlanner(names(3)) // a b c
	if c := p.Apply(nil, []Edge{{0, 1}}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	p.ExecuteNext() // a
	p.ExecuteNext() // b
	s := p.Snapshot()

	// Swap the executed prefix so it violates a->b.
	s.Executed = []int{1, 0}
	c := Restore(s).Verify()
	if c == nil || c.Kind != ConflictLocked {
		t.Fatalf("conflict = %v, want ConflictLocked", c)
	}
	if !equalInts(c.Chain, []int{1, 0}) {
		t.Fatalf("chain = %v, want evidence [b a]", c.Chain)
	}

	// Drop the predecessor from the executed record.
	s = p.Snapshot()
	s.Executed = []int{1}
	c = Restore(s).Verify()
	if c == nil || c.Kind != ConflictLocked {
		t.Fatalf("conflict = %v, want ConflictLocked", c)
	}
	if !strings.Contains(c.Detail, "never executed") {
		t.Fatalf("detail = %q, want missing-predecessor evidence", c.Detail)
	}
}

func TestDuplicateConstraintIsANoOp(t *testing.T) {
	p := NewPlanner(names(3))
	if c := p.AddEdge(Edge{0, 1}); c != nil {
		t.Fatalf("apply: %v", c)
	}
	if c := p.AddEdge(Edge{0, 1}); c != nil {
		t.Fatalf("duplicate constraint rejected: %v", c)
	}
	if got := len(p.Snapshot().Edges); got != 1 {
		t.Fatalf("edges = %d, want 1 after dedup", got)
	}
}
