package mvrewrite

import "testing"

func TestAliasBoundCatalogAndAmbiguity(t *testing.T) {
	cat := NewCatalog()
	cat.Table("ta", PKCol("id"))
	cat.Table("tb", PKCol("id"), NotNullCol("aid"), Col("note"))

	q := &Query{
		Schema: cat,
		Root: Left(
			ScanAs("ta", "x"),
			ScanAs("tb", "y"),
			On(C("x", "id"), C("y", "aid")),
		),
		NullReject: []ColRef{C("y", "id")},
		Select:     []ColRef{C("x", "id"), C("y", "note")},
	}
	v := &View{
		ID: "aliased",
		Root: Left(
			ScanAs("ta", "x"),
			ScanAs("tb", "y"),
			On(C("x", "id"), C("y", "aid")),
		),
		Select: []ColRef{C("x", "id"), C("y", "note")},
	}

	d := New(cat).Decide(q, v)[0]
	if d.Rewritable {
		t.Fatalf("expected alias missing discriminator failure")
	}
	if d.Ambiguity == nil {
		t.Fatalf("expected alias-aware ambiguity proof: %s", d.Reason)
	}
	if d.Ambiguity.Padded == nil || d.Ambiguity.TrueNull == nil {
		t.Fatal("incomplete ambiguity proof")
	}
}
