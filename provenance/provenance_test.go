package provenance

import (
	"fmt"
	"testing"
)

var (
	users = []SourceRecord{
		{ID: "u1", Key: "u1", Val: 30},
		{ID: "u2", Key: "u2", Val: 40},
		{ID: "u3", Key: "u3", Val: 17}, // minor: filtered out downstream
	}
	orders = []SourceRecord{
		{ID: "o1", Key: "u1", Val: 10},
		{ID: "o2", Key: "u1", Val: 20},
		{ID: "o3", Key: "u2", Val: 5},
	}
)

// build runs the pipeline: copy users and orders, keep adults, join on the
// user key, then count orders per user.
func build(us, os []SourceRecord) (*Pipeline, *Aggregate) {
	p := NewPipeline(NewStore())
	cu := NewCopy(p, "users", us)
	adults := NewFilter(p, "adults", cu, func(t Tuple) bool { return t.Val >= 18 })
	co := NewCopy(p, "orders", os)
	j := NewJoin(p, "user_orders", adults, co)
	agg := NewAggregate(p, "order_counts", j, AggCount)
	return p, agg
}

func aggVal(a *Aggregate, key string) (float64, bool) {
	for _, t := range a.Output() {
		if t.Key == key {
			return t.Val, true
		}
	}
	return 0, false
}

func TestExecution(t *testing.T) {
	p, agg := build(users, orders)

	joined := p.Nodes()[3].Output()
	if len(joined) != 3 {
		t.Fatalf("joined tuples = %d, want 3", len(joined))
	}
	if got := joined[0].Prov.String(); got != "(* u1 o1)" {
		t.Fatalf("join prov = %s, want (* u1 o1)", got)
	}
	if v, ok := aggVal(agg, "u1"); !ok || v != 2 {
		t.Fatalf("count(u1) = %v,%v want 2,true", v, ok)
	}
	if v, ok := aggVal(agg, "u2"); !ok || v != 1 {
		t.Fatalf("count(u2) = %v,%v want 1,true", v, ok)
	}
	// u3 is a minor: filtered out, so no derived object may depend on it.
	for _, n := range p.Nodes() {
		if n.Name() == "users" || n.Name() == "orders" {
			continue // raw copies legitimately reference their own sources
		}
		for _, tup := range n.Output() {
			if DependsOn(tup.Prov, map[string]bool{"u3": true}) {
				t.Fatalf("%s depends on filtered-out u3", tup.Desc)
			}
		}
	}
}

// recomputeSet indexes an impact's recompute list by object identity.
func recomputeSet(imp *Impact) map[string]bool {
	m := make(map[string]bool)
	for _, a := range imp.Recompute {
		m[fmt.Sprintf("%s/%d", a.Object.Node, a.Object.Index)] = true
	}
	return m
}

// checkAgainstRerun verifies the impact analysis against a brute-force
// rerun without the deleted records: every unaffected object must be
// byte-identical in the rerun, and every object flagged for recompute must
// be gone or changed there. Objects are matched across runs by node name
// and semantic description, since positions shift after deletion.
func checkAgainstRerun(t *testing.T, before *Pipeline, imp *Impact, us, os []SourceRecord) {
	t.Helper()
	after, _ := build(us, os)
	recompute := recomputeSet(imp)

	afterTuples := make(map[string]Tuple)
	for _, n := range after.Nodes() {
		for _, tup := range n.Output() {
			afterTuples[n.Name()+"/"+tup.Desc] = tup
		}
	}
	for _, n := range before.Nodes() {
		for i, tup := range n.Output() {
			id := fmt.Sprintf("%s/%d", n.Name(), i)
			rerun, exists := afterTuples[n.Name()+"/"+tup.Desc]
			same := exists && rerun.Val == tup.Val && rerun.Prov.String() == tup.Prov.String()
			if recompute[id] && same {
				t.Errorf("%s flagged for recompute but unchanged in rerun", id)
			}
			if !recompute[id] && !same {
				t.Errorf("%s claimed unaffected but changed in rerun", id)
			}
		}
	}
}

func TestDeleteUser(t *testing.T) {
	before, _ := build(users, orders)
	imp := before.Impact("u1")

	want := map[string]bool{
		"users/0": true, "adults/0": true,
		"user_orders/0": true, "user_orders/1": true,
		"order_counts/0": true,
	}
	got := recomputeSet(imp)
	for id := range want {
		if !got[id] {
			t.Errorf("missing recompute object %s", id)
		}
	}
	if len(got) != len(want) {
		t.Errorf("recompute set = %v, want exactly %v", got, want)
	}
	for _, a := range imp.Recompute {
		if len(a.Via) != 1 || a.Via[0] != "u1" {
			t.Errorf("%s via = %v, want [u1]", a.Object, a.Via)
		}
	}
	for _, pr := range imp.Unaffected {
		if !pr.Verify(imp.Deleted) {
			t.Errorf("proof for %s does not verify", pr.Object)
		}
	}
	// Brute-force comparison: rerun without u1.
	checkAgainstRerun(t, before, imp, users[1:], orders)
}

func TestDeleteOrder(t *testing.T) {
	before, agg := build(users, orders)
	imp := before.Impact("o1")

	got := recomputeSet(imp)
	for _, id := range []string{"orders/0", "user_orders/0", "order_counts/0"} {
		if !got[id] {
			t.Errorf("missing recompute object %s", id)
		}
	}
	// Aggregate count(u1) keeps one surviving contribution of u1 but must
	// still be recomputed: o1's duplicate contribution is lost.
	if v, _ := aggVal(agg, "u1"); v != 2 {
		t.Fatalf("count(u1) before delete = %v, want 2", v)
	}
	checkAgainstRerun(t, before, imp, users, orders[1:])
}

func TestDeleteFilteredOutSource(t *testing.T) {
	before, _ := build(users, orders)
	imp := before.Impact("u3")
	if len(imp.Recompute) != 1 { // only the raw copy tuple itself
		t.Fatalf("recompute = %v, want only users/2", imp.Recompute)
	}
	if imp.Recompute[0].Object.Node != "users" || imp.Recompute[0].Object.Index != 2 {
		t.Fatalf("recompute = %v, want users/2", imp.Recompute[0].Object)
	}
	checkAgainstRerun(t, before, imp, users[:2], orders)
}

func TestDuplicateContributionsSurviveCompression(t *testing.T) {
	s := NewStore()
	p := NewPipeline(s)
	// Two distinct records share source ID "d" (e.g. replicated ingest).
	recs := []SourceRecord{
		{ID: "d", Key: "k", Val: 1},
		{ID: "d", Key: "k", Val: 2},
	}
	c := NewCopy(p, "dup", recs)
	agg := NewAggregate(p, "dup_count", c, AggCount)

	out := agg.Output()
	if len(out) != 1 || out[0].Val != 2 {
		t.Fatalf("aggregate = %+v, want one group of count 2", out)
	}
	prov := out[0].Prov
	if prov.Op() != OpSum || len(prov.Args()) != 2 {
		t.Fatalf("prov = %s, want a 2-argument sum", prov)
	}
	if got := Count(prov); got != 2 {
		t.Fatalf("Count(%s) = %d, want 2", prov, got)
	}
	// Compression: both copies reference the same interned Var node.
	if c.Output()[0].Prov != c.Output()[1].Prov {
		t.Fatal("identical Var expressions were not shared")
	}
	if s.Size() != 2 { // Var(d) + Sum(d,d)
		t.Fatalf("store size = %d, want 2", s.Size())
	}
	// Deleting "d" must flag the aggregate: both contributions vanish.
	imp := p.Impact("d")
	if len(imp.Recompute) != 3 {
		t.Fatalf("recompute = %v, want 3 objects", imp.Recompute)
	}
}

func TestSharedSubexpressionsCompressed(t *testing.T) {
	s := NewStore()
	x, y := s.Var("x"), s.Var("y")
	a := s.Prod(x, y)
	b := s.Prod(y, x) // argument order must not matter
	if a != b {
		t.Fatal("structurally identical products were not shared")
	}
	if s.Sum(x, x) == s.Sum(x) {
		t.Fatal("Sum(x,x) collapsed into x: duplicate contribution lost")
	}
	if got := Count(s.Sum(a, a, b)); got != 3 {
		t.Fatalf("Count(Sum(a,a,b)) = %d, want 3", got)
	}
	// Interned nodes: x, y, x*y, Sum(x,x), Sum(a,a,b); Prod(y,x) reused x*y.
	if s.Size() != 5 {
		t.Fatalf("store size = %d, want 5", s.Size())
	}
}

func TestProofTamperingDetected(t *testing.T) {
	before, _ := build(users, orders)
	imp := before.Impact("u1")
	if len(imp.Unaffected) == 0 {
		t.Fatal("no unaffected proofs generated")
	}
	pr := imp.Unaffected[0]
	if !pr.Verify(imp.Deleted) {
		t.Fatal("genuine proof rejected")
	}
	pr.Sources = append(pr.Sources, "u1")
	if pr.Verify(imp.Deleted) {
		t.Fatal("proof claiming deleted source accepted")
	}
	pr.Sources = pr.Sources[:len(pr.Sources)-1]
	pr.Prov = "(* o3 u2) extra"
	if pr.Verify(imp.Deleted) {
		t.Fatal("proof with corrupted expression accepted")
	}
}
