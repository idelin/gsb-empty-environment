package streamsel

import (
	"fmt"
	"strings"
	"testing"
)

// benchDoc generates a catalog page with n items, ~120 bytes each.
func benchDoc(n int) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html><head><title>catalog</title></head><body>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb,
			`<div class="item" data-id="%d"><span class="price">$%d.99</span><p>item %d &amp; friends</p></div>`,
			i, i%100, i)
	}
	sb.WriteString(`</body></html>`)
	return sb.String()
}

const benchSel = "div.item > span.price"

var benchHTML = benchDoc(20000) // ~2 MB

// BenchmarkStreaming feeds the document in 32 KiB chunks and never holds
// more than the current element stack.
func BenchmarkStreaming(b *testing.B) {
	const chunk = 32 << 10
	b.ReportAllocs()
	b.SetBytes(int64(len(benchHTML)))
	for i := 0; i < b.N; i++ {
		m, err := NewMatcher(benchSel)
		if err != nil {
			b.Fatal(err)
		}
		n := 0
		for off := 0; off < len(benchHTML); off += chunk {
			end := off + chunk
			if end > len(benchHTML) {
				end = len(benchHTML)
			}
			n += len(m.Write([]byte(benchHTML[off:end])))
		}
		n += len(m.Close())
		if n == 0 {
			b.Fatal("no matches")
		}
	}
}

// BenchmarkNaive builds the full document tree and then queries it.
func BenchmarkNaive(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchHTML)))
	for i := 0; i < b.N; i++ {
		root := ParseDOM(benchHTML)
		out, err := QueryAll(root, benchSel)
		if err != nil {
			b.Fatal(err)
		}
		if len(out) == 0 {
			b.Fatal("no matches")
		}
	}
}

// BenchmarkStreamingByteChunks stresses the chunk-boundary logic with tiny
// 7-byte writes.
func BenchmarkStreamingByteChunks(b *testing.B) {
	const chunk = 7
	b.ReportAllocs()
	b.SetBytes(int64(len(benchHTML)))
	for i := 0; i < b.N; i++ {
		m, err := NewMatcher(benchSel)
		if err != nil {
			b.Fatal(err)
		}
		for off := 0; off < len(benchHTML); off += chunk {
			end := off + chunk
			if end > len(benchHTML) {
				end = len(benchHTML)
			}
			m.Write([]byte(benchHTML[off:end]))
		}
		m.Close()
	}
}
