// Package blockfilter applies a 3x3 mean filter to an integer-pixel image
// that arrives block by block, in any order. A filtered block is published
// only once every block holding pixels of its 3x3 neighborhood has been
// received. Pixels outside the image are clamped to the nearest edge pixel.
package blockfilter

import "fmt"

// Filter accumulates the blocks of one image and emits filtered blocks.
type Filter struct {
	width, height  int
	blockW, blockH int
	cols, rows     int
	data           []int  // full image, row-major; valid only where received
	received       []bool // per block
	emitted        []bool // per block
	pending        int    // blocks not yet emitted
}

// Output is one filtered block.
type Output struct {
	BX, BY int   // block coordinates, in block units
	W, H   int   // block size in pixels (smaller at the right/bottom edge)
	Data   []int // filtered pixels, row-major, len == W*H
}

// New creates a Filter for a width x height image delivered in blocks of
// blockW x blockH pixels. The last block row/column may be partial.
func New(width, height, blockW, blockH int) (*Filter, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("blockfilter: invalid image size %dx%d", width, height)
	}
	if blockW <= 0 || blockH <= 0 {
		return nil, fmt.Errorf("blockfilter: invalid block size %dx%d", blockW, blockH)
	}
	cols := (width + blockW - 1) / blockW
	rows := (height + blockH - 1) / blockH
	return &Filter{
		width: width, height: height,
		blockW: blockW, blockH: blockH,
		cols: cols, rows: rows,
		data:     make([]int, width*height),
		received: make([]bool, cols*rows),
		emitted:  make([]bool, cols*rows),
		pending:  cols * rows,
	}, nil
}

// blockSize returns the pixel size of block (bx, by); edge blocks may be
// smaller than blockW x blockH.
func (f *Filter) blockSize(bx, by int) (w, h int) {
	w = f.blockW
	if rem := f.width - bx*f.blockW; rem < w {
		w = rem
	}
	h = f.blockH
	if rem := f.height - by*f.blockH; rem < h {
		h = rem
	}
	return w, h
}

// AddBlock stores the block at block coordinates (bx, by). data must hold
// the block's pixels in row-major order, len == W*H of the (possibly
// partial) block. Re-delivering identical data is a no-op; conflicting
// data for an already received block is an error.
//
// It returns the filtered blocks that became publishable by this delivery,
// in row-major block order. Every block of the image is published exactly
// once across all calls.
func (f *Filter) AddBlock(bx, by int, data []int) ([]Output, error) {
	if bx < 0 || bx >= f.cols || by < 0 || by >= f.rows {
		return nil, fmt.Errorf("blockfilter: block (%d,%d) out of range %dx%d", bx, by, f.cols, f.rows)
	}
	w, h := f.blockSize(bx, by)
	if len(data) != w*h {
		return nil, fmt.Errorf("blockfilter: block (%d,%d) expects %d pixels, got %d", bx, by, w*h, len(data))
	}
	idx := by*f.cols + bx
	if f.received[idx] {
		for y := 0; y < h; y++ {
			off := (by*f.blockH+y)*f.width + bx*f.blockW
			for x := 0; x < w; x++ {
				if f.data[off+x] != data[y*w+x] {
					return nil, fmt.Errorf("blockfilter: block (%d,%d) conflicts with previously received data", bx, by)
				}
			}
		}
		return nil, nil
	}
	for y := 0; y < h; y++ {
		off := (by*f.blockH+y)*f.width + bx*f.blockW
		copy(f.data[off:off+w], data[y*w:(y+1)*w])
	}
	f.received[idx] = true
	return f.publishReady(), nil
}

// Done reports whether every block has been published.
func (f *Filter) Done() bool { return f.pending == 0 }

// publishReady emits every not-yet-emitted block whose required neighbor
// blocks have all been received.
func (f *Filter) publishReady() []Output {
	var out []Output
	for by := 0; by < f.rows; by++ {
		for bx := 0; bx < f.cols; bx++ {
			idx := by*f.cols + bx
			if f.emitted[idx] || !f.ready(bx, by) {
				continue
			}
			f.emitted[idx] = true
			f.pending--
			out = append(out, f.render(bx, by))
		}
	}
	return out
}

// ready reports whether all blocks overlapping the 1-pixel halo of block
// (bx, by), clipped to the image, have been received.
func (f *Filter) ready(bx, by int) bool {
	w, h := f.blockSize(bx, by)
	x0 := bx*f.blockW - 1
	if x0 < 0 {
		x0 = 0
	}
	y0 := by*f.blockH - 1
	if y0 < 0 {
		y0 = 0
	}
	x1 := bx*f.blockW + w // inclusive halo edge
	if x1 > f.width-1 {
		x1 = f.width - 1
	}
	y1 := by*f.blockH + h
	if y1 > f.height-1 {
		y1 = f.height - 1
	}
	for by2 := y0 / f.blockH; by2 <= y1/f.blockH; by2++ {
		for bx2 := x0 / f.blockW; bx2 <= x1/f.blockW; bx2++ {
			if !f.received[by2*f.cols+bx2] {
				return false
			}
		}
	}
	return true
}

// render computes the filtered pixels of one block from the full-image buffer.
func (f *Filter) render(bx, by int) Output {
	w, h := f.blockSize(bx, by)
	out := Output{BX: bx, BY: by, W: w, H: h, Data: make([]int, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Data[y*w+x] = meanAt(f.data, f.width, f.height, bx*f.blockW+x, by*f.blockH+y)
		}
	}
	return out
}

// meanAt returns the truncated mean of the 3x3 neighborhood around (x, y),
// clamping coordinates to the image (edge replication).
func meanAt(pix []int, width, height, x, y int) int {
	sum := 0
	for dy := -1; dy <= 1; dy++ {
		yy := y + dy
		if yy < 0 {
			yy = 0
		}
		if yy >= height {
			yy = height - 1
		}
		for dx := -1; dx <= 1; dx++ {
			xx := x + dx
			if xx < 0 {
				xx = 0
			}
			if xx >= width {
				xx = width - 1
			}
			sum += pix[yy*width+xx]
		}
	}
	return sum / 9
}

// Blur filters a whole image at once; it is the reference against which
// block-wise results are checked.
func Blur(width, height int, pix []int) []int {
	out := make([]int, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			out[y*width+x] = meanAt(pix, width, height, x, y)
		}
	}
	return out
}
