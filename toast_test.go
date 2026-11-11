package pgoutput

import "testing"

func testRelation() []byte {
	return wireRelation(1, "public", "docs", 'd',
		RelationColumn{Key: true, Name: "id", DataType: 23, TypeModifier: -1},
		RelationColumn{Name: "title", DataType: 25, TypeModifier: -1},
		RelationColumn{Name: "body", DataType: 25, TypeModifier: -1},
	)
}

// TestUnchangedToastMerged verifies that 'u' columns in an old tuple are
// reconstructed from the new row instead of being exposed as NULL.
func TestUnchangedToastMerged(t *testing.T) {
	a := NewAssembler()
	feed(t, a, testRelation())
	feed(t, a, wireType(25, "pg_catalog", "text"))

	// Old tuple carries the key and an unchanged TOAST body; the new row
	// carries the actual TOAST value.
	old := []TupleColumn{textCol("1"), textCol("old title"), unchangedCol()}
	newRow := []TupleColumn{textCol("1"), textCol("new title"), textCol("big toasted body")}

	feed(t, a, wireBegin(10, 100, fixedTime()))
	feed(t, a, wireUpdate(1, 'O', old, newRow))
	res := feed(t, a, wireCommit(100, 116, fixedTime()))
	if len(res.Transactions) != 1 {
		t.Fatalf("expected 1 published transaction, got %d", len(res.Transactions))
	}
	ch := res.Transactions[0].Changes[0]
	if ch.Op != OpUpdate {
		t.Fatalf("expected update, got %s", ch.Op)
	}
	body := ch.Old[2]
	if body.Unchanged || body.Value == nil || string(body.Value) != "big toasted body" {
		t.Fatalf("TOAST column not merged: %+v", body)
	}
	if string(ch.New[1].Value) != "new title" {
		t.Fatalf("new tuple damaged: %+v", ch.New)
	}
}

// TestUnchangedToastBothSides verifies an opaque 'u' marker survives when the
// value is absent from both tuples.
func TestUnchangedToastBothSides(t *testing.T) {
	rel := Relation{ID: 1, Columns: []RelationColumn{{Key: true}, {}}}
	old := []TupleColumn{textCol("1"), unchangedCol()}
	newRow := []TupleColumn{textCol("1"), unchangedCol()}

	merged, err := mergeToast(&rel, old, newRow)
	if err != nil {
		t.Fatalf("mergeToast: %v", err)
	}
	if !merged[1].Unchanged || merged[1].Value != nil {
		t.Fatalf("expected preserved unchanged marker, got %+v", merged[1])
	}
}

// TestKeyOnlyOldTuple verifies 'K' tuples are widened to full relation width
// and non-key columns stay NULL rather than shifting column positions.
func TestKeyOnlyOldTuple(t *testing.T) {
	a := NewAssembler()
	feed(t, a, testRelation())

	feed(t, a, wireBegin(10, 200, fixedTime()))
	feed(t, a, wireDelete(1, 'K', []TupleColumn{textCol("1")}))
	res := feed(t, a, wireCommit(200, 216, fixedTime()))

	if len(res.Transactions) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(res.Transactions))
	}
	ch := res.Transactions[0].Changes[0]
	if ch.Op != OpDelete || len(ch.Old) != 3 {
		t.Fatalf("bad delete change: %+v", ch)
	}
	if string(ch.Old[0].Value) != "1" || ch.Old[1].Flag != tupleNull || ch.Old[2].Flag != tupleNull {
		t.Fatalf("key tuple not widened correctly: %+v", ch.Old)
	}
}

func TestCatalogLookupAndTypeInfo(t *testing.T) {
	c := NewCatalog()
	c.AddType(Type{ID: 23, Namespace: "pg_catalog", Name: "int4"})
	c.AddRelation(Relation{
		ID: 7,
		Columns: []RelationColumn{
			{Key: true, Name: "id", DataType: 23},
		},
	})
	rel, ok := c.Relation(7)
	if !ok {
		t.Fatal("relation not cached")
	}
	typ, ok := rel.LookupType(0, c.types)
	if !ok || typ.Name != "int4" {
		t.Fatalf("type lookup failed: %+v", typ)
	}
	if _, ok := c.Relation(999); ok {
		t.Fatal("unknown relation should miss")
	}
}

func TestUnknownRelationRejected(t *testing.T) {
	a := NewAssembler()
	feed(t, a, wireBegin(10, 300, fixedTime()))
	feedExpectErr(t, a, wireInsert(99, []TupleColumn{textCol("x")}))
}
