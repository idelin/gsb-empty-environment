// Package pixelcanvas implements a pixel canvas with flood fill,
// rectangle fill, and diff-based undo/redo history.
package pixelcanvas

import "errors"

// Color is a 32-bit pixel value (e.g. 0xRRGGBBAA).
type Color uint32

// ErrOutOfBounds is returned when coordinates fall outside the canvas.
var ErrOutOfBounds = errors.New("pixelcanvas: coordinates out of bounds")

// pixelChange records a single pixel mutation for undo/redo.
type pixelChange struct {
	index int
	old   Color
	new   Color
}

// operation is one complete user action (a fill or a rect). History is
// evicted only at operation granularity, never mid-operation.
type operation struct {
	kind    string
	changes []pixelChange
}

// size returns the history cost of the operation (number of changed pixels).
func (op operation) size() int { return len(op.changes) }

// Canvas is a width x height grid of pixels with undo/redo history.
// History stores per-operation diffs only; the full image is never copied.
type Canvas struct {
	width  int
	height int
	pixels []Color

	undo []operation
	redo []operation

	// undoSize is the total number of changed pixels held in the undo stack.
	undoSize int
	// maxHistory is the maximum number of changed pixels retained in the
	// undo stack. When exceeded, the oldest complete operations are evicted.
	// A value <= 0 means unlimited.
	maxHistory int
}

// NewCanvas creates a width x height canvas filled with the zero Color.
// maxHistory limits the total number of changed pixels kept in the undo
// history; pass <= 0 for unlimited history.
func NewCanvas(width, height, maxHistory int) *Canvas {
	if width < 0 || height < 0 {
		panic("pixelcanvas: negative dimensions")
	}
	return &Canvas{
		width:      width,
		height:     height,
		pixels:     make([]Color, width*height),
		maxHistory: maxHistory,
	}
}

// Width returns the canvas width in pixels.
func (c *Canvas) Width() int { return c.width }

// Height returns the canvas height in pixels.
func (c *Canvas) Height() int { return c.height }

// At returns the color of the pixel at (x, y).
func (c *Canvas) At(x, y int) (Color, error) {
	if !c.inBounds(x, y) {
		return 0, ErrOutOfBounds
	}
	return c.pixels[y*c.width+x], nil
}

// Pixels returns a copy of the raw pixel buffer in row-major order.
func (c *Canvas) Pixels() []Color {
	out := make([]Color, len(c.pixels))
	copy(out, c.pixels)
	return out
}

func (c *Canvas) inBounds(x, y int) bool {
	return x >= 0 && x < c.width && y >= 0 && y < c.height
}

// Fill flood-fills the 4-connected region of same-colored pixels
// containing (x, y) with color. If the region already has that color,
// nothing changes and no history entry is recorded.
func (c *Canvas) Fill(x, y int, color Color) error {
	if !c.inBounds(x, y) {
		return ErrOutOfBounds
	}
	target := c.pixels[y*c.width+x]
	if target == color {
		// Filling with the same color: no-op, no fake history entry.
		return nil
	}

	var changes []pixelChange
	stack := []int{y*c.width + x}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if c.pixels[i] != target {
			continue
		}
		changes = append(changes, pixelChange{index: i, old: target, new: color})
		c.pixels[i] = color

		px, py := i%c.width, i/c.width
		if px > 0 {
			stack = append(stack, i-1)
		}
		if px < c.width-1 {
			stack = append(stack, i+1)
		}
		if py > 0 {
			stack = append(stack, i-c.width)
		}
		if py < c.height-1 {
			stack = append(stack, i+c.width)
		}
	}
	c.commit(operation{kind: "fill", changes: changes})
	return nil
}

// FillRect overwrites the w x h rectangle with its top-left corner at
// (x, y). Pixels already holding color are not recorded as changes; if
// every pixel in the rectangle already matches, no history entry is
// recorded.
func (c *Canvas) FillRect(x, y, w, h int, color Color) error {
	if w < 0 || h < 0 {
		return errors.New("pixelcanvas: negative rectangle size")
	}
	if w == 0 || h == 0 {
		return nil
	}
	if !c.inBounds(x, y) || !c.inBounds(x+w-1, y+h-1) {
		return ErrOutOfBounds
	}

	var changes []pixelChange
	for dy := 0; dy < h; dy++ {
		row := (y+dy)*c.width + x
		for dx := 0; dx < w; dx++ {
			i := row + dx
			if c.pixels[i] == color {
				continue
			}
			changes = append(changes, pixelChange{index: i, old: c.pixels[i], new: color})
			c.pixels[i] = color
		}
	}
	if len(changes) == 0 {
		return nil
	}
	c.commit(operation{kind: "rect", changes: changes})
	return nil
}

// commit records a completed operation and invalidates the redo stack.
func (c *Canvas) commit(op operation) {
	c.redo = c.redo[:0]
	c.undo = append(c.undo, op)
	c.undoSize += op.size()
	c.evict()
}

// evict drops the oldest complete operations until the undo history is
// within budget. The most recent operation is always kept, even if it
// alone exceeds the budget, so a just-applied action stays undoable.
func (c *Canvas) evict() {
	if c.maxHistory <= 0 {
		return
	}
	for c.undoSize > c.maxHistory && len(c.undo) > 1 {
		c.undoSize -= c.undo[0].size()
		c.undo = c.undo[1:]
	}
}

// Undo reverts the most recent operation. It reports whether an
// operation was undone.
func (c *Canvas) Undo() bool {
	if len(c.undo) == 0 {
		return false
	}
	op := c.undo[len(c.undo)-1]
	c.undo = c.undo[:len(c.undo)-1]
	c.undoSize -= op.size()
	for i := len(op.changes) - 1; i >= 0; i-- {
		c.pixels[op.changes[i].index] = op.changes[i].old
	}
	c.redo = append(c.redo, op)
	return true
}

// Redo re-applies the most recently undone operation. It reports
// whether an operation was redone.
func (c *Canvas) Redo() bool {
	if len(c.redo) == 0 {
		return false
	}
	op := c.redo[len(c.redo)-1]
	c.redo = c.redo[:len(c.redo)-1]
	for _, ch := range op.changes {
		c.pixels[ch.index] = ch.new
	}
	c.undo = append(c.undo, op)
	c.undoSize += op.size()
	c.evict()
	return true
}

// CanUndo reports whether there is an operation to undo.
func (c *Canvas) CanUndo() bool { return len(c.undo) > 0 }

// CanRedo reports whether there is an operation to redo.
func (c *Canvas) CanRedo() bool { return len(c.redo) > 0 }

// UndoLen returns the number of complete operations in the undo history.
func (c *Canvas) UndoLen() int { return len(c.undo) }

// HistorySize returns the total number of changed pixels currently held
// in the undo history.
func (c *Canvas) HistorySize() int { return c.undoSize }
