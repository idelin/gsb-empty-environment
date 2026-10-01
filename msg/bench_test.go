package msg

import (
	"testing"

	"protowire/wire"
)

// BenchmarkDecodeOuter proves the hot decode path is allocation-free once
// the pools are warm: go test -bench=. -benchmem should report 0 allocs/op.
func BenchmarkDecodeOuter(b *testing.B) {
	src := sample()
	w := wire.AcquireWriter()
	EncodeOuter(w, src)
	data := append([]byte(nil), w.Bytes()...)
	w.Release()
	src.Release()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := wire.AcquireReader(data)
		m := AcquireOuter()
		if err := DecodeOuter(r, m); err != nil {
			b.Fatal(err)
		}
		m.Release()
		r.Release()
	}
}

func BenchmarkEncodeOuter(b *testing.B) {
	src := sample()
	defer src.Release()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := wire.AcquireWriter()
		EncodeOuter(w, src)
		w.Release()
	}
}
