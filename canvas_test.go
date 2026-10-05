package pixelcanvas

import "testing"

const (
	black Color = 0x00000000
	red   Color = 0xFF0000FF
	green Color = 0x00FF00FF
	blue  Color = 0x0000FFFF
)

func mustFill(t *testing.T, c *Canvas, x, y int, col Color) {
	t.Helper()
	if err := c.Fill(x, y, col); err != nil {
		t.Fatalf("Fill(%d,%d): %v", x, y, err)
	}
}

func mustRect(t *testing.T, c *Canvas, x, y, w, h int, col Color) {
	t.Helper()
	if err := c.FillRect(x, y, w, h, col); err != nil {
		t.Fatalf("FillRect(%d,%d,%d,%d): %v", x, y, w, h, err)
	}
}

func mustAt(t *testing.T, c *Canvas, x, y int, want Color) {
	t.Helper()
	got, err := c.At(x, y)
	if err != nil {
		t.Fatalf("At(%d,%d): %v", x, y, err)
	}
	if got != want {
		t.Fatalf("At(%d,%d) = %#x, want %#x", x, y, got, want)
	}
}

// TestFloodFillRespectsBoundaries verifies 4-neighborhood flood fill
// stops at differently colored pixels and does not leak diagonally.
func TestFloodFillRespectsBoundaries(t *testing.T) {
	c := NewCanvas(5, 5, 0)
	// Vertical wall of green at x=2.
	mustRect(t, c, 2, 0, 1, 5, green)

	mustFill(t, c, 0, 0, red)
	for y := 0; y < 5; y++ {
		mustAt(t, c, 0, y, red)
		mustAt(t, c, 1, y, red)
		mustAt(t, c, 2, y, green)
		mustAt(t, c, 3, y, black)
		mustAt(t, c, 4, y, black)
	}

	// Diagonal contact must not connect regions: fill the right side
	// and confirm the left side is untouched.
	mustFill(t, c, 4, 4, blue)
	mustAt(t, c, 3, 0, blue)
	mustAt(t, c, 0, 0, red)
}

// TestFillSameColorIsNoOp verifies that filling a region with its own
// color records no history entry (no fake change).
func TestFillSameColorIsNoOp(t *testing.T) {
	c := NewCanvas(4, 4, 0)
	mustFill(t, c, 0, 0, red)
	if c.UndoLen() != 1 {
		t.Fatalf("UndoLen = %d, want 1", c.UndoLen())
	}

	// Fill the all-red canvas with red again: must be a no-op.
	mustFill(t, c, 2, 2, red)
	if c.UndoLen() != 1 {
		t.Fatalf("UndoLen after same-color fill = %d, want 1", c.UndoLen())
	}
	if c.HistorySize() != 16 {
		t.Fatalf("HistorySize = %d, want 16", c.HistorySize())
	}

	// Undo must restore the original black canvas exactly.
	if !c.Undo() {
		t.Fatal("Undo returned false")
	}
	for _, p := range c.Pixels() {
		if p != black {
			t.Fatalf("pixel = %#x after undo, want black", p)
		}
	}
	if c.CanUndo() {
		t.Fatal("CanUndo = true after undoing the only real op")
	}
}

// TestFillRectSameColorIsNoOp verifies a rect fully matching the target
// color records no history entry.
func TestFillRectSameColorIsNoOp(t *testing.T) {
	c := NewCanvas(4, 4, 0)
	mustRect(t, c, 0, 0, 4, 4, red)
	if c.UndoLen() != 1 {
		t.Fatalf("UndoLen = %d, want 1", c.UndoLen())
	}
	mustRect(t, c, 1, 1, 2, 2, red) // entirely red already
	if c.UndoLen() != 1 {
		t.Fatalf("UndoLen after no-op rect = %d, want 1", c.UndoLen())
	}
}

// TestUndoRedoPixelConsistency applies a sequence of operations, undoes
// them all, then redoes them all, and requires the final image to be
// pixel-identical to the snapshot taken right after the operations.
func TestUndoRedoPixelConsistency(t *testing.T) {
	c := NewCanvas(8, 8, 0)
	mustFill(t, c, 0, 0, red)
	mustRect(t, c, 2, 2, 3, 3, green)
	mustFill(t, c, 0, 0, blue) // splits around the green block
	mustRect(t, c, 5, 5, 2, 2, red)
	want := c.Pixels()

	for c.CanUndo() {
		c.Undo()
	}
	for _, p := range c.Pixels() {
		if p != black {
			t.Fatal("canvas not fully reverted after undoing all ops")
		}
	}

	for c.CanRedo() {
		c.Redo()
	}
	got := c.Pixels()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d = %#x after redo, want %#x", i, got[i], want[i])
		}
	}
}

// TestUndoRestoresOverlappingOps checks that later ops only revert
// their own diff, leaving earlier pixels intact.
func TestUndoRestoresOverlappingOps(t *testing.T) {
	c := NewCanvas(4, 4, 0)
	mustRect(t, c, 0, 0, 4, 4, red)
	mustRect(t, c, 1, 1, 2, 2, green)

	if !c.Undo() {
		t.Fatal("Undo returned false")
	}
	mustAt(t, c, 0, 0, red)
	mustAt(t, c, 1, 1, red) // restored, not black
	mustAt(t, c, 2, 2, red)

	if !c.Redo() {
		t.Fatal("Redo returned false")
	}
	mustAt(t, c, 1, 1, green)
	mustAt(t, c, 0, 0, red)
}

// TestNewOpClearsRedo verifies a fresh operation invalidates redo.
func TestNewOpClearsRedo(t *testing.T) {
	c := NewCanvas(4, 4, 0)
	mustFill(t, c, 0, 0, red)
	c.Undo()
	if !c.CanRedo() {
		t.Fatal("CanRedo = false after undo")
	}
	mustFill(t, c, 0, 0, blue)
	if c.CanRedo() {
		t.Fatal("CanRedo = true after new operation")
	}
}

// TestHistoryEvictsWholeOperations verifies the size limit evicts only
// complete operations, oldest first, and keeps the newest op undoable
// even if it alone exceeds the budget.
func TestHistoryEvictsWholeOperations(t *testing.T) {
	// Budget of 20 changed pixels; each op below changes 9 pixels.
	c := NewCanvas(10, 10, 20)
	mustRect(t, c, 0, 0, 3, 3, red)   // op1: 9
	mustRect(t, c, 3, 0, 3, 3, green) // op2: 9 -> total 18
	mustRect(t, c, 6, 0, 3, 3, blue)  // op3: 9 -> total 27, evict op1 -> 18

	if c.UndoLen() != 2 {
		t.Fatalf("UndoLen = %d, want 2 (oldest op evicted whole)", c.UndoLen())
	}
	if c.HistorySize() != 18 {
		t.Fatalf("HistorySize = %d, want 18", c.HistorySize())
	}

	// Remaining ops must still undo correctly and completely.
	c.Undo() // undo blue rect
	mustAt(t, c, 6, 0, black)
	c.Undo() // undo green rect
	mustAt(t, c, 3, 0, black)
	if c.CanUndo() {
		t.Fatal("CanUndo = true; partial op should not remain")
	}
	// op1 was evicted, so its pixels stay red.
	mustAt(t, c, 0, 0, red)

	// A single op larger than the budget is kept whole and undoable.
	c2 := NewCanvas(10, 10, 5)
	mustRect(t, c2, 0, 0, 4, 4, red) // 16 changes > budget 5
	if c2.UndoLen() != 1 {
		t.Fatalf("UndoLen = %d, want 1 (newest op kept whole)", c2.UndoLen())
	}
	if !c2.Undo() {
		t.Fatal("Undo returned false for oversized op")
	}
	mustAt(t, c2, 0, 0, black)
	mustAt(t, c2, 3, 3, black)
}

// TestEvictionDoesNotCorruptRedo pushes ops past the budget, undoes the
// survivors, and redoes them, requiring pixel-identical results.
func TestEvictionDoesNotCorruptRedo(t *testing.T) {
	c := NewCanvas(6, 6, 10)
	mustRect(t, c, 0, 0, 2, 2, red)   // 4
	mustRect(t, c, 2, 0, 2, 2, green) // 4
	mustRect(t, c, 4, 0, 2, 2, blue)  // 4 -> total 12, evict red -> 8
	want := c.Pixels()

	for c.CanUndo() {
		c.Undo()
	}
	for c.CanRedo() {
		c.Redo()
	}
	got := c.Pixels()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d = %#x after undo/redo, want %#x", i, got[i], want[i])
		}
	}
}

// TestOutOfBounds verifies invalid coordinates are rejected without
// mutating state or history.
func TestOutOfBounds(t *testing.T) {
	c := NewCanvas(4, 4, 0)
	if err := c.Fill(4, 0, red); err != ErrOutOfBounds {
		t.Fatalf("Fill err = %v, want ErrOutOfBounds", err)
	}
	if err := c.Fill(-1, 0, red); err != ErrOutOfBounds {
		t.Fatalf("Fill err = %v, want ErrOutOfBounds", err)
	}
	if err := c.FillRect(2, 2, 3, 3, red); err != ErrOutOfBounds {
		t.Fatalf("FillRect err = %v, want ErrOutOfBounds", err)
	}
	if _, err := c.At(0, 4); err != ErrOutOfBounds {
		t.Fatalf("At err = %v, want ErrOutOfBounds", err)
	}
	if c.UndoLen() != 0 {
		t.Fatalf("UndoLen = %d after rejected ops, want 0", c.UndoLen())
	}
}

// TestHistoryStoresDiffsOnly verifies the history cost equals the number
// of actually changed pixels, not the canvas size.
func TestHistoryStoresDiffsOnly(t *testing.T) {
	c := NewCanvas(100, 100, 0)
	mustRect(t, c, 0, 0, 2, 2, red) // 4 pixels on a 10000-pixel canvas
	if c.HistorySize() != 4 {
		t.Fatalf("HistorySize = %d, want 4 (diff only, not full image)", c.HistorySize())
	}
}
