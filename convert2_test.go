package netsec

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTrailerSmugglingContentLengthRejected(t *testing.T) {
	trailerBlock := encodeFields(
		[2]string{"content-length", "0"},
		[2]string{"x-evil", "1"},
	)
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, requestHeaders()),
		dataFrame(testStream, false, []byte("abc")),
		headersFrame(testStream, flagEndHeaders|flagEndStream, trailerBlock),
	)
	if !errors.Is(err, ErrTrailer) {
		t.Fatalf("err = %v, want ErrTrailer", err)
	}
	if conv.Ready() {
		t.Fatal("request must not be released when trailer is invalid")
	}
}

func TestTrailerPseudoHeaderRejected(t *testing.T) {
	trailerBlock := encodeFields([2]string{":path", "/admin"})
	_, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, requestHeaders()),
		dataFrame(testStream, false, []byte("abc")),
		headersFrame(testStream, flagEndHeaders|flagEndStream, trailerBlock),
	)
	if !errors.Is(err, ErrTrailer) {
		t.Fatalf("err = %v, want ErrTrailer", err)
	}
}

func TestTrailerConnectionFieldsRejected(t *testing.T) {
	for _, name := range []string{"connection", "transfer-encoding", "host", "upgrade"} {
		trailerBlock := encodeFields([2]string{name, "x"})
		_, err := runFrames(testStream,
			headersFrame(testStream, flagEndHeaders, requestHeaders()),
			dataFrame(testStream, false, []byte("abc")),
			headersFrame(testStream, flagEndHeaders|flagEndStream, trailerBlock),
		)
		if !errors.Is(err, ErrTrailer) {
			t.Fatalf("%s in trailer: err = %v", name, err)
		}
	}
}

func TestValidTrailerSerializedAsChunked(t *testing.T) {
	trailerBlock := encodeFields([2]string{"x-checksum", "deadbeef"})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, requestHeaders()),
		dataFrame(testStream, false, []byte("abc")),
		headersFrame(testStream, flagEndHeaders|flagEndStream, trailerBlock),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var raw bytes.Buffer
	if err := conv.WriteHTTP1(&raw); err != nil {
		t.Fatalf("WriteHTTP1: %v", err)
	}
	wire := raw.String()
	for _, want := range []string{
		"Trailer: X-Checksum\r\n",
		"Transfer-Encoding: chunked\r\n\r\n",
		"3\r\nabc\r\n0\r\n",
		"X-Checksum: deadbeef\r\n\r\n",
	} {
		if !strings.Contains(wire, want) {
			t.Fatalf("wire missing %q:\n%s", want, wire)
		}
	}
	if strings.Contains(wire, "Content-Length") {
		t.Fatalf("chunked trailer message must not carry Content-Length:\n%s", wire)
	}
}

func TestContentLengthMismatchRejected(t *testing.T) {
	block := requestHeaders([2]string{"content-length", "99"})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, block),
		dataFrame(testStream, true, []byte("abc")),
	)
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("err = %v, want ErrLengthMismatch", err)
	}
	if conv.Ready() {
		t.Fatal("request must not be released on length mismatch")
	}
}

func TestNoContentLengthUsesObservedBodyLength(t *testing.T) {
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, requestHeaders()),
		dataFrame(testStream, false, []byte("ab")),
		dataFrame(testStream, true, []byte("cd")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conv.Request().ContentLength != 4 {
		t.Fatalf("ContentLength = %d, want 4", conv.Request().ContentLength)
	}
}

func TestMidStreamRSTReleasesNoRequest(t *testing.T) {
	block := requestHeaders([2]string{"content-length", "10"})
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, block),
		dataFrame(testStream, false, []byte("abc")),
		buildFrame(FrameRSTStream, 0, testStream, RSTStreamFrame(ErrCodeCancel)),
	)
	if err != nil {
		t.Fatalf("RST_STREAM should be signalled by state, not HandleFrame error: %v", err)
	}
	if conv.Ready() {
		t.Fatal("request must not be released after RST_STREAM")
	}
	if conv.state != stateClosed {
		t.Fatalf("state = %d, want stateClosed", conv.state)
	}
}

func TestTruncatedStreamNotReady(t *testing.T) {
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, requestHeaders()),
		dataFrame(testStream, false, []byte("abc")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conv.Ready() {
		t.Fatal("request must not be released before END_STREAM")
	}
}

func TestDataAfterEndStreamRejected(t *testing.T) {
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, requestHeaders()),
		dataFrame(testStream, true, []byte("abc")),
		dataFrame(testStream, true, []byte("def")),
	)
	if !errors.Is(err, ErrStreamState) {
		t.Fatalf("err = %v, want ErrStreamState", err)
	}
	if conv.Ready() {
		t.Fatal("no request on protocol error")
	}
}

func TestContinuationReassembly(t *testing.T) {
	block := requestHeaders()
	half := len(block) / 2
	conv, err := runFrames(testStream,
		// END_STREAM rides on the opening HEADERS frame; CONTINUATION only
		// carries END_HEADERS per RFC 7540 section 6.10.
		headersFrame(testStream, flagEndStream, block[:half]),
		continuationFrame(testStream, flagEndHeaders, block[half:]),
	)
	if err != nil {
		t.Fatalf("CONTINUATION reassembly failed: %v", err)
	}
	if !conv.Ready() || conv.Request().URL.Path != "/submit" {
		t.Fatal("reassembled block did not produce the request")
	}
}

func TestDataWhileBlockIncompleteRejected(t *testing.T) {
	block := requestHeaders()
	_, err := runFrames(testStream,
		headersFrame(testStream, 0, block[:len(block)/2]),
		dataFrame(testStream, true, []byte("abc")),
	)
	if !errors.Is(err, ErrStreamState) {
		t.Fatalf("err = %v, want ErrStreamState", err)
	}
}

func TestMissingPseudoRejected(t *testing.T) {
	block := encodeFields(
		[2]string{":method", "GET"},
		[2]string{":scheme", "https"},
		[2]string{":authority", "example.com"},
	)
	_, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	)
	if !errors.Is(err, ErrMissingPseudo) {
		t.Fatalf("err = %v, want ErrMissingPseudo", err)
	}
}

func TestCRLFInHeaderRejected(t *testing.T) {
	block := requestHeaders([2]string{"x-evil", "a\r\nB: c"})
	_, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	)
	if err == nil {
		t.Fatal("CRLF in header value must be rejected")
	}
}

func TestConflictingContentLengthRejected(t *testing.T) {
	block := requestHeaders(
		[2]string{"content-length", "3"},
		[2]string{"content-length", "4"},
	)
	_, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders|flagEndStream, block),
	)
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("err = %v, want ErrLengthMismatch", err)
	}
}

func TestHTTP1RequestParsesBack(t *testing.T) {
	block := requestHeaders(
		[2]string{"x-trace", "abc"},
		[2]string{"content-length", "4"},
	)
	conv, err := runFrames(testStream,
		headersFrame(testStream, flagEndHeaders, block),
		dataFrame(testStream, true, []byte("body")),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var raw bytes.Buffer
	if err := conv.WriteHTTP1(&raw); err != nil {
		t.Fatalf("WriteHTTP1: %v", err)
	}
	req, err := http.ReadRequest(bufioReader(raw.Bytes()))
	if err != nil {
		t.Fatalf("downstream cannot parse emitted request: %v\n%s", err, raw.String())
	}
	if req.Method != "POST" || req.Host != "example.com" {
		t.Fatalf("parsed request mismatch: %+v", req)
	}
	if req.ContentLength != 4 {
		t.Fatalf("parsed ContentLength = %d", req.ContentLength)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "body" || req.Header.Get("X-Trace") != "abc" {
		t.Fatalf("parsed body/header mismatch: %q %v", body, req.Header)
	}
}
