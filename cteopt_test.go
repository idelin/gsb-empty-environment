package cteopt

import (
	"sort"
	"testing"
)

const cteQuery = `
WITH big_orders AS (
  SELECT o.id, o.cust_id, o.amount
  FROM orders o JOIN customers c ON o.cust_id = c.id
  WHERE o.amount > 100
)
SELECT * FROM big_orders x JOIN big_orders y ON x.cust_id = y.cust_id
`

func sampleTables() map[string][]Row {
	customers := []Row{
		{"id": 1.0, "name": "alice", "region": "cn"},
		{"id": 2.0, "name": "bob", "region": "us"},
		{"id": 3.0, "name": "carol", "region": "cn"},
		{"id": 4.0, "name": "dave", "region": "eu"},
		{"id": 5.0, "name": "erin", "region": "us"},
	}
	orders := []Row{
		{"id": 101.0, "cust_id": 1.0, "amount": 250.0},
		{"id": 102.0, "cust_id": 1.0, "amount": 80.0},
		{"id": 103.0, "cust_id": 2.0, "amount": 300.0},
		{"id": 104.0, "cust_id": 2.0, "amount": 450.0},
		{"id": 105.0, "cust_id": 3.0, "amount": 40.0},
		{"id": 106.0, "cust_id": 3.0, "amount": 900.0},
		{"id": 107.0, "cust_id": 4.0, "amount": 120.0},
		{"id": 108.0, "cust_id": 1.0, "amount": 500.0},
		{"id": 109.0, "cust_id": 5.0, "amount": 60.0},
		{"id": 110.0, "cust_id": 5.0, "amount": 700.0},
		{"id": 111.0, "cust_id": 2.0, "amount": 30.0},
		{"id": 112.0, "cust_id": 3.0, "amount": 200.0},
	}
	return map[string][]Row{"customers": customers, "orders": orders}
}

// bigStats makes the CTE body expensive so materialization should win.
func bigStats() Stats { return Stats{"orders": 1_000_000, "customers": 100_000} }

// smallStats makes the CTE body cheap so inlining should win.
func smallStats() Stats { return Stats{"orders": 10, "customers": 5} }

func countOps(n *Node, op Op) int {
	total := 0
	seen := map[*Node]bool{}
	var walk func(*Node)
	walk = func(x *Node) {
		if seen[x] {
			return
		}
		if x.Op == op {
			total++
		}
		seen[x] = true
		for _, c := range x.Children {
			walk(c)
		}
		if x.Src != nil {
			walk(x.Src)
		}
	}
	walk(n)
	return total
}

func findScans(n *Node, table string) []*Node {
	var out []*Node
	seen := map[*Node]bool{}
	var walk func(*Node)
	walk = func(x *Node) {
		if seen[x] {
			return
		}
		seen[x] = true
		if x.Op == Scan && x.Table == table {
			out = append(out, x)
		}
		for _, c := range x.Children {
			walk(c)
		}
		if x.Src != nil {
			walk(x.Src)
		}
	}
	walk(n)
	return out
}

func sortedKeys(rows []Row) []string {
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, RowKey(r))
	}
	sort.Strings(keys)
	return keys
}

func equalRows(a, b []Row) bool {
	ka, kb := sortedKeys(a), sortedKeys(b)
	if len(ka) != len(kb) {
		return false
	}
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

// Acceptance: a CTE referenced by two branches is materialized once into an
// in-memory buffer when the estimated cost says so, saving recomputation.
func TestOptimizeMaterializesExpensiveCTE(t *testing.T) {
	root, err := Parse(cteQuery)
	if err != nil {
		t.Fatal(err)
	}

	// Baseline: no optimization -> the shared subplan is recomputed twice.
	engInline := NewEngine(sampleTables())
	inlineRows := engInline.Execute(root)
	scans := findScans(root, "orders")
	if len(scans) != 1 {
		t.Fatalf("expected 1 shared orders scan node, got %d", len(scans))
	}
	if got := engInline.ExecCounts[scans[0]]; got != 2 {
		t.Fatalf("inline plan: orders scan executed %d times, want 2", got)
	}

	// Optimized: one Materialize spool + two CTERef readers.
	opt := Optimize(root, bigStats())
	if got := countOps(opt, Materialize); got != 1 {
		t.Fatalf("optimized plan has %d Materialize nodes, want 1", got)
	}
	if got := countOps(opt, CTERef); got != 2 {
		t.Fatalf("optimized plan has %d CTERef nodes, want 2", got)
	}

	engOpt := NewEngine(sampleTables())
	optRows := engOpt.Execute(opt)
	optScans := findScans(opt, "orders")
	if len(optScans) != 1 {
		t.Fatalf("expected 1 orders scan node in optimized plan, got %d", len(optScans))
	}
	if got := engOpt.ExecCounts[optScans[0]]; got != 1 {
		t.Fatalf("materialized plan: orders scan executed %d times, want 1", got)
	}

	// Results must be identical and non-empty.
	if len(optRows) == 0 {
		t.Fatal("empty result")
	}
	if !equalRows(inlineRows, optRows) {
		t.Fatalf("results differ: inline=%d rows, materialized=%d rows", len(inlineRows), len(optRows))
	}
}

// A cheap shared subexpression stays inlined (recomputed per reference).
func TestOptimizeInlinesCheapCTE(t *testing.T) {
	root, err := Parse(cteQuery)
	if err != nil {
		t.Fatal(err)
	}
	opt := Optimize(root, smallStats())
	if got := countOps(opt, Materialize); got != 0 {
		t.Fatalf("cheap CTE should stay inlined, got %d Materialize nodes", got)
	}
	eng := NewEngine(sampleTables())
	eng.Execute(opt)
	scans := findScans(opt, "orders")
	if len(scans) != 1 || eng.ExecCounts[scans[0]] != 2 {
		t.Fatalf("cheap CTE: expected shared scan executed twice, got nodes=%d", len(scans))
	}
}

// CSE works even when the common subexpression is not a shared pointer but
// two structurally identical subtrees built independently.
func TestCommonSubexpressionDistinctPointers(t *testing.T) {
	build := func() *Node {
		return &Node{Op: Rename, Alias: "x", Children: []*Node{
			{Op: Filter, Pred: "amount > 100", Children: []*Node{
				{Op: Scan, Table: "orders", Alias: "orders"},
			}},
		}}
	}
	left := build()
	right := build()
	right.Alias = "y"
	root := &Node{Op: Join, JoinKey: [2]string{"x.cust_id", "y.cust_id"}, Children: []*Node{left, right}}

	opt := Optimize(root, bigStats())
	if got := countOps(opt, Materialize); got != 1 {
		t.Fatalf("expected 1 Materialize for identical subtrees, got %d", got)
	}
	if got := countOps(opt, CTERef); got != 2 {
		t.Fatalf("expected 2 CTERef readers, got %d", got)
	}
	eng := NewEngine(sampleTables())
	rows := eng.Execute(opt)
	scans := findScans(opt, "orders")
	if len(scans) != 1 || eng.ExecCounts[scans[0]] != 1 {
		t.Fatalf("expected single orders scan executed once, got nodes=%d", len(scans))
	}
	if len(rows) == 0 {
		t.Fatal("empty result")
	}
}

func TestEstimateFillsStats(t *testing.T) {
	root, err := Parse(cteQuery)
	if err != nil {
		t.Fatal(err)
	}
	Estimate(root, bigStats())
	if root.Rows <= 0 || root.Cost <= 0 {
		t.Fatalf("estimates not filled: rows=%v cost=%v", root.Rows, root.Cost)
	}
}
