package h2guard

import "testing"

func handleBlock(t *testing.T, p *Parser, streamID uint32, fields []HeaderField) (*Event, error) {
	t.Helper()
	block := hpackBlock(fields)
	return p.Handle(headersFrame(streamID, true, false, block))
}

// TestPseudoHeaderViolations covers duplicate pseudo-headers, pseudo-headers
// after regular headers and unknown pseudo-headers.
func TestPseudoHeaderViolations(t *testing.T) {
	base := func() []HeaderField {
		return []HeaderField{
			{Name: ":method", Value: "GET"},
			{Name: ":scheme", Value: "https"},
			{Name: ":path", Value: "/"},
			{Name: ":authority", Value: "example.com"},
		}
	}

	cases := []struct {
		name   string
		fields []HeaderField
	}{
		{
			name: "duplicate method",
			fields: append(base(),
				HeaderField{Name: ":method", Value: "GET"}),
		},
		{
			name: "duplicate path",
			fields: append(base(),
				HeaderField{Name: ":path", Value: "/dup"}),
		},
		{
			name: "pseudo after regular",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
				{Name: ":authority", Value: "example.com"},
				{Name: "x-normal", Value: "1"},
				{Name: ":method", Value: "POST"},
			},
		},
		{
			name: "unknown pseudo",
			fields: append(base(),
				HeaderField{Name: ":evil", Value: "1"}),
		},
		{
			name: "missing method",
			fields: []HeaderField{
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
				{Name: ":authority", Value: "example.com"},
			},
		},
		{
			name: "missing path",
			fields: []HeaderField{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "example.com"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(nil)
			_, err := handleBlock(t, p, 1, tc.fields)
			assertConnError(t, err, ErrCodeProtocol)
			assertGoAway(t, p, ErrCodeProtocol)
		})
	}
}

// TestFieldNameValueSmuggling covers uppercase names, CR/LF/NUL injection and
// forbidden hop-by-hop headers.
func TestFieldNameValueSmuggling(t *testing.T) {
	base := func(extra HeaderField) []HeaderField {
		return requestHeaders(extra)
	}

	cases := []struct {
		name   string
		fields []HeaderField
	}{
		{"uppercase name", base(HeaderField{Name: "X-Evil", Value: "1"})},
		{"mixed case content-length", base(HeaderField{Name: "Content-Length", Value: "1"})},
		{"cr in value", base(HeaderField{Name: "x-evil", Value: "a\rb"})},
		{"lf in value", base(HeaderField{Name: "x-evil", Value: "a\nb"})},
		{"crlf in value", base(HeaderField{Name: "x-evil", Value: "a\r\nb"})},
		{"nul in value", base(HeaderField{Name: "x-evil", Value: "a\x00b"})},
		{"nul in name", base(HeaderField{Name: "x-evil\x00", Value: "1"})},
		{"connection header", base(HeaderField{Name: "connection", Value: "keep-alive"})},
		{"transfer-encoding chunked", base(HeaderField{Name: "transfer-encoding", Value: "chunked"})},
		{"keep-alive header", base(HeaderField{Name: "keep-alive", Value: "timeout=5"})},
		{"upgrade header", base(HeaderField{Name: "upgrade", Value: "h2c"})},
		{"te with bad value", base(HeaderField{Name: "te", Value: "compress"})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(nil)
			_, err := handleBlock(t, p, 1, tc.fields)
			assertConnError(t, err, ErrCodeProtocol)
			assertGoAway(t, p, ErrCodeProtocol)
		})
	}
}

// TestTEtrailersAllowed is the one permitted TE form.
func TestTEtrailersAllowed(t *testing.T) {
	p := NewParser(nil)
	ev, err := handleBlock(t, p, 1, requestHeaders(HeaderField{Name: "te", Value: "trailers"}))
	if err != nil {
		t.Fatalf("TE: trailers must be accepted: %v", err)
	}
	if len(ev.Headers.Regular) != 1 {
		t.Fatalf("want 1 regular header, got %d", len(ev.Headers.Regular))
	}
}

// TestContentLengthSmuggling: declared Content-Length that disagrees with the
// actual DATA byte total at END_STREAM is a PROTOCOL_ERROR + GOAWAY. Both
// over- and under-declaration are covered.
func TestContentLengthSmuggling(t *testing.T) {
	cases := []struct {
		name  string
		cl    string
		data  [][]byte
		endAt int // index of DATA frame carrying END_STREAM
	}{
		{
			name:  "under declared, extra data hidden",
			cl:    "3",
			data:  [][]byte{[]byte("abc"), []byte("smuggled")},
			endAt: 1,
		},
		{
			name:  "over declared, short body",
			cl:    "10",
			data:  [][]byte{[]byte("abc")},
			endAt: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewParser(nil)
			_, err := handleBlock(t, p, 1, requestHeaders(
				HeaderField{Name: "content-length", Value: tc.cl}))
			if err != nil {
				t.Fatalf("headers rejected: %v", err)
			}

			var lastErr error
			for i, chunk := range tc.data {
				_, lastErr = p.Handle(dataFrame(1, i == tc.endAt, chunk))
				if lastErr != nil {
					break
				}
			}
			if lastErr == nil {
				t.Fatal("expected Content-Length smuggling to be rejected")
			}
			assertConnError(t, lastErr, ErrCodeProtocol)
			assertGoAway(t, p, ErrCodeProtocol)
		})
	}
}

// TestContentLengthExceededMidStream trips as soon as DATA exceeds the
// declared length, before END_STREAM arrives.
func TestContentLengthExceededMidStream(t *testing.T) {
	p := NewParser(nil)
	_, err := handleBlock(t, p, 1, requestHeaders(
		HeaderField{Name: "content-length", Value: "4"}))
	if err != nil {
		t.Fatalf("headers: %v", err)
	}
	if _, err := p.Handle(dataFrame(1, false, []byte("abcd"))); err != nil {
		t.Fatalf("matching DATA rejected: %v", err)
	}
	_, err = p.Handle(dataFrame(1, false, []byte("X")))
	assertConnError(t, err, ErrCodeProtocol)
	assertGoAway(t, p, ErrCodeProtocol)
}

// TestContentLengthHappyPath confirms exact matching DATA is accepted.
func TestContentLengthHappyPath(t *testing.T) {
	p := NewParser(nil)
	_, err := handleBlock(t, p, 1, requestHeaders(
		HeaderField{Name: "content-length", Value: "5"}))
	if err != nil {
		t.Fatalf("headers: %v", err)
	}
	if _, err := p.Handle(dataFrame(1, false, []byte("ab"))); err != nil {
		t.Fatalf("first DATA: %v", err)
	}
	ev, err := p.Handle(dataFrame(1, true, []byte("cde")))
	if err != nil {
		t.Fatalf("terminal DATA: %v", err)
	}
	if !ev.EndStream {
		t.Fatal("END_STREAM flag lost")
	}
}

// TestConflictingContentLength rejects duplicate differing values.
func TestConflictingContentLength(t *testing.T) {
	p := NewParser(nil)
	_, err := handleBlock(t, p, 1, requestHeaders(
		HeaderField{Name: "content-length", Value: "5"},
		HeaderField{Name: "content-length", Value: "6"}))
	assertConnError(t, err, ErrCodeProtocol)
	assertGoAway(t, p, ErrCodeProtocol)
}
