package hexgrid

import (
	"strings"
	"testing"
)

// buildDiskStore builds a store from a k-ring disk with internally
// consistent parent/neighbor references.
func buildDiskStore(t *testing.T, center Cell, k int) *Store {
	t.Helper()
	cells, err := KRing(center, k)
	if err != nil {
		t.Fatal(err)
	}
	set := make(map[Cell]bool, len(cells))
	for _, c := range cells {
		set[c] = true
	}
	s := NewStore()
	for _, c := range cells {
		var parent Cell
		if p, err := c.Parent(); err == nil && set[p] {
			parent = p
		}
		var nbs []Cell
		all, err := Neighbors(c)
		if err != nil {
			t.Fatal(err)
		}
		for _, nb := range all {
			if set[nb] {
				nbs = append(nbs, nb)
			}
		}
		s.Upsert(Record{Cell: c, Parent: parent, Neighbors: nbs, Payload: 1})
	}
	return s
}

// buildGlobalStore builds a balanced store: 7 records around every
// base-cell center (7 records per base cell bucket, no adjacency
// declarations, so the skew check is exercised in isolation).
func buildGlobalStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	for b := 0; b < NumBaseCells; b++ {
		ll := FaceCenter(b)
		center, err := LatLngToCell(ll.Lat, ll.Lng, 8)
		if err != nil {
			t.Fatal(err)
		}
		disk, err := KRing(center, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range disk {
			s.Upsert(Record{Cell: c, Payload: 1})
		}
	}
	return s
}

func violationsOfKind(rep *Report, kind ViolationKind) []Violation {
	var out []Violation
	for _, v := range rep.Violations {
		if v.Kind == kind {
			out = append(out, v)
		}
	}
	return out
}

func TestInspectorCleanStore(t *testing.T) {
	center, err := LatLngToCell(39.9042, 116.4074, 8)
	if err != nil {
		t.Fatal(err)
	}
	s := buildDiskStore(t, center, 2)
	rep := Inspector{SkewThreshold: 0}.Inspect(s) // skew disabled: single region
	if !rep.OK() {
		t.Fatalf("clean store flagged:\n%s", rep.Summary())
	}
	if rep.Records != 19 || rep.Components != 1 || rep.MainComponent != 19 {
		t.Fatalf("unexpected stats: %s", rep.Summary())
	}
}

func TestInspectorBalancedGlobalStore(t *testing.T) {
	s := buildGlobalStore(t)
	rep := DefaultInspector().Inspect(s)
	if !rep.OK() {
		t.Fatalf("balanced global store flagged:\n%s", rep.Summary())
	}
	if rep.SkewRatio != 1.0 {
		t.Fatalf("skew ratio %v, want 1.0", rep.SkewRatio)
	}
}

func TestInspectorEmptyStore(t *testing.T) {
	rep := DefaultInspector().Inspect(NewStore())
	if !rep.OK() || rep.Records != 0 || rep.Components != 0 {
		t.Fatalf("empty store: %s", rep.Summary())
	}
}

func TestInspectorDanglingParent(t *testing.T) {
	s := buildDiskStore(t, beijingCell(t), 1)
	ghost := MustCell(2, 7, []uint8{3, 3, 3, 3, 3, 3, 3})
	s.Upsert(Record{Cell: MustCell(9, 8, []uint8{1, 1, 1, 1, 1, 1, 1, 1}), Parent: ghost})
	rep := Inspector{SkewThreshold: 0}.Inspect(s)
	dangling := violationsOfKind(rep, ViolationDanglingRef)
	if len(dangling) != 1 {
		t.Fatalf("%d dangling violations, want 1:\n%s", len(dangling), rep.Summary())
	}
	if !strings.Contains(dangling[0].Detail, ghost.String()) {
		t.Fatalf("violation does not name the ghost parent: %v", dangling[0])
	}
}

func TestInspectorDanglingNeighbor(t *testing.T) {
	s := buildDiskStore(t, beijingCell(t), 1)
	ghost := MustCell(4, 8, []uint8{6, 6, 6, 6, 6, 6, 6, 6})
	center := MustCell(9, 8, []uint8{2, 2, 2, 2, 2, 2, 2, 2})
	s.Upsert(Record{Cell: center, Neighbors: []Cell{ghost}})
	rep := Inspector{SkewThreshold: 0}.Inspect(s)
	dangling := violationsOfKind(rep, ViolationDanglingRef)
	if len(dangling) != 1 {
		t.Fatalf("%d dangling violations, want 1:\n%s", len(dangling), rep.Summary())
	}
}

func TestInspectorIsolatedLoop(t *testing.T) {
	s := buildDiskStore(t, beijingCell(t), 2) // main component: 19 cells
	// Plant a closed triangle far away (base cell 10, equator).
	far, err := LatLngToCell(0, 20.9052, 8)
	if err != nil {
		t.Fatal(err)
	}
	nbs, err := Neighbors(far)
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := far, nbs[0], nbs[1]
	s.Upsert(Record{Cell: a, Neighbors: []Cell{b, c}})
	s.Upsert(Record{Cell: b, Neighbors: []Cell{a, c}})
	s.Upsert(Record{Cell: c, Neighbors: []Cell{a, b}})
	rep := Inspector{SkewThreshold: 0}.Inspect(s)
	loops := violationsOfKind(rep, ViolationIsolatedLoop)
	if len(loops) != 1 {
		t.Fatalf("%d isolated-loop violations, want 1:\n%s", len(loops), rep.Summary())
	}
	if !strings.Contains(loops[0].Detail, "closed loop") {
		t.Fatalf("component not classified as closed loop: %v", loops[0])
	}
	if rep.Components != 2 || rep.MainComponent != 19 {
		t.Fatalf("components=%d main=%d, want 2/19", rep.Components, rep.MainComponent)
	}
}

func TestInspectorIsolatedFragment(t *testing.T) {
	s := buildDiskStore(t, beijingCell(t), 2)
	// Plant a detached 2-cell fragment (not a loop).
	far, err := LatLngToCell(0, -20.9052, 8)
	if err != nil {
		t.Fatal(err)
	}
	nbs, err := Neighbors(far)
	if err != nil {
		t.Fatal(err)
	}
	s.Upsert(Record{Cell: far, Neighbors: []Cell{nbs[0]}})
	s.Upsert(Record{Cell: nbs[0], Neighbors: []Cell{far}})
	rep := Inspector{SkewThreshold: 0}.Inspect(s)
	loops := violationsOfKind(rep, ViolationIsolatedLoop)
	if len(loops) != 1 {
		t.Fatalf("%d violations, want 1:\n%s", len(loops), rep.Summary())
	}
	if !strings.Contains(loops[0].Detail, "fragment") {
		t.Fatalf("component not classified as fragment: %v", loops[0])
	}
}

func TestInspectorDataSkew(t *testing.T) {
	s := buildGlobalStore(t) // balanced: 7 records per base cell
	// Flood base cell of Beijing with 127 extra records.
	flood, err := KRing(beijingCell(t), 6)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range flood {
		s.Upsert(Record{Cell: c})
	}
	rep := DefaultInspector().Inspect(s)
	skew := violationsOfKind(rep, ViolationDataSkew)
	if len(skew) != 1 {
		t.Fatalf("%d skew violations, want 1:\n%s", len(skew), rep.Summary())
	}
	if rep.SkewRatio <= 3.0 {
		t.Fatalf("skew ratio %.2f, want > 3", rep.SkewRatio)
	}
}

func TestInspectorInvariantBreach(t *testing.T) {
	s := buildDiskStore(t, beijingCell(t), 1)
	cells := s.Snapshot()
	victim := cells[3]

	// Wrong parent: present in store but not the hierarchical parent.
	wrongParent := cells[4].Cell
	s.Upsert(Record{Cell: victim.Cell, Parent: wrongParent})
	// Invalid cell key.
	s.Upsert(Record{Cell: Cell(0xdead)})
	// Invalid neighbor reference.
	s.Upsert(Record{Cell: MustCell(11, 8, []uint8{5, 5, 5, 5, 5, 5, 5, 5}), Neighbors: []Cell{Cell(0)}})

	rep := Inspector{SkewThreshold: 0}.Inspect(s)
	breaches := violationsOfKind(rep, ViolationInvariant)
	if len(breaches) != 3 {
		t.Fatalf("%d invariant breaches, want 3:\n%s", len(breaches), rep.Summary())
	}
}

func TestInspectorReportDeterministic(t *testing.T) {
	s := buildDiskStore(t, beijingCell(t), 1)
	ghost := MustCell(2, 7, []uint8{3, 3, 3, 3, 3, 3, 3})
	s.Upsert(Record{Cell: MustCell(9, 8, []uint8{1, 1, 1, 1, 1, 1, 1, 1}), Parent: ghost})
	r1 := Inspector{SkewThreshold: 0}.Inspect(s).Summary()
	r2 := Inspector{SkewThreshold: 0}.Inspect(s).Summary()
	if r1 != r2 {
		t.Fatalf("non-deterministic report:\n%s\nvs\n%s", r1, r2)
	}
}
