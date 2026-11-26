package joinenum

import (
	"testing"
	"time"
)

func TestEightTableMixedQuery(t *testing.T) {
	rels := make([]Relation, 8)
	for i := range rels {
		rels[i] = Relation{Name: string(rune('A' + i)), Cardinality: 4, Attributes: []string{"k"}}
	}
	ab := LeftOuter(Rel(0), Rel(1), eqPred("p_AB", 1, 2, 0, 1))
	cd := Inner(Rel(2), Rel(3), eqPred("p_CD", 1, 2, 2, 3))
	left := Inner(ab, cd, eqPred("q_BC", 1, 2, 1, 2))
	ef := Semi(Rel(4), Rel(5), eqPred("p_EF", 1, 2, 4, 5))
	gh := Anti(Rel(6), Rel(7), eqPred("p_GH", 1, 2, 6, 7))
	right := LeftOuter(ef, gh, eqPred("q_EG", 1, 2, 4, 6))
	root := Inner(left, right, eqPred("q_DE", 1, 2, 3, 4))
	q := mustQuery(t, rels, root)
	start := time.Now()
	res := mustOptimize(t, q)
	elapsed := time.Since(start)
	if elapsed > 10*time.Second {
		t.Fatalf("8-table enumeration took %v", elapsed)
	}
	// The plan must be in the semantic core; evaluate on a small database and
	// compare with the syntax tree.
	db := NewDatabase([][]Row{
		rows(1, 2), rows(1, 2), rows(1, 2), rows(1, 2),
		rows(1, 2), rows(1, 2), rows(1, 2), rows(1, 2),
	})
	want, err := q.EvaluateOriginal(db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := q.Evaluate(res.Plan, db)
	if err != nil {
		t.Fatal(err)
	}
	if !sameBag(want, got) {
		t.Fatalf("8-table optimal plan is not result-equivalent")
	}
	legal, err := q.LegalPlans()
	if err != nil {
		t.Fatal(err)
	}
	for _, lr := range legal {
		rows, err := q.Evaluate(lr.Plan, db)
		if err != nil {
			t.Fatalf("legal plan %s: %v", lr.Encoding, err)
		}
		if !sameBag(want, rows) {
			t.Fatalf("legal plan %s is not result-equivalent", lr.Encoding)
		}
	}
	t.Logf("8-table optimal cost=%d card=%d encoding=%s in %v", res.Cost, res.Cardinality, res.Encoding, elapsed)
}
