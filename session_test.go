package plansel

import (
	"reflect"
	"testing"
	"time"
)

// Plans: 1 (g1,c10,p20), 2 (g1,c10,p25), 3 (g2,c10,p30), 4 (g2,c5,p1);
// fees g1=g2=5, budget 40. Optimum: {1,2,3}, cost 40, profit 75.
func checkpointInstance() Instance {
	return Instance{
		Budget:   40,
		GroupFee: map[int]int64{1: 5, 2: 5},
		Plans: []Plan{
			{ID: 1, Group: 1, Cost: 10, Profit: 20},
			{ID: 2, Group: 1, Cost: 10, Profit: 25},
			{ID: 3, Group: 2, Cost: 10, Profit: 30},
			{ID: 4, Group: 2, Cost: 5, Profit: 1},
		},
	}
}

// Committed plans survive checkpoint revisions; executed preparations are
// not charged again; revised profits and budget apply to the re-solve.
func TestCheckpointCommitRevise(t *testing.T) {
	sess, err := NewSession(checkpointInstance())
	if err != nil {
		t.Fatal(err)
	}
	r1, err := sess.Solve(noDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(r1.Selected, want) {
		t.Fatalf("r1.Selected = %v, want %v", r1.Selected, want)
	}
	if r1.Cost != 40 || r1.Profit != 75 {
		t.Fatalf("r1 cost/profit = %d/%d, want 40/75", r1.Cost, r1.Profit)
	}

	// Commit the selection: costs and group fees become sunk.
	if err := sess.Commit(r1.Selected...); err != nil {
		t.Fatal(err)
	}
	if want := int64(30 + 5 + 5); sess.Sunk() != want {
		t.Fatalf("Sunk = %d, want %d", sess.Sunk(), want)
	}
	if want := []int{1, 2}; !reflect.DeepEqual(sess.Prepared(), want) {
		t.Fatalf("Prepared = %v, want %v", sess.Prepared(), want)
	}

	// Checkpoint revision: plan 4 becomes valuable, new budget is 10.
	if err := sess.Revise(10, map[int]int64{4: 50}); err != nil {
		t.Fatal(err)
	}
	r2, err := sess.Solve(noDeadline)
	if err != nil {
		t.Fatal(err)
	}
	// Committed plans are preserved; plan 4 joins at cost 5 only, because
	// group 2's preparation was already executed and is not charged again.
	if want := []int{1, 2, 3, 4}; !reflect.DeepEqual(r2.Selected, want) {
		t.Fatalf("r2.Selected = %v, want %v", r2.Selected, want)
	}
	if r2.Cost != 5 {
		t.Fatalf("r2.Cost = %d, want 5 (prepared group fee not re-charged)", r2.Cost)
	}
	if r2.Profit != 125 {
		t.Fatalf("r2.Profit = %d, want 125 (committed profits kept)", r2.Profit)
	}
	if err := Verify(sess.Instance(), sess.View(), r2); err != nil {
		t.Fatalf("Verify r2: %v", err)
	}
}

// Committing a plan auto-commits its requirements and prepares its group.
func TestCommitClosureAuto(t *testing.T) {
	sess, err := NewSession(Instance{
		Budget:   100,
		GroupFee: map[int]int64{7: 3},
		Plans: []Plan{
			{ID: 1, Group: 7, Cost: 2, Profit: 9, Requires: []int{2}},
			{ID: 2, Group: 8, Cost: 4, Profit: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Commit(1); err != nil {
		t.Fatal(err)
	}
	if want := []int{1, 2}; !reflect.DeepEqual(sess.Committed(), want) {
		t.Fatalf("Committed = %v, want %v (closure auto-added)", sess.Committed(), want)
	}
	if want := []int{7, 8}; !reflect.DeepEqual(sess.Prepared(), want) {
		t.Fatalf("Prepared = %v, want %v", sess.Prepared(), want)
	}
	if want := int64(2 + 4 + 3); sess.Sunk() != want {
		t.Fatalf("Sunk = %d, want %d", sess.Sunk(), want)
	}
}

// A standalone preparation is sunk and never re-charged, even if the group
// ends up unused.
func TestStandalonePrepare(t *testing.T) {
	sess, err := NewSession(checkpointInstance())
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Prepare(2); err != nil {
		t.Fatal(err)
	}
	if want := int64(5); sess.Sunk() != want {
		t.Fatalf("Sunk = %d, want %d", sess.Sunk(), want)
	}
	res, err := sess.Solve(noDeadline)
	if err != nil {
		t.Fatal(err)
	}
	// Group 2 is free now: {1,2,3,4} costs 10+10+10+5+5(g1) = 40 <= 40.
	if want := []int{1, 2, 3, 4}; !reflect.DeepEqual(res.Selected, want) {
		t.Fatalf("Selected = %v, want %v", res.Selected, want)
	}
	if res.Cost != 40 {
		t.Fatalf("Cost = %d, want 40 (group 2 fee sunk)", res.Cost)
	}
	if err := Verify(sess.Instance(), sess.View(), res); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// A deadline-limited search returns bracketing bounds and a resume token;
// resuming finishes the same search and proves optimality.
func TestDeadlineBoundsAndResume(t *testing.T) {
	inst := Instance{
		Budget:   45,
		GroupFee: map[int]int64{1: 7, 2: 3},
	}
	for i := 0; i < 18; i++ {
		p := Plan{ID: i + 1, Group: 1 + i%3, Cost: int64(3 + i%7), Profit: int64(5 + (i*7)%13)}
		if i%4 == 3 {
			p.Requires = []int{i} // depends on the previous plan
		}
		inst.Plans = append(inst.Plans, p)
	}
	sess, err := NewSession(inst)
	if err != nil {
		t.Fatal(err)
	}

	// Deadline already in the past: the search must stop almost immediately.
	r1, err := sess.Solve(time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if r1.Status != Feasible {
		t.Fatalf("r1.Status = %v, want Feasible", r1.Status)
	}
	if r1.Lower > r1.Upper {
		t.Fatalf("r1 bounds [%d,%d] inverted", r1.Lower, r1.Upper)
	}
	if len(r1.ResumeToken()) == 0 {
		t.Fatal("r1 carries no resume token")
	}
	// The independent verifier brackets the recomputed optimum.
	if err := Verify(sess.Instance(), sess.View(), r1); err != nil {
		t.Fatalf("Verify r1: %v", err)
	}

	// Resume the exact same search to completion.
	r2, err := sess.Resume(r1.ResumeToken(), noDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Status != Optimal {
		t.Fatalf("r2.Status = %v, want Optimal", r2.Status)
	}
	if r2.Lower != r2.Upper || r2.Profit != r2.Lower {
		t.Fatalf("r2 bounds [%d,%d] profit %d, want all equal", r2.Lower, r2.Upper, r2.Profit)
	}
	if r2.Profit < r1.Lower || r2.Profit > r1.Upper {
		t.Fatalf("resumed optimum %d outside r1 bounds [%d,%d]", r2.Profit, r1.Lower, r1.Upper)
	}
	if len(r2.ResumeToken()) != 0 {
		t.Fatal("optimal result must not carry a resume token")
	}
	if err := Verify(sess.Instance(), sess.View(), r2); err != nil {
		t.Fatalf("Verify r2: %v", err)
	}

	// Resuming with the consumed token again still works (same checkpoint),
	// but a revision must invalidate it.
	if _, err := sess.Resume(r1.ResumeToken(), noDeadline); err != nil {
		t.Fatalf("resume within same checkpoint: %v", err)
	}
	if err := sess.Revise(90, map[int]int64{1: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.Resume(r1.ResumeToken(), noDeadline); err == nil {
		t.Fatal("stale token accepted after checkpoint revision")
	}
}

// After a revision, the verifier rejects bounds proved for the old
// instance: old optimality proofs cannot be reused.
func TestVerifierRejectsStaleProof(t *testing.T) {
	sess, err := NewSession(checkpointInstance())
	if err != nil {
		t.Fatal(err)
	}
	r1, err := sess.Solve(noDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(sess.Instance(), sess.View(), r1); err != nil {
		t.Fatalf("Verify before revision: %v", err)
	}
	// Revise profits: the old "optimal" answer is no longer optimal.
	if err := sess.Revise(40, map[int]int64{4: 500}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(sess.Instance(), sess.View(), r1); err == nil {
		t.Fatal("verifier accepted a stale optimality proof after revision")
	}
	// A fresh solve under the revised instance verifies cleanly.
	r2, err := sess.Solve(noDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(sess.Instance(), sess.View(), r2); err != nil {
		t.Fatalf("Verify after revision: %v", err)
	}
}

// The verifier catches forged results.
func TestVerifierCatchesForgery(t *testing.T) {
	inst := checkpointInstance()
	view := View{}
	// Inflated profit.
	bad := Result{Status: Optimal, Selected: []int{1, 2, 3}, Profit: 76, Cost: 40, Lower: 76, Upper: 76}
	if err := Verify(inst, view, bad); err == nil {
		t.Fatal("accepted inflated profit")
	}
	// Fee charged twice.
	bad = Result{Status: Optimal, Selected: []int{1, 2, 3}, Profit: 75, Cost: 45, Lower: 75, Upper: 75}
	if err := Verify(inst, view, bad); err == nil {
		t.Fatal("accepted double-charged group fee")
	}
	// Over-budget selection.
	bad = Result{Status: Optimal, Selected: []int{1, 2, 3, 4}, Profit: 76, Cost: 45, Lower: 76, Upper: 76}
	if err := Verify(inst, view, bad); err == nil {
		t.Fatal("accepted over-budget selection")
	}
	// Upper bound below the true optimum.
	bad = Result{Status: Feasible, Selected: []int{3}, Profit: 30, Cost: 15, Lower: 30, Upper: 50}
	if err := Verify(inst, view, bad); err == nil {
		t.Fatal("accepted upper bound below optimum")
	}
	// Missing committed plan.
	if err := Verify(inst, View{Committed: []int{1}, Prepared: []int{1}},
		Result{Status: Feasible, Selected: []int{2}, Profit: 25, Cost: 15, Lower: 25, Upper: 100}); err == nil {
		t.Fatal("accepted selection missing a committed plan")
	}
	// Committed plan in an unprepared group.
	if err := Verify(inst, View{Committed: []int{1}},
		Result{Status: Feasible, Selected: []int{1}, Profit: 20, Cost: 15, Lower: 20, Upper: 100}); err == nil {
		t.Fatal("accepted committed plan in unprepared group")
	}
}
