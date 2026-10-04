package sedt

import (
	"math/rand"
	"reflect"
	"testing"
)

func cellsFromRows(rows ...string) (w, h int, cells []bool) {
	h = len(rows)
	w = len(rows[0])
	for _, row := range rows {
		if len(row) != w {
			panic("ragged rows")
		}
		for x := 0; x < w; x++ {
			cells = append(cells, row[x] == '1')
		}
	}
	return w, h, cells
}

func checkField(t *testing.T, w, h int, cells []bool) []Cell {
	t.Helper()
	got := ComputeField(w, h, cells)
	want := ReferenceField(w, h, cells)
	if !reflect.DeepEqual(got, want) {
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("pixel (%d,%d): got %+v, want %+v",
					i/w, i%w, got[i], want[i])
			}
		}
	}
	return got
}

func TestSinglePixel(t *testing.T) {
	w, h, cells := cellsFromRows(
		"00000",
		"00000",
		"00100",
		"00000",
		"00000",
	)
	f := checkField(t, w, h, cells)

	// The unique set pixel: nearest clear pixels are its 4 neighbors,
	// tie broken by row then column -> (1,2).
	c := f[2*w+2]
	if !c.HasTarget || c.Dist2 != 1 || c.Src != (Point{1, 2}) {
		t.Errorf("center: got %+v", c)
	}
	// Corner (0,0) is clear; its nearest set pixel is (2,2), dist^2 = 8.
	c = f[0]
	if !c.HasTarget || c.Dist2 != 8 || c.Src != (Point{2, 2}) {
		t.Errorf("corner: got %+v", c)
	}
}

func TestCheckerboard(t *testing.T) {
	w, h := 6, 5
	cells := make([]bool, w*h)
	for i := range cells {
		r, c := i/w, i%w
		cells[i] = (r+c)%2 == 0
	}
	f := checkField(t, w, h, cells)
	for i, cell := range f {
		if !cell.HasTarget || cell.Dist2 != 1 {
			t.Errorf("pixel (%d,%d): got %+v, want dist 1", i/w, i%w, cell)
		}
	}
}

func TestTwoEquidistantSeeds(t *testing.T) {
	// Set pixels at (0,2) and (2,0); several pixels are equidistant to both
	// and must resolve to (0,2) by the row-then-column rule.
	w, h, cells := cellsFromRows(
		"001",
		"000",
		"100",
	)
	f := checkField(t, w, h, cells)
	for _, p := range []Point{{0, 0}, {1, 1}, {2, 2}} {
		c := f[p.Row*w+p.Col]
		if !c.HasTarget || c.Dist2 != 4 && c.Dist2 != 2 {
			t.Fatalf("pixel %v: unexpected %+v", p, c)
		}
		if c.Src != (Point{0, 2}) {
			t.Errorf("pixel %v: tie resolved to %v, want (0,2)", p, c.Src)
		}
	}
	// The seed (2,0) itself: nearest clear pixel tie among (1,0) and (2,1)
	// resolves to (1,0).
	if c := f[2*w+0]; c.Src != (Point{1, 0}) || c.Dist2 != 1 {
		t.Errorf("seed (2,0): got %+v", c)
	}
}

func TestUniformGridHasNoTarget(t *testing.T) {
	for _, v := range []bool{false, true} {
		w, h := 3, 4
		cells := make([]bool, w*h)
		for i := range cells {
			cells[i] = v
		}
		f := checkField(t, w, h, cells)
		for i, c := range f {
			if c.HasTarget || c.Dist2 != -1 {
				t.Errorf("value=%v pixel (%d,%d): got %+v, want no target",
					v, i/w, i%w, c)
			}
		}
	}
}

func TestDeleteUniqueOppositePixel(t *testing.T) {
	st := NewStore()
	w, h, cells := cellsFromRows(
		"000",
		"000",
		"000",
	)
	sn0, err := st.NewImage(w, h, cells)
	if err != nil {
		t.Fatal(err)
	}

	// Create the unique opposite pixel.
	sn1, res1, err := st.Flip(sn0.ID, 1, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Examined != w*h {
		t.Errorf("examined = %d, want %d", res1.Examined, w*h)
	}
	if len(res1.Changed) != w*h {
		t.Errorf("changed = %d, want %d (whole grid gained a target)",
			len(res1.Changed), w*h)
	}

	// Delete it again: every pixel loses its target, everywhere.
	sn2, res2, err := st.Flip(sn1.ID, 1, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Examined != w*h {
		t.Errorf("examined = %d, want %d", res2.Examined, w*h)
	}
	if len(res2.Changed) != w*h {
		t.Fatalf("changed = %d, want %d (whole grid lost its target)",
			len(res2.Changed), w*h)
	}
	for _, ch := range res2.Changed {
		if !ch.Old.HasTarget || ch.New.HasTarget {
			t.Errorf("change at (%d,%d): %+v -> %+v", ch.Row, ch.Col, ch.Old, ch.New)
		}
	}
	for i, c := range sn2.Field {
		if c.HasTarget {
			t.Errorf("pixel (%d,%d): %+v, want no target", i/w, i%w, c)
		}
	}

	// Old snapshots still answer with their old distances.
	if c := sn1.At(0, 0); !c.HasTarget || c.Dist2 != 2 || c.Src != (Point{1, 1}) {
		t.Errorf("snapshot 1 query: got %+v", c)
	}
	if c := sn0.At(0, 0); c.HasTarget {
		t.Errorf("snapshot 0 query: got %+v, want no target", c)
	}
	if got := st.Latest(); got != sn2 {
		t.Errorf("latest = snapshot %d, want %d", got.ID, sn2.ID)
	}
}

func TestChangeSetExtendsBeyondEditBox(t *testing.T) {
	// Seeds at both ends of a row; flipping only the left seed must move the
	// nearest point of pixels far outside the edit box.
	st := NewStore()
	w, h, cells := cellsFromRows("10001")
	sn0, err := st.NewImage(w, h, cells)
	if err != nil {
		t.Fatal(err)
	}
	// Middle pixel: equidistant, tie goes to (0,0).
	if c := sn0.At(0, 2); c.Src != (Point{0, 0}) || c.Dist2 != 4 {
		t.Fatalf("before: %+v", c)
	}
	_, res, err := st.Flip(sn0.ID, 0, 0, 1, 1) // edit box = left seed only
	if err != nil {
		t.Fatal(err)
	}
	changed := make(map[Point]Change)
	for _, ch := range res.Changed {
		changed[Point{ch.Row, ch.Col}] = ch
	}
	for _, col := range []int{0, 1, 2} {
		ch, ok := changed[Point{0, col}]
		if !ok {
			t.Fatalf("pixel (0,%d) missing from change set", col)
		}
		if col == 2 && (ch.New.Src != (Point{0, 4}) || ch.New.Dist2 != 4) {
			t.Errorf("pixel (0,2): now %+v, want src (0,4) dist 4", ch.New)
		}
	}
}

func TestExhaustiveSmallGrids(t *testing.T) {
	sizes := [][2]int{{1, 1}, {1, 2}, {2, 1}, {1, 5}, {5, 1}, {2, 2}, {3, 3}, {2, 4}, {4, 4}}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		n := w * h
		for mask := 0; mask < 1<<uint(n); mask++ {
			cells := make([]bool, n)
			for i := 0; i < n; i++ {
				cells[i] = mask>>uint(i)&1 == 1
			}
			got := ComputeField(w, h, cells)
			want := ReferenceField(w, h, cells)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%dx%d mask %b: mismatch\n got %v\nwant %v",
					w, h, mask, got, want)
			}
		}
	}
}

func TestRandomFlipsAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		w := 1 + rng.Intn(8)
		h := 1 + rng.Intn(8)
		cells := make([]bool, w*h)
		for i := range cells {
			cells[i] = rng.Intn(2) == 1
		}
		st := NewStore()
		sn, err := st.NewImage(w, h, cells)
		if err != nil {
			t.Fatal(err)
		}
		prev := sn
		for step := 0; step < 15; step++ {
			x := rng.Intn(w)
			y := rng.Intn(h)
			rw := 1 + rng.Intn(w-x)
			rh := 1 + rng.Intn(h-y)
			next, res, err := st.Flip(prev.ID, x, y, rw, rh)
			if err != nil {
				t.Fatal(err)
			}
			if res.Examined != w*h {
				t.Errorf("examined = %d, want %d", res.Examined, w*h)
			}
			// New field matches the independent reference.
			wantNew := ReferenceField(w, h, next.Cells)
			if !reflect.DeepEqual(next.Field, wantNew) {
				t.Fatalf("trial %d step %d: field mismatch after flip", trial, step)
			}
			// Change set is exactly the diff against the old reference field.
			wantOld := ReferenceField(w, h, prev.Cells)
			var wantChanges []Change
			for i := range wantNew {
				if wantOld[i] != wantNew[i] {
					wantChanges = append(wantChanges, Change{
						Row: i / w, Col: i % w, Old: wantOld[i], New: wantNew[i],
					})
				}
			}
			if !reflect.DeepEqual(res.Changed, wantChanges) {
				t.Fatalf("trial %d step %d: change set mismatch", trial, step)
			}
			// The old snapshot still serves its old field.
			if !reflect.DeepEqual(prev.Field, wantOld) {
				t.Fatalf("trial %d step %d: old snapshot mutated", trial, step)
			}
			prev = next
		}
	}
}
