package cteopt

import "testing"

func findOp(n *Node, op Op, out *[]*Node) {
	seen := map[*Node]bool{}
	var walk func(*Node)
	walk = func(x *Node) {
		if seen[x] {
			return
		}
		seen[x] = true
		if x.Op == op {
			*out = append(*out, x)
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
}

func TestOptimizeMaterializesExpensiveCTE(t *testing.T) {
	tables := DemoTables()
	root, cte := BuildDemoPlan()

	opt := &Optimizer{Tables: tables}
	optimized := opt.Optimize(root)

	var mats []*Node
	findOp(optimized, OpMaterialize, &mats)
	if len(mats) != 1 {
		t.Fatalf("expected exactly 1 Materialize node, got %d", len(mats))
	}
	if mats[0].Children[0] != cte {
		t.Fatalf("Materialize should wrap the shared CTE subtree")
	}

	// Both branches must reference the same Materialize instance.
	parents := 0
	for _, n := range collect(optimized) {
		for _, c := range n.Children {
			if c == mats[0] {
				parents++
			}
		}
	}
	if parents != 2 {
		t.Fatalf("expected Materialize to be referenced twice, got %d", parents)
	}

	if len(opt.Decisions) == 0 || !opt.Decisions[0].Materialized {
		t.Fatalf("expected a MATERIALIZE decision, got %+v", opt.Decisions)
	}
	d := opt.Decisions[0]
	if d.Refs != 2 || d.MaterializeCost >= d.InlineCost {
		t.Fatalf("unexpected decision numbers: %+v", d)
	}
}

func TestOptimizeInlinesCheapCTE(t *testing.T) {
	root, _ := BuildCheapCTEPlan()
	opt := &Optimizer{Tables: CheapTables()}
	optimized := opt.Optimize(root)

	var mats []*Node
	findOp(optimized, OpMaterialize, &mats)
	if len(mats) != 0 {
		t.Fatalf("cheap CTE should stay inlined, got %d Materialize nodes", len(mats))
	}
	if len(opt.Decisions) != 1 || opt.Decisions[0].Materialized {
		t.Fatalf("expected a single INLINE decision, got %+v", opt.Decisions)
	}
}

func TestOptimizeDeduplicatesStructuralCSE(t *testing.T) {
	// Two structurally identical but separately constructed subtrees:
	// the optimizer must recognize them as the same computation.
	tables := DemoTables()
	build := func() *Node {
		return Filter("amount > 100",
			func(r Row) bool { return r[2].(float64) > 100 },
			Join("orders.user_id = users.user_id",
				Scan("orders"), Scan("users"),
				func(r Row) any { return r[1] },
				func(r Row) any { return r[0] }))
	}
	root := UnionAll(build(), build())

	opt := &Optimizer{Tables: tables}
	optimized := opt.Optimize(root)

	var mats []*Node
	findOp(optimized, OpMaterialize, &mats)
	if len(mats) != 1 {
		t.Fatalf("expected 1 shared Materialize for duplicate subtrees, got %d", len(mats))
	}
	parents := 0
	for _, n := range collect(optimized) {
		for _, c := range n.Children {
			if c == mats[0] {
				parents++
			}
		}
	}
	if parents != 2 {
		t.Fatalf("expected both parents to share one Materialize, got %d refs", parents)
	}
}

func TestFingerprint(t *testing.T) {
	a := Filter("x > 1", nil, Scan("t"))
	b := Filter("x > 1", nil, Scan("t"))
	c := Filter("x > 2", nil, Scan("t"))
	memo := map[*Node]string{}
	if fingerprint(a, memo) != fingerprint(b, memo) {
		t.Fatal("structurally equal subtrees must share a fingerprint")
	}
	if fingerprint(a, memo) == fingerprint(c, memo) {
		t.Fatal("different predicates must not share a fingerprint")
	}
}

func TestAnnotate(t *testing.T) {
	tables := DemoTables()
	root, cte := BuildDemoPlan()
	Annotate(root, tables)
	if cte.Cost <= MinCostToMaterialize {
		t.Fatalf("demo CTE should be expensive, cost=%v", cte.Cost)
	}
	if cte.EstRows != 300 {
		t.Fatalf("unexpected CTE row estimate: %v", cte.EstRows)
	}
}
