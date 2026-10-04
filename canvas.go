// Package pixelcanvas implements an in-memory pixel canvas with
// 4-neighborhood flood fill, rectangle overwrite, and undo/redo.
//
// History is stored as difference spans (only the pixels an operation
// actually changed), never as full-canvas snapshots. When the configured
// history size limit is exceeded, only whole operations are evicted,
// oldest first, so the remaining history is always a valid sequence.
package pixelcanvas

// Color is a pixel value. Zero is a valid color.
type Color = uint32

// spanHeaderBytes estimates the fixed bookkeeping cost of one span
// (row, x-range and slice header). Used only for history accounting.
const spanHeaderBytes = 48

// span records a maximal horizontal run of pixels changed by one
// operation, together with their previous colors.
type span struct {
	y      int
	x0, x1 int // inclusive column range
	old    []Color
}

// operation is a complete, atomic edit (one fill or one rect).
type operation struct {
	name  string
	color Color // the new color applied by the operation
	spans []span
}

// bytes estimates the memory retained by this operation.
func (op *operation) bytes() int {
	n := 0
	for i := range op.spans {
		n += spanHeaderBytes + 4*len(op.spans[i].old)
	}
	return n
}

// Canvas is a width*height grid of pixels with undo/redo history.
type Canvas struct {
	width, height int
	pix           []Color

	undo []operation
	redo []operation

	historyLimit int // max bytes retained by the undo stack; 0 = unlimited
	historyBytes int
}

// Option configures a Canvas.
type Option func(*Canvas)

// WithHistoryLimit caps the undo history to approximately n bytes.
// Eviction removes whole operations, oldest first; a limit of 0
// (the default) keeps history unbounded.
func WithHistoryLimit(n int) Option {
	return func(c *Canvas) { c.historyLimit = n }
}

// New returns a Canvas of the given size with all pixels set to 0.
func New(width, height int, opts ...Option) *Canvas {
	if width < 0 || height < 0 {
		panic("pixelcanvas: negative size")
	}
	c := &Canvas{
		width:  width,
		height: height,
		pix:    make([]Color, width*height),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Width returns the canvas width in pixels.
func (c *Canvas) Width() int { return c.width }

// Height returns the canvas height in pixels.
func (c *Canvas) Height() int { return c.height }

// At returns the color at (x, y). Out-of-bounds reads return 0.
func (c *Canvas) At(x, y int) Color {
	if !c.inBounds(x, y) {
		return 0
	}
	return c.pix[y*c.width+x]
}

// HistoryLen returns the number of undoable operations retained.
func (c *Canvas) HistoryLen() int { return len(c.undo) }

// HistoryBytes returns the estimated bytes retained by the undo history.
func (c *Canvas) HistoryBytes() int { return c.historyBytes }

// RedoLen returns the number of operations available for redo.
func (c *Canvas) RedoLen() int { return len(c.redo) }

func (c *Canvas) inBounds(x, y int) bool {
	return x >= 0 && x < c.width && y >= 0 && y < c.height
}

// Fill flood-fills the 4-connected region of same-colored pixels
// containing (x, y) with color. It reports whether any pixel changed.
// Filling with the region's current color is a no-op and records
// nothing in the history.
func (c *Canvas) Fill(x, y int, color Color) bool {
	if !c.inBounds(x, y) {
		return false
	}
	target := c.pix[y*c.width+x]
	if target == color {
		return false
	}
	op := operation{name: "fill", color: color}
	type point struct{ x, y int }
	stack := []point{{x, y}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if c.pix[p.y*c.width+p.x] != target {
			continue
		}
		x0, x1 := p.x, p.x
		for x0 > 0 && c.pix[p.y*c.width+x0-1] == target {
			x0--
		}
		for x1 < c.width-1 && c.pix[p.y*c.width+x1+1] == target {
			x1++
		}
		old := make([]Color, x1-x0+1)
		copy(old, c.pix[p.y*c.width+x0:p.y*c.width+x1+1])
		op.spans = append(op.spans, span{y: p.y, x0: x0, x1: x1, old: old})
		for i := x0; i <= x1; i++ {
			c.pix[p.y*c.width+i] = color
		}
		for dy := -1; dy <= 1; dy += 2 {
			ny := p.y + dy
			if ny < 0 || ny >= c.height {
				continue
			}
			for nx := x0; nx <= x1; nx++ {
				if c.pix[ny*c.width+nx] != target {
					continue
				}
				stack = append(stack, point{nx, ny})
				for nx <= x1 && c.pix[ny*c.width+nx] == target {
					nx++
				}
			}
		}
	}
	c.pushOp(op)
	return true
}

// FillRect overwrites the rectangle (x, y, w, h) with color, clipped to
// the canvas. Only pixels whose color actually differs are recorded, so
// repainting an area with its existing color is a no-op that records
// nothing. It reports whether any pixel changed.
func (c *Canvas) FillRect(x, y, w, h int, color Color) bool {
	x0 := maxInt(x, 0)
	y0 := maxInt(y, 0)
	x1 := minInt(x+w, c.width)
	y1 := minInt(y+h, c.height)
	if x0 >= x1 || y0 >= y1 {
		return false
	}
	op := operation{name: "rect", color: color}
	for row := y0; row < y1; row++ {
		base := row * c.width
		for col := x0; col < x1; {
			if c.pix[base+col] == color {
				col++
				continue
			}
			start := col
			for col < x1 && c.pix[base+col] != color {
				col++
			}
			old := make([]Color, col-start)
			copy(old, c.pix[base+start:base+col])
			op.spans = append(op.spans, span{y: row, x0: start, x1: col - 1, old: old})
			for i := start; i < col; i++ {
				c.pix[base+i] = color
			}
		}
	}
	if len(op.spans) == 0 {
		return false
	}
	c.pushOp(op)
	return true
}

// Undo reverts the most recent operation. It reports whether an
// operation was undone.
func (c *Canvas) Undo() bool {
	n := len(c.undo)
	if n == 0 {
		return false
	}
	op := c.undo[n-1]
	c.undo = c.undo[:n-1]
	c.historyBytes -= op.bytes()
	for _, s := range op.spans {
		copy(c.pix[s.y*c.width+s.x0:s.y*c.width+s.x1+1], s.old)
	}
	c.redo = append(c.redo, op)
	return true
}

// Redo re-applies the most recently undone operation. It reports
// whether an operation was redone.
func (c *Canvas) Redo() bool {
	n := len(c.redo)
	if n == 0 {
		return false
	}
	op := c.redo[n-1]
	c.redo = c.redo[:n-1]
	for _, s := range op.spans {
		base := s.y*c.width + s.x0
		for i := 0; i <= s.x1-s.x0; i++ {
			c.pix[base+i] = op.color
		}
	}
	c.undo = append(c.undo, op)
	c.historyBytes += op.bytes()
	c.evict()
	return true
}

// pushOp records a completed operation and invalidates the redo stack.
func (c *Canvas) pushOp(op operation) {
	c.redo = c.redo[:0]
	c.undo = append(c.undo, op)
	c.historyBytes += op.bytes()
	c.evict()
}

// evict drops the oldest complete operations until the history fits
// within the configured limit. Partial operations are never dropped.
func (c *Canvas) evict() {
	if c.historyLimit <= 0 {
		return
	}
	for c.historyBytes > c.historyLimit && len(c.undo) > 0 {
		c.historyBytes -= c.undo[0].bytes()
		c.undo = c.undo[1:]
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
