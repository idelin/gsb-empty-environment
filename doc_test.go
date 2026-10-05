package anno

import (
	"reflect"
	"testing"
)

func applyAll(t *testing.T, d *Doc, ops ...Op) {
	t.Helper()
	for _, op := range ops {
		d.Apply(op)
	}
	if n := d.Pending(); n != 0 {
		t.Fatalf("%d operations still pending", n)
	}
}

func checkRuns(t *testing.T, d *Doc, want []Run) {
	t.Helper()
	if got := d.Merge(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge() = %+v, want %+v", got, want)
	}
	if got, want := RunsFrom(d.Reference()), d.Merge(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reference evaluator disagrees: RunsFrom(Reference()) = %+v, Merge() = %+v", got, want)
	}
}

func TestConcurrentSamePredOrdering(t *testing.T) {
	d := New()
	applyAll(t, d,
		Insert{ID: ID{1, "amy"}, Pred: Root, Char: 'a'},
		Insert{ID: ID{1, "bob"}, Pred: Root, Char: 'b'},
		Insert{ID: ID{2, "amy"}, Pred: Root, Char: 'c'},
	)
	// Same predecessor: ordered by operation ID, higher ID first.
	if got, want := d.Text(), "cba"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestLeftEndInsert(t *testing.T) {
	a, b, c := ID{1, "amy"}, ID{2, "amy"}, ID{3, "amy"}

	// A character inserted before the left anchor stays outside the span.
	d := New()
	applyAll(t, d,
		Insert{ID: a, Pred: Root, Char: 'a'},
		Insert{ID: b, Pred: a, Char: 'b'},
		Insert{ID: c, Pred: b, Char: 'c'},
		Annotate{ID: ID{4, "amy"}, Ann: "b1", Key: "bold", Val: "1",
			Left: a, LeftIncl: true, Right: c, RightIncl: true},
		Insert{ID: ID{5, "bob"}, Pred: Root, Char: 'Z'},
	)
	if got, want := d.Text(), "Zabc"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	checkRuns(t, d, []Run{
		{Text: "Z", Attrs: Attrs{}},
		{Text: "abc", Attrs: Attrs{"bold": "1"}},
	})

	// Anchoring the left end at Root extends the span over anything later
	// inserted at the beginning, even when the insert arrives after the
	// annotation: inheritance follows the final anchor sequence.
	d2 := New()
	applyAll(t, d2,
		Insert{ID: a, Pred: Root, Char: 'a'},
		Annotate{ID: ID{4, "amy"}, Ann: "b1", Key: "bold", Val: "1",
			Left: Root, LeftIncl: false, Right: a, RightIncl: true},
		Insert{ID: ID{5, "bob"}, Pred: Root, Char: 'Z'},
	)
	checkRuns(t, d2, []Run{
		{Text: "Za", Attrs: Attrs{"bold": "1"}},
	})
}

func TestRightEndInsert(t *testing.T) {
	a, b := ID{1, "amy"}, ID{2, "amy"}
	d := New()
	applyAll(t, d,
		Insert{ID: a, Pred: Root, Char: 'a'},
		Insert{ID: b, Pred: a, Char: 'b'},
		Annotate{ID: ID{3, "amy"}, Ann: "b1", Key: "bold", Val: "1",
			Left: a, LeftIncl: true, Right: b, RightIncl: true},
		Insert{ID: ID{4, "bob"}, Pred: b, Char: 'Y'},
	)
	if got, want := d.Text(), "abY"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	// The span is closed by its inclusive right anchor; anything inserted
	// after that anchor is outside.
	checkRuns(t, d, []Run{
		{Text: "ab", Attrs: Attrs{"bold": "1"}},
		{Text: "Y", Attrs: Attrs{}},
	})
}

func TestBothAnchorsDeleted(t *testing.T) {
	a, b, c := ID{1, "amy"}, ID{2, "amy"}, ID{3, "amy"}
	d := New()
	applyAll(t, d,
		Insert{ID: a, Pred: Root, Char: 'a'},
		Insert{ID: b, Pred: a, Char: 'b'},
		Insert{ID: c, Pred: b, Char: 'c'},
		Annotate{ID: ID{4, "amy"}, Ann: "b1", Key: "bold", Val: "1",
			Left: a, LeftIncl: true, Right: c, RightIncl: true},
		Delete{ID: ID{5, "bob"}, Target: a},
		Delete{ID: ID{6, "bob"}, Target: c},
	)
	// Both anchors are tombstones, but deletes keep anchors: the annotation
	// still applies to the surviving character between them.
	if got, want := d.Text(), "b"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	checkRuns(t, d, []Run{
		{Text: "b", Attrs: Attrs{"bold": "1"}},
	})
}

func TestEmptyInterval(t *testing.T) {
	a, b := ID{1, "amy"}, ID{2, "amy"}
	d := New()
	applyAll(t, d,
		Insert{ID: a, Pred: Root, Char: 'a'},
		Insert{ID: b, Pred: a, Char: 'b'},
		// Adjacent anchors, both exclusive: covers nothing.
		Annotate{ID: ID{3, "amy"}, Ann: "e1", Key: "ital", Val: "1",
			Left: a, LeftIncl: false, Right: b, RightIncl: false},
		// Same anchor on both ends, both exclusive: also empty.
		Annotate{ID: ID{4, "amy"}, Ann: "e2", Key: "und", Val: "1",
			Left: a, LeftIncl: false, Right: a, RightIncl: false},
		// Same anchor, both inclusive: exactly that one character.
		Annotate{ID: ID{5, "amy"}, Ann: "e3", Key: "bold", Val: "1",
			Left: a, LeftIncl: true, Right: a, RightIncl: true},
	)
	checkRuns(t, d, []Run{
		{Text: "a", Attrs: Attrs{"bold": "1"}},
		{Text: "b", Attrs: Attrs{}},
	})
}

func TestOverlappingUndo(t *testing.T) {
	a, b, c, e := ID{1, "amy"}, ID{2, "amy"}, ID{3, "amy"}, ID{4, "amy"}
	d := New()
	applyAll(t, d,
		Insert{ID: a, Pred: Root, Char: 'a'},
		Insert{ID: b, Pred: a, Char: 'b'},
		Insert{ID: c, Pred: b, Char: 'c'},
		Insert{ID: e, Pred: c, Char: 'd'},
		Annotate{ID: ID{5, "amy"}, Ann: "a1", Key: "bold", Val: "1",
			Left: a, LeftIncl: true, Right: c, RightIncl: true},
		Annotate{ID: ID{6, "bob"}, Ann: "a2", Key: "bold", Val: "2",
			Left: b, LeftIncl: true, Right: e, RightIncl: true},
	)
	// Overlapping same-key annotations: max (logical time, op ID) wins.
	checkRuns(t, d, []Run{
		{Text: "a", Attrs: Attrs{"bold": "1"}},
		{Text: "bcd", Attrs: Attrs{"bold": "2"}},
	})

	// Undo references a specific annotation ID; the loser resurfaces.
	applyAll(t, d, Undo{ID: ID{7, "amy"}, Ann: "a2"})
	checkRuns(t, d, []Run{
		{Text: "abc", Attrs: Attrs{"bold": "1"}},
		{Text: "d", Attrs: Attrs{}},
	})

	applyAll(t, d, Undo{ID: ID{8, "bob"}, Ann: "a1"})
	checkRuns(t, d, []Run{
		{Text: "abcd", Attrs: Attrs{}},
	})
}

func TestDeleteKeepsOtherCharactersAnnotations(t *testing.T) {
	a, b, c := ID{1, "amy"}, ID{2, "amy"}, ID{3, "amy"}
	d := New()
	applyAll(t, d,
		Insert{ID: a, Pred: Root, Char: 'a'},
		Insert{ID: b, Pred: a, Char: 'b'},
		Insert{ID: c, Pred: b, Char: 'c'},
		Annotate{ID: ID{4, "amy"}, Ann: "b1", Key: "bold", Val: "1",
			Left: a, LeftIncl: true, Right: c, RightIncl: true},
		Delete{ID: ID{5, "amy"}, Target: b},
	)
	// Deleting one character must not drop the annotation from the others.
	checkRuns(t, d, []Run{
		{Text: "ac", Attrs: Attrs{"bold": "1"}},
	})
}
