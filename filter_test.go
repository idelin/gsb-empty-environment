package chunkfilter

import (
	"fmt"
	"math/rand"
	"testing"
)

// referenceFilter applies the 3x3 mean filter to a whole image at once,
// replicating edge pixels outside the boundary.
func referenceFilter(img []int, width, height int) []int {
	at := func(x, y int) int {
		x = clamp(x, 0, width-1)
		y = clamp(y, 0, height-1)
		return img[y*width+x]
	}
	out := make([]int, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sum := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					sum += at(x+dx, y+dy)
				}
			}
			out[y*width+x] = sum / 9
		}
	}
	return out
}

// splitBlocks cuts an image into blocks in row-major grid order.
func splitBlocks(img []int, width, height, blockSize int) []Block {
	var blocks []Block
	cols := (width + blockSize - 1) / blockSize
	rows := (height + blockSize - 1) / blockSize
	for by := 0; by < rows; by++ {
		for bx := 0; bx < cols; bx++ {
			w := minInt(blockSize, width-bx*blockSize)
			h := minInt(blockSize, height-by*blockSize)
			data := make([]int, w*h)
			for r := 0; r < h; r++ {
				copy(data[r*w:(r+1)*w], img[(by*blockSize+r)*width+bx*blockSize:(by*blockSize+r)*width+bx*blockSize+w])
			}
			blocks = append(blocks, Block{X: bx, Y: by, Width: w, Height: h, Data: data})
		}
	}
	return blocks
}

// runFilter feeds blocks to a Filter and reassembles the emitted blocks
// into a full image.
func runFilter(t *testing.T, width, height, blockSize int, blocks []Block) ([]int, int) {
	t.Helper()
	out := make([]int, width*height)
	emitted := 0
	seen := make(map[[2]int]int)
	f, err := New(width, height, blockSize, func(b Block) {
		key := [2]int{b.X, b.Y}
		seen[key]++
		if seen[key] > 1 {
			t.Errorf("block (%d,%d) emitted more than once", b.X, b.Y)
		}
		for r := 0; r < b.Height; r++ {
			copy(out[(b.Y*blockSize+r)*width+b.X*blockSize:(b.Y*blockSize+r)*width+b.X*blockSize+b.Width],
				b.Data[r*b.Width:(r+1)*b.Width])
		}
		emitted++
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i, b := range blocks {
		if err := f.Add(b); err != nil {
			t.Fatalf("Add block %d (%d,%d): %v", i, b.X, b.Y, err)
		}
	}
	if !f.Complete() {
		t.Fatalf("filter incomplete: emitted %d of %d blocks", emitted, len(blocks))
	}
	return out, emitted
}

func makeImage(width, height int, seed int64) []int {
	rng := rand.New(rand.NewSource(seed))
	img := make([]int, width*height)
	for i := range img {
		img[i] = rng.Intn(256)
	}
	return img
}

func checkAgainstReference(t *testing.T, width, height, blockSize int, order []Block, img []int) {
	t.Helper()
	got, emitted := runFilter(t, width, height, blockSize, order)
	cols := (width + blockSize - 1) / blockSize
	rows := (height + blockSize - 1) / blockSize
	if emitted != cols*rows {
		t.Fatalf("emitted %d blocks, want %d", emitted, cols*rows)
	}
	want := referenceFilter(img, width, height)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d (x=%d,y=%d): got %d, want %d", i, i%width, i/width, got[i], want[i])
		}
	}
}

func TestOutOfOrderDelivery(t *testing.T) {
	width, height, blockSize := 12, 8, 4
	img := makeImage(width, height, 1)
	blocks := splitBlocks(img, width, height, blockSize)
	rng := rand.New(rand.NewSource(42))
	rng.Shuffle(len(blocks), func(i, j int) { blocks[i], blocks[j] = blocks[j], blocks[i] })
	checkAgainstReference(t, width, height, blockSize, blocks, img)
}

func TestCornerBlocksFirst(t *testing.T) {
	width, height, blockSize := 12, 8, 4
	img := makeImage(width, height, 2)
	blocks := splitBlocks(img, width, height, blockSize)
	cols := (width + blockSize - 1) / blockSize
	rows := (height + blockSize - 1) / blockSize
	corners := map[[2]int]bool{
		{0, 0}:               true,
		{cols - 1, 0}:        true,
		{0, rows - 1}:        true,
		{cols - 1, rows - 1}: true,
	}
	var first, rest []Block
	for _, b := range blocks {
		if corners[[2]int{b.X, b.Y}] {
			first = append(first, b)
		} else {
			rest = append(rest, b)
		}
	}
	checkAgainstReference(t, width, height, blockSize, append(first, rest...), img)
}

func TestPartialLastBlocks(t *testing.T) {
	// Width and height are not multiples of the block size, so the last
	// block in each row/column is smaller.
	for _, dims := range [][3]int{
		{10, 7, 4},  // 3x2 grid, rightmost 2 wide, bottom 3 high
		{9, 9, 4},   // 3x3 grid, 1-pixel-wide last row/column
		{5, 3, 4},   // single partial row and column
		{13, 11, 5}, // 3x3 grid, last block 3x1
	} {
		width, height, blockSize := dims[0], dims[1], dims[2]
		t.Run(fmt.Sprintf("%dx%d_bs%d", width, height, blockSize), func(t *testing.T) {
			img := makeImage(width, height, int64(width*height))
			blocks := splitBlocks(img, width, height, blockSize)
			rng := rand.New(rand.NewSource(int64(width + height)))
			rng.Shuffle(len(blocks), func(i, j int) { blocks[i], blocks[j] = blocks[j], blocks[i] })
			checkAgainstReference(t, width, height, blockSize, blocks, img)
		})
	}
}

func TestSingleBlockImage(t *testing.T) {
	// Image smaller than one block: a single partial block, no neighbors.
	width, height, blockSize := 3, 2, 8
	img := makeImage(width, height, 3)
	blocks := splitBlocks(img, width, height, blockSize)
	if len(blocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(blocks))
	}
	checkAgainstReference(t, width, height, blockSize, blocks, img)
}

func TestSinglePixelImage(t *testing.T) {
	img := []int{7}
	blocks := splitBlocks(img, 1, 1, 4)
	checkAgainstReference(t, 1, 1, 4, blocks, img)
}

func TestNoEmissionBeforeNeighborsReady(t *testing.T) {
	width, height, blockSize := 12, 12, 4 // 3x3 grid
	img := makeImage(width, height, 4)
	blocks := splitBlocks(img, width, height, blockSize)
	var center, others []Block
	for _, b := range blocks {
		if b.X == 1 && b.Y == 1 {
			center = append(center, b)
		} else {
			others = append(others, b)
		}
	}
	emitted := 0
	delivered := make(map[[2]int]bool)
	var f *Filter
	deliver := func(b Block) {
		delivered[[2]int{b.X, b.Y}] = true
		if err := f.Add(b); err != nil {
			t.Fatalf("Add (%d,%d): %v", b.X, b.Y, err)
		}
	}
	f, err := New(width, height, blockSize, func(b Block) {
		emitted++
		if b.X == 1 && b.Y == 1 && len(delivered) < len(blocks) {
			t.Errorf("center block emitted with only %d of %d blocks delivered", len(delivered), len(blocks))
		}
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Deliver the center block first: nothing may be emitted yet.
	deliver(center[0])
	if emitted != 0 {
		t.Fatalf("emitted %d blocks with only the center delivered", emitted)
	}
	for _, b := range others {
		deliver(b)
	}
	if !f.Complete() {
		t.Fatalf("incomplete after all blocks delivered")
	}
}

func TestIdenticalDuplicateIsNoOp(t *testing.T) {
	width, height, blockSize := 8, 8, 4
	img := makeImage(width, height, 5)
	blocks := splitBlocks(img, width, height, blockSize)
	// Deliver every block twice; identical duplicates must be ignored and
	// each block still emitted exactly once.
	var order []Block
	for _, b := range blocks {
		order = append(order, b, b)
	}
	checkAgainstReference(t, width, height, blockSize, order, img)
}

func TestInconsistentDuplicateFails(t *testing.T) {
	width, height, blockSize := 8, 8, 4
	img := makeImage(width, height, 6)
	blocks := splitBlocks(img, width, height, blockSize)
	f, err := New(width, height, blockSize, func(Block) {})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := f.Add(blocks[0]); err != nil {
		t.Fatalf("Add: %v", err)
	}
	dup := blocks[0]
	dup.Data = make([]int, len(blocks[0].Data))
	copy(dup.Data, blocks[0].Data)
	dup.Data[0]++
	if err := f.Add(dup); err == nil {
		t.Fatalf("expected error for inconsistent duplicate block")
	}
	// Re-delivering identical content after the mismatch must still be fine.
	if err := f.Add(blocks[0]); err != nil {
		t.Fatalf("identical re-delivery after mismatch: %v", err)
	}
}

func TestInvalidBlocksRejected(t *testing.T) {
	f, err := New(8, 8, 4, func(Block) {})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []Block{
		{X: 2, Y: 0, Width: 4, Height: 4, Data: make([]int, 16)},  // out of range
		{X: 0, Y: -1, Width: 4, Height: 4, Data: make([]int, 16)}, // out of range
		{X: 0, Y: 0, Width: 3, Height: 4, Data: make([]int, 12)},  // wrong size
		{X: 0, Y: 0, Width: 4, Height: 4, Data: make([]int, 15)},  // wrong data length
	}
	for i, b := range cases {
		if err := f.Add(b); err == nil {
			t.Errorf("case %d: expected error, got nil", i)
		}
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, err := New(0, 8, 4, func(Block) {}); err == nil {
		t.Errorf("expected error for zero width")
	}
	if _, err := New(8, 8, 0, func(Block) {}); err == nil {
		t.Errorf("expected error for zero block size")
	}
	if _, err := New(8, 8, 4, nil); err == nil {
		t.Errorf("expected error for nil callback")
	}
}
