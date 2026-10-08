package tileindex

import (
	"sync"
	"testing"
)

func mustTile(t *testing.T, z, x, y int) TileID {
	t.Helper()
	id, err := NewTileID(z, x, y)
	if err != nil {
		t.Fatalf("NewTileID(%d,%d,%d): %v", z, x, y, err)
	}
	return id
}

func TestNegativeCoordinatesRejected(t *testing.T) {
	cases := [][3]int{
		{1, -1, 0},
		{1, 0, -1},
		{-1, 0, 0},
		{2, 4, 0}, // x == 2^z, out of range
		{2, 0, 4}, // y == 2^z, out of range
	}
	for _, c := range cases {
		if _, err := NewTileID(c[0], c[1], c[2]); err == nil {
			t.Errorf("NewTileID%v: want error, got nil", c)
		}
	}
	// Invalid identifiers must not resolve against the index.
	idx := NewIndex(mustTile(t, 0, 0, 0))
	for _, c := range cases {
		bad := TileID{Z: c[0], X: c[1], Y: c[2]}
		if _, _, ok := idx.Snapshot().Cover(bad); ok {
			t.Errorf("Cover(%v): want ok=false for invalid tile", bad)
		}
	}
}

func TestExactTileCoversFullInterval(t *testing.T) {
	tile := mustTile(t, 3, 5, 2)
	idx := NewIndex(tile)
	anc, clip, ok := idx.Snapshot().Cover(tile)
	if !ok || anc != tile {
		t.Fatalf("Cover: got (%v, %v), want exact tile", anc, ok)
	}
	full := Rect{NewRat(0, 1), NewRat(0, 1), NewRat(1, 1), NewRat(1, 1)}
	if clip != full {
		t.Fatalf("clip = %+v, want full [0,1]x[0,1]", clip)
	}
}

func TestAncestorFallbackInterval(t *testing.T) {
	// Only the level-1 tile (1,1) is available; a level-3 descendant must
	// resolve to it with the exact rational sub-interval.
	anc := mustTile(t, 1, 1, 1)
	idx := NewIndex(anc)
	// Descendant (3, 5, 6): shift=2, scale=4, u0=(5-4)/4=1/4, v0=(6-4)/4=1/2.
	got, clip, ok := idx.Snapshot().Cover(mustTile(t, 3, 5, 6))
	if !ok || got != anc {
		t.Fatalf("Cover: got (%v, %v), want ancestor %v", got, ok, anc)
	}
	want := Rect{NewRat(1, 4), NewRat(1, 2), NewRat(1, 2), NewRat(3, 4)}
	if clip != want {
		t.Fatalf("clip = %+v, want %+v", clip, want)
	}
}

// TestBoundaryTilesScalingFormula checks every descendant of an ancestor
// against the coordinate scaling formula, including the boundary tiles
// whose edges must land exactly on 0 and 1.
func TestBoundaryTilesScalingFormula(t *testing.T) {
	anc := mustTile(t, 2, 1, 3)
	idx := NewIndex(anc)
	snap := idx.Snapshot()
	const targetZ = 5
	shift := uint(targetZ - anc.Z)
	scale := int64(1) << shift
	for dx := int64(0); dx < scale; dx++ {
		for dy := int64(0); dy < scale; dy++ {
			x := anc.X<<shift + int(dx)
			y := anc.Y<<shift + int(dy)
			got, clip, ok := snap.Cover(mustTile(t, targetZ, x, y))
			if !ok || got != anc {
				t.Fatalf("Cover(%d,%d,%d): got (%v,%v)", targetZ, x, y, got, ok)
			}
			want := Rect{
				U0: NewRat(dx, scale), V0: NewRat(dy, scale),
				U1: NewRat(dx+1, scale), V1: NewRat(dy+1, scale),
			}
			if clip != want {
				t.Errorf("tile (%d,%d,%d): clip = %+v, want %+v", targetZ, x, y, clip, want)
			}
		}
	}
	// Boundary corners: first descendant starts at 0, last ends at 1.
	_, first, _ := snap.Cover(mustTile(t, targetZ, anc.X<<shift, anc.Y<<shift))
	if first.U0 != NewRat(0, 1) || first.V0 != NewRat(0, 1) {
		t.Errorf("first descendant: U0/V0 = %v/%v, want 0", first.U0, first.V0)
	}
	_, last, _ := snap.Cover(mustTile(t, targetZ, (anc.X+1)<<shift-1, (anc.Y+1)<<shift-1))
	if last.U1 != NewRat(1, 1) || last.V1 != NewRat(1, 1) {
		t.Errorf("last descendant: U1/V1 = %v/%v, want 1", last.U1, last.V1)
	}
}

func TestMissingAncestorReturnsEmpty(t *testing.T) {
	// Nothing available at all: even the root is missing.
	idx := NewIndex()
	if _, _, ok := idx.Snapshot().Cover(mustTile(t, 4, 3, 9)); ok {
		t.Fatal("Cover: want ok=false when no ancestor is available")
	}
}

func TestNeighbourIsNotParent(t *testing.T) {
	// Available tile (1,0,0) is a neighbour of the parent (1,1,1) of the
	// target (2,3,3), but it is not an ancestor and must not be used.
	idx := NewIndex(mustTile(t, 1, 0, 0))
	if _, _, ok := idx.Snapshot().Cover(mustTile(t, 2, 3, 3)); ok {
		t.Fatal("Cover: neighbour tile must not substitute for missing parent")
	}
	// Same idea one level up: (2,2,2) available, target (2,3,3) missing,
	// parent (1,1,1) missing, root missing -> empty.
	idx2 := NewIndex(mustTile(t, 2, 2, 2))
	if _, _, ok := idx2.Snapshot().Cover(mustTile(t, 2, 3, 3)); ok {
		t.Fatal("Cover: sibling tile must not substitute for missing parent")
	}
}

func TestAtomicUpdateKeepsOldSnapshot(t *testing.T) {
	root := mustTile(t, 0, 0, 0)
	deep := mustTile(t, 3, 2, 5)
	idx := NewIndex(root)

	old := idx.Snapshot()
	next := idx.Update([]TileID{deep}, []TileID{root})

	// Old snapshot is frozen: root still there, deep tile still resolves
	// to the root ancestor.
	if !old.Has(root) || old.Has(deep) {
		t.Fatal("old snapshot mutated by Update")
	}
	anc, clip, ok := old.Cover(deep)
	if !ok || anc != root || clip != (Rect{NewRat(1, 4), NewRat(5, 8), NewRat(3, 8), NewRat(3, 4)}) {
		t.Fatalf("old snapshot Cover = (%v,%+v,%v)", anc, clip, ok)
	}

	// New snapshot reflects the update: exact hit, root gone.
	if !next.Has(deep) || next.Has(root) {
		t.Fatal("new snapshot does not reflect Update")
	}
	anc, clip, ok = next.Cover(deep)
	if !ok || anc != deep || clip != (Rect{NewRat(0, 1), NewRat(0, 1), NewRat(1, 1), NewRat(1, 1)}) {
		t.Fatalf("new snapshot Cover = (%v,%+v,%v)", anc, clip, ok)
	}
	// A tile whose only ancestor was the root now resolves to nothing.
	if _, _, ok := next.Cover(mustTile(t, 2, 0, 0)); ok {
		t.Fatal("Cover: want ok=false after root removed")
	}
}

func TestConcurrentUpdateAndRead(t *testing.T) {
	root := mustTile(t, 0, 0, 0)
	idx := NewIndex(root)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tile := mustTile(t, 4, w, w)
			for i := 0; i < 50; i++ {
				idx.Update([]TileID{tile}, nil)
				idx.Update(nil, []TileID{tile})
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				// Root is never removed, so every snapshot must cover.
				if _, _, ok := idx.Snapshot().Cover(mustTile(t, 4, 15, 15)); !ok {
					t.Error("Cover: root ancestor missing during concurrent update")
					return
				}
			}
		}()
	}
	wg.Wait()
}
