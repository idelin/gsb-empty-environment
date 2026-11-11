package linscale

import (
	"image"
	"testing"
)

func TestParallelMatchesSerial(t *testing.T) {
	// Forcing the stripe path on a small image must produce byte-identical
	// output to the single-goroutine path, proving determinism.
	w, h := 200, 120
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			src.Pix[i+0] = uint8((x * 7) % 256)
			src.Pix[i+1] = uint8((y * 11) % 256)
			src.Pix[i+2] = uint8((x + y) % 256)
			src.Pix[i+3] = uint8((x*3 + y*5) % 256)
		}
	}

	serial, err := resizeSerial(src, 96, 64)
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := resizeForcedParallel(src, 96, 64)
	if err != nil {
		t.Fatal(err)
	}
	if len(serial.Pix) != len(parallel.Pix) {
		t.Fatal("size mismatch")
	}
	for i := range serial.Pix {
		if serial.Pix[i] != parallel.Pix[i] {
			t.Fatalf("parallel output differs at byte %d: %d vs %d",
				i, serial.Pix[i], parallel.Pix[i])
		}
	}
}

func TestLargeImageParallelPath(t *testing.T) {
	// Exceeds parallelPixels so the production path uses workers.
	w, h := 1100, 1100
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			src.Pix[i+0] = uint8(x % 256)
			src.Pix[i+1] = uint8(y % 256)
			src.Pix[i+2] = uint8((x ^ y) % 256)
			src.Pix[i+3] = 255
		}
	}
	dst, err := Resize(src, 400, 300)
	if err != nil {
		t.Fatal(err)
	}
	if dst.Rect.Dx() != 400 || dst.Rect.Dy() != 300 {
		t.Fatalf("bad size: %v", dst.Rect)
	}

	serial, err := resizeSerial(src, 400, 300)
	if err != nil {
		t.Fatal(err)
	}
	for i := range dst.Pix {
		if dst.Pix[i] != serial.Pix[i] {
			t.Fatalf("large image mismatch at %d: %d vs %d",
				i, dst.Pix[i], serial.Pix[i])
		}
	}
}

func TestEdgeClamp(t *testing.T) {
	// A uniform image must survive any resize unchanged: clamp handling
	// preserves border energy instead of bleeding in phantom zeros.
	w, h := 10, 10
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		src.Pix[i*4+0] = 10
		src.Pix[i*4+1] = 120
		src.Pix[i*4+2] = 200
		src.Pix[i*4+3] = 210
	}
	dst, err := Resize(src, 33, 27)
	if err != nil {
		t.Fatal(err)
	}
	want := [4]uint8{10, 120, 200, 210}
	for i := 0; i < 33*27; i++ {
		got := dst.Pix[i*4 : i*4+4]
		for c := 0; c < 4; c++ {
			d := int(got[c]) - int(want[c])
			if d > 1 || d < -1 {
				t.Fatalf("uniform pixel %d channel %d drifted: got %d want %d",
					i, c, got[c], want[c])
			}
		}
	}
}

func TestInvalidSize(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	if _, err := Resize(src, 0, 4); err != ErrInvalidSize {
		t.Fatalf("got %v, want ErrInvalidSize", err)
	}
}

func TestPremultipliedInput(t *testing.T) {
	// image.RGBA stores premultiplied colors: 50% white is
	// (128,128,128,128), equivalent to straight (255,255,255,128).
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < 4*4; i++ {
		src.Pix[i*4+0] = 128
		src.Pix[i*4+1] = 128
		src.Pix[i*4+2] = 128
		src.Pix[i*4+3] = 128
	}
	dst, err := Resize(src, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4*4; i++ {
		got := dst.Pix[i*4 : i*4+4]
		if got[0] != 255 || got[1] != 255 || got[2] != 255 || got[3] != 128 {
			t.Fatalf("premultiplied decode wrong at %d: %v", i, got)
		}
	}
}
