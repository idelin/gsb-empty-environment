package joinenum

import "testing"

func TestInvalidInputs(t *testing.T) {
	rels := testRelations()[:2]
	p := eqPred("p", 1, 2, 0, 1)
	cases := []struct {
		name string
		rels []Relation
		root *Node
	}{
		{"duplicate leaf", rels, Inner(Rel(0), Rel(0), p)},
		{"missing leaf", testRelations()[:3], Inner(Rel(0), Rel(1), p)},
		{"predicate outside", rels, Inner(Rel(0), Rel(1), eqPred("bad", 1, 1, 0, 2))},
		{"nil predicate", rels, Inner(Rel(0), Rel(1), nil)},
		{"bad selectivity", rels, Inner(Rel(0), Rel(1), &Predicate{Name: "bad", Refs: []int{0, 1}, Selectivity: Sel(-1, 1)})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewQuery(tc.rels, tc.root); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestTopLevelOptimize(t *testing.T) {
	rels := testRelations()[:2]
	pred := eqPred("p", 1, 2, 0, 1)
	res, err := Optimize(Input{
		Relations: rels,
		Root:      Inner(Rel(0), Rel(1), pred),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Encoding != "I(A,B)" && res.Encoding != "I(B,A)" {
		t.Fatalf("unexpected encoding %q", res.Encoding)
	}
}

func TestSingleRelationQuery(t *testing.T) {
	rels := []Relation{{Name: "A", Cardinality: 3, Attributes: []string{"k"}}}
	q := mustQuery(t, rels, Rel(0))
	res := mustOptimize(t, q)
	if res.Encoding != "A" || res.Cost != 0 || res.Cardinality != 3 {
		t.Fatalf("single relation result = %+v", res)
	}
	rows, err := q.Evaluate(res.Plan, NewDatabase([][]Row{rows(1, 2, 3)}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
}
