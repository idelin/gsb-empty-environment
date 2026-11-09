package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func runString(t *testing.T, in io.Reader) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := run(in, &out)
	return out.String(), code
}

// oneByteReader forces every Read to return at most one byte, simulating
// arbitrarily fragmented TCP delivery.
type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return o.r.Read(p)
}

func TestNormalPipeline(t *testing.T) {
	stream := "POST /a HTTP/1.1\r\n" +
		"Content-Length: 5\r\n" +
		"\r\n" +
		"hello" +
		"GET /b HTTP/1.1\r\n" +
		"Host: example\r\n" +
		"\r\n" +
		"POST /c HTTP/1.1\r\n" +
		"Transfer-Encoding: chunked\r\n" +
		"\r\n" +
		"3\r\nabc\r\n" +
		"2;x=y\r\nde\r\n" +
		"0\r\n" +
		"X-Trailer: done\r\n" +
		"\r\n"
	got, code := runString(t, strings.NewReader(stream))
	if got != "OK\n" || code != 0 {
		t.Fatalf("got %q (code %d), want OK", got, code)
	}
}

func TestChunkedAcrossReads(t *testing.T) {
	stream := "POST /upload HTTP/1.1\r\n" +
		"transfer-encoding: Chunked\r\n" +
		"\r\n" +
		"a;foo=bar;baz\r\n0123456789\r\n" +
		"0\r\n" +
		"\r\n" +
		"GET /next HTTP/1.1\r\n" +
		"\r\n"
	got, code := runString(t, oneByteReader{strings.NewReader(stream)})
	if got != "OK\n" || code != 0 {
		t.Fatalf("got %q (code %d), want OK", got, code)
	}
}

func TestConflictingContentLengths(t *testing.T) {
	cases := []string{
		"POST / HTTP/1.1\r\nContent-Length: 5\r\nContent-Length: 6\r\n\r\nhello!",
		"POST / HTTP/1.1\r\nContent-Length: 5, 6\r\n\r\nhello!",
		"POST / HTTP/1.1\r\nContent-Length: 5\r\nContent-Length: 5, 6\r\n\r\nhello!",
	}
	for _, stream := range cases {
		got, code := runString(t, strings.NewReader(stream))
		want := `{"start":0,"gatewayEnd":-1,"originEnd":-1}` + "\n"
		if got != want || code != 1 {
			t.Fatalf("stream %q: got %q (code %d), want %q", stream, got, code, want)
		}
	}
}

func TestEqualDuplicateContentLengths(t *testing.T) {
	stream := "POST / HTTP/1.1\r\n" +
		"Content-Length: 5\r\n" +
		"content-LENGTH: 5, 05\r\n" +
		"\r\n" +
		"hello"
	got, code := runString(t, strings.NewReader(stream))
	if got != "OK\n" || code != 0 {
		t.Fatalf("got %q (code %d), want OK", got, code)
	}
}

func TestBoundaryDivergence(t *testing.T) {
	head := "POST / HTTP/1.1\r\n" +
		"Content-Length: 6\r\n" +
		"Transfer-Encoding: chunked\r\n" +
		"\r\n"
	// Gateway reads 6 bytes ("0\r\n\r\nG"); origin sees a zero chunk plus an
	// empty trailer line and stops after 5 bytes, leaving "GET ..." as the
	// next request.
	stream := head + "0\r\n\r\nGET / HTTP/1.1\r\n\r\n"
	got, code := runString(t, strings.NewReader(stream))
	bodyStart := len(head)
	want := jsonLine(0, int64(bodyStart+6), int64(bodyStart+5))
	if got != want || code != 1 {
		t.Fatalf("got %q (code %d), want %q", got, code, want)
	}
}

func TestBoundaryDivergenceAfterCleanRequests(t *testing.T) {
	first := "GET /ok HTTP/1.1\r\nContent-Length: 3\r\n\r\nabc"
	head := "POST / HTTP/1.1\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n"
	stream := first + head + "0\r\n\r\n"
	got, code := runString(t, strings.NewReader(stream))
	start := int64(len(first))
	bodyStart := start + int64(len(head))
	// Gateway consumes 4 bytes ("0\r\n\r"); origin consumes the full
	// 5-byte chunked terminator "0\r\n\r\n".
	want := jsonLine(start, bodyStart+4, bodyStart+5)
	if got != want || code != 1 {
		t.Fatalf("got %q (code %d), want %q", got, code, want)
	}
}

func TestPoliciesAgreeByCoincidence(t *testing.T) {
	// CL=5 covers exactly the bytes of "0\r\n\r\n", so both policies end at
	// the same offset and pipelining continues.
	head := "POST / HTTP/1.1\r\nContent-Length: 5\r\nTransfer-Encoding: chunked\r\n\r\n"
	stream := head + "0\r\n\r\nGET /next HTTP/1.1\r\n\r\n"
	got, code := runString(t, strings.NewReader(stream))
	if got != "OK\n" || code != 0 {
		t.Fatalf("got %q (code %d), want OK", got, code)
	}
}

func TestInvalidTransferEncoding(t *testing.T) {
	cases := []string{
		"POST / HTTP/1.1\r\nTransfer-Encoding: gzip\r\n\r\n",
		"POST / HTTP/1.1\r\nTransfer-Encoding: chunked, chunked\r\n\r\n",
		"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n",
	}
	for _, stream := range cases {
		got, code := runString(t, strings.NewReader(stream))
		want := `{"start":0,"gatewayEnd":-1,"originEnd":-1}` + "\n"
		if got != want || code != 1 {
			t.Fatalf("stream %q: got %q (code %d), want %q", stream, got, code, want)
		}
	}
}

func TestTruncatedBody(t *testing.T) {
	stream := "POST / HTTP/1.1\r\nContent-Length: 10\r\n\r\nabc"
	got, code := runString(t, strings.NewReader(stream))
	want := `{"start":0,"gatewayEnd":-1,"originEnd":-1}` + "\n"
	if got != want || code != 1 {
		t.Fatalf("got %q (code %d), want %q", got, code, want)
	}
}

func TestTruncatedChunkedMixed(t *testing.T) {
	// Origin (chunked) hits a truncated chunk; gateway still has enough
	// bytes for its Content-Length.
	head := "POST / HTTP/1.1\r\nContent-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n"
	stream := head + "5\r\nabc"
	got, code := runString(t, strings.NewReader(stream))
	want := jsonLine(0, int64(len(head))+3, -1)
	if got != want || code != 1 {
		t.Fatalf("got %q (code %d), want %q", got, code, want)
	}
}

func TestBinaryBody(t *testing.T) {
	body := []byte{0x00, 0xff, '\r', '\n', '\r', 0x10}
	var stream bytes.Buffer
	stream.WriteString("POST /bin HTTP/1.1\r\nContent-Length: 6\r\n\r\n")
	stream.Write(body)
	stream.WriteString("GET / HTTP/1.1\r\n\r\n")
	got, code := runString(t, &stream)
	if got != "OK\n" || code != 0 {
		t.Fatalf("got %q (code %d), want OK", got, code)
	}
}

func TestEmptyStream(t *testing.T) {
	got, code := runString(t, strings.NewReader(""))
	if got != "OK\n" || code != 0 {
		t.Fatalf("got %q (code %d), want OK", got, code)
	}
}

func jsonLine(start, gatewayEnd, originEnd int64) string {
	b, _ := json.Marshal(result{start, gatewayEnd, originEnd})
	return string(b) + "\n"
}
