package dkimcanon

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

// BenchmarkTrailingCRLF shows that an ever-growing tail of blank lines stays
// O(1) in both time-per-line and memory: pending lines are counted, not stored.
func BenchmarkTrailingCRLF(b *testing.B) {
	cr := []byte{'\r'}
	lf := []byte{'\n'}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		c := NewSimple(discardSink{})
		for i := 0; i < 10000; i++ {
			if _, err := c.Write(cr); err != nil {
				b.Fatal(err)
			}
			if _, err := c.Write(lf); err != nil {
				b.Fatal(err)
			}
		}
		if err := c.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRelaxedDigest(b *testing.B) {
	body := bytes.Repeat([]byte("Hello:  Foo \t Bar\r\n"), 1000)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		d := NewDigester(Relaxed, sha256.New())
		if _, err := d.Write(body); err != nil {
			b.Fatal(err)
		}
		if _, err := d.Sum(); err != nil {
			b.Fatal(err)
		}
	}
}
