package netsec

import (
	"io"
	"testing"
)

func TestDecodeSucceedsWhenTerminatorHasNoTrailingBytes(t *testing.T) {
	input := []byte("POST /submit HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n")

	verdict := Decode(input)
	if verdict.Status != Complete {
		t.Fatalf("status = %v, error = %v", verdict.Status, verdict.Err)
	}
	if string(verdict.Body) != "hello" {
		t.Fatalf("body = %q", verdict.Body)
	}
	if len(verdict.Remaining) != 0 {
		t.Fatalf("remaining = %q", verdict.Remaining)
	}
}

func TestDecodeKeepsPipelinedRequestLineForNextRequest(t *testing.T) {
	nextRequest := "GET /next HTTP/1.1\r\nHost: example.test\r\n\r\n"
	input := []byte("POST /submit HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n" + nextRequest)

	verdict := Decode(input)
	if verdict.Status != CompleteWithNextRequest {
		t.Fatalf("status = %v, error = %v", verdict.Status, verdict.Err)
	}
	if string(verdict.Remaining) != nextRequest {
		t.Fatalf("remaining = %q", verdict.Remaining)
	}
}

func TestDecodeRejectsAnotherChunkAfterTerminator(t *testing.T) {
	input := []byte("POST /submit HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n3\r\nbar\r\n")

	verdict := Decode(input)
	if verdict.Status != Invalid {
		t.Fatalf("status = %v", verdict.Status)
	}
	if verdict.Err == nil {
		t.Fatal("expected an error")
	}
}

func TestDecodeRejectsTrailerFollowedByAnotherChunk(t *testing.T) {
	input := []byte("POST /submit HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nChecksum: abc\r\n\r\n3\r\nbar\r\n")

	verdict := Decode(input)
	if verdict.Status != Invalid {
		t.Fatalf("status = %v, error = %v", verdict.Status, verdict.Err)
	}
}

func TestDecodeReportsIncompleteInput(t *testing.T) {
	input := []byte("POST /submit HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhel")

	verdict := Decode(input)
	if verdict.Status != Incomplete {
		t.Fatalf("status = %v, error = %v", verdict.Status, verdict.Err)
	}
}

func TestDecodeChunkedAcceptsChunkExtensionsAndTrailers(t *testing.T) {
	input := []byte("5; name=value\r\nhello\r\n0\r\nChecksum: abc\r\n\r\n")

	verdict := DecodeChunked(input)
	if verdict.Status != Complete {
		t.Fatalf("status = %v, error = %v", verdict.Status, verdict.Err)
	}
	if string(verdict.Body) != "hello" {
		t.Fatalf("body = %q", verdict.Body)
	}
}

func TestDecoderBuffersPartialWrites(t *testing.T) {
	decoder := NewDecoder()
	input := []byte("3\r\nabc\r\n0\r\n\r\nGET /next HTTP/1.1\r\n\r\n")
	for _, part := range [][]byte{input[:8], input[8:18], input[18:]} {
		if _, err := decoder.Write(part); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	verdict := decoder.Decode()
	if verdict.Status != CompleteWithNextRequest {
		t.Fatalf("status = %v, error = %v", verdict.Status, verdict.Err)
	}
	if string(verdict.Body) != "abc" {
		t.Fatalf("body = %q", verdict.Body)
	}
}

func TestReadChunkedReturnsRemainingRequest(t *testing.T) {
	input := []byte("3\r\nabc\r\n0\r\n\r\nGET /next HTTP/1.1\r\n\r\n")

	body, remaining, err := ReadChunked(input)
	if err != nil {
		t.Fatalf("ReadChunked: %v", err)
	}
	if string(body) != "abc" {
		t.Fatalf("body = %q", body)
	}
	if string(remaining) != "GET /next HTTP/1.1\r\n\r\n" {
		t.Fatalf("remaining = %q", remaining)
	}
}

func TestReadChunkedAcceptsPartialNextRequest(t *testing.T) {
	input := []byte("3\r\nabc\r\n0\r\n\r\nGET /next HTTP/1.1\r\nHost: ")

	body, remaining, err := ReadChunked(input)
	if err != nil {
		t.Fatalf("ReadChunked: %v", err)
	}
	if string(body) != "abc" {
		t.Fatalf("body = %q", body)
	}
	if string(remaining) != "GET /next HTTP/1.1\r\nHost: " {
		t.Fatalf("remaining = %q", remaining)
	}
}

func TestDecoderImplementsReader(t *testing.T) {
	decoder := NewDecoder()
	if _, err := decoder.Write([]byte("3\r\nabc\r\n0\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	body, err := io.ReadAll(decoder)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "abc" {
		t.Fatalf("body = %q", body)
	}
}
