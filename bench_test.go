package jsonpathstream_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	jps "example.org/jsonpathstream"
)

// syntheticReader emits {"records":[ ... ]} one record at a time, holding no
// document-sized buffer.
type syntheticReader struct {
	n, i  int
	buf   []byte
	stage int
}

func (s *syntheticReader) Read(p []byte) (int, error) {
	if len(s.buf) == 0 {
		switch s.stage {
		case 0:
			s.buf = []byte("{\"records\":[")
			s.stage = 1
		case 1:
			if s.i >= s.n {
				s.buf = []byte("]}")
				s.stage = 2
			} else {
				comma := ","
				if s.i == 0 {
					comma = ""
				}
				status := "skip"
				if s.i%2 == 0 {
					status = "keep"
				}
				s.buf = []byte(fmt.Sprintf("%s{\"id\":%d,\"status\":\"%s\",\"payload\":\"%s\"}", comma, s.i, status, strings.Repeat("p", 256)))
				s.i++
			}
		default:
			return 0, io.EOF
		}
	}
	k := copy(p, s.buf)
	s.buf = s.buf[k:]
	return k, nil
}

func BenchmarkStreamSynthetic(b *testing.B) {
	p, _ := jps.Parse("$.records[?(@.status == \"keep\")].id")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var count int
		if err := jps.EvaluateReader(p, &syntheticReader{n: 20000}, func(jps.Match) error { count++; return nil }); err != nil {
			b.Fatal(err)
		}
		if count != 10000 {
			b.Fatalf("count=%d", count)
		}
	}
}

func benchDoc(b *testing.B, n int) []byte {
	type rec struct {
		ID      int    `json:"id"`
		Status  string `json:"status"`
		Payload string `json:"payload"`
	}
	recs := make([]rec, n)
	for i := range recs {
		st := "skip"
		if i%2 == 0 {
			st = "keep"
		}
		recs[i] = rec{ID: i, Status: st, Payload: string(bytes.Repeat([]byte("p"), 256))}
	}
	doc, err := json.Marshal(map[string]any{"records": recs})
	if err != nil {
		b.Fatal(err)
	}
	return doc
}

func BenchmarkDecodeAllFilter(b *testing.B) {
	doc := benchDoc(b, 20000)
	p, _ := jps.Parse("$.records[?(@.status == \"keep\")].id")
	b.ReportAllocs()
	b.SetBytes(int64(len(doc)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root, err := jps.DecodeAll(json.NewDecoder(bytes.NewReader(doc)))
		if err != nil {
			b.Fatal(err)
		}
		matches := jps.EvaluateAll(p, root)
		if len(matches) != 10000 {
			b.Fatalf("count=%d", len(matches))
		}
	}
}
