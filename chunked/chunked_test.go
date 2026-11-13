package chunked_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"example.org/netsec/q0637/chunked"
)

func TestDecodeValidChunked(t *testing.T) {
	const input = "4\r\nWiki\r\n5\r\npedia\r\n0\r\n\r\n"

	body, err := chunked.Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got, want := string(body), "Wikipedia"; got != want {
		t.Fatalf("Decode() body = %q, want %q", got, want)
	}
}

func TestDecodeValidChunkExtensionsAndTrailers(t *testing.T) {
	const input = "4;foo=bar;x=\"a;b \\\"c\\\"\"\r\nWiki\r\n" +
		"5\r\npedia\r\n" +
		"0;done=yes\r\nX-Checksum: abc\r\n\r\n"

	body, trailers, err := chunked.DecodeWithTrailers(strings.NewReader(input))
	if err != nil {
		t.Fatalf("DecodeWithTrailers() error = %v", err)
	}
	if got, want := string(body), "Wikipedia"; got != want {
		t.Fatalf("DecodeWithTrailers() body = %q, want %q", got, want)
	}
	if got, want := trailers.Get("X-Checksum"), "abc"; got != want {
		t.Fatalf("Trailer X-Checksum = %q, want %q", got, want)
	}
}

func TestDecodeAllowsLiteralBackslashNAndLFInChunkData(t *testing.T) {
	// The extension contains the two bytes '\' and 'n', not a newline.
	// The chunk payload itself may contain LF because it is not line-framed.
	const input = "4;x=\"a\\nb\"\r\na\nb\n\r\n0\r\n\r\n"

	body, err := chunked.Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got, want := string(body), "a\nb\n"; got != want {
		t.Fatalf("Decode() body = %q, want %q", got, want)
	}
}

func TestRejectNewlineInQuotedChunkExtension(t *testing.T) {
	const input = "1;x=\"a\nb\"\r\nZ\r\n0\r\n\r\nGET /admin HTTP/1.1\r\nHost: example.com\r\n\r\n"

	_, err := chunked.Decode(strings.NewReader(input))
	if !errors.Is(err, chunked.ErrMalformed) {
		t.Fatalf("Decode() error = %v, want ErrMalformed", err)
	}
}

func TestRejectBareLF(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "chunk size line",
			input: "1\nZ\r\n0\r\n\r\nGET /admin HTTP/1.1\r\nHost: example.com\r\n\r\n",
		},
		{
			name:  "chunk data terminator",
			input: "1\r\nZ\n0\r\n\r\nGET /admin HTTP/1.1\r\nHost: example.com\r\n\r\n",
		},
		{
			name:  "last chunk line",
			input: "0\n\r\nGET /admin HTTP/1.1\r\nHost: example.com\r\n\r\n",
		},
		{
			name:  "trailer line",
			input: "0\r\nX-Test: ok\n\r\nGET /admin HTTP/1.1\r\nHost: example.com\r\n\r\n",
		},
		{
			name:  "CR not followed by LF",
			input: "1\rZ\r\n0\r\n\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := chunked.Decode(strings.NewReader(tt.input))
			if !errors.Is(err, chunked.ErrMalformed) {
				t.Fatalf("Decode() error = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestRejectNonHexChunkSize(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "0x prefix", input: "0x10\r\n0123456789abcdef\r\n0\r\n\r\n"},
		{name: "plus sign", input: "+10\r\n0123456789abcdef\r\n0\r\n\r\n"},
		{name: "negative", input: "-1\r\nZ\r\n0\r\n\r\n"},
		{name: "leading space", input: " 10\r\n0123456789abcdef\r\n0\r\n\r\n"},
		{name: "trailing space", input: "10 \r\n0123456789abcdef\r\n0\r\n\r\n"},
		{name: "trailing tab", input: "10\t\r\n0123456789abcdef\r\n0\r\n\r\n"},
		{name: "non-hex letter", input: "g\r\nZ\r\n0\r\n\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := chunked.Decode(strings.NewReader(tt.input))
			if !errors.Is(err, chunked.ErrMalformed) {
				t.Fatalf("Decode() error = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestChunkedReaderRejectsTruncation(t *testing.T) {
	for _, input := range []string{"1\r\n", "1\r\nZ", "1\r\nZ\r\n", "0\r\n"} {
		reader := chunked.NewReader(strings.NewReader(input))
		if _, err := io.ReadAll(reader); !errors.Is(err, chunked.ErrUnexpectedEOF) {
			t.Fatalf("ReadAll(%q) error = %v, want ErrUnexpectedEOF", input, err)
		}
	}
}

func TestRejectConflictingContentLengthAndTransferEncoding(t *testing.T) {
	req := &http.Request{
		Header: http.Header{
			"Content-Length":    {"5"},
			"Transfer-Encoding": {"chunked"},
		},
	}

	err := chunked.ValidateRequest(req)
	if !errors.Is(err, chunked.ErrConflictingFraming) {
		t.Fatalf("ValidateRequest() error = %v, want ErrConflictingFraming", err)
	}
	if !chunked.HasConflictingFraming(req.Header) {
		t.Fatal("HasConflictingFraming() = false, want true")
	}
}

func TestValidateRequestHeaderNamesAreCaseInsensitive(t *testing.T) {
	req := &http.Request{
		Header: http.Header{
			"content-length":    {"5"},
			"transfer-encoding": {"chunked"},
		},
	}

	err := chunked.ValidateRequest(req)
	if !errors.Is(err, chunked.ErrConflictingFraming) {
		t.Fatalf("ValidateRequest() error = %v, want ErrConflictingFraming", err)
	}
}

func TestValidateRequestRejectsStructuredContentLengthField(t *testing.T) {
	req := &http.Request{
		ContentLength:    5,
		TransferEncoding: []string{"chunked"},
	}

	err := chunked.ValidateRequest(req)
	if !errors.Is(err, chunked.ErrConflictingFraming) {
		t.Fatalf("ValidateRequest() error = %v, want ErrConflictingFraming", err)
	}
}

func TestValidateRequestAllowsUnambiguousFraming(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
	}{
		{name: "content length only", header: http.Header{"Content-Length": {"5"}}},
		{name: "chunked only", header: http.Header{"Transfer-Encoding": {"chunked"}}},
		{name: "chunked after another coding", header: http.Header{"Transfer-Encoding": {"gzip, chunked"}}},
		{name: "no framing headers", header: http.Header{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := chunked.ValidateHeaders(tt.header); err != nil {
				t.Fatalf("ValidateHeaders() error = %v", err)
			}
		})
	}
}

func TestValidateRequestRejectsNonChunkedTransferEncoding(t *testing.T) {
	for _, value := range []string{"gzip", "chunked, gzip", "chunked, chunked"} {
		t.Run(value, func(t *testing.T) {
			err := chunked.ValidateHeaders(http.Header{"Transfer-Encoding": {value}})
			if !errors.Is(err, chunked.ErrUnsupportedTransferEncoding) {
				t.Fatalf("ValidateHeaders() error = %v, want ErrUnsupportedTransferEncoding", err)
			}
		})
	}
}
