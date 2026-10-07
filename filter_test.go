package blockfilter

import (
	"math/rand"
	"testing"
)

// randomImage returns a deterministic pseudo-random image.
func randomImage(w, h int, seed int64) []int {
	r := rand.New(rand.NewSource(seed))
	pix := make([]int, w*h)
	for i := range pix {
		pix[i] = r.Intn(256)
	}
	return pix
}

// blockSlice extracts block (bx, by) from a full image.
func blockSlice(pix []int, width, height, bw, bh, bx, by int) []int {
	w, h := bw, bh
	if rem := width - bx*bw; rem < w {
		w = rem
	}
	if rem := height - by*bh; rem < h {
		h = rem
	}
	out := make([]int, w*h)
	for y := 0; y < h; y++ {
		copy(out[y*w:(y+1)*w], pix[(by*bh+y)*width+bx*bw:(by*bh+y)*width+bx*bw+w])
	}
	return out
}

// runCase feeds all blocks in the given order and checks that the assembled
// output equals the whole-image Blur, with every block emitted exactly once.
func runCase(t *testing.T, w, h, bw, bh int, pix []int, order []int) {
	t.Helper()
	f, err := New(w, h, bw, bh)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cols := (w + bw - 1) / bw
	rows := (h + bh - 1) / bh
	got := make([]int, w*h)
	emitted := make([]int, cols*rows)
	for _, idx := range order {
		bx, by := idx%cols, idx/cols
		outs, err := f.AddBlock(bx, by, blockSlice(pix, w, h, bw, bh, bx, by))
		if err != nil {
			t.Fatalf("AddBlock(%d,%d): %v", bx, by, err)
		}
		for _, o := range outs {
			emitted[o.BY*cols+o.BX]++
			for y := 0; y < o.H; y++ {
				off := (o.BY*bh+y)*w + o.BX*bw
				copy(got[off:off+o.W], o.Data[y*o.W:(y+1)*o.W])
			}
		}
	}
	if !f.Done() {
		t.Fatalf("not all blocks published")
	}
	for i, n := range emitted {
		if n != 1 {
			t.Fatalf("block (%d,%d) emitted %d times, want 1", i%cols, i/cols, n)
		}
	}
	want := Blur(w, h, pix)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func shuffled(n int, seed int64) []int {
	r := rand.New(rand.NewSource(seed))
	order := r.Perm(n)
	return order
}

func sequential(n int) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	return order
}

func TestOutOfOrderBlocks(t *testing.T) {
	w, h, bw, bh := 16, 12, 4, 4
	pix := randomImage(w, h, 1)
	cols, rows := w/bw, h/bh
	runCase(t, w, h, bw, bh, pix, shuffled(cols*rows, 42))
}

func TestPartialLastBlocks(t *testing.T) {
	geometries := []struct{ w, h, bw, bh int }{
		{10, 7, 4, 4},  // partial right and bottom edges
		{5, 5, 8, 8},   // block larger than the image
		{1, 1, 1, 1},   // single pixel
		{3, 3, 2, 2},   // tiny partial blocks
		{9, 1, 4, 1},   // single row
		{1, 9, 1, 4},   // single column
		{13, 11, 1, 1}, // pixel-sized blocks
		{7, 6, 3, 2},   // uneven split both axes
	}
	for _, g := range geometries {
		pix := randomImage(g.w, g.h, int64(g.w*1000+g.h))
		cols := (g.w + g.bw - 1) / g.bw
		rows := (g.h + g.bh - 1) / g.bh
		runCase(t, g.w, g.h, g.bw, g.bh, pix, shuffled(cols*rows, int64(g.bw*100+g.bh)))
	}
}

func TestAllOrdersSmallGrid(t *testing.T) {
	w, h, bw, bh := 6, 6, 3, 3 // 2x2 blocks: 24 permutations
	pix := randomImage(w, h, 7)
	var permute func([]int, int)
	permute = func(a []int, i int) {
		if i == len(a) {
			runCase(t, w, h, bw, bh, pix, append([]int(nil), a...))
			return
		}
		for j := i; j < len(a); j++ {
			a[i], a[j] = a[j], a[i]
			permute(a, i+1)
			a[i], a[j] = a[j], a[i]
		}
	}
	permute([]int{0, 1, 2, 3}, 0)
}

func TestCornerPublishedWithoutFullNeighborhood(t *testing.T) {
	// 3x3 block grid; corner block (0,0) needs only blocks (0,0),(1,0),(0,1),(1,1).
	w, h, bw, bh := 12, 12, 4, 4
	pix := randomImage(w, h, 3)
	f, err := New(w, h, bw, bh)
	if err != nil {
		t.Fatal(err)
	}
	add := func(bx, by int) []Output {
		outs, err := f.AddBlock(bx, by, blockSlice(pix, w, h, bw, bh, bx, by))
		if err != nil {
			t.Fatalf("AddBlock(%d,%d): %v", bx, by, err)
		}
		return outs
	}
	for _, b := range [][2]int{{0, 0}, {1, 0}, {0, 1}} {
		if outs := add(b[0], b[1]); len(outs) != 0 {
			t.Fatalf("after block %v: got %d outputs, want 0", b, len(outs))
		}
	}
	outs := add(1, 1)
	if len(outs) != 1 || outs[0].BX != 0 || outs[0].BY != 0 {
		t.Fatalf("got %v, want only corner block (0,0)", outs)
	}
	if f.Done() {
		t.Fatalf("filter done before all blocks arrived")
	}
	// Deliver the rest; everything else must follow, each exactly once.
	got := map[[2]int]int{{0, 0}: 1}
	for _, idx := range shuffled(9, 9) {
		bx, by := idx%3, idx/3
		if bx <= 1 && by <= 1 {
			continue
		}
		for _, o := range add(bx, by) {
			got[[2]int{o.BX, o.BY}]++
		}
	}
	if !f.Done() {
		t.Fatalf("not done after all blocks")
	}
	for by := 0; by < 3; by++ {
		for bx := 0; bx < 3; bx++ {
			if got[[2]int{bx, by}] != 1 {
				t.Fatalf("block (%d,%d) emitted %d times", bx, by, got[[2]int{bx, by}])
			}
		}
	}
}

func TestDuplicateIdenticalBlock(t *testing.T) {
	w, h, bw, bh := 8, 8, 4, 4
	pix := randomImage(w, h, 5)
	f, _ := New(w, h, bw, bh)
	data := blockSlice(pix, w, h, bw, bh, 0, 0)
	if _, err := f.AddBlock(0, 0, data); err != nil {
		t.Fatal(err)
	}
	outs, err := f.AddBlock(0, 0, data)
	if err != nil {
		t.Fatalf("identical duplicate rejected: %v", err)
	}
	if len(outs) != 0 {
		t.Fatalf("duplicate triggered %d outputs", len(outs))
	}
	// Finish the image; block (0,0) must still be emitted exactly once.
	count := 0
	for _, idx := range sequential(4)[1:] {
		bx, by := idx%2, idx/2
		outs, err := f.AddBlock(bx, by, blockSlice(pix, w, h, bw, bh, bx, by))
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range outs {
			if o.BX == 0 && o.BY == 0 {
				count++
			}
		}
	}
	if count != 1 || !f.Done() {
		t.Fatalf("corner emitted %d times, done=%v", count, f.Done())
	}
}

func TestDuplicateConflictingBlock(t *testing.T) {
	w, h, bw, bh := 8, 8, 4, 4
	pix := randomImage(w, h, 6)
	f, _ := New(w, h, bw, bh)
	data := blockSlice(pix, w, h, bw, bh, 1, 1)
	if _, err := f.AddBlock(1, 1, data); err != nil {
		t.Fatal(err)
	}
	bad := append([]int(nil), data...)
	bad[0]++
	if _, err := f.AddBlock(1, 1, bad); err == nil {
		t.Fatalf("conflicting duplicate accepted")
	}
}

func TestInvalidInput(t *testing.T) {
	if _, err := New(0, 4, 2, 2); err == nil {
		t.Fatalf("zero width accepted")
	}
	if _, err := New(4, 4, 0, 2); err == nil {
		t.Fatalf("zero block width accepted")
	}
	f, _ := New(4, 4, 2, 2)
	if _, err := f.AddBlock(2, 0, make([]int, 4)); err == nil {
		t.Fatalf("out-of-range block accepted")
	}
	if _, err := f.AddBlock(0, 0, make([]int, 3)); err == nil {
		t.Fatalf("wrong-length block accepted")
	}
	// Partial edge block must carry exactly its clipped size.
	if _, err := f.AddBlock(1, 1, make([]int, 4)); err != nil {
		t.Fatalf("full-size edge block rejected: %v", err)
	}
	g, _ := New(6, 6, 4, 4) // edge blocks are 2x4, 4x2, 2x2
	if _, err := g.AddBlock(1, 1, make([]int, 16)); err == nil {
		t.Fatalf("oversized partial block accepted")
	}
	if _, err := g.AddBlock(1, 1, make([]int, 4)); err != nil {
		t.Fatalf("partial block rejected: %v", err)
	}
}
