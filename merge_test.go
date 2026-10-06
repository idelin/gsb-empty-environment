package patchmerge

import (
	"reflect"
	"testing"
)

func ins(pos int, text string) Edit { return Edit{Kind: Insert, Pos: pos, End: pos, Text: text} }
func del(pos, end int) Edit         { return Edit{Kind: Delete, Pos: pos, End: end} }

func TestIdenticalInsertsAreDeduplicated(t *testing.T) {
	a := PatchSet{ID: "a", Edits: []Edit{ins(5, "x")}}
	b := PatchSet{ID: "b", Edits: []Edit{ins(5, "x")}}
	res := Merge(a, b)
	if len(res.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", res.Conflicts)
	}
	if len(res.Merged) != 1 {
		t.Fatalf("want 1 merged edit, got %d: %+v", len(res.Merged), res.Merged)
	}
	m := res.Merged[0]
	if m.Kind != Insert || m.Pos != 5 || m.Text != "x" {
		t.Fatalf("bad merged edit: %+v", m)
	}
	if !reflect.DeepEqual(m.Sources, []string{"a", "b"}) {
		t.Fatalf("want sources [a b], got %v", m.Sources)
	}
}

func TestSamePointDifferentInsertsOrderedBySourceID(t *testing.T) {
	a := PatchSet{ID: "a", Edits: []Edit{ins(3, "from-a")}}
	b := PatchSet{ID: "b", Edits: []Edit{ins(3, "from-b")}}
	// Feed sets in reverse order too; output order must follow source IDs.
	for _, sets := range [][2]PatchSet{{a, b}, {b, a}} {
		res := Merge(sets[0], sets[1])
		if len(res.Merged) != 2 {
			t.Fatalf("want 2 merged edits, got %+v", res.Merged)
		}
		if res.Merged[0].Text != "from-a" || res.Merged[1].Text != "from-b" {
			t.Fatalf("inserts not ordered by source id: %+v", res.Merged)
		}
	}
}

func TestDeleteConflictsWithStrictlyInternalInsert(t *testing.T) {
	a := PatchSet{ID: "a", Edits: []Edit{del(2, 8)}}
	b := PatchSet{ID: "b", Edits: []Edit{ins(5, "x"), ins(20, "ok")}}
	res := Merge(a, b)
	if len(res.Conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %+v", res.Conflicts)
	}
	c := res.Conflicts[0]
	if c.Start != 2 || c.End != 8 {
		t.Fatalf("conflict range = [%d,%d), want [2,8)", c.Start, c.End)
	}
	if c.Deletion.Sources[0] != "a" || c.Insertion.Sources[0] != "b" {
		t.Fatalf("bad conflict parties: %+v", c)
	}
	// Conflicting delete and insert excluded; the unrelated insert survives.
	if len(res.Merged) != 1 || res.Merged[0].Kind != Insert || res.Merged[0].Pos != 20 {
		t.Fatalf("bad mergeable part: %+v", res.Merged)
	}
}

func TestEndpointInsertsDoNotConflict(t *testing.T) {
	a := PatchSet{ID: "a", Edits: []Edit{del(2, 8)}}
	b := PatchSet{ID: "b", Edits: []Edit{ins(2, "L"), ins(8, "R")}}
	res := Merge(a, b)
	if len(res.Conflicts) != 0 {
		t.Fatalf("endpoint inserts must not conflict: %+v", res.Conflicts)
	}
	if len(res.Merged) != 3 {
		t.Fatalf("want 3 merged edits, got %+v", res.Merged)
	}
}

func TestOverlappingDeletesUnion(t *testing.T) {
	a := PatchSet{ID: "a", Edits: []Edit{del(2, 6)}}
	b := PatchSet{ID: "b", Edits: []Edit{del(4, 9)}}
	res := Merge(a, b)
	if len(res.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", res.Conflicts)
	}
	if len(res.Merged) != 1 {
		t.Fatalf("want 1 unioned delete, got %+v", res.Merged)
	}
	m := res.Merged[0]
	if m.Kind != Delete || m.Pos != 2 || m.End != 9 {
		t.Fatalf("bad union: %+v", m)
	}
	if !reflect.DeepEqual(m.Sources, []string{"a", "b"}) {
		t.Fatalf("want sources [a b], got %v", m.Sources)
	}
}

func TestIdenticalDeletesDeduplicated(t *testing.T) {
	a := PatchSet{ID: "a", Edits: []Edit{del(1, 4)}}
	b := PatchSet{ID: "b", Edits: []Edit{del(1, 4)}}
	res := Merge(a, b)
	if len(res.Merged) != 1 || len(res.Conflicts) != 0 {
		t.Fatalf("bad result: %+v", res)
	}
	if !reflect.DeepEqual(res.Merged[0].Sources, []string{"a", "b"}) {
		t.Fatalf("want sources [a b], got %v", res.Merged[0].Sources)
	}
}

func TestDeleteDoesNotConflictWithOwnInsert(t *testing.T) {
	// Same-source delete+insert pairs model replacements; they must merge.
	a := PatchSet{ID: "a", Edits: []Edit{del(2, 5), ins(3, "x")}}
	b := PatchSet{ID: "b", Edits: []Edit{ins(10, "y")}}
	res := Merge(a, b)
	if len(res.Conflicts) != 0 {
		t.Fatalf("same-source delete/insert must not conflict: %+v", res.Conflicts)
	}
	if len(res.Merged) != 3 {
		t.Fatalf("want 3 merged edits, got %+v", res.Merged)
	}
}
