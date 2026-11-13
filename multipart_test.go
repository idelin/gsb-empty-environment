package netsec

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// helper: drain every part and return their bodies in order.
func readAllBodies(t *testing.T, contentType string, body string) ([][]byte, error) {
	t.Helper()
	boundary, err := Boundary(contentType)
	if err != nil {
		return nil, err
	}
	mr, err := NewReader(strings.NewReader(body), boundary)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		data, err := io.ReadAll(part)
		if err != nil {
			return out, err
		}
		out = append(out, data)
	}
}

// An ordinary unquoted multipart document yields every part's body, and
// content that resembles a boundary never bleeds into the next part header.
func TestPlainMultipartParts(t *testing.T) {
	contentType := `multipart/mixed; boundary=simpleBoundary`
	body := "--simpleBoundary\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"first body line\r\n-- not a boundary\r\n" +
		"\r\n--simpleBoundary\r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"second body\r\n" +
		"\r\n--simpleBoundary--\r\n"

	bodies, err := readAllBodies(t, contentType, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("got %d parts, want 2", len(bodies))
	}
	wantFirst := "first body line\r\n-- not a boundary\r\n"
	if string(bodies[0]) != wantFirst {
		t.Errorf("first body = %q, want %q", bodies[0], wantFirst)
	}
	if string(bodies[1]) != "second body\r\n" {
		t.Errorf("second body = %q, want %q", bodies[1], "second body\r\n")
	}
}

// A quoted boundary parameter must match unquoted delimiter lines. The
// delimiter line additionally carries trailing space padding, which is
// transport padding rather than part content.
func TestQuotedBoundaryMatchesUnquotedDelimiter(t *testing.T) {
	contentType := `multipart/mixed; boundary="simpleBoundary"`
	body := "--simpleBoundary \r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"alpha\r\n" +
		"\r\n--simpleBoundary \r\n" +
		"Content-Type: text/plain\r\n\r\n" +
		"beta\r\n" +
		"\r\n--simpleBoundary-- \t \r\n"

	bodies, err := readAllBodies(t, contentType, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("got %d parts, want 2", len(bodies))
	}
	if string(bodies[0]) != "alpha\r\n" {
		t.Errorf("first body = %q, want %q", bodies[0], "alpha\r\n")
	}
	if string(bodies[1]) != "beta\r\n" {
		t.Errorf("second body = %q, want %q", bodies[1], "beta\r\n")
	}
}

// A body line that looks like a delimiter but continues with non-space
// characters is ordinary content: it must not terminate the part.
func TestLongerBoundaryLikeLineIsBody(t *testing.T) {
	contentType := `multipart/mixed; boundary="abc"`
	body := "--abc\r\n\r\n" +
		"--abcdef is body\r\n" +
		"--abcX\r\n" +
		"\r\n--abc--\r\n"

	bodies, err := readAllBodies(t, contentType, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("got %d parts, want 1", len(bodies))
	}
	want := "--abcdef is body\r\n--abcX\r\n"
	if string(bodies[0]) != want {
		t.Errorf("body = %q, want %q", bodies[0], want)
	}
}

func TestBoundaryValidation(t *testing.T) {
	cases := []struct {
		name     string
		boundary string
		wantErr  bool
	}{
		{"empty", "", true},
		{"contains space", "abc def", true},
		{"contains tab", "abc\tdef", true},
		{"control char", "abc\x00def", true},
		{"valid punctuation", "a'()+_,-./:=?", false},
		{"max length", strings.Repeat("a", 70), false},
		{"too long", strings.Repeat("a", 71), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBoundary(tc.boundary)
			if tc.wantErr && !errors.Is(err, ErrInvalidBoundary) {
				t.Fatalf("got %v, want ErrInvalidBoundary", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestPaddingRules(t *testing.T) {
	first := "--b\r\n\r\nbody\r\n"
	second := "\r\n--b\r\n\r\nother\r\n"
	// Padded part delimiters must be followed by another part; the closing
	// delimiter may carry padding and end the stream.
	legal := []string{
		first + "\r\n--b--\r\n",
		first + "\r\n--b-- \t \r\n",
		first + "\r\n--b \t \r\n" + second + "\r\n--b--\r\n",
		first + "\r\n--b\t\r\n" + second + "\r\n--b--\r\n",
	}
	for _, suffix := range legal {
		if _, err := readAllBodies(t, `multipart/mixed; boundary=b`, suffix); err != nil {
			t.Errorf("legal document %q rejected: %v", suffix, err)
		}
	}

	illegal := []string{
		first + "\r\n--b x\r\n",   // non-whitespace after padding
		first + "\r\n--b \rx\r\n", // CR not followed directly by LF
		first + "\r\n--b \nx\r\n", // bare LF inside the delimiter line
		first + "\r\n--b- \r\n",   // single dash then junk
	}
	for _, suffix := range illegal {
		if _, err := readAllBodies(t, `multipart/mixed; boundary=b`, suffix); err == nil {
			t.Errorf("illegal document %q accepted", suffix)
		}
	}
}

func TestBoundaryParameterErrors(t *testing.T) {
	if _, err := Boundary(`text/plain`); !errors.Is(err, ErrNotMultipart) {
		t.Errorf("non-multipart: got %v, want ErrNotMultipart", err)
	}
	if _, err := Boundary(`multipart/mixed`); !errors.Is(err, ErrMissingBoundary) {
		t.Errorf("missing boundary: got %v, want ErrMissingBoundary", err)
	}
	if _, err := Boundary(`multipart/mixed; boundary="bad boundary"`); !errors.Is(err, ErrInvalidBoundary) {
		t.Errorf("quoted space boundary: got %v, want ErrInvalidBoundary", err)
	}
	if _, err := Boundary(`multipart/mixed; boundary="unterminated`); err == nil {
		t.Error("unterminated quoted value accepted")
	}
}

// Streaming reads with a tiny part buffer must still split a large body at
// the correct boundary.
func TestStreamingAcrossBufferBoundaries(t *testing.T) {
	contentType := `multipart/mixed; boundary="stream"`
	big := strings.Repeat("x", 6000)
	body := "--stream\r\n\r\n" + big + "\r\n\r\n--stream\r\n\r\nsmall\r\n\r\n--stream--\r\n"

	boundary, err := Boundary(contentType)
	if err != nil {
		t.Fatal(err)
	}
	mr, err := NewReader(strings.NewReader(body), boundary)
	if err != nil {
		t.Fatal(err)
	}

	part, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	buf := make([]byte, 7)
	if _, err := io.CopyBuffer(&got, part, buf); err != nil {
		t.Fatal(err)
	}
	if got.Len() != len(big)+2 {
		t.Fatalf("first body length = %d, want %d", got.Len(), len(big)+2)
	}
	if !strings.HasPrefix(got.String(), big) {
		t.Error("large body content mismatch")
	}

	part2, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(part2)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "small\r\n" {
		t.Errorf("second body = %q", rest)
	}
	if _, err := mr.NextPart(); err != io.EOF {
		t.Errorf("final NextPart = %v, want io.EOF", err)
	}
}

func TestDecodeMultipartConvenience(t *testing.T) {
	contentType := `multipart/form-data; boundary="formBoundary"`
	body := "--formBoundary\r\n" +
		"Content-Disposition: form-data; name=\"a\"\r\n\r\n" +
		"value-a\r\n" +
		"\r\n--formBoundary--\r\n"

	parts, err := DecodeMultipart(contentType, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	got := parts[0].Header["Content-Disposition"]
	if len(got) != 1 || !strings.Contains(got[0], `name="a"`) {
		t.Errorf("header = %q", got)
	}
	if string(parts[0].Body) != "value-a\r\n" {
		t.Errorf("body = %q", parts[0].Body)
	}
}

func TestTruncatedStream(t *testing.T) {
	contentType := `multipart/mixed; boundary=b`
	body := "--b\r\n\r\nbody\r\n\r\n--b"
	if _, err := readAllBodies(t, contentType, body); !errors.Is(err, io.ErrUnexpectedEOF) &&
		!errors.Is(err, ErrMalformedBoundary) {
		t.Errorf("got %v, want UnexpectedEOF or ErrMalformedBoundary", err)
	}
}

func TestPreambleIsSkipped(t *testing.T) {
	contentType := `multipart/mixed; boundary=b`
	body := "preamble line one\r\npreamble line two\r\n--b\r\n\r\nonly part\r\n\r\n--b--\r\n"
	bodies, err := readAllBodies(t, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || string(bodies[0]) != "only part\r\n" {
		t.Fatalf("bodies = %q", bodies)
	}
}

// RFC 2046 allows the closing boundary to omit its trailing CRLF.
func TestClosingBoundaryWithoutFinalCRLF(t *testing.T) {
	contentType := `multipart/mixed; boundary=b`
	body := "--b\r\n\r\npayload\r\n\r\n--b--"
	bodies, err := readAllBodies(t, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || string(bodies[0]) != "payload\r\n" {
		t.Fatalf("bodies = %q", bodies)
	}
}

func TestEmptyAndBinaryBodies(t *testing.T) {
	contentType := `multipart/mixed; boundary="B"`
	body := "--B\r\nA: 1\r\n\r\n" +
		"\r\n--B\r\nB: 2\r\n\r\n\x00\x01\x02\r\n" +
		"\r\n--B--\r\n"
	bodies, err := readAllBodies(t, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 {
		t.Fatalf("got %d parts, want 2", len(bodies))
	}
	if len(bodies[0]) != 0 {
		t.Errorf("first body = %q, want empty", bodies[0])
	}
	if string(bodies[1]) != "\x00\x01\x02\r\n" {
		t.Errorf("binary body = %q", bodies[1])
	}
}

func TestHeaderFoldingAndCanonicalization(t *testing.T) {
	contentType := `multipart/mixed; boundary=b`
	body := "--b\r\n" +
		"X-Custom: part-one\r\n" +
		" folded value\r\n\r\n" +
		"x\r\n\r\n--b--\r\n"
	boundary, err := Boundary(contentType)
	if err != nil {
		t.Fatal(err)
	}
	mr, err := NewReader(strings.NewReader(body), boundary)
	if err != nil {
		t.Fatal(err)
	}
	part, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if got := part.Header.Get("X-Custom"); got != "part-one folded value" {
		t.Errorf("folded header = %q", got)
	}
}

func TestMalformedHeaderRejected(t *testing.T) {
	contentType := `multipart/mixed; boundary=b`
	body := "--b\r\nnot-a-header\r\n\r\nx\r\n\r\n--b--\r\n"
	if _, err := readAllBodies(t, contentType, body); !errors.Is(err, ErrMalformedHeader) {
		t.Errorf("got %v, want ErrMalformedHeader", err)
	}
}

func TestQuotedParameterEscapes(t *testing.T) {
	mediaType, params, err := ParseMediaType(`multipart/mixed; boundary="a\\b\"c"`)
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/mixed" {
		t.Errorf("media type = %q", mediaType)
	}
	// Backslash escapes resolve to the escaped character literally.
	if params["boundary"] != `a\b"c` {
		t.Errorf("boundary = %q", params["boundary"])
	}
}

// A non-final delimiter that ends the stream without a CRLF is invalid;
// only the closing boundary may omit its final CRLF.
func TestPaddedPartDelimiterMissingCRLF(t *testing.T) {
	contentType := `multipart/mixed; boundary=b`
	body := "--b\r\n\r\nbody\r\n\r\n--b \t "
	if _, err := readAllBodies(t, contentType, body); err == nil {
		t.Fatal("expected error for part delimiter without terminating CRLF")
	}
}

// A delimiter-like token at the very start of a part body is body content,
// not a premature end of part.
func TestBoundaryLikeTokenAtBodyStart(t *testing.T) {
	contentType := `multipart/mixed; boundary=b`
	body := "--b\r\n\r\n--bX starts the body\r\nreal\r\n\r\n--b--\r\n"
	bodies, err := readAllBodies(t, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || string(bodies[0]) != "--bX starts the body\r\nreal\r\n" {
		t.Fatalf("bodies = %q", bodies)
	}
}

// A line introduced by a bare LF rather than CRLF is not a delimiter, so it
// remains part of the body; the later CRLF delimiter still ends the part.
func TestBareLFLineStaysBody(t *testing.T) {
	contentType := `multipart/mixed; boundary=b`
	body := "--b\r\n\r\nbody\n--b\n\r\nx\r\n\r\n--b--\r\n"
	bodies, err := readAllBodies(t, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("got %d parts, want 1", len(bodies))
	}
	want := "body\n--b\n\r\nx\r\n"
	if string(bodies[0]) != want {
		t.Errorf("body = %q, want %q", bodies[0], want)
	}
}
