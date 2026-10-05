package foldindex

import (
	"errors"
	"testing"
)

func mustTable(t *testing.T, rules map[string]string) *Table {
	t.Helper()
	tbl, err := NewTable(rules)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tbl
}

// One character expands to many: "ß" folds to "ss".
func TestOneToManyExpansion(t *testing.T) {
	tbl := mustTable(t, map[string]string{"ß": "ss"})
	ix := NewIndex(tbl)
	ix.Add("doc", "aßb") // folded: "assb"; ß occupies original bytes [1,3)

	hits, next, err := ix.Search("ß", 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if next != "" {
		t.Fatalf("unexpected cursor %q", next)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1: %+v", len(hits), hits)
	}
	h := hits[0]
	if h.Start != 1 || h.End != 3 {
		t.Fatalf("hit interval = [%d,%d), want [1,3)", h.Start, h.End)
	}
	if !h.Exact {
		t.Fatalf("full-expansion query should align exactly")
	}
	if got := h.Text(ix); got != "ß" {
		t.Fatalf("hit text = %q, want %q", got, "ß")
	}
}

// A query matching only part of an expansion returns the minimal covering
// original interval, marked inexact; duplicate folded hits mapping to the
// same interval merge into one.
func TestPartialExpansionHitMergesDuplicates(t *testing.T) {
	tbl := mustTable(t, map[string]string{"ß": "ss"})
	ix := NewIndex(tbl)
	ix.Add("doc", "aßb")

	hits, _, err := ix.Search("s", 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Folded "assb" matches "s" twice (both inside the "ss" expansion);
	// both map to original [1,3) and must merge into a single hit.
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1 merged hit: %+v", len(hits), hits)
	}
	h := hits[0]
	if h.Start != 1 || h.End != 3 {
		t.Fatalf("hit interval = [%d,%d), want [1,3)", h.Start, h.End)
	}
	if h.Exact {
		t.Fatalf("partial expansion hit must be marked inexact")
	}

	// The same interval from a fully aligned query is exact.
	hits, _, err = ix.Search("ss", 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || !hits[0].Exact || hits[0].Start != 1 || hits[0].End != 3 {
		t.Fatalf("got %+v, want one exact hit [1,3)", hits)
	}
}

// Combined replacement: a multi-character sequence is replaced as a unit,
// and the longest key wins at each position.
func TestCombinedReplacement(t *testing.T) {
	tbl := mustTable(t, map[string]string{"a": "q", "ab": "xy"})
	ix := NewIndex(tbl)
	ix.Add("doc", "zabz") // folded: "zxyz" ("ab" wins over "a")

	// Longest match: "ab" folded to "xy", not "a"->"q" plus "b".
	hits, _, err := ix.Search("qb", 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("got %+v, want no hits for per-char folding", hits)
	}

	// Fully aligned combined replacement.
	hits, _, err = ix.Search("xy", 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || !hits[0].Exact || hits[0].Start != 1 || hits[0].End != 3 {
		t.Fatalf("got %+v, want one exact hit [1,3)", hits)
	}

	// Partial hit inside the combined replacement expands to cover it.
	hits, _, err = ix.Search("y", 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Exact || hits[0].Start != 1 || hits[0].End != 3 {
		t.Fatalf("got %+v, want one inexact hit [1,3)", hits)
	}
	if got := hits[0].Text(ix); got != "ab" {
		t.Fatalf("hit text = %q, want %q", got, "ab")
	}
}

// Deletion rules (empty replacement) leave no folded bytes; hits spanning
// the deleted character cover it in the original interval.
func TestDeletionRule(t *testing.T) {
	tbl := mustTable(t, map[string]string{"-": ""})
	ix := NewIndex(tbl)
	ix.Add("doc", "a-b") // folded: "ab"

	hits, _, err := ix.Search("ab", 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || !hits[0].Exact || hits[0].Start != 0 || hits[0].End != 3 {
		t.Fatalf("got %+v, want one exact hit [0,3)", hits)
	}
	if got := hits[0].Text(ix); got != "a-b" {
		t.Fatalf("hit text = %q, want %q", got, "a-b")
	}
}

// A new table version produces a new index generation; cursors from the old
// generation are rejected and cannot be mixed in.
func TestCursorPaginationAndVersioning(t *testing.T) {
	v1 := mustTable(t, map[string]string{"ß": "ss"})
	ix := NewIndex(v1)
	ix.Add("d1", "ß")
	ix.Add("d2", "ß")

	page1, cursor, err := ix.Search("ss", 1, "")
	if err != nil {
		t.Fatalf("Search page 1: %v", err)
	}
	if len(page1) != 1 || page1[0].DocID != "d1" {
		t.Fatalf("page 1 = %+v, want one hit in d1", page1)
	}
	if cursor == "" {
		t.Fatalf("expected a cursor for page 2")
	}

	page2, next, err := ix.Search("ss", 1, cursor)
	if err != nil {
		t.Fatalf("Search page 2: %v", err)
	}
	if len(page2) != 1 || page2[0].DocID != "d2" || next != "" {
		t.Fatalf("page 2 = %+v next=%q, want one hit in d2 and no cursor", page2, next)
	}

	// Table revision: same documents re-indexed under a new generation.
	v2 := mustTable(t, map[string]string{"ß": "ss", "ä": "ae"})
	if v1.Version() == v2.Version() {
		t.Fatalf("different rules must yield different table versions")
	}
	ix2 := ix.Reindex(v2)
	if ix2.Generation() != ix.Generation()+1 {
		t.Fatalf("generation = %d, want %d", ix2.Generation(), ix.Generation()+1)
	}

	if _, _, err := ix2.Search("ss", 1, cursor); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("old cursor on new index: err = %v, want ErrStaleCursor", err)
	}

	// Cursors issued by the new index are likewise rejected by the old one.
	_, cursor2, err := ix2.Search("ss", 1, "")
	if err != nil {
		t.Fatalf("Search on new index: %v", err)
	}
	if _, _, err := ix.Search("ss", 1, cursor2); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("new cursor on old index: err = %v, want ErrStaleCursor", err)
	}

	// Malformed cursors are rejected as malformed, not as stale.
	if _, _, err := ix.Search("ss", 1, "garbage"); err == nil || errors.Is(err, ErrStaleCursor) {
		t.Fatalf("malformed cursor: err = %v, want a decode error", err)
	}
}

func TestEmptyQueryAfterFolding(t *testing.T) {
	tbl := mustTable(t, map[string]string{"-": ""})
	ix := NewIndex(tbl)
	ix.Add("doc", "a-b")

	for _, q := range []string{"", "-", "---"} {
		if _, _, err := ix.Search(q, 0, ""); !errors.Is(err, ErrEmptyQuery) {
			t.Fatalf("query %q: err = %v, want ErrEmptyQuery", q, err)
		}
	}
}

func TestOverlappingHitsAcrossDocuments(t *testing.T) {
	tbl := mustTable(t, map[string]string{"ß": "ss"})
	ix := NewIndex(tbl)
	ix.Add("b", "ßß")  // folded "ssss": query "ss" hits at 0,1,2 -> intervals [0,2),[0,4),[2,4)
	ix.Add("a", "xßx") // folded "xssx"

	hits, _, err := ix.Search("ss", 0, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 4 {
		t.Fatalf("got %d hits, want 4: %+v", len(hits), hits)
	}
	// Documents are visited in sorted ID order.
	if hits[0].DocID != "a" || hits[0].Start != 1 || hits[0].End != 3 || !hits[0].Exact {
		t.Fatalf("hit[0] = %+v, want exact [1,3) in doc a", hits[0])
	}
	// In doc b: middle folded hit straddles two expansions and is inexact.
	var straddle *Hit
	for i := range hits {
		if hits[i].DocID == "b" && hits[i].Start == 0 && hits[i].End == 4 {
			straddle = &hits[i]
		}
	}
	if straddle == nil {
		t.Fatalf("missing straddling hit [0,4) in doc b: %+v", hits)
	}
	if straddle.Exact {
		t.Fatalf("hit straddling two expansions must be inexact")
	}
}
