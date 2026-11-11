package jsonpathstream

import (
	"runtime"
	"testing"
)

// TestMemoryStaysFlat confirms live heap does not grow with the number of
// already-consumed records: it samples HeapAlloc at checkpoints and requires
// later checkpoints to stay close to the first one.
func TestMemoryStaysFlat(t *testing.T) {
	p, _ := Parse("$.records[?(@.status == \"keep\")].id")
	const total = 60000
	samples := []uint64{}
	seen := 0
	err := EvaluateReader(p, &largeReader{total: total}, func(m Match) error {
		seen++
		if seen%10000 == 0 {
			runtime.GC()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			samples = append(samples, ms.HeapAlloc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != total/2 {
		t.Fatalf("seen=%d", seen)
	}
	base := samples[0]
	for i, s := range samples {
		growth := int64(s) - int64(base)
		t.Logf("checkpoint %d: HeapAlloc=%dKB (delta from first=%dKB)", i, s/1024, growth/1024)
		// Allow a generous tolerance for runtime noise; the document itself is
		// ~36MB and must not accumulate.
		if growth > 2*1024*1024 {
			t.Fatalf("live heap grew %dKB as more records streamed", growth/1024)
		}
	}
}
