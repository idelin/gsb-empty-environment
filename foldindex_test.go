package foldindex_test

import (
	"testing"

	"foldindex"
)

func mustTable(t *testing.T, version string, rules []foldindex.Rule) *foldindex.Table {
	t.Helper()
	tb, err := foldindex.NewTable(version, rules)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tb
}

func assertHits(t *testing.T, got []foldindex.Hit, want []foldindex.Hit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d hits %+v, want %d hits %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("hit %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// One character ("ß") expands to many ("ss"); hits on the expansion map
// back to the single original character.
func TestOneCharExpandsToMany(t *testing.T) {
	tb := mustTable(t, "v1", []foldindex.Rule{{From: "ß", To: "ss"}})
	ix := foldindex.Build(tb, "straße") // runes: s t r a ß e

	if got, want := ix.FoldedText(), "strasse"; got != want {
		t.Fatalf("folded text: got %q, want %q", got, want)
	}

	assertHits(t, ix.Search("strasse"), []foldindex.Hit{{Start: 0, End: 6, Exact: true}})
	assertHits(t, ix.Search("ß"), []foldindex.Hit{{Start: 4, End: 5, Exact: true}})
	assertHits(t, ix.Search("ss"), []foldindex.Hit{{Start: 4, End: 5, Exact: true}})
}

// A query that cuts through the middle of an expansion returns the
// minimal covering original interval and is marked inexact.
func TestPartialExpansionHit(t *testing.T) {
	tb := mustTable(t, "v1", []foldindex.Rule{{From: "ß", To: "ss"}})
	ix := foldindex.Build(tb, "straße")

	// "as" matches folded [3,5): aligned at the 'a' boundary but ends in
	// the middle of the ß -> "ss" expansion, so it is inexact.
	assertHits(t, ix.Search("as"), []foldindex.Hit{{Start: 3, End: 5, Exact: false}})

	// "se" starts mid-expansion but ends on a unit boundary: inexact.
	assertHits(t, ix.Search("se"), []foldindex.Hit{{Start: 4, End: 6, Exact: false}})
}

// Repeated hits on one expansion collapse into a single merged hit.
func TestDuplicateHitsMerged(t *testing.T) {
	tb := mustTable(t, "v1", []foldindex.Rule{{From: "ß", To: "ss"}})
	ix := foldindex.Build(tb, "straße")

	// "s" hits folded offsets 0, 4 and 5. Offsets 4 and 5 both lie inside
	// the ß expansion and map to the same original interval [4,5), which
	// must be merged into one inexact hit.
	assertHits(t, ix.Search("s"), []foldindex.Hit{
		{Start: 0, End: 1, Exact: true},
		{Start: 4, End: 5, Exact: false},
	})
}

// Combined replacements: many chars collapse to one ("..." -> "…") and a
// ligature expands ("ﬁ" -> "fi").
func TestCombinedReplacement(t *testing.T) {
	tb := mustTable(t, "v1", []foldindex.Rule{
		{From: "...", To: "…"},
		{From: "ﬁ", To: "fi"},
	})
	ix := foldindex.Build(tb, "hmm... ﬁsh") // runes: h m m . . . sp ﬁ s h

	if got, want := ix.FoldedText(), "hmm… fish"; got != want {
		t.Fatalf("folded text: got %q, want %q", got, want)
	}

	// Query "..." folds to "…" and maps back to the three original dots.
	assertHits(t, ix.Search("..."), []foldindex.Hit{{Start: 3, End: 6, Exact: true}})
	assertHits(t, ix.Search("…"), []foldindex.Hit{{Start: 3, End: 6, Exact: true}})

	// Ligature expansion, reachable via either form.
	assertHits(t, ix.Search("fi"), []foldindex.Hit{{Start: 7, End: 8, Exact: true}})
	assertHits(t, ix.Search("ﬁ"), []foldindex.Hit{{Start: 7, End: 8, Exact: true}})
	assertHits(t, ix.Search("fish"), []foldindex.Hit{{Start: 7, End: 10, Exact: true}})

	// "i" lands in the middle of the ﬁ -> "fi" expansion: inexact.
	assertHits(t, ix.Search("i"), []foldindex.Hit{{Start: 7, End: 8, Exact: false}})
}

// Longest rule wins when rules share a prefix.
func TestLongestMatchWins(t *testing.T) {
	tb := mustTable(t, "v1", []foldindex.Rule{
		{From: "oe", To: "ö"},
		{From: "o", To: "0"},
	})
	ix := foldindex.Build(tb, "o oe")
	if got, want := ix.FoldedText(), "0 ö"; got != want {
		t.Fatalf("folded text: got %q, want %q", got, want)
	}
	assertHits(t, ix.Search("ö"), []foldindex.Hit{{Start: 2, End: 4, Exact: true}})
}

// Pagination works within one index, and cursors issued by an index
// built with another table version are rejected.
func TestCursorPaginationAndVersioning(t *testing.T) {
	rules := []foldindex.Rule{{From: "ß", To: "ss"}}
	ixV1 := foldindex.Build(mustTable(t, "v1", rules), "aßaßa")

	p1, err := ixV1.SearchPage("ss", 1, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	assertHits(t, p1.Hits, []foldindex.Hit{{Start: 1, End: 2, Exact: true}})
	if p1.NextCursor == "" {
		t.Fatal("page 1: expected a next cursor")
	}

	p2, err := ixV1.SearchPage("ss", 1, p1.NextCursor)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	assertHits(t, p2.Hits, []foldindex.Hit{{Start: 3, End: 4, Exact: true}})
	if p2.NextCursor != "" {
		t.Fatalf("page 2: expected no next cursor, got %q", p2.NextCursor)
	}

	// Table re-versioned -> new index; the old cursor must not be accepted.
	ixV2 := foldindex.Build(mustTable(t, "v2", rules), "aßaßa")
	if _, err := ixV2.SearchPage("ss", 1, p1.NextCursor); err != foldindex.ErrStaleCursor {
		t.Fatalf("stale cursor: got %v, want ErrStaleCursor", err)
	}

	// A cursor from a different document is rejected as well.
	ixOther := foldindex.Build(mustTable(t, "v1", rules), "aßa")
	if _, err := ixOther.SearchPage("ss", 1, p1.NextCursor); err != foldindex.ErrStaleCursor {
		t.Fatalf("foreign cursor: got %v, want ErrStaleCursor", err)
	}
}

func TestInvalidCursor(t *testing.T) {
	ix := foldindex.Build(mustTable(t, "v1", nil), "abc")
	for _, tok := range []string{"not-base64!!", "ZngxfGE=" /* missing fields */} {
		if _, err := ix.SearchPage("a", 1, tok); err != foldindex.ErrInvalidCursor {
			t.Fatalf("cursor %q: got %v, want ErrInvalidCursor", tok, err)
		}
	}
}

func TestTableValidation(t *testing.T) {
	if _, err := foldindex.NewTable("", nil); err == nil {
		t.Fatal("empty version: expected error")
	}
	if _, err := foldindex.NewTable("a|b", nil); err == nil {
		t.Fatal("version with '|': expected error")
	}
	if _, err := foldindex.NewTable("v1", []foldindex.Rule{{From: "", To: "x"}}); err == nil {
		t.Fatal("empty From: expected error")
	}
	dup := []foldindex.Rule{{From: "a", To: "x"}, {From: "a", To: "y"}}
	if _, err := foldindex.NewTable("v1", dup); err == nil {
		t.Fatal("duplicate From: expected error")
	}
}

func TestEmptyAndMissingQueries(t *testing.T) {
	ix := foldindex.Build(mustTable(t, "v1", []foldindex.Rule{{From: "ß", To: "ss"}}), "straße")
	if hits := ix.Search(""); hits != nil {
		t.Fatalf("empty query: got %+v", hits)
	}
	if hits := ix.Search("xyz"); hits != nil {
		t.Fatalf("missing query: got %+v", hits)
	}
	if hits := ix.Search("a longer query than the document itself"); hits != nil {
		t.Fatalf("overlong query: got %+v", hits)
	}
}
