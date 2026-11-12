package netsec

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCloseEarlyForbidsReuse(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"length", "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"},
		{"chunked", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(strings.NewReader(tc.raw))
			resp, err := r.ReadResponse("GET")
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			one := make([]byte, 1)
			if _, err := io.ReadFull(resp.Body, one); err != nil {
				t.Fatalf("peek: %v", err)
			}
			if err := resp.Body.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			if r.Reusable() {
				t.Fatal("closing a partially consumed body must forbid reuse")
			}
		})
	}
}

func TestCloseAfterFullBodyKeepsReuse(t *testing.T) {
	r := NewReader(strings.NewReader("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nhi"))
	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hi" {
		t.Fatalf("body = %q", body)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !r.Reusable() {
		t.Fatal("fully read length body must remain reusable after Close")
	}
}

func TestMalformedResponses(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		method string
	}{
		{"bad status line", "GARBAGE\r\n\r\n", "GET"},
		{"bad version", "HTTP/x.y 200 OK\r\n\r\n", "GET"},
		{"bad code", "HTTP/1.1 20x OK\r\n\r\n", "GET"},
		{"negative length", "HTTP/1.1 200 OK\r\nContent-Length: -1\r\n\r\n", "GET"},
		{"duplicate conflicting length", "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Length: 3\r\n\r\nab", "GET"},
		{"comma folded length", "HTTP/1.1 200 OK\r\nContent-Length: 2, 3\r\n\r\nab", "GET"},
		{"header truncated", "HTTP/1.1 200 OK\r\nContent-Length: 5\r\n", "GET"},
		{"bad chunk size", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n", "GET"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReader(strings.NewReader(tc.raw))
			resp, err := r.ReadResponse(tc.method)
			if err != nil {
				if r.Reusable() {
					t.Fatalf("%s: protocol/truncation error must forbid reuse", tc.name)
				}
				return
			}
			_, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				t.Fatalf("%s: expected framing error, got response %s", tc.name, resp.Framing)
			}
			if r.Reusable() {
				t.Fatalf("%s: error must forbid reuse", tc.name)
			}
		})
	}
}

func TestNonChunkedTransferEncodingFallsBackToClose(t *testing.T) {
	r := NewReader(strings.NewReader("HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip\r\n\r\nabc"))
	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Framing != FramingClose {
		t.Fatalf("framing = %s, want close-delimited", resp.Framing)
	}
	if r.Reusable() {
		t.Fatal("non-self-delimiting transfer encoding must forbid reuse")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "abc" {
		t.Fatalf("body = %q %v", body, err)
	}
}

func TestZeroLengthBody(t *testing.T) {
	r := NewReader(strings.NewReader("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\nx"))
	first, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if first.Framing != FramingLength || first.ContentLength != 0 {
		t.Fatalf("framing = %s len = %d", first.Framing, first.ContentLength)
	}
	body, err := io.ReadAll(first.Body)
	if err != nil || len(body) != 0 {
		t.Fatalf("zero body = %q %v", body, err)
	}
	second, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	tail, _ := io.ReadAll(second.Body)
	if string(tail) != "x" {
		t.Fatalf("second body = %q, want x", tail)
	}
}

func Test304HasNoBodyEvenWithLength(t *testing.T) {
	r := NewReader(strings.NewReader("HTTP/1.1 304 Not Modified\r\nContent-Length: 100\r\n\r\nx"))
	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Framing != FramingNone {
		t.Fatalf("304 framing = %s", resp.Framing)
	}
	// 头部之后的字节不能被 304 响应吞掉。
	peek, err := r.br.Peek(1)
	if err != nil || string(peek) != "x" {
		t.Fatalf("buffered byte after 304 = %q %v", peek, err)
	}
}

func TestChunkedCRLFMalformed(t *testing.T) {
	// 块数据后缺少 CRLF。
	r := NewReader(strings.NewReader("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nhiX"))
	resp, err := r.ReadResponse("GET")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("read chunk: %v", err)
	}
	if _, err := io.ReadAll(resp.Body); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v, want ErrProtocol", err)
	}
}

func TestTunnelBufferedReader(t *testing.T) {
	r := NewReader(strings.NewReader("HTTP/1.1 200 OK\r\n\r\npre-buffered"))
	resp, err := r.ReadResponse("CONNECT")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Framing != FramingTunnel {
		t.Fatalf("framing = %s", resp.Framing)
	}
	rest, err := io.ReadAll(r.BufferedReader())
	if err != nil || string(rest) != "pre-buffered" {
		t.Fatalf("buffered tunnel prefix = %q %v", rest, err)
	}
}

func TestReadOnClosedEmptyStream(t *testing.T) {
	r := NewReader(strings.NewReader(""))
	if _, err := r.ReadResponse("GET"); !errors.Is(err, io.EOF) {
		t.Fatalf("empty stream err = %v, want io.EOF", err)
	}
}
