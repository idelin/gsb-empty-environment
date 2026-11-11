package linscale

import (
	"image"
	"testing"
)

func benchImage(w, h int) *image.NRGBA {
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			src.Pix[i+0] = uint8(x % 256)
			src.Pix[i+1] = uint8(y % 256)
			src.Pix[i+2] = uint8((x + y) % 256)
			src.Pix[i+3] = 255
		}
	}
	return src
}

func BenchmarkResizeDownscaleLarge(b *testing.B) {
	src := benchImage(2000, 2000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Resize(src, 800, 600); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResizeUpscaleSmall(b *testing.B) {
	src := benchImage(320, 240)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Resize(src, 640, 480); err != nil {
			b.Fatal(err)
		}
	}
}
