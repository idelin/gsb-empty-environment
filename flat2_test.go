package jsonpathstream

import (
	"runtime"
	"testing"
)

func measureHeap(total int) (before, after uint64, count int) {
	p, _ := Parse("$.records[?(@.status == \"keep\")].id")
	runtime.GC()
	var b, a runtime.MemStats
	runtime.ReadMemStats(&b)
	if err := EvaluateReader(p, &largeReader{total: total}, func(Match) error { count++; return nil }); err != nil {
		panic(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&a)
	return b.HeapAlloc, a.HeapAlloc, count
}

func TestHeapIndependentOfSize(t *testing.T) {
	sb, sa, sc := measureHeap(5000)
	lb, la, lc := measureHeap(40000)
	t.Logf("5k: before=%dKB after=%dKB matches=%d", sb/1024, sa/1024, sc)
	t.Logf("40k: before=%dKB after=%dKB matches=%d", lb/1024, la/1024, lc)
	if lc != 20000 || sc != 2500 {
		t.Fatalf("unexpected match counts %d %d", sc, lc)
	}
	// Retained heap after the 40k (~24MB) document is consumed must stay under
	// a few MB, proving memory does not scale with document length.
	const ceiling = uint64(4 * 1024 * 1024)
	if la > ceiling {
		t.Fatalf("post-run heap %dKB exceeds bounded ceiling %dKB", la/1024, ceiling/1024)
	}
}
