// Package chunkfilter applies a 3x3 mean filter to an image that is
// delivered block by block, in any order. A filtered block is published
// only after the block itself and all of its required neighbor blocks
// have been received. Pixels outside the image boundary are replicated
// from the nearest edge pixel.
package chunkfilter

import "fmt"

// Block is a rectangular chunk of an image. X and Y are block-grid
// coordinates (not pixel coordinates). Data holds Width*Height integer
// pixels in row-major order.
type Block struct {
	X, Y          int
	Width, Height int
	Data          []int
}

// Filter accumulates image blocks and emits filtered blocks through the
// Emit callback. Each block is emitted exactly once, as soon as every
// block it depends on is available.
type Filter struct {
	width, height int
	blockSize     int
	cols, rows    int
	blocks        map[[2]int]*Block
	emitted       map[[2]int]bool
	emit          func(Block)
}

// New creates a Filter for an image of width x height pixels, delivered
// in blocks of blockSize x blockSize pixels (the last block in each row
// or column may be smaller). emit is called once per block, in
// availability order.
func New(width, height, blockSize int, emit func(Block)) (*Filter, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("chunkfilter: invalid image size %dx%d", width, height)
	}
	if blockSize <= 0 {
		return nil, fmt.Errorf("chunkfilter: invalid block size %d", blockSize)
	}
	if emit == nil {
		return nil, fmt.Errorf("chunkfilter: nil emit callback")
	}
	return &Filter{
		width:     width,
		height:    height,
		blockSize: blockSize,
		cols:      (width + blockSize - 1) / blockSize,
		rows:      (height + blockSize - 1) / blockSize,
		blocks:    make(map[[2]int]*Block),
		emitted:   make(map[[2]int]bool),
		emit:      emit,
	}, nil
}

// Add delivers one block. It returns an error if the block is out of
// range, has the wrong dimensions for its grid position, or duplicates a
// previously delivered block with different content. Re-delivering an
// identical block is a no-op.
func (f *Filter) Add(b Block) error {
	if b.X < 0 || b.X >= f.cols || b.Y < 0 || b.Y >= f.rows {
		return fmt.Errorf("chunkfilter: block (%d,%d) out of range for %dx%d grid", b.X, b.Y, f.cols, f.rows)
	}
	wantW := minInt(f.blockSize, f.width-b.X*f.blockSize)
	wantH := minInt(f.blockSize, f.height-b.Y*f.blockSize)
	if b.Width != wantW || b.Height != wantH {
		return fmt.Errorf("chunkfilter: block (%d,%d) has size %dx%d, want %dx%d", b.X, b.Y, b.Width, b.Height, wantW, wantH)
	}
	if len(b.Data) != b.Width*b.Height {
		return fmt.Errorf("chunkfilter: block (%d,%d) has %d pixels, want %d", b.X, b.Y, len(b.Data), b.Width*b.Height)
	}
	key := [2]int{b.X, b.Y}
	if prev, ok := f.blocks[key]; ok {
		if !equalPixels(prev.Data, b.Data) {
			return fmt.Errorf("chunkfilter: block (%d,%d) re-delivered with inconsistent content", b.X, b.Y)
		}
		return nil
	}
	data := make([]int, len(b.Data))
	copy(data, b.Data)
	stored := b
	stored.Data = data
	f.blocks[key] = &stored
	f.tryEmitAround(b.X, b.Y)
	return nil
}

// Complete reports whether every block of the image has been emitted.
func (f *Filter) Complete() bool {
	return len(f.emitted) == f.cols*f.rows
}

// tryEmitAround checks the 3x3 neighborhood of block (x, y) for blocks
// that have become emittable.
func (f *Filter) tryEmitAround(x, y int) {
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			nx, ny := x+dx, y+dy
			if nx < 0 || nx >= f.cols || ny < 0 || ny >= f.rows {
				continue
			}
			f.tryEmit(nx, ny)
		}
	}
}

// tryEmit emits block (x, y) if it has not been emitted yet and all of
// its existing neighbors (including itself) have been delivered.
func (f *Filter) tryEmit(x, y int) {
	key := [2]int{x, y}
	if f.emitted[key] {
		return
	}
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			nx, ny := x+dx, y+dy
			if nx < 0 || nx >= f.cols || ny < 0 || ny >= f.rows {
				continue
			}
			if _, ok := f.blocks[[2]int{nx, ny}]; !ok {
				return
			}
		}
	}
	f.emitted[key] = true
	f.emit(f.compute(x, y))
}

// compute produces the filtered block at grid position (x, y). All
// required neighbors must already be present.
func (f *Filter) compute(x, y int) Block {
	src := f.blocks[[2]int{x, y}]
	out := make([]int, src.Width*src.Height)
	for r := 0; r < src.Height; r++ {
		for c := 0; c < src.Width; c++ {
			px := x*f.blockSize + c
			py := y*f.blockSize + r
			sum := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					sum += f.pixel(px+dx, py+dy)
				}
			}
			out[r*src.Width+c] = sum / 9
		}
	}
	return Block{X: x, Y: y, Width: src.Width, Height: src.Height, Data: out}
}

// pixel returns the pixel at image coordinates (px, py), clamping to the
// image bounds so border pixels replicate the nearest edge pixel.
func (f *Filter) pixel(px, py int) int {
	px = clamp(px, 0, f.width-1)
	py = clamp(py, 0, f.height-1)
	bx, by := px/f.blockSize, py/f.blockSize
	b := f.blocks[[2]int{bx, by}]
	return b.Data[(py-by*f.blockSize)*b.Width+(px-bx*f.blockSize)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func equalPixels(a, b []int) bool {
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
