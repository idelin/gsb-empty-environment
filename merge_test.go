package annmerge

import (
	"reflect"
	"testing"
)

func ins(id, parent string, ch rune) Op {
	return Op{ID: id, Kind: KindInsert, Parent: parent, Char: ch}
}

func del(id, target string) Op {
	return Op{ID: id, Kind: KindDelete, Target: target}
}

func ann(opID, annID, left string, leftIncl bool, right string, rightIncl bool, time uint64, attrs map[string]string) Op {
	return Op{
		ID: opID, Kind: KindAnnotate, AnnID: annID,
		Left: left, LeftIncl: leftIncl, Right: right, RightIncl: rightIncl,
		Time: time, Attrs: attrs,
	}
}

func undo(opID, annID string) Op {
	return Op{ID: opID, Kind: KindUndo, Undo: annID}
}

func mustEngine(t *testing.T, ops []Op) *Engine {
	t.Helper()
	e := New()
	if err := e.ApplyAll(ops); err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	return e
}

// permutations enumerates all n! index orders (Heap's algorithm).
func permutations(n int) [][]int {
	var res [][]int
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var heap func(k int)
	heap = func(k int) {
		if k <= 1 {
			res = append(res, append([]int(nil), perm...))
			return
		}
		for i := 0; i < k; i++ {
			heap(k - 1)
			if k%2 == 0 {
				perm[i], perm[k-1] = perm[k-1], perm[i]
			} else {
				perm[0], perm[k-1] = perm[k-1], perm[0]
			}
		}
	}
	heap(n)
	return res
}

// TestDeliveryPermutations feeds the same operation set in every possible
// delivery order and requires the final text and per-character attributes
// to be identical, for both the engine and the reference evaluator.
func TestDeliveryPermutations(t *testing.T) {
	ops := []Op{
		ins("a1", RootID, 'h'), // site A: 'h'
		ins("b1", RootID, 'i'), // site B: concurrent, same predecessor
		ins("a2", "a1", '!'),   // site A: '!' after 'h'
		del("b2", "b1"),        // site B deletes its own char (tombstone)
		ann("a3", "annA", "a1", true, "a2", true, 1, map[string]string{"weight": "bold"}),
		ann("b3", "annB", RootID, false, EndID, false, 2, map[string]string{"style": "italic"}),
		ann("a4", "annC", "a1", true, "a2", true, 1, map[string]string{"lang": "en"}),
		undo("b4", "annC"), // retracts annC; may arrive before annC itself
	}

	wantText := "h!" // 'i' deleted; "b1" > "a1" so the tombstone sits before 'h'
	want := []CharAttr{
		{ID: "a1", Char: 'h', Attrs: map[string]string{"weight": "bold", "style": "italic"}},
		{ID: "a2", Char: '!', Attrs: map[string]string{"weight": "bold", "style": "italic"}},
	}

	perms := permutations(len(ops))
	if len(perms) != 40320 {
		t.Fatalf("expected 40320 permutations, got %d", len(perms))
	}
	for _, perm := range perms {
		ordered := make([]Op, 0, len(ops))
		for _, idx := range perm {
			ordered = append(ordered, ops[idx])
		}

		e := New()
		if err := e.ApplyAll(ordered); err != nil {
			t.Fatalf("perm %v: %v", perm, err)
		}
		if got := e.Text(); got != wantText {
			t.Fatalf("perm %v: text = %q, want %q", perm, got, wantText)
		}
		if got := e.CharAttrs(); !reflect.DeepEqual(got, want) {
			t.Fatalf("perm %v: engine attrs = %+v, want %+v", perm, got, want)
		}

		ref, err := ReferenceEval(ordered)
		if err != nil {
			t.Fatalf("perm %v: reference: %v", perm, err)
		}
		if !reflect.DeepEqual(ref, want) {
			t.Fatalf("perm %v: reference attrs = %+v, want %+v", perm, ref, want)
		}
	}
}

// TestLeftEndInsert: a character inserted concurrently at the very left
// edge inherits the annotation iff it lands inside the anchor span in the
// final sequence -- regardless of when its message arrived.
func TestLeftEndInsert(t *testing.T) {
	base := []Op{
		ins("n1", RootID, 'b'),
		ins("n2", "n1", 'c'),
		ann("n3", "ann1", RootID, false, "n2", true, 1, map[string]string{"weight": "bold"}),
	}
	// Late inserts at the left edge, delivered after the annotation.
	// "z9" sorts before "n1" (inside the span), "a0" sorts after the
	// whole subtree of "n1" (outside, past the inclusive right anchor).
	e := mustEngine(t, append(base,
		ins("z9", RootID, 'a'),
		ins("a0", RootID, 'x'),
	))
	want := []Run{
		{Text: "abc", Attrs: map[string]string{"weight": "bold"}},
		{Text: "x", Attrs: map[string]string{}},
	}
	if got := e.Runs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
}

// TestRightEndInsert: an annotation anchored at EndID covers characters
// appended later at the right edge; one anchored at the last character
// exclusively does not.
func TestRightEndInsert(t *testing.T) {
	e := mustEngine(t, []Op{
		ins("n1", RootID, 'a'),
		ins("n2", "n1", 'b'),
		ann("n3", "annOpen", RootID, false, EndID, false, 1, map[string]string{"style": "italic"}),
		ann("n4", "annClosed", RootID, false, "n2", false, 1, map[string]string{"lang": "en"}),
		ins("n5", "n2", 'c'), // appended after the annotations existed
	})
	want := []Run{
		{Text: "a", Attrs: map[string]string{"style": "italic", "lang": "en"}},
		{Text: "bc", Attrs: map[string]string{"style": "italic"}},
	}
	if got := e.Runs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
}

// TestBothAnchorsDeleted: deleting both endpoint characters keeps them as
// tombstone anchors; the span still covers what is between them, and a
// character inserted off a tombstone anchor lands inside the span.
func TestBothAnchorsDeleted(t *testing.T) {
	e := mustEngine(t, []Op{
		ins("n1", RootID, 'a'),
		ins("n2", "n1", 'b'),
		ins("n3", "n2", 'c'),
		ann("n4", "ann1", "n1", true, "n3", true, 1, map[string]string{"weight": "bold"}),
		del("n5", "n1"),      // delete left anchor
		del("n6", "n3"),      // delete right anchor
		ins("n7", "n1", 'x'), // child of the tombstone: sits between the anchors
	})
	if got := e.Text(); got != "xb" {
		t.Fatalf("text = %q, want %q", got, "xb")
	}
	want := []Run{{Text: "xb", Attrs: map[string]string{"weight": "bold"}}}
	if got := e.Runs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
}

// TestEmptyInterval: adjacent anchors with exclusive endpoints, and a
// single anchor with mismatched inclusivity, cover nothing; the same
// anchor inclusive on both ends covers exactly that character.
func TestEmptyInterval(t *testing.T) {
	e := mustEngine(t, []Op{
		ins("n1", RootID, 'a'),
		ins("n2", "n1", 'b'),
		ann("n3", "annAdj", "n1", false, "n2", false, 1, map[string]string{"k": "adjacent-exclusive"}),
		ann("n4", "annHalf", "n1", true, "n1", false, 1, map[string]string{"k": "half-open"}),
		ann("n5", "annPoint", "n1", true, "n1", true, 1, map[string]string{"mark": "point"}),
	})
	want := []Run{
		{Text: "a", Attrs: map[string]string{"mark": "point"}},
		{Text: "b", Attrs: map[string]string{}},
	}
	if got := e.Runs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
}

// TestOverlapAndUndo: for the same attribute key the annotation with the
// greatest (logicalTime, operationID) wins; undoing that annotation by ID
// revives the older one. Annotations of other characters are untouched.
func TestOverlapAndUndo(t *testing.T) {
	ops := []Op{
		ins("n1", RootID, 'a'),
		ins("n2", "n1", 'b'),
		ann("n3", "annOld", "n1", true, "n2", true, 1, map[string]string{"weight": "bold"}),
		ann("n4", "annNew", "n1", true, "n1", true, 2, map[string]string{"weight": "light"}),
	}
	e := mustEngine(t, ops)
	want := []Run{
		{Text: "a", Attrs: map[string]string{"weight": "light"}}, // annNew wins on 'a'
		{Text: "b", Attrs: map[string]string{"weight": "bold"}},
	}
	if got := e.Runs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("before undo: runs = %+v, want %+v", got, want)
	}

	if !e.Apply(undo("n5", "annNew")) {
		t.Fatal("undo op was not applied")
	}
	want = []Run{{Text: "ab", Attrs: map[string]string{"weight": "bold"}}}
	if got := e.Runs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after undo: runs = %+v, want %+v", got, want)
	}
}

// TestSameTieBreakByOpID: equal logicalTime falls back to operation ID.
func TestSameTimeTieBreakByOpID(t *testing.T) {
	e := mustEngine(t, []Op{
		ins("n1", RootID, 'a'),
		ann("z2", "annZ", "n1", true, "n1", true, 5, map[string]string{"weight": "bold"}),
		ann("z9", "annZ2", "n1", true, "n1", true, 5, map[string]string{"weight": "black"}),
	})
	want := []Run{{Text: "a", Attrs: map[string]string{"weight": "black"}}}
	if got := e.Runs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs = %+v, want %+v", got, want)
	}
}

// TestEngineMatchesReference cross-checks the engine against the naive
// per-character reference evaluator on the scenario corpus above.
func TestEngineMatchesReference(t *testing.T) {
	scenarios := [][]Op{
		{
			ins("n1", RootID, 'b'), ins("n2", "n1", 'c'),
			ann("n3", "a1", RootID, false, "n2", true, 1, map[string]string{"weight": "bold"}),
			ins("z9", RootID, 'a'), ins("a0", RootID, 'x'),
		},
		{
			ins("n1", RootID, 'a'), ins("n2", "n1", 'b'),
			ann("n3", "a1", RootID, false, EndID, false, 1, map[string]string{"style": "italic"}),
			ann("n4", "a2", RootID, false, "n2", false, 1, map[string]string{"lang": "en"}),
			ins("n5", "n2", 'c'),
		},
		{
			ins("n1", RootID, 'a'), ins("n2", "n1", 'b'), ins("n3", "n2", 'c'),
			ann("n4", "a1", "n1", true, "n3", true, 1, map[string]string{"weight": "bold"}),
			del("n5", "n1"), del("n6", "n3"), ins("n7", "n1", 'x'),
		},
		{
			ins("n1", RootID, 'a'), ins("n2", "n1", 'b'),
			ann("n3", "a1", "n1", false, "n2", false, 1, map[string]string{"k": "v"}),
			ann("n4", "a2", "n1", true, "n1", true, 1, map[string]string{"mark": "point"}),
		},
		{
			ins("n1", RootID, 'a'), ins("n2", "n1", 'b'),
			ann("n3", "a1", "n1", true, "n2", true, 1, map[string]string{"weight": "bold"}),
			ann("n4", "a2", "n1", true, "n1", true, 2, map[string]string{"weight": "light"}),
			undo("n5", "a2"),
		},
	}
	for i, ops := range scenarios {
		e := mustEngine(t, ops)
		ref, err := ReferenceEval(ops)
		if err != nil {
			t.Fatalf("scenario %d: %v", i, err)
		}
		if got := e.CharAttrs(); !reflect.DeepEqual(got, ref) {
			t.Fatalf("scenario %d: engine = %+v, reference = %+v", i, got, ref)
		}
	}
}
