package pixelcanvas

import "testing"

func snapshot(c *Canvas) []Color {
	s := make([]Color, c.width*c.height)
	copy(s, c.pix)
	return s
}

func equalPix(a, b []Color) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFloodFillBasic(t *testing.T) {
	c := New(4, 4)
	if !c.Fill(0, 0, 7) {
		t.Fatal("fill should change pixels")
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if c.At(x, y) != 7 {
				t.Fatalf("pixel (%d,%d) = %d, want 7", x, y, c.At(x, y))
			}
		}
	}
	if c.HistoryLen() != 1 {
		t.Fatalf("history len = %d, want 1", c.HistoryLen())
	}
}

func TestFloodFillFourNeighborhood(t *testing.T) {
	// Diagonal contact must not leak: fill (0,0) region, the barrier
	// pixel at (1,0) and (0,1) isolates (1,1) diagonally.
	c := New(3, 3)
	c.pix[1] = 9 // (1,0)
	c.pix[3] = 9 // (0,1)
	c.Fill(0, 0, 5)
	if c.At(1, 1) != 0 {
		t.Fatalf("diagonal leak: (1,1) = %d, want 0", c.At(1, 1))
	}
	if c.At(0, 0) != 5 {
		t.Fatalf("(0,0) = %d, want 5", c.At(0, 0))
	}
}

func TestFillSameColorIsNoOp(t *testing.T) {
	c := New(4, 4)
	c.Fill(0, 0, 3)
	before := snapshot(c)
	hlen := c.HistoryLen()
	hbytes := c.HistoryBytes()
	if c.Fill(2, 2, 3) {
		t.Fatal("filling with the same color must report no change")
	}
	if c.HistoryLen() != hlen || c.HistoryBytes() != hbytes {
		t.Fatal("same-color fill must not grow history")
	}
	if !equalPix(before, snapshot(c)) {
		t.Fatal("same-color fill must not modify pixels")
	}
}

func TestRectSameColorIsNoOp(t *testing.T) {
	c := New(5, 5)
	c.FillRect(0, 0, 5, 5, 8)
	before := snapshot(c)
	hlen := c.HistoryLen()
	if c.FillRect(1, 1, 3, 3, 8) {
		t.Fatal("rect fully covered by same color must report no change")
	}
	if c.HistoryLen() != hlen {
		t.Fatal("same-color rect must not grow history")
	}
	if !equalPix(before, snapshot(c)) {
		t.Fatal("same-color rect must not modify pixels")
	}
}

func TestRectPartialOverlapRecordsOnlyDiff(t *testing.T) {
	c := New(4, 1)
	c.FillRect(0, 0, 2, 1, 6) // pixels 0,1 = 6
	c.FillRect(1, 0, 3, 1, 6) // only pixels 2,3 actually change
	op := c.undo[len(c.undo)-1]
	if len(op.spans) != 1 || op.spans[0].x0 != 2 || op.spans[0].x1 != 3 {
		t.Fatalf("spans = %+v, want single span [2,3]", op.spans)
	}
}

func TestRectClipping(t *testing.T) {
	c := New(3, 3)
	if !c.FillRect(-2, -2, 4, 4, 4) {
		t.Fatal("clipped rect should still paint")
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			want := Color(0)
			if x <= 1 && y <= 1 {
				want = 4
			}
			if c.At(x, y) != want {
				t.Fatalf("(%d,%d) = %d, want %d", x, y, c.At(x, y), want)
			}
		}
	}
	if c.FillRect(10, 10, 2, 2, 4) {
		t.Fatal("fully out-of-bounds rect must be a no-op")
	}
}

func TestUndoRedoConsistency(t *testing.T) {
	c := New(8, 8)
	c.FillRect(1, 1, 3, 3, 1)
	c.Fill(0, 0, 2)
	c.FillRect(4, 4, 2, 2, 3)
	c.Fill(5, 5, 4)
	c.FillRect(0, 7, 8, 1, 5)
	final := snapshot(c)
	n := c.HistoryLen()
	for i := 0; i < n; i++ {
		if !c.Undo() {
			t.Fatalf("undo %d failed", i)
		}
	}
	for _, p := range snapshot(c) {
		if p != 0 {
			t.Fatal("undoing all ops must restore the blank canvas")
		}
	}
	for i := 0; i < n; i++ {
		if !c.Redo() {
			t.Fatalf("redo %d failed", i)
		}
	}
	if !equalPix(final, snapshot(c)) {
		t.Fatal("redo after undo must reproduce identical pixels")
	}
	if c.Redo() {
		t.Fatal("redo stack must be empty after redoing all ops")
	}
	if !c.Undo() {
		t.Fatal("redone ops must be undoable again")
	}
}

func TestNewOpClearsRedo(t *testing.T) {
	c := New(4, 4)
	c.FillRect(0, 0, 2, 2, 1)
	c.Undo()
	if c.RedoLen() != 1 {
		t.Fatal("redo stack should hold the undone op")
	}
	c.Fill(3, 3, 9)
	if c.RedoLen() != 0 {
		t.Fatal("new operation must clear the redo stack")
	}
}

func TestHistoryLimitEvictsWholeOps(t *testing.T) {
	// Each rect op on a 4x1 row of distinct pixels costs
	// 48 + 4*4 = 64 bytes. Limit 130 keeps at most 2 ops.
	c := New(4, 1, WithHistoryLimit(130))
	c.FillRect(0, 0, 4, 1, 1)
	c.FillRect(0, 0, 4, 1, 2)
	c.FillRect(0, 0, 4, 1, 3)
	if c.HistoryBytes() > 130 {
		t.Fatalf("history bytes %d exceed limit 130", c.HistoryBytes())
	}
	if c.HistoryLen() != 2 {
		t.Fatalf("history len = %d, want 2 complete ops", c.HistoryLen())
	}
	// The evicted op is gone: undoing everything must land on the
	// state right after the evicted op (all 1s), not the blank canvas.
	for c.Undo() {
	}
	for x := 0; x < 4; x++ {
		if c.At(x, 0) != 1 {
			t.Fatalf("pixel %d = %d, want 1 (state after evicted op)", x, c.At(x, 0))
		}
	}
	// Redoing the surviving ops still reproduces the final state.
	for c.Redo() {
	}
	for x := 0; x < 4; x++ {
		if c.At(x, 0) != 3 {
			t.Fatalf("pixel %d = %d, want 3 after redo", x, c.At(x, 0))
		}
	}
}

func TestHistoryLimitZeroMeansUnlimited(t *testing.T) {
	c := New(2, 2)
	for i := 1; i <= 10; i++ {
		c.FillRect(0, 0, 2, 2, Color(i))
	}
	if c.HistoryLen() != 10 {
		t.Fatalf("history len = %d, want 10", c.HistoryLen())
	}
}

func TestSingleOpLargerThanLimit(t *testing.T) {
	// A single op bigger than the limit is evicted as a whole;
	// the canvas keeps its pixels, only undoability is lost.
	c := New(4, 4, WithHistoryLimit(10))
	c.Fill(0, 0, 5)
	if c.HistoryLen() != 0 {
		t.Fatal("oversized op should be evicted entirely")
	}
	if c.At(0, 0) != 5 {
		t.Fatal("eviction must not revert pixels")
	}
}

func TestOutOfBoundsFill(t *testing.T) {
	c := New(2, 2)
	if c.Fill(-1, 0, 1) || c.Fill(0, 2, 1) {
		t.Fatal("out-of-bounds fill must be a no-op")
	}
	if c.HistoryLen() != 0 {
		t.Fatal("no-op fill must not record history")
	}
}
