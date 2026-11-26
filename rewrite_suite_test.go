package mvrewrite

import (
	"encoding/json"
	"testing"
)

func tinyCatalog() *Catalog {
	c := NewCatalog()
	c.Table("a", PKCol("id"), Col("x"))
	c.Table("b", PKCol("id"), NotNullCol("aid"), Col("y"))
	return c
}

func tinyLeftQuery() *Query {
	return &Query{
		ID:     "q",
		Schema: tinyCatalog(),
		Root:   Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
		Where:  []Predicate{NotNull(C("b", "id"))},
		Select: []ColRef{C("a", "id"), C("b", "id")},
	}
}

func TestAnalyzeReturnsAllAndOnlyRewritableViews(t *testing.T) {
	cat := tinyCatalog()
	q := tinyLeftQuery()

	goodCompensated := &View{
		ID:     "good-compensated",
		Root:   Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
		Select: []ColRef{C("a", "id"), C("b", "id")},
	}
	goodExact := &View{
		ID: "good-exact-inner",
		Root: func() *Node {
			n := Inner(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid")))
			return n
		}(),
		Select: []ColRef{C("a", "id"), C("b", "id")},
	}
	missingKey := &View{
		ID:     "bad-missing-key",
		Root:   Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
		Select: []ColRef{C("a", "id")},
	}
	equivalentInner := &View{
		ID:     "good-null-rejected-inner",
		Root:   Inner(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
		Select: []ColRef{C("a", "id"), C("b", "id")},
	}

	rep, err := Analyze(cat, q, []*View{goodCompensated, goodExact, missingKey, equivalentInner})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, d := range rep.Decisions {
		got[d.ViewID] = d.Rewritable
		t.Logf("decision %s rewritable=%v code=%s", d.ViewID, d.Rewritable, d.Code)
		if d.Rewritable {
			if d.Compensation == nil {
				t.Fatalf("rewritable view %s missing compensation", d.ViewID)
			}
			vv := viewByID([]*View{goodCompensated, goodExact, missingKey, equivalentInner}, d.ViewID)
			if vv == nil {
				t.Fatalf("test setup error: no view for decision %q", d.ViewID)
			}
			assertCompensationHoldsOnAllTinyInstances(t, cat, q, vv, *d.Compensation)
		} else if d.Counterexample == nil {
			t.Fatalf("non-rewritable view %s lacks counterexample", d.ViewID)
		}
	}
	if !got["good-compensated"] || !got["good-exact-inner"] || !got["good-null-rejected-inner"] {
		t.Fatalf("expected good views rewritable, got %#v", got)
	}
	if got["bad-missing-key"] {
		t.Fatalf("bad views incorrectly accepted: %#v", got)
	}
}

func TestExtraInnerJoinLosesPreservedOuterRows(t *testing.T) {
	cat := tinyCatalog()
	q := &Query{
		Schema: cat,
		Root:   Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
		Select: []ColRef{C("a", "id"), C("b", "id")},
	}
	v := &View{
		ID:     "extra-inner",
		Root:   Inner(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
		Select: []ColRef{C("a", "id"), C("b", "id")},
	}
	rep, err := Analyze(cat, q, []*View{v})
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Decisions[0]
	if d.Rewritable {
		t.Fatalf("inner view must lose preserved LOJ complement: %#v", d.Compensation)
	}
	if d.Counterexample == nil {
		t.Fatal("expected loss counterexample")
	}
	if cx, err := CheckCoverage(cat, q, v, d.Counterexample.Database); err != nil {
		t.Fatal(err)
	} else if cx == nil && d.AttemptedCompensation == nil {
		t.Fatalf("counterexample should show coverage loss or a refuted compensation")
	}
}

func TestStrongerViewPredicateCannotRewrite(t *testing.T) {
	cat := tinyCatalog()
	q := &Query{
		Schema: cat,
		Root:   Scan("a"),
		Where:  []Predicate{Ge(C("a", "x"), int64(0))},
		Select: []ColRef{C("a", "id"), C("a", "x")},
	}
	v := &View{
		ID:     "strong",
		Root:   &Node{Kind: ScanNode, Alias: "a", Table: "a", Where: []Predicate{Gt(C("a", "x"), int64(0))}},
		Select: []ColRef{C("a", "id"), C("a", "x")},
	}
	rep, err := Analyze(cat, q, []*View{v})
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Decisions[0]
	if d.Rewritable {
		t.Fatalf("stronger predicate should not be rewritable: %#v", d.Compensation)
	}
	if d.Counterexample == nil {
		t.Fatal("missing counterexample")
	}
	if d.Counterexample.Database == nil {
		t.Fatal("missing db")
	}
}

func TestRangeCompensationHoldsExhaustively(t *testing.T) {
	cat := tinyCatalog()
	q := &Query{
		Schema: cat,
		Root:   Scan("a"),
		Where:  []Predicate{Between(C("a", "x"), int64(0), int64(2))},
		Select: []ColRef{C("a", "id"), C("a", "x")},
	}
	v := &View{ID: "all-a", Root: Scan("a"), Select: []ColRef{C("a", "id"), C("a", "x")}}
	rep, err := Analyze(cat, q, []*View{v})
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Decisions[0]
	if !d.Rewritable {
		t.Fatalf("expected range rewrite, got %s: %s / %#v", d.Code, d.Reason, d.Counterexample)
	}
	assertCompensationHoldsOnAllTinyInstances(t, cat, q, v, *d.Compensation)
}

func assertCompensationHoldsOnAllTinyInstances(t *testing.T, cat *Catalog, q *Query, v *View, comp Compensation) {
	t.Helper()
	opts := SearchOptions{
		MaxRowsPerTable: 2,
		Domain:          []any{int64(0), int64(1)},
		MaxModels:       2000000,
		Minimize:        false,
	}
	cx, report, err := FindRewriteCounterexample(cat, q, v, comp, opts)
	if err != nil {
		t.Fatalf("exhaustive semantic validation errored: %v (report=%#v)", err, report)
	}
	if cx != nil {
		t.Fatalf("compensation failed on exhaustive tiny instances: %s", mustJSON(cx))
	}
	if report == nil || !report.Complete {
		t.Fatalf("exhaustive validation did not complete: %#v", report)
	}
}

func viewByID(views []*View, id string) *View {
	for _, v := range views {
		if v.ID == id {
			return v
		}
	}
	return nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}
