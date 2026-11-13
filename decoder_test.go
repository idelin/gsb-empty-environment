package netsec

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestDecoderRejectsEarlyRequestLine(t *testing.T) {
	body := []byte("GET /admin HTTP/1.1\r\nHost: example.test\r\n\r\n")
	err := ValidateEarlyBody(body, 1024)
	if !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("ValidateEarlyBody() error = %v, want ErrSmuggledRequest", err)
	}
}

func TestDecoderRejectsRequestLineForms(t *testing.T) {
	cases := [][]byte{
		[]byte("POST /upload HTTP/1.0\r\n"),
		[]byte("OPTIONS * HTTP/1.1\r\n"),
		[]byte("CONNECT example.test:443 HTTP/1.1\r\n"),
		[]byte("PRI * HTTP/2.0\r\n"),
		[]byte("M-SEARCH * HTTP/1.1\r\n"),
	}
	for _, body := range cases {
		if err := ValidateEarlyBody(body, 1024); !errors.Is(err, ErrSmuggledRequest) {
			t.Errorf("ValidateEarlyBody(%q) error = %v, want ErrSmuggledRequest", body, err)
		}
	}
}

func TestDecoderRejectsRequestLineAfterBodyPrefix(t *testing.T) {
	body := []byte("legitimate-looking bytes\r\nGET /admin HTTP/1.1\r\n")
	err := ValidateEarlyBody(body, 1024)
	if !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("ValidateEarlyBody() error = %v, want ErrSmuggledRequest", err)
	}
}

func TestDecoderRejectsSplitRequestLine(t *testing.T) {
	decoder := NewDecoder(1024)
	chunks := [][]byte{
		[]byte("GE"),
		[]byte("T /adm"),
		[]byte("in HTTP/1.1\r"),
		[]byte("\nHost: example.test\r\n"),
	}
	for _, chunk := range chunks {
		err := decoder.Feed(chunk)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrSmuggledRequest) {
			t.Fatalf("Feed(%q) error = %v, want ErrSmuggledRequest", chunk, err)
		}
		if len(decoder.OffendingLine()) == 0 {
			t.Fatal("OffendingLine() is empty after rejection")
		}
		return
	}
	t.Fatal("split request line was not rejected")
}

func TestDecoderAllowsShortBodyWithoutRequestLine(t *testing.T) {
	body := []byte("short body")
	if err := ValidateEarlyBody(body, 1024); err != nil {
		t.Fatalf("ValidateEarlyBody() error = %v, want nil", err)
	}
}

func TestDecoderStopsAtConfiguredLimit(t *testing.T) {
	requestLine := []byte("GET /admin HTTP/1.1\r\n")
	limit := len(requestLine) - 1
	if err := ValidateEarlyBody(requestLine, limit); err != nil {
		t.Fatalf("ValidateEarlyBody() error = %v, want nil at limit boundary", err)
	}

	decoder := NewDecoder(limit)
	if err := decoder.Feed(requestLine); err != nil {
		t.Fatalf("Feed() error = %v, want nil", err)
	}
	if got := len(decoder.Buffered()); got != limit {
		t.Fatalf("buffered length = %d, want %d", got, limit)
	}
	if !decoder.Full() {
		t.Fatal("decoder is not full at the configured limit")
	}
}

func TestDecoderDoesNotInspectBytesPastLimit(t *testing.T) {
	body := append([]byte("xxxxxxxx"), []byte("GET /admin HTTP/1.1\r\n")...)
	if err := ValidateEarlyBody(body, 4); err != nil {
		t.Fatalf("ValidateEarlyBody() error = %v, want nil", err)
	}
}

func TestDecoderRequiresCompleteRequestLine(t *testing.T) {
	cases := [][]byte{
		[]byte("GET /admin HTTP/1.1"),
		[]byte("GET /admin HTTP/1.1\r"),
		[]byte("GET /admin HTTP/1.1x\r\n"),
		[]byte("GET /admin\r\n"),
		[]byte(" GET /admin HTTP/1.1\r\n"),
		[]byte("GET /admin HTTP/1.1 extra\r\n"),
	}
	for _, body := range cases {
		if err := ValidateEarlyBody(body, 1024); err != nil {
			t.Errorf("ValidateEarlyBody(%q) error = %v, want nil", body, err)
		}
	}
}

func TestDecoderDetectsBareLFRequestLine(t *testing.T) {
	body := []byte("GET /admin HTTP/1.1\n")
	if err := ValidateEarlyBody(body, 1024); !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("ValidateEarlyBody() error = %v, want ErrSmuggledRequest", err)
	}
}

func TestDecoderWriteRejectsWholeChunk(t *testing.T) {
	decoder := NewDecoder(1024)
	n, err := decoder.Write([]byte("GET /admin HTTP/1.1\r\n"))
	if n != 0 || !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("Write() = (%d, %v), want (0, ErrSmuggledRequest)", n, err)
	}
}

func TestDecoderReader(t *testing.T) {
	body := "short body"
	if err := Decode(strings.NewReader(body), 1024); err != nil {
		t.Fatalf("Decode(reader) error = %v, want nil", err)
	}

	if err := Decode(bytes.NewBufferString("GET / HTTP/1.1\r\n"), 1024); !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("Decode(reader) error = %v, want ErrSmuggledRequest", err)
	}
}

func TestDecoderConfigForms(t *testing.T) {
	for _, opts := range [][]any{
		{1024},
		{int64(1024)},
		{Config{MaxBody: 1024}},
		{&Config{MaxBody: 1024}},
		{Options{MaxBody: 1024}},
	} {
		decoder := NewDecoder(opts...)
		if got := decoder.Limit(); got != 1024 {
			t.Fatalf("NewDecoder(%v).Limit() = %d, want 1024", opts, got)
		}
	}
}

func TestDecoderReset(t *testing.T) {
	decoder := NewDecoder(1024)
	if err := decoder.Feed([]byte("GET / HTTP/1.1\r\n")); !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("Feed() error = %v, want ErrSmuggledRequest", err)
	}
	decoder.Reset()
	if err := decoder.Feed([]byte("short")); err != nil {
		t.Fatalf("Feed() after Reset error = %v, want nil", err)
	}
}

func TestHasExpectContinue(t *testing.T) {
	cases := []struct {
		header http.Header
		want   bool
	}{
		{http.Header{"Expect": {"100-continue"}}, true},
		{http.Header{"Expect": {"100-Continue"}}, true},
		{http.Header{"Expect": {"100-continue, foo"}}, true},
		{http.Header{"Expect": {"foo, 100-continue"}}, true},
		{http.Header{"Expect": {"foo"}}, false},
		{http.Header{"Other": {"100-continue"}}, false},
		{http.Header{}, false},
	}
	for _, tc := range cases {
		if got := HasExpectContinue(tc.header); got != tc.want {
			t.Errorf("HasExpectContinue(%v) = %v, want %v", tc.header, got, tc.want)
		}
	}
}

func TestScreenHonorsExpectHeader(t *testing.T) {
	attack := []byte("GET /admin HTTP/1.1\r\n")
	headers := http.Header{"Expect": {"100-continue"}}
	if err := Screen(headers, attack, 1024); !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("Screen() error = %v, want ErrSmuggledRequest", err)
	}
	if err := Screen(http.Header{}, attack, 1024); err != nil {
		t.Fatalf("Screen() without Expect error = %v, want nil", err)
	}
}

func TestDecoderNegativeConfig(t *testing.T) {
	if err := Decode([]byte("short"), -1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Decode() error = %v, want ErrInvalidConfig", err)
	}
	if got := NewDecoder(-1).Limit(); got != 0 {
		t.Fatalf("NewDecoder(-1).Limit() = %d, want 0", got)
	}
}

func TestDecoderZeroValueUsesDefaultLimit(t *testing.T) {
	var decoder Decoder
	if got := decoder.Limit(); got != DefaultMaxBody {
		t.Fatalf("zero Decoder Limit() = %d, want %d", got, DefaultMaxBody)
	}
	if err := decoder.Feed([]byte("GET /admin HTTP/1.1\r\n")); !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("zero Decoder Feed() error = %v, want ErrSmuggledRequest", err)
	}
}

func TestGatewayScreensBodyWrittenBeforeContinue(t *testing.T) {
	const earlyBody = "GET /admin HTTP/1.1\r\nHost: example.test\r\n\r\n"

	client, gateway := net.Pipe()
	defer client.Close()
	defer gateway.Close()

	go func() {
		fmt.Fprintf(client, "POST /upload HTTP/1.1\r\n")
		fmt.Fprintf(client, "Host: example.test\r\n")
		fmt.Fprintf(client, "Expect: 100-continue\r\n")
		fmt.Fprintf(client, "Content-Length: %d\r\n\r\n", len(earlyBody))
		_, _ = io.WriteString(client, earlyBody)
	}()

	req, err := http.ReadRequest(bufio.NewReader(gateway))
	if err != nil {
		t.Fatalf("http.ReadRequest() error = %v", err)
	}
	defer req.Body.Close()

	if !HasExpectContinue(req.Header) {
		t.Fatal("request does not advertise Expect: 100-continue")
	}

	decoder := NewDecoder(1024)
	err = decoder.Decode(io.LimitReader(req.Body, req.ContentLength))
	if !errors.Is(err, ErrSmuggledRequest) {
		t.Fatalf("Decode() error = %v, want ErrSmuggledRequest", err)
	}
	if got := len(decoder.Buffered()); got > decoder.Limit() {
		t.Fatalf("buffered %d bytes, limit is %d", got, decoder.Limit())
	}
}
