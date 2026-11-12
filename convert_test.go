package netsec

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

const testStream uint32 = 1

func encodeFields(fields ...[2]string) []byte {
	var enc hpackEncoder
	for _, f := range fields {
		enc.literal(f[0], f[1])
	}
	return enc.buf
}

func requestHeaders(extra ...[2]string) []byte {
	fields := [][2]string{
		{":method", "POST"},
		{":scheme", "https"},
		{":path", "/submit"},
		{":authority", "example.com"},
	}
	fields = append(fields, extra...)
	return encodeFields(fields...)
}

func TestHappyPathAccumulatesDataFrames(t *testing.T) {
	block := requestHeaders([2]string{"content-length", "11"})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, block),
		dataFrame(testStream, false, []byte("hello ")),
		dataFrame(testStream, false, []byte("wor")),
		dataFrame(testStream, true, []byte("ld")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !conv.Ready() {
		t.Fatal("converter not ready after END_STREAM")
	}
	req := conv.Request()
	if req.Method != "POST" || req.Host != "example.com" || req.URL.Path != "/submit" {
		t.Fatalf("bad request line: %+v", req)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "hello world" {
		t.Fatalf("body = %q, want %q", body, "hello world")
	}
	if req.ContentLength != 11 {
		t.Fatalf("ContentLength = %d, want 11", req.ContentLength)
	}

	var raw bytes.Buffer
	if err := conv.WriteHTTP1(&raw); err != nil {
		t.Fatalf("WriteHTTP1: %v", err)
	}
	wire := raw.String()
	if !strings.HasPrefix(wire, "POST /submit HTTP/1.1\r\nHost: example.com\r\n") {
		t.Fatalf("bad request line/host: %q", wire)
	}
	if !strings.Contains(wire, "Content-Length: 11\r\n\r\nhello world") {
		t.Fatalf("bad framing/body: %q", wire)
	}
	lower := strings.ToLower(wire)
	if strings.Contains(lower, "connection:") ||
		strings.Contains(lower, "transfer-encoding") {
		t.Fatalf("hop-by-hop header leaked: %q", wire)
	}
}

func TestPaddedDataFrameLengthExcludesPadding(t *testing.T) {
	block := requestHeaders([2]string{"content-length", "5"})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, block),
		paddedDataFrame(testStream, true, []byte("abcde"), []byte{0, 0, 0}),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body, _ := io.ReadAll(conv.Request().Body)
	if string(body) != "abcde" {
		t.Fatalf("body = %q (padding counted?)", body)
	}
}

func TestDuplicatePseudoHeaderRejected(t *testing.T) {
	var enc hpackEncoder
	enc.literal(":method", "GET")
	enc.literal(":method", "POST")
	enc.literal(":scheme", "https")
	enc.literal(":path", "/")
	enc.literal(":authority", "example.com")
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, enc.buf),
	)
	if !errors.Is(err, ErrDuplicatePseudo) {
		t.Fatalf("err = %v, want ErrDuplicatePseudo", err)
	}
	if conv.ErrorCode() != ErrCodeProtocol || conv.Ready() {
		t.Fatal("duplicate pseudo must fail the stream without a request")
	}
}

func TestPseudoHeaderAfterRegularRejected(t *testing.T) {
	block := encodeFields(
		[2]string{":method", "GET"},
		[2]string{":scheme", "https"},
		[2]string{":path", "/"},
		[2]string{"x-regular", "1"},
		[2]string{":authority", "example.com"},
	)
	_, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	)
	if !errors.Is(err, ErrPseudoOrder) {
		t.Fatalf("err = %v, want ErrPseudoOrder", err)
	}
}

func TestUnknownPseudoHeaderRejected(t *testing.T) {
	block := encodeFields(
		[2]string{":method", "GET"},
		[2]string{":scheme", "https"},
		[2]string{":path", "/"},
		[2]string{":authority", "example.com"},
		[2]string{":smuggled", "x"},
	)
	_, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	)
	if !errors.Is(err, ErrUnknownPseudo) {
		t.Fatalf("err = %v, want ErrUnknownPseudo", err)
	}
}

func TestConnectionSpecificFieldsRejected(t *testing.T) {
	forbidden := []string{
		"connection", "keep-alive", "proxy-connection",
		"transfer-encoding", "upgrade", "trailer",
	}
	for _, name := range forbidden {
		t.Run(name, func(t *testing.T) {
			block := requestHeaders([2]string{name, "x"})
			_, err := runFrames(testStream,
				headersFrame(testStream, flagEndHeaders|flagEndStream, block),
			)
			if !errors.Is(err, ErrConnectionField) {
				t.Fatalf("%s: err = %v, want ErrConnectionField", name, err)
			}
		})
	}
}

func TestTEHeaderOnlyAllowsTrailers(t *testing.T) {
	block := requestHeaders([2]string{"te", "trailers"})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, block),
		dataFrame(testStream, true, nil),
	)
	if err != nil {
		t.Fatalf("te: trailers should be accepted: %v", err)
	}
	if conv.Request().Header.Get("Te") != "" {
		t.Fatal("TE must not be forwarded to HTTP/1")
	}

	block = requestHeaders([2]string{"te", "gzip"})
	if _, err = runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	); !errors.Is(err, ErrConnectionField) {
		t.Fatalf("te: gzip err = %v, want ErrConnectionField", err)
	}
}

func TestAuthorityHostMismatchRejected(t *testing.T) {
	block := requestHeaders([2]string{"host", "evil.example"})
	_, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	)
	if !errors.Is(err, ErrAuthorityMismatch) {
		t.Fatalf("err = %v, want ErrAuthorityMismatch", err)
	}
}

func TestAuthorityHostConsistencyAccepted(t *testing.T) {
	block := requestHeaders([2]string{"host", "example.com"})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	)
	if err != nil {
		t.Fatalf("matching Host rejected: %v", err)
	}
	if conv.Request().Host != "example.com" {
		t.Fatalf("Host = %q", conv.Request().Host)
	}
}

func TestHostSynthesizedFromAuthority(t *testing.T) {
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, requestHeaders()),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conv.Request().Host != "example.com" {
		t.Fatalf("Host = %q, want example.com", conv.Request().Host)
	}
}
