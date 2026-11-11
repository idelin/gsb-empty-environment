package jsonpathstream

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
)

// largeReader synthesizes a large JSON array of records without holding the
// serialized document in memory. Each record is small and self-contained.
type largeReader struct {
	total   int
	idx     int
	buf     []byte
	started bool
	closed  bool
}

func (l *largeReader) Read(p []byte) (int, error) {
	if len(l.buf) == 0 {
		switch {
		case !l.started:
			l.buf = []byte("{\"records\":[")
			l.started = true
		case l.idx < l.total:
			l.buf = l.record()
			l.idx++
		case !l.closed:
			l.buf = []byte("]}")
			l.closed = true
		default:
			return 0, io.EOF
		}
	}
	n := copy(p, l.buf)
	l.buf = l.buf[n:]
	return n, nil
}

func (l *largeReader) record() []byte {
	comma := ""
	if l.idx > 0 {
		comma = ","
	}
	status := "skip"
	if l.idx%2 == 0 {
		status = "keep"
	}
	payload := strings.Repeat("p", 512)
	s := fmt.Sprintf("%s{\"id\":%d,\"name\":\"item-%d\",\"payload\":\"%s\",\"status\":\"%s\"}", comma, l.idx, l.idx, payload, status)
	return []byte(s)
}

// TestBoundedMemory runs over a document much larger than the allowed heap
// growth and asserts correctness plus bounded retained memory. A
// whole-document decode of this input would not fit the same budget.
func TestBoundedMemory(t *testing.T) {
	const records = 20000 // roughly 12 MB including the 512-byte payloads
	p, err := Parse("$.records[?(@.status == \"keep\")].id")
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var ms1, ms2 runtime.MemStats
	runtime.ReadMemStats(&ms1)
	count := 0
	next := 0
	err = EvaluateReader(p, &largeReader{total: records}, func(m Match) error {
		if m.Value.(float64) != float64(next) {
			return fmt.Errorf("out of order: got %v want %d", m.Value, next)
		}
		next += 2
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := records / 2; count != want {
		t.Fatalf("got %d matches, want %d", count, want)
	}
	runtime.GC()
	runtime.ReadMemStats(&ms2)
	grew := int64(ms2.HeapAlloc) - int64(ms1.HeapAlloc)
	const budget = int64(4 * 1024 * 1024)
	if grew > budget {
		t.Fatalf("heap grew by %d bytes, exceeding budget %d", grew, budget)
	}
	t.Logf("records=%d matches=%d heapGrowth=%dKB", records, count, grew/1024)
}
