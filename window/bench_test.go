package window

import "testing"

func BenchmarkAppendAndPeriod(b *testing.B) {
	win := New(1 << 16)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		win.Append(byte(i))
		win.Period()
	}
}
