package mvrewrite

import (
	"testing"
)

func acceptanceCatalog() *Catalog {
	c := NewCatalog()
	c.Table("a", PKCol("id"), Col("name"))
	c.Table("b", PKCol("id"), NotNullCol("a_id"), Col("note"))
	c.Table("c", PKCol("id"), NotNullCol("b_id"), Col("note"))
	return c
}

func TestUpperNullRejectionDegradesLeftJoin(t *testing.T) {
	cat := acceptanceCatalog()
	a := Scan("a")
	b := Scan("b")
	q := &Query{
		ID:         "q-inner-by-null-reject",
		Schema:     cat,
		Root:       Left(a, b, On(C("a", "id"), C("b", "a_id"))),
		NullReject: []ColRef{C("b", "id")},
		Select:     []ColRef{C("a", "id"), C("b", "id")},
	}
	v := &View{
		ID:     "v-full-left",
		Root:   Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "a_id"))),
		Select: []ColRef{C("a", "id"), C("b", "id")},
	}

	rep, err := Analyze(cat, q, []*View{v})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Decisions) != 1 || !rep.Decisions[0].Rewritable {
		t.Fatalf("expected rewritable decision, got %#v", rep.Decisions)
	}
	d := rep.Decisions[0]
	if d.Compensation == nil || d.Compensation.Filter.SQL() != "(b.id IS NOT NULL)" {
		t.Fatalf("unexpected compensation: %#v", d.Compensation)
	}
	if d.ExhaustiveCheck == nil || d.ExhaustiveCheck.InstancesTested == 0 {
		t.Fatalf("expected semantic validation report, got %#v", d.ExhaustiveCheck)
	}
	assertCompensationHoldsOnAllTinyInstances(t, cat, q, v, *d.Compensation)
}

func TestMissingNonNullKeyCreatesAmbiguityCounterexample(t *testing.T) {
	cat := acceptanceCatalog()
	q := &Query{
		ID:     "q-needs-b-key",
		Schema: cat,
		Root:   Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "a_id"))),
		Where:  []Predicate{NotNull(C("b", "id"))},
		Select: []ColRef{C("a", "id"), C("b", "note")},
	}
	v := &View{
		ID:     "v-only-nullable-note",
		Root:   Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "a_id"))),
		Select: []ColRef{C("a", "id"), C("b", "note")},
	}

	rep, err := Analyze(cat, q, []*View{v})
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Decisions[0]
	if d.Rewritable {
		t.Fatalf("expected non-rewritable, got %#v", d)
	}
	if d.Code != ReasonMissingDiscriminator {
		t.Fatalf("expected missing discriminator, got %s: %s", d.Code, d.Reason)
	}
	if d.Counterexample == nil || d.Counterexample.Database == nil {
		t.Fatalf("expected concrete counterexample, got %#v", d.Counterexample)
	}
	if d.Ambiguity == nil {
		t.Fatalf("expected null ambiguity proof, decision=%#v", d)
	}
	if d.Ambiguity.Padded == nil || d.Ambiguity.TrueNull == nil {
		t.Fatalf("ambiguity proof must contain both instances")
	}
	if d.RawCounterexample == nil {
		t.Fatalf("expected raw query/view counterexample")
	}
	rawMismatch, err := CheckResult(cat, q, v, Compensation{Filter: NoFilter(), Project: q.Select}, d.RawCounterexample.Database)
	if err != nil {
		t.Fatal(err)
	}
	if rawMismatch == nil {
		t.Fatalf("raw counterexample did not distinguish query and unfiltered view")
	}
	assertCounterexampleDifferentiates(t, cat, q, v, d)
}

func TestNestedLeftJoinCompensationFailure(t *testing.T) {
	cat := acceptanceCatalog()

	bc := Left(Scan("b"), Scan("c"), On(C("b", "id"), C("c", "b_id")))
	bc.Where = []Predicate{NotNull(C("c", "id"))}
	q := &Query{
		ID:     "q-nested-inner-right-subtree",
		Schema: cat,
		Root:   Left(Scan("a"), bc, On(C("a", "id"), C("b", "a_id"))),
		Select: []ColRef{C("a", "id"), C("b", "id"), C("c", "id")},
	}

	// The query applies c.id IS NOT NULL inside the B/C right subtree and
	// then outer-joins that subtree to A.  A B-without-C row is removed
	// internally and therefore re-padded by the A LOJ, turning b.id to NULL.
	// The flattened view still exposes the real b.id; filtering cannot turn
	// that value into padding NULL, so rewrite is impossible.
	v := &View{
		ID: "v-flat-missing-b-key",
		Root: Left(
			Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "a_id"))),
			Scan("c"),
			On(C("b", "id"), C("c", "b_id")),
		),
		Select: []ColRef{C("a", "id"), C("b", "id"), C("c", "id")},
	}

	rep, err := Analyze(cat, q, []*View{v})
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Decisions[0]
	if d.Rewritable {
		t.Fatalf("expected nested compensation failure, got %#v", d)
	}
	if d.Counterexample == nil {
		t.Fatalf("expected counterexample")
	}
	db := d.Counterexample.Database
	if db == nil {
		t.Fatal("nil db")
	}
	for name, td := range db.Tables {
		if len(td.Rows) > 2 {
			t.Fatalf("table %s has >2 rows", name)
		}
	}
	raw, err := CheckCoverage(cat, q, v, db)
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		// It may also refute the attached structured candidate directly.
		if d.Compensation != nil {
			cx, err := CheckResult(cat, q, v, *d.Compensation, db)
			if err != nil || cx == nil {
				t.Fatalf("expected coverage or compensation mismatch, coverage=%v checkErr=%v cx=%v", raw, err, cx)
			}
		} else {
			t.Fatalf("counterexample does not show view coverage loss and no compensation attached")
		}
	}
}

func assertCounterexampleDifferentiates(t *testing.T, cat *Catalog, q *Query, v *View, d Decision) {
	t.Helper()
	db := d.Counterexample.Database
	if db == nil {
		t.Fatal("nil counterexample database")
	}
	for name, td := range db.Tables {
		if len(td.Rows) > 2 {
			t.Fatalf("table %s has %d rows, want at most 2", name, len(td.Rows))
		}
	}
	// The returned Decision may carry the last refuted candidate filter, while
	// a missing-discriminator result is fundamentally about no expressible
	// filter.  At minimum the concrete witness must make raw view output
	// differ from query output or make the attached compensation mismatch.
	attempted := d.AttemptedCompensation
	if attempted == nil && d.Compensation != nil {
		attempted = d.Compensation
	}
	if attempted != nil {
		cx, err := CheckResult(cat, q, v, *attempted, db)
		if err != nil {
			t.Fatal(err)
		}
		if cx == nil {
			t.Fatalf("attached compensation does not fail on its own counterexample")
		}
		return
	}
	cx, err := CheckResult(cat, q, v, Compensation{Filter: NoFilter(), Project: q.Select}, db)
	if err != nil {
		t.Fatal(err)
	}
	if cx == nil {
		t.Fatalf("raw view unexpectedly matches query on counterexample")
	}
}
