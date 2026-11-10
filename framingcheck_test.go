package main

import (
	"bytes"
	"encoding/json"
	"io"
	"runtime"
	"strings"
	"testing"
)

// slowReader returns data one byte at a time, forcing every protocol line and
// body to span many underlying reads.
type slowReader struct {
	data []byte
	pos  int
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

func fixedRequest(target, body string, extraHeaders ...string) []byte {
	var b strings.Builder
	b.WriteString("POST " + target + " HTTP/1.1\r\n")
	b.WriteString("Host: example.test\r\n")
	for _, h := range extraHeaders {
		b.WriteString(h)
		b.WriteString("\r\n")
	}
	b.WriteString("Content-Length: ")
	b.WriteString(itoa(len(body)))
	b.WriteString("\r\n\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

type chunkSpec struct {
	sizeLine string
	payload  []byte
}

func chunkedRequest(specs []chunkSpec, trailer string) []byte {
	var b strings.Builder
	b.WriteString("POST /c HTTP/1.1\r\n")
	b.WriteString("Host: example.test\r\n")
	b.WriteString("Transfer-Encoding: chunked\r\n\r\n")
	for _, spec := range specs {
		b.WriteString(spec.sizeLine)
		b.WriteString("\r\n")
		b.Write(spec.payload)
		b.WriteString("\r\n")
	}
	b.WriteString("0\r\n")
	if trailer != "" {
		b.WriteString(trailer)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return []byte(b.String())
}

func simpleChunks(payloads ...string) []chunkSpec {
	specs := make([]chunkSpec, 0, len(payloads))
	for _, p := range payloads {
		specs = append(specs, chunkSpec{sizeLine: itoa(len(p)), payload: []byte(p)})
	}
	return specs
}
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func mustOK(t *testing.T, data []byte) {
	t.Helper()
	f, err := Analyze(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Fatalf("expected OK, got finding %+v", f)
	}
}

func TestNormalPipeline(t *testing.T) {
	stream := append(
		append(
			fixedRequest("/a", "ping"),
			fixedRequest("/b", "pong-body")...,
		),
		chunkedRequest([]chunkSpec{
			{sizeLine: "4", payload: []byte("wiki")},
			{sizeLine: "5;\text=foo", payload: []byte("pedia")},
		}, "X-Trailer: yes")...,
	)
	mustOK(t, stream)
}

func TestPipelineAcrossReads(t *testing.T) {
	stream := append(
		fixedRequest("/a", "abc"),
		chunkedRequest(simpleChunks("hi"), "")...,
	)
	f, err := Analyze(&slowReader{data: stream})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Fatalf("expected OK across fragmented reads, got %+v", f)
	}
}

func TestConflictingContentLength(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "duplicate headers disagree",
			data: []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\nContent-Length: 6\r\n\r\n12345"),
		},
		{
			name: "comma list disagrees",
			data: []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 5, 6\r\n\r\n12345"),
		},
		{
			name: "non decimal value",
			data: []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 0x5\r\n\r\n"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Analyze(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f == nil {
				t.Fatal("expected a finding, got OK")
			}
			if f.Start != 0 || f.GatewayEnd != -1 || f.OriginEnd != -1 {
				t.Fatalf("unexpected finding: %+v", f)
			}
		})
	}
}

func TestRealBoundaryDivergence(t *testing.T) {
	// CL.TE: the gateway trusts Content-Length while the origin trusts the
	// chunked coding and stops at the zero chunk, splitting the stream earlier.
	clte := []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 13\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\nSMUGGLED")
	f, err := Analyze(bytes.NewReader(clte))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected CL.TE divergence, got OK")
	}
	if f.Start != 0 {
		t.Fatalf("start = %d, want 0", f.Start)
	}
	headEnd := int64(bytes.Index(clte, []byte("\r\n\r\n")) + 4)
	// Chunked body is "0\r\n\r\n": five bytes including the terminating
	// empty line, so the origin boundary is headEnd + 5.
	wantOriginEnd := headEnd + 5
	if f.OriginEnd != wantOriginEnd {
		t.Fatalf("originEnd = %d, want %d", f.OriginEnd, wantOriginEnd)
	}
	if f.GatewayEnd != int64(len(clte)) {
		t.Fatalf("gatewayEnd = %d, want %d", f.GatewayEnd, len(clte))
	}

	// A second, otherwise identical request starts exactly at the origin's
	// boundary: only the residual bytes cause the disagreement.
	residual := clte[headEnd+5:]
	if string(residual) != "SMUGGLED" {
		t.Fatalf("unexpected residual %q", residual)
	}
}

func TestTruncation(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "fixed body short",
			data: []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 8\r\n\r\nabc"),
		},
		{
			name: "chunk without terminator",
			data: []byte("POST /x HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n4\r\nab"),
		},
		{
			name: "partial header",
			data: []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Len"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Analyze(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f == nil || f.GatewayEnd != -1 || f.OriginEnd != -1 {
				t.Fatalf("expected both ends -1, got %+v", f)
			}
		})
	}
}

func TestChunkedFeatures(t *testing.T) {
	// Extensions, trailers, multiple chunks and binary chunk payloads.
	stream := chunkedRequest([]chunkSpec{
		{sizeLine: "4; name=x", payload: []byte("ab\x00\xff")},
		{sizeLine: "A", payload: []byte("0123456789")},
	}, "Digest: abc")
	mustOK(t, stream)

	// Missing empty line after the zero chunk is truncation for both ends.
	bad := []byte("POST /x HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nX-T: v\r\n")
	f, err := Analyze(bytes.NewReader(bad))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil || f.GatewayEnd != -1 || f.OriginEnd != -1 {
		t.Fatalf("expected truncation for both ends, got %+v", f)
	}
}

func TestOneSideError(t *testing.T) {
	// Gateway reads four fixed bytes successfully; the origin applies chunked
	// parsing to "ABCD" and fails on the non-hex chunk-size line.
	data := []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\nABCD\r\n\r\n")
	f, err := Analyze(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f == nil {
		t.Fatal("expected a finding, got OK")
	}
	headEnd := int64(bytes.Index(data, []byte("\r\n\r\n")) + 4)
	if f.GatewayEnd != headEnd+4 {
		t.Fatalf("gatewayEnd = %d, want %d", f.GatewayEnd, headEnd+4)
	}
	if f.OriginEnd != -1 {
		t.Fatalf("originEnd = %d, want -1", f.OriginEnd)
	}
}

func TestHeaderCaseAndTransferEncodingValidation(t *testing.T) {
	// Header names are case-insensitive, including comma-separated CL values.
	mustOK(t, []byte("POST /x HTTP/1.1\r\nhOsT: h\r\ncOnTeNt-LeNgTh: 7, 7\r\n\r\n1234567"))
	mustOK(t, []byte("POST /x HTTP/1.1\r\nHost: h\r\ntRANSFER-eNCODING: CHUNKED\r\n\r\n0\r\n\r\n"))

	bad := []struct {
		name string
		data []byte
	}{
		{"multiple TE headers", []byte("POST /x HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n")},
		{"TE list", []byte("POST /x HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: gzip, chunked\r\n\r\n0\r\n\r\n")},
		{"TE empty", []byte("POST /x HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: \r\n\r\n")},
		{"folded header", []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\n X: y\r\n\r\n12345")},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Analyze(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if f == nil || f.GatewayEnd != -1 || f.OriginEnd != -1 {
				t.Fatalf("expected shared header failure, got %+v", f)
			}
		})
	}
}

func TestEmptyAndJSONShape(t *testing.T) {
	mustOK(t, nil)
	mustOK(t, []byte("GET /a HTTP/1.1\r\nHost: h\r\n\r\nGET /b HTTP/1.0\r\nHost: h\r\n\r\n"))
	mustOK(t, []byte("POST /z HTTP/1.1\r\nHost: h\r\nContent-Length: 0\r\n\r\n"))

	data := []byte("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: 1\r\nTransfer-Encoding: chunked\r\n\r\nX")
	f, err := Analyze(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]int64
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"start", "gatewayEnd", "originEnd"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("JSON missing field %q in %s", key, raw)
		}
	}
}

func TestLargeStreamStaysBounded(t *testing.T) {
	const requestCount = 256
	const bodySize = 4 * 1024
	var stream []byte
	for i := 0; i < requestCount; i++ {
		stream = append(stream, fixedRequest("/big", strings.Repeat("z", bodySize))...)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	f, err := Analyze(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Fatalf("expected OK, got %+v", f)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	// The input itself is ~1 MiB; retained parser state must stay far below it.
	if growth > 8*1024*1024 {
		t.Fatalf("heap grew by %d bytes, parser is not streaming", growth)
	}
}
