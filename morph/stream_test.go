package morph_test

import (
	"math/rand"
	"testing"

	"morphology/morph"
)

// naive is the reference per-pixel whole-image model: for every output
// pixel, fold the operation over every structuring-element offset, with
// out-of-image samples fixed to zero (never skipped).
func naive(img []byte, w, h int, el morph.Element, op morph.Op) []byte {
	out := make([]byte, w*h)
	pts := el.Points()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var acc uint8
			if op == morph.Erode {
				acc = 0xff
			}
			for _, p := range pts {
				yy, xx := y+p.Y, x+p.X
				var v uint8
				if yy >= 0 && yy < h && xx >= 0 && xx < w {
					v = img[yy*w+xx]
				}
				if op == morph.Dilate {
					if v > acc {
						acc = v
					}
				} else if v < acc {
					acc = v
				}
			}
			out[y*w+x] = acc
		}
	}
	return out
}

func eq(a, b []byte) bool {
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

// streamAll feeds img to a Stream in the given block sizes (in rows) and
// returns the full output image.
func streamAll(t *testing.T, img []byte, w, h int, el morph.Element, op morph.Op, blocks []int) []byte {
	t.Helper()
	s, err := morph.NewStream(w, h, el, op)
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	var out []byte
	row := 0
	for _, b := range blocks {
		if b == 0 {
			continue
		}
		part, err := s.WriteRows(img[row*w : (row+b)*w])
		if err != nil {
			t.Fatalf("WriteRows: %v", err)
		}
		out = append(out, part...)
		row += b
	}
	if row != h {
		t.Fatalf("blocks cover %d rows, want %d", row, h)
	}
	rest, err := s.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	out = append(out, rest...)
	if len(out) != w*h {
		t.Fatalf("stream produced %d bytes, want %d", len(out), w*h)
	}
	return out
}

func randBlocks(r *rand.Rand, h int) []int {
	var blocks []int
	for left := h; left > 0; {
		b := 1 + r.Intn(left)
		blocks = append(blocks, b)
		left -= b
	}
	return blocks
}

func TestEmptyElementRejected(t *testing.T) {
	if _, err := morph.NewElement(); err != morph.ErrEmptyElement {
		t.Fatalf("NewElement() err = %v, want ErrEmptyElement", err)
	}
	if _, err := morph.NewStream(4, 4, morph.Element{}, morph.Dilate); err != morph.ErrEmptyElement {
		t.Fatalf("NewStream zero element err = %v, want ErrEmptyElement", err)
	}
	if _, err := morph.DilateImage(make([]byte, 16), 4, 4, morph.Element{}); err != morph.ErrEmptyElement {
		t.Fatalf("DilateImage zero element err = %v, want ErrEmptyElement", err)
	}
}

func TestStreamingMatchesNaiveRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for trial := 0; trial < 300; trial++ {
		w := 1 + r.Intn(11)
		h := 1 + r.Intn(11)
		n := 1 + r.Intn(6)
		pts := make([]morph.Point, n)
		for i := range pts {
			// Asymmetric offsets on both axes, possibly far outside
			// the image and not containing the origin.
			pts[i] = morph.Point{X: r.Intn(9) - 4, Y: r.Intn(9) - 4}
		}
		el, err := morph.NewElement(pts...)
		if err != nil {
			t.Fatalf("NewElement: %v", err)
		}
		img := make([]byte, w*h)
		for i := range img {
			img[i] = uint8(r.Intn(256))
		}
		for _, op := range []morph.Op{morph.Dilate, morph.Erode} {
			want := naive(img, w, h, el, op)
			got := streamAll(t, img, w, h, el, op, randBlocks(r, h))
			if !eq(got, want) {
				t.Fatalf("trial %d op %d w=%d h=%d pts=%v: stream != naive", trial, op, w, h, pts)
			}
			// One-shot helpers must agree too.
			once, err := morph.Apply(img, w, h, el, op)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if !eq(once, want) {
				t.Fatalf("trial %d op %d: Apply != naive", trial, op)
			}
		}
	}
}

func TestErosionDoesNotIgnoreOutside(t *testing.T) {
	// Element reaching one pixel right of the origin: the rightmost
	// column samples outside the image, which is fixed zero, so those
	// eroded pixels must be 0 even though all inputs are bright.
	el, err := morph.NewElement(morph.Point{X: 0, Y: 0}, morph.Point{X: 1, Y: 0})
	if err != nil {
		t.Fatal(err)
	}
	w, h := 4, 3
	img := make([]byte, w*h)
	for i := range img {
		img[i] = 200
	}
	out, err := morph.ErodeImage(img, w, h, el)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < h; y++ {
		if got := out[y*w+w-1]; got != 0 {
			t.Fatalf("right edge eroded to %d, want 0 (outside must count as zero)", got)
		}
		if got := out[y*w]; got != 200 {
			t.Fatalf("interior eroded to %d, want 200", got)
		}
	}
}

func TestAsymmetricElementExample(t *testing.T) {
	// 1 2 3
	// 4 5 6   element: offsets {(0,0), (-1,0), (0,-1), (2,1)} (asymmetric)
	// 7 8 9
	el, err := morph.NewElement(
		morph.Point{X: 0, Y: 0},
		morph.Point{X: -1, Y: 0},
		morph.Point{X: 0, Y: -1},
		morph.Point{X: 2, Y: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	w, h := 3, 3
	img := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9}
	dil, err := morph.DilateImage(img, w, h, el)
	if err != nil {
		t.Fatal(err)
	}
	// Pixel (0,1): neighbors are 4 (self), 1 (up), (-1,1) outside,
	// and (2,2)=9 via offset (2,1). Dilation -> 9.
	if got := dil[1*w+0]; got != 9 {
		t.Fatalf("dilate (0,1) = %d, want 9", got)
	}
	ero, err := morph.ErodeImage(img, w, h, el)
	if err != nil {
		t.Fatal(err)
	}
	// Top-left pixel (0,0): offsets hit (-1,0) and (0,-1), both outside
	// -> fixed zero -> erosion is 0.
	if got := ero[0]; got != 0 {
		t.Fatalf("erode corner = %d, want 0", got)
	}
	if !eq(dil, naive(img, w, h, el, morph.Dilate)) || !eq(ero, naive(img, w, h, el, morph.Erode)) {
		t.Fatal("one-shot disagrees with naive model")
	}
}

func TestRowBufferBounded(t *testing.T) {
	el, err := morph.NewElement(morph.Point{X: 0, Y: -2}, morph.Point{X: 3, Y: 1})
	if err != nil {
		t.Fatal(err)
	}
	s, err := morph.NewStream(5, 1000, el, morph.Dilate)
	if err != nil {
		t.Fatal(err)
	}
	// Span is maxDY-minDY+1 = 4 rows, independent of image height.
	if got := s.BufferRows(); got != 4 {
		t.Fatalf("BufferRows = %d, want 4", got)
	}
}

func TestWriteValidation(t *testing.T) {
	el, _ := morph.NewElement(morph.Point{X: 0, Y: 0})
	s, err := morph.NewStream(4, 2, el, morph.Dilate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteRows(make([]byte, 3)); err == nil {
		t.Fatal("short block: want error")
	}
	if _, err := s.WriteRows(make([]byte, 12)); err == nil {
		t.Fatal("too many rows: want error")
	}
	if _, err := s.Close(); err == nil {
		t.Fatal("close before complete: want error")
	}
	if _, err := s.WriteRows(make([]byte, 8)); err != nil {
		t.Fatalf("full image: %v", err)
	}
	if _, err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := s.WriteRows(make([]byte, 4)); err == nil {
		t.Fatal("write after complete: want error")
	}
}
