package mvrewrite

import "testing"

func TestDeepNestedLeftJoinCompensationFailure(t *testing.T) {
	cat := NewCatalog()
	cat.Table("a", PKCol("id"))
	cat.Table("b", PKCol("id"), NotNullCol("aid"))
	cat.Table("c", PKCol("id"), NotNullCol("bid"))
	cat.Table("d", PKCol("id"), NotNullCol("cid"))

	cd := Left(Scan("c"), Scan("d"), On(C("c", "id"), C("d", "cid")))
	cd.NullReject = []ColRef{C("d", "id")}
	bcd := Left(Scan("b"), cd, On(C("b", "id"), C("c", "bid")))
	q := &Query{
		Schema: cat,
		Root:   Left(Scan("a"), bcd, On(C("a", "id"), C("b", "aid"))),
		Select: []ColRef{C("a", "id"), C("b", "id"), C("c", "id"), C("d", "id")},
	}

	flat := Left(
		Left(
			Left(Scan("a"), Scan("b"), On(C("a", "id"), C("b", "aid"))),
			Scan("c"),
			On(C("b", "id"), C("c", "bid")),
		),
		Scan("d"),
		On(C("c", "id"), C("d", "cid")),
	)
	v := &View{ID: "flat-four-level", Root: flat, Select: q.Select}

	rep, err := Analyze(cat, q, []*View{v})
	if err != nil {
		t.Fatal(err)
	}
	d := rep.Decisions[0]
	t.Logf("decision rewritable=%v code=%s reason=%s check=%#v comp=%s", d.Rewritable, d.Code, d.Reason, d.ExhaustiveCheck, func() string {
		if d.Compensation != nil {
			return d.Compensation.SQL
		}
		return ""
	}())
	if d.Rewritable {
		t.Fatalf("deep re-padding mismatch should not be rewritable: %#v", d.Compensation)
	}
	if d.Counterexample == nil || d.Counterexample.Database == nil {
		t.Fatal("expected counterexample")
	}
	for name, td := range d.Counterexample.Database.Tables {
		if len(td.Rows) > 2 {
			t.Fatalf("table %s has %d rows", name, len(td.Rows))
		}
	}
	cx, err := CheckCoverage(cat, q, v, d.Counterexample.Database)
	if err != nil {
		t.Fatal(err)
	}
	if cx == nil {
		t.Fatalf("expected raw view coverage mismatch on deep witness")
	}
	if len(cx.Missing) == 0 {
		t.Fatalf("expected missing query tuples, got %#v", cx)
	}
}
