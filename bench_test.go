package taskpkg

import "testing"

func BenchmarkSample(b *testing.B) {
	tex := NewTexture(64, 64, checker(64, 64), AddressWrap, AddressWrap)
	p := SampleParams{
		U: 0.373, V: 0.617,
		DuDx: 0.18, DuDy: -0.01,
		DvDx: 0.05, DvDy: 0.02,
		MaxAnisotropy: 8,
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Sample(tex, p)
	}
}
