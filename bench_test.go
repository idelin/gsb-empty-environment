package yuvconv

import "testing"

func BenchmarkI420(b *testing.B) {
	const w, h = 1920, 1080
	frame := buildI420(w, h, smpteBars)
	p := Params{Width: w, Height: h, Matrix: MatrixBT709, Range: RangeLimited, Chroma: ChromaCenter}

	b.SetBytes(w * h * 3 / 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := I420(frame, p); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkP010(b *testing.B) {
	const w, h = 1920, 1080
	cw, ch := (w+1)/2, (h+1)/2
	frame := i420ToP010(buildI420(w, h, smpteBars), w, h, cw, ch)
	p := Params{Width: w, Height: h, Matrix: MatrixBT2020, Range: RangeLimited, Chroma: ChromaLeft}

	b.SetBytes(w * h * 3)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := P010(frame, p); err != nil {
			b.Fatal(err)
		}
	}
}
