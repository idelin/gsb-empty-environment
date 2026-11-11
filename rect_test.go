package netsec

import "testing"

func TestSubtractManyDisjointAndExhaustive(t *testing.T) {
	u, err := NewUniverse(
		[]string{"p1", "p2", "p3"},
		[]string{"a1", "a2", "a3"},
		[]string{"r1", "r2"},
	)
	if err != nil {
		t.Fatal(err)
	}

	cuts := []rect{
		mustRect(t, u, [][]string{{"p1"}, {"a1", "a2"}, {"r1", "r2"}}),
		mustRect(t, u, [][]string{{"p1", "p2"}, {"a3"}, {"r1"}}),
		mustRect(t, u, [][]string{{"p3"}, {"a1", "a2", "a3"}, {"r2"}}),
	}

	base := fullRect(u)
	pieces := subtractMany(base, cuts)

	// Pieces are pairwise disjoint.
	for i := range pieces {
		for j := i + 1; j < len(pieces); j++ {
			if !intersect(pieces[i], pieces[j]).empty() {
				t.Fatalf("pieces %d and %d overlap", i, j)
			}
		}
	}

	// Pieces plus cuts partition the universe exactly once.
	covered := 0
	u.enumerate(func(p Point) {
		idx, err := u.indexPoint(p)
		if err != nil {
			t.Fatal(err)
		}
		inPiece := 0
		for _, piece := range pieces {
			if piece.containsPoint(idx) {
				inPiece++
			}
		}
		inCut := 0
		for _, cut := range cuts {
			if cut.containsPoint(idx) {
				inCut++
			}
		}
		if inPiece > 1 {
			t.Fatalf("point %v in %d pieces", p, inPiece)
		}
		if inPiece == 1 {
			covered++
			if inCut != 0 {
				t.Fatalf("point %v in both piece and cut", p)
			}
		} else if inCut == 0 {
			t.Fatalf("point %v in neither piece nor cut", p)
		}
	})

	cutVolume := 0
	for _, cut := range cuts {
		cutVolume += cut.volume()
	}
	if covered+cutVolume != u.Size() {
		t.Fatalf("partition size %d+%d != universe %d", covered, cutVolume, u.Size())
	}
}

func TestMergeRectsNoOverlap(t *testing.T) {
	u, err := NewUniverse(
		[]string{"p1", "p2", "p3", "p4"},
		[]string{"a1", "a2", "a3"},
		[]string{"r1", "r2"},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Singleton cells on a checkerboard-like subset; merging must never
	// introduce overlap or lose volume.
	var cells []rect
	for _, p := range []string{"p1", "p2", "p3"} {
		for _, a := range []string{"a1", "a2"} {
			cells = append(cells, mustRect(t, u, [][]string{{p}, {a}, {"r1", "r2"}}))
		}
	}
	originalVolume := 0
	for _, c := range cells {
		originalVolume += c.volume()
	}

	merged := mergeRects(cells, func(rect) string { return "k" })
	mergedVolume := 0
	for i := range merged {
		mergedVolume += merged[i].volume()
		for j := i + 1; j < len(merged); j++ {
			if !intersect(merged[i], merged[j]).empty() {
				t.Fatalf("merged rectangles %d and %d overlap", i, j)
			}
		}
	}
	if mergedVolume != originalVolume {
		t.Fatalf("merged volume %d != original %d", mergedVolume, originalVolume)
	}
}

func mustRect(t *testing.T, u *Universe, values [][]string) rect {
	t.Helper()
	r, err := u.toIndex(Rect{Values: values})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
