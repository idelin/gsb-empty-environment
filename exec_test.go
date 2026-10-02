package cteopt

import "testing"

func TestFilterProjectJoin(t *testing.T) {
	eng := NewEngine(sampleTables())
	plan := &Node{Op: Project, Cols: []string{"o.id", "o.amount"}, Children: []*Node{
		{Op: Filter, Pred: "o.amount >= 200 AND o.amount < 600", Children: []*Node{
			{Op: Scan, Table: "orders", Alias: "o"},
		}},
	}}
	rows := eng.Execute(plan)
	if len(rows) != 5 { // 250, 300, 450, 500, 200
		t.Fatalf("got %d rows, want 5", len(rows))
	}
	for _, r := range rows {
		if len(r) != 2 {
			t.Fatalf("projection should keep 2 columns, got %v", r)
		}
	}
}

func TestJoinAndRename(t *testing.T) {
	eng := NewEngine(sampleTables())
	plan := &Node{Op: Join, JoinKey: [2]string{"o.cust_id", "c.id"}, Children: []*Node{
		{Op: Scan, Table: "orders", Alias: "o"},
		{Op: Scan, Table: "customers", Alias: "c"},
	}}
	rows := eng.Execute(plan)
	if len(rows) != 12 {
		t.Fatalf("join produced %d rows, want 12", len(rows))
	}
	if _, ok := resolve(rows[0], "c.name"); !ok {
		t.Fatalf("joined row missing c.name: %v", rows[0])
	}
}

func TestPredicateEval(t *testing.T) {
	r := Row{"o.amount": 250.0, "c.region": "cn"}
	cases := map[string]bool{
		"o.amount > 100":                     true,
		"o.amount <= 100":                    false,
		"c.region = 'cn'":                    true,
		"c.region != 'cn'":                   false,
		"o.amount >= 250 AND o.amount < 300": true,
	}
	for pred, want := range cases {
		got := filterRows([]Row{r}, pred)
		if (len(got) == 1) != want {
			t.Errorf("pred %q: got %v, want %v", pred, got, want)
		}
	}
}

func TestMaterializeComputesOnce(t *testing.T) {
	child := &Node{Op: Scan, Table: "orders", Alias: "o"}
	mat := &Node{Op: Materialize, Name: "m1", Children: []*Node{child}}
	root := &Node{Op: Join, JoinKey: [2]string{"a.id", "b.id"}, Children: []*Node{
		{Op: Rename, Alias: "a", Children: []*Node{{Op: CTERef, Name: "m1", Src: mat}}},
		{Op: Rename, Alias: "b", Children: []*Node{{Op: CTERef, Name: "m1", Src: mat}}},
	}}
	eng := NewEngine(sampleTables())
	rows := eng.Execute(root)
	if got := eng.ExecCounts[child]; got != 1 {
		t.Fatalf("spool child executed %d times, want 1", got)
	}
	if len(rows) != 12 { // self-join on id
		t.Fatalf("got %d rows, want 12", len(rows))
	}
}
