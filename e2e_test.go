package netsec

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"
)

// inProcessHTTP1 pipes the raw bytes from WriteHTTP1 into the standard
// library's HTTP/1.1 request parser without touching the network.
func inProcessHTTP1(t *testing.T, conv *Converter) *http.Request {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	type result struct {
		req *http.Request
		err error
	}
	done := make(chan result, 1)
	go func() {
		req, err := http.ReadRequest(bufio.NewReader(server))
		done <- result{req, err}
	}()
	if err := conv.WriteHTTP1(client); err != nil {
		t.Fatalf("WriteHTTP1: %v", err)
	}
	res := <-done
	server.Close()
	if res.err != nil {
		t.Fatalf("standard library rejected the HTTP/1 message: %v", res.err)
	}
	return res.req
}

func TestEndToEndStandardParser(t *testing.T) {

	block := requestHeaders(
		[2]string{"x-trace", "trace-123"},
		[2]string{"content-length", "11"},
	)
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, block),
		dataFrame(testStream, false, []byte("hello ")),
		dataFrame(testStream, true, []byte("world")),
	)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}

	req := inProcessHTTP1(t, conv)
	t.Logf("headers: %v", req.Header)
	t.Logf("trailer declared: %v", req.Trailer)
	body, _ := io.ReadAll(req.Body)
	if string(body) != "hello world" {
		t.Fatalf("parsed body = %q", body)
	}
	if req.ContentLength != 11 {
		t.Fatalf("parsed Content-Length = %d, want 11", req.ContentLength)
	}
	if req.Header.Get("X-Trace") != "trace-123" {
		t.Fatal("custom header did not cross the boundary")
	}
	if req.Host != "example.com" {
		t.Fatalf("parsed Host = %q", req.Host)
	}
}

func TestEndToEndTrailersOverChunked(t *testing.T) {
	trailerBlock := encodeFields([2]string{"x-checksum", "deadbeef"})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, requestHeaders()),
		dataFrame(testStream, false, []byte("abc")),
		headersFrame(testStream, flagEndHeaders|flagEndStream, trailerBlock),
	)
	if err != nil {
		t.Fatalf("conversion failed: %v", err)
	}
	req := inProcessHTTP1(t, conv)
	if req.TransferEncoding == nil || req.TransferEncoding[0] != "chunked" {
		t.Fatalf("TransferEncoding = %v, want chunked", req.TransferEncoding)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "abc" {
		t.Fatalf("parsed chunked body = %q", body)
	}
	if req.Trailer.Get("X-Checksum") != "deadbeef" {
		t.Fatalf("parsed trailer = %v headers = %v", req.Trailer, req.Header)
	}

	// The raw serialized form must not contain a Content-Length.
	var raw bytes.Buffer
	if err := conv.WriteHTTP1(&raw); err != nil {
		t.Fatalf("WriteHTTP1: %v", err)
	}
	if bytes.Contains(bytes.ToLower(raw.Bytes()), []byte("content-length")) {
		t.Fatal("trailer request must be chunked without Content-Length")
	}
}
