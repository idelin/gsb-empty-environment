package morph

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestEmptyElementRejected(t *testing.T) {
	if _, err := NewElement(nil); err != ErrEmptyElement {
		t.Fatalf("NewElement(nil) err = %v, want ErrEmptyElement", err)
	}
	if _, err := NewElement([]Point{}); err != ErrEmptyElement {
		t.Fatalf("NewElement(empty) err = %v, want ErrEmptyElement", err)
	}
}

func randImage(rng *rand.Rand, width, height int) [][]uint8 {
	img := make([][]uint8, height)
	for y := range img {
		img[y] = make([]uint8, width)
		for x := range img[y] {
			img[y][x] = uint8(rng.Intn(256))
		}
	}
	return img
}

// streamAll feeds img to a fresh stream in blocks of blockSize and
// returns the concatenated output.
func streamAll(t *testing.T, op Op, el Element, img [][]uint8, blockSize int) [][]uint8 {
	t.Helper()
	width := 0
	if len(img) > 0 {
		width = len(img[0])
	}
	st, err := NewStream(op, el, width)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	var out [][]uint8
	for i := 0; i < len(img); i += blockSize {
		j := i + blockSize
		if j > len(img) {
			j = len(img)
		}
		rows, err := st.Push(img[i:j])
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		out = append(out, rows...)
		// The row cache must stay bounded by the element's row span.
		if got := st.BufferedRows(); got > el.RowSpan() {
			t.Fatalf("BufferedRows = %d, exceeds RowSpan %d", got, el.RowSpan())
		}
	}
	rows, err := st.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	out = append(out, rows...)
	return out
}

func TestStreamMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	elements := []Element{
		mustElement([]Point{{0, 0}}),
		mustElement([]Point{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {0, 0}}), // plus
		mustElement([]Point{{2, 3}}),                                   // single off-origin
		mustElement([]Point{{-3, -2}, {-1, 0}, {2, 1}, {3, 2}}),        // asymmetric diagonal
		mustElement([]Point{{0, -2}, {0, -1}, {0, 1}, {0, 2}}),         // vertical, no origin
		mustElement([]Point{{-2, 1}, {0, 1}, {4, 1}}),                  // horizontal, one row below
		mustElement([]Point{{5, -4}, {-5, 4}}),                         // wide asymmetric pair
	}
	sizes := [][2]int{{1, 1}, {1, 7}, {9, 1}, {3, 4}, {16, 11}, {8, 8}}
	for _, el := range elements {
		for _, sz := range sizes {
			width, height := sz[0], sz[1]
			img := randImage(rng, width, height)
			wantD, err := Dilate(img, el)
			if err != nil {
				t.Fatalf("Dilate: %v", err)
			}
			wantE, err := Erode(img, el)
			if err != nil {
				t.Fatalf("Erode: %v", err)
			}
			for _, block := range []int{1, 2, 3, 5, height + 2} {
				if block < 1 {
					block = 1
				}
				gotD := streamAll(t, Dilation, el, img, block)
				if !reflect.DeepEqual(gotD, wantD) {
					t.Fatalf("dilation mismatch el=%v size=%dx%d block=%d", el.Offsets(), width, height, block)
				}
				gotE := streamAll(t, Erosion, el, img, block)
				if !reflect.DeepEqual(gotE, wantE) {
					t.Fatalf("erosion mismatch el=%v size=%dx%d block=%d", el.Offsets(), width, height, block)
				}
			}
		}
	}
}

func mustElement(offsets []Point) Element {
	el, err := NewElement(offsets)
	if err != nil {
		panic(err)
	}
	return el
}

func TestErosionCountsOutsideAsZero(t *testing.T) {
	// Element reaching one pixel left and one row up: every output pixel
	// on the left column or top row has an offset outside the image and
	// must erode to zero, not to the min of the in-image neighbors.
	img := [][]uint8{
		{200, 200, 200},
		{200, 200, 200},
		{200, 200, 200},
	}
	el := mustElement([]Point{{-1, 0}, {0, -1}, {0, 0}})
	got, err := Erode(img, el)
	if err != nil {
		t.Fatalf("Erode: %v", err)
	}
	want := [][]uint8{
		{0, 0, 0},
		{0, 200, 200},
		{0, 200, 200},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("erosion at border = %v, want %v", got, want)
	}
	// Streaming must agree, including rows emitted only at Close.
	if sgot := streamAll(t, Erosion, el, img, 1); !reflect.DeepEqual(sgot, want) {
		t.Fatalf("streamed erosion at border = %v, want %v", sgot, want)
	}
}

func TestErosionBottomAndRightBorders(t *testing.T) {
	// Element reaching right and down: bottom row and right column must
	// be zero in the output.
	img := [][]uint8{
		{10, 20, 30},
		{40, 50, 60},
		{70, 80, 90},
	}
	el := mustElement([]Point{{0, 0}, {1, 0}, {0, 1}})
	got, err := Erode(img, el)
	if err != nil {
		t.Fatalf("Erode: %v", err)
	}
	want := [][]uint8{
		{10, 20, 0},
		{40, 50, 0},
		{0, 0, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("erosion = %v, want %v", got, want)
	}
}

func TestDilationIgnoresOutside(t *testing.T) {
	// Same element as above: dilation is unaffected by out-of-image
	// zeros because max with zero never raises a uint8 value.
	img := [][]uint8{
		{0, 0, 0},
		{0, 9, 0},
		{0, 0, 0},
	}
	el := mustElement([]Point{{-1, 0}, {0, -1}, {0, 0}})
	got, err := Dilate(img, el)
	if err != nil {
		t.Fatalf("Dilate: %v", err)
	}
	// The 9 spreads one pixel right (offset {-1,0}) and one pixel down
	// (offset {0,-1}); out-of-image zeros never raise the max.
	want := [][]uint8{
		{0, 0, 0},
		{0, 9, 9},
		{0, 9, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dilation = %v, want %v", got, want)
	}
}

func TestAsymmetricElementShiftsOutput(t *testing.T) {
	// A single offset {X: 2, Y: 1} copies img[y+1][x+2] to out[y][x]:
	// the bright pixel moves up-left in the output.
	img := [][]uint8{
		{0, 0, 0, 0},
		{0, 0, 0, 0},
		{0, 0, 0, 7},
	}
	el := mustElement([]Point{{2, 1}})
	got, err := Dilate(img, el)
	if err != nil {
		t.Fatalf("Dilate: %v", err)
	}
	want := [][]uint8{
		{0, 0, 0, 0},
		{0, 7, 0, 0},
		{0, 0, 0, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dilation = %v, want %v", got, want)
	}
	if sgot := streamAll(t, Dilation, el, img, 1); !reflect.DeepEqual(sgot, want) {
		t.Fatalf("streamed dilation = %v, want %v", sgot, want)
	}
}

func TestStreamEmitsIncrementally(t *testing.T) {
	el := mustElement([]Point{{0, -1}, {0, 0}, {0, 1}})
	st, err := NewStream(Dilation, el, 2)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	img := [][]uint8{{1, 2}, {3, 4}, {5, 6}, {7, 8}}
	total := 0
	for i, row := range img {
		out, err := st.Push([][]uint8{row})
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		total += len(out)
		// With maxDY=1, pushing row i makes row i-1 emittable.
		want := i
		if want > 0 && total != want {
			t.Fatalf("after push %d: emitted %d rows, want %d", i, total, want)
		}
	}
	out, err := st.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	total += len(out)
	if total != len(img) {
		t.Fatalf("emitted %d rows, want %d", total, len(img))
	}
	if st.BufferedRows() != 0 {
		t.Fatalf("BufferedRows after Close = %d, want 0", st.BufferedRows())
	}
}

func TestStreamErrors(t *testing.T) {
	el := mustElement([]Point{{0, 0}})
	st, _ := NewStream(Erosion, el, 3)
	if _, err := st.Push([][]uint8{{1, 2}}); err == nil {
		t.Fatal("Push with wrong width: want error")
	}
	if _, err := st.Push([][]uint8{nil}); err == nil {
		t.Fatal("Push with nil row: want error")
	}
	if _, err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := st.Push([][]uint8{{1, 2, 3}}); err == nil {
		t.Fatal("Push after Close: want error")
	}
	if _, err := st.Close(); err == nil {
		t.Fatal("double Close: want error")
	}
	if _, err := NewStream(Dilation, el, -1); err == nil {
		t.Fatal("negative width: want error")
	}
}

func TestEmptyImage(t *testing.T) {
	el := mustElement([]Point{{0, 0}, {1, 1}})
	for _, op := range []Op{Dilation, Erosion} {
		if got := streamAll(t, op, el, nil, 3); len(got) != 0 {
			t.Fatalf("op %d on empty image: got %d rows, want 0", op, len(got))
		}
	}
}
