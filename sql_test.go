package cteopt

import "testing"

func TestParseBuildsSharedDAG(t *testing.T) {
	root, err := Parse(cteQuery)
	if err != nil {
		t.Fatal(err)
	}
	if root.Op != Project {
		t.Fatalf("root op = %v, want Project", root.Op)
	}
	join := root.Children[0]
	if join.Op != Join {
		t.Fatalf("expected Join below Project, got %v", join.Op)
	}
	l, r := join.Children[0], join.Children[1]
	if l.Op != Rename || r.Op != Rename {
		t.Fatalf("expected Renames below Join, got %v / %v", l.Op, r.Op)
	}
	if l.Children[0] != r.Children[0] {
		t.Fatal("CTE body should be one shared pointer across both branches")
	}
}

func TestParseErrors(t *testing.T) {
	for _, q := range []string{
		"SELECT FROM t",
		"SELECT * FROM",
		"WITH c AS (SELECT * FROM t",
		"SELECT * FROM t WHERE a ~ 1",
		"SELECT * FROM t extra junk",
	} {
		if _, err := Parse(q); err == nil {
			t.Errorf("query %q: expected error, got nil", q)
		}
	}
}

func TestParseMultipleCTEs(t *testing.T) {
	q := `WITH a AS (SELECT * FROM orders WHERE amount > 10),
	             b AS (SELECT * FROM customers)
	      SELECT * FROM a JOIN b ON a.cust_id = b.id`
	root, err := Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	Estimate(root, smallStats())
	if root.Rows <= 0 {
		t.Fatal("estimates not filled")
	}
}
