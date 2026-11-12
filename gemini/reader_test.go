package gemini

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

var testConfig = Config{Host: "example.com", Port: 1965}

var errExtraRead = errors.New("unexpected Read after the request was complete")

// chunkReader replays fixed chunks. A Read past the last chunk fails with
// errExtraRead unless allowEOF is set, proving that the reader stops touching
// the connection as soon as the request line is framed.
type chunkReader struct {
	chunks   [][]byte
	next     int
	reads    int
	allowEOF bool
}

func (r *chunkReader) Read(p []byte) (int, error) {
	r.reads++
	if r.next >= len(r.chunks) {
		if r.allowEOF {
			return 0, io.EOF
		}
		return 0, errExtraRead
	}
	chunk := r.chunks[r.next]
	if len(chunk) > len(p) {
		r.chunks[r.next] = chunk[len(p):]
		chunk = chunk[:len(p)]
	} else {
		r.next++
	}
	return copy(p, chunk), nil
}

func split(data []byte, sizes ...int) *chunkReader {
	var chunks [][]byte
	pos, i := 0, 0
	for pos < len(data) {
		size := sizes[i%len(sizes)]
		i++
		if size > len(data)-pos {
			size = len(data) - pos
		}
		chunks = append(chunks, append([]byte(nil), data[pos:pos+size]...))
		pos += size
	}
	return &chunkReader{chunks: chunks, allowEOF: true}
}

func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	var ge *Error
	if !errors.As(err, &ge) {
		t.Fatalf("want *gemini.Error with status %d, got %v", status, err)
	}
	if ge.Status != status {
		t.Fatalf("want status %d, got %d (%q)", status, ge.Status, ge.Meta)
	}
}

func TestValidRequestsArbitraryChunks(t *testing.T) {
	inputs := map[string]string{
		"root":         "gemini://example.com/\r\n",
		"nested":       "gemini://example.com/a/b/c\r\n",
		"with query":   "gemini://example.com/search?q=hello\r\n",
		"dot cleanup":  "gemini://example.com/a/./b/../c/\r\n",
		"no path":      "gemini://example.com\r\n",
		"ipv6":         "gemini://[::1]/\r\n",
		"custom port":  "gemini://example.com:1337/\r\n",
		"encoded safe": "gemini://example.com/a/%2e%2e/b\r\n",
	}
	wantPath := map[string]string{
		"root":         "/",
		"nested":       "/a/b/c",
		"with query":   "/search",
		"dot cleanup":  "/a/c/",
		"no path":      "/",
		"ipv6":         "/",
		"custom port":  "/",
		"encoded safe": "/b",
	}
	configs := map[string]Config{
		"ipv6":        {Host: "::1", Port: 1965},
		"custom port": {Host: "example.com", Port: 1337},
	}

	for name, input := range inputs {
		cfg, ok := configs[name]
		if !ok {
			cfg = testConfig
		}
		for _, size := range []int{1, 2, 3, 5, 7, 16, 64, 512, 1024} {
			reader := split([]byte(input), size)
			req, err := ReadRequest(reader, cfg)
			if err != nil {
				t.Fatalf("%s (chunk size %d): %v", name, size, err)
			}
			if req.Path != wantPath[name] {
				t.Fatalf("%s (chunk size %d): path = %q, want %q",
					name, size, req.Path, wantPath[name])
			}
		}
	}
}

func TestFramingErrors(t *testing.T) {
	cases := map[string]string{
		"bare LF":         "gemini://example.com/\n",
		"bare CR midline": "gemini://example.com/\rfoo\r\n",
		"no terminator":   "gemini://example.com/",
		"empty line":      "\r\n",
		"space in URL":    "gemini://example.com/ bad\r\n",
		"control in URL":  "gemini://example.com/\x01\r\n",
	}
	for name, input := range cases {
		for _, size := range []int{1, 4, 33} {
			reader := split([]byte(input), size)
			_, err := ReadRequest(reader, testConfig)
			if err == nil {
				t.Fatalf("%s (chunk size %d): expected 59, got success", name, size)
			}
			wantStatus(t, err, 59)
		}
	}
}

func TestCRLFSplitAcrossReads(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{
		[]byte("gemini://example.com/"),
		[]byte("\r"),
		[]byte("\n"),
		[]byte("gemini://evil.example.org/\r\n"),
	}}
	req, err := ReadRequest(reader, testConfig)
	if err != nil {
		t.Fatalf("CRLF split across reads: %v", err)
	}
	if req.Path != "/" {
		t.Fatalf("path = %q, want /", req.Path)
	}
	if reader.reads != 3 {
		t.Fatalf("reads = %d, want 3 (smuggled chunk must never be read)", reader.reads)
	}
}

func TestAppendedRequestInSameRead(t *testing.T) {
	first := "gemini://example.com/\r\n"
	smuggled := "gemini://evil.example.org/admin?x=1\r\n"
	reader := &chunkReader{chunks: [][]byte{[]byte(first + smuggled)}}

	req, err := ReadRequest(reader, testConfig)
	if err != nil {
		t.Fatalf("appended request: %v", err)
	}
	if req.Host != "example.com" || req.Path != "/" {
		t.Fatalf("got host=%q path=%q, want example.com /", req.Host, req.Path)
	}
	if reader.reads != 1 {
		t.Fatalf("reads = %d, want 1; bytes after CRLF must be discarded", reader.reads)
	}
}

func TestSchemeUserinfoFragment(t *testing.T) {
	cases := map[string]string{
		"https scheme":   "https://example.com/\r\n",
		"relative URL":   "/path\r\n",
		"userinfo":       "gemini://user:pass@example.com/\r\n",
		"fragment":       "gemini://example.com/path#frag\r\n",
		"empty fragment": "gemini://example.com/path#\r\n",
	}
	for name, input := range cases {
		_, err := ReadRequest(split([]byte(input), 5), testConfig)
		if err == nil {
			t.Fatalf("%s: expected 59, got success", name)
		}
		wantStatus(t, err, 59)
	}
}

func TestHostPortMismatch(t *testing.T) {
	cases := map[string]string{
		"wrong host":       "gemini://other.example.com/\r\n",
		"wrong port":       "gemini://example.com:1966/\r\n",
		"missing port cfg": "gemini://example.com/\r\n",
	}
	configs := map[string]Config{
		"wrong host":       testConfig,
		"wrong port":       testConfig,
		"missing port cfg": {Host: "example.com", Port: 1337},
	}
	for name, input := range cases {
		_, err := ReadRequest(split([]byte(input), 7), configs[name])
		if err == nil {
			t.Fatalf("%s: expected 53, got success", name)
		}
		wantStatus(t, err, 53)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	cases := map[string]string{
		"plain dotdot":         "gemini://example.com/a/../../etc/passwd\r\n",
		"encoded dotdot":       "gemini://example.com/%2e%2e/%2e%2e/etc/passwd\r\n",
		"encoded dotdot slash": "gemini://example.com/%2e%2e%2f%2e%2e%2fetc\r\n",
		"mixed encoding":       "gemini://example.com/a/..%2f..%2fetc\r\n",
		"encoded slash":        "gemini://example.com/a%2f..%2f..%2fetc\r\n",
		"root dotdot":          "gemini://example.com/../etc\r\n",
		"all encoded root":     "gemini://example.com/%2e%2e%2fetc\r\n",
	}
	for name, input := range cases {
		_, err := ReadRequest(split([]byte(input), 3), testConfig)
		if err == nil {
			t.Fatalf("%s: expected traversal rejection 59, got success", name)
		}
		wantStatus(t, err, 59)
	}
}

func TestDotSegmentsNormalizedWithoutEscape(t *testing.T) {
	cases := map[string]string{
		"gemini://example.com/a/b/../../x\r\n":  "/x",
		"gemini://example.com/a/%2e%2e/\r\n":    "/",
		"gemini://example.com/a/./b/./c\r\n":    "/a/b/c",
		"gemini://example.com/a%2Fb%2Fc\r\n":    "/a/b/c",
		"gemini://example.com/a%2F..%2Fetc\r\n": "/etc",
		"gemini://example.com/dir/\r\n":         "/dir/",
	}
	for input, want := range cases {
		req, err := ReadRequest(split([]byte(input), 11), testConfig)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if req.Path != want {
			t.Fatalf("%q: path = %q, want %q", input, req.Path, want)
		}
	}
}

func TestMalformedPercentEncoding(t *testing.T) {
	for _, input := range []string{
		"gemini://example.com/%zz\r\n",
		"gemini://example.com/%2\r\n",
		"gemini://example.com/%\r\n",
	} {
		_, err := ReadRequest(split([]byte(input), 2), testConfig)
		wantStatus(t, err, 59)
	}
}

func TestMissingHostAndInvalidPort(t *testing.T) {
	_, err := ReadRequest(split([]byte("gemini:///path\r\n"), 4), testConfig)
	wantStatus(t, err, 53)

	_, err = ReadRequest(split([]byte("gemini://example.com:notaport/\r\n"), 6), testConfig)
	wantStatus(t, err, 59)
}

func TestOverlongLines(t *testing.T) {
	// 1025-byte URL line plus CRLF: rejected as soon as byte 1027 is read.
	longLine := "gemini://example.com/" + strings.Repeat("x", 1024)
	if _, err := ReadRequest(split([]byte(longLine+"\r\n"), 400), testConfig); err == nil {
		t.Fatal("1025-byte URL: expected 59, got success")
	} else {
		wantStatus(t, err, 59)
	}

	// 1027 bytes with no terminator: size limit fires before EOF.
	noTerm := bytes.Repeat([]byte("a"), MaxRequest+1)
	if _, err := ReadRequest(split(noTerm, 512), testConfig); err == nil {
		t.Fatal("1027 unterminated bytes: expected 59, got success")
	} else {
		wantStatus(t, err, 59)
	}

	// Exactly 1026 bytes without CRLF: EOF, still a framing error.
	shortEOF := bytes.Repeat([]byte("a"), MaxRequest)
	_, err := ReadRequest(split(shortEOF, 333), testConfig)
	wantStatus(t, err, 59)
}

func TestExactMaxURLAccepted(t *testing.T) {
	base := "gemini://example.com/"
	line := base + strings.Repeat("x", MaxURL-len(base))
	if len(line) != MaxURL {
		t.Fatalf("test setup: line length %d", len(line))
	}
	req, err := ReadRequest(split([]byte(line+"\r\n"), 127), testConfig)
	if err != nil {
		t.Fatalf("max-length URL: %v", err)
	}
	if want := "/" + strings.Repeat("x", MaxURL-len(base)); req.Path != want {
		t.Fatalf("path length = %d, want %d", len(req.Path), len(want))
	}
}

func TestQueryPreservedAndPortExposed(t *testing.T) {
	req, err := ReadRequest(
		split([]byte("gemini://example.com:1965/foo?q=%2e%2e\r\n"), 9),
		testConfig,
	)
	if err != nil {
		t.Fatalf("query request: %v", err)
	}
	if req.Port != 1965 || req.Host != "example.com" {
		t.Fatalf("host=%q port=%d", req.Host, req.Port)
	}
	if req.URL.RawQuery != "q=%2e%2e" || req.Path != "/foo" {
		t.Fatalf("query=%q path=%q", req.URL.RawQuery, req.Path)
	}
}

func TestWriteStatusAndError(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteStatus(&buf, 59, "bad\r\nmeta"); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "59 bad  meta\r\n" {
		t.Fatalf("status line = %q", got)
	}

	buf.Reset()
	if err := WriteError(&buf, proxyRefused("nope")); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "53 nope\r\n" {
		t.Fatalf("error line = %q", got)
	}
}
