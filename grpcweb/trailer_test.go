package grpcweb

import (
	"bytes"
	"errors"
	"net/http"
	"testing"
)

func mustMarshal(t *testing.T, trailers http.Header) []byte {
	t.Helper()
	frame, err := MarshalTrailerFrame(trailers)
	if err != nil {
		t.Fatalf("MarshalTrailerFrame returned unexpected error: %v", err)
	}
	return frame
}

func TestMarshalTrailerFrame_HappyPath(t *testing.T) {
	trailers := http.Header{}
	trailers.Set("Grpc-Status", "0")
	trailers.Set("Grpc-Message", "ok")
	trailers.Set("x-trace-id", "abc123")

	frame := mustMarshal(t, trailers)

	if frame[0] != FlagTrailer {
		t.Fatalf("frame flag = 0x%x, want 0x%x", frame[0], FlagTrailer)
	}
	payload := frame[FrameHeaderSize:]
	want := "x-trace-id: abc123\r\ngrpc-message: ok\r\ngrpc-status: 0\r\n"
	if string(payload) != want {
		t.Fatalf("payload = %q, want %q", payload, want)
	}

	block, err := ParseTrailerBlock(payload)
	if err != nil {
		t.Fatalf("ParseTrailerBlock returned error: %v", err)
	}
	if !block.HasStatus || block.Status != 0 {
		t.Fatalf("status = (%d,%v), want 0 present", block.Status, block.HasStatus)
	}
	if block.Message != "ok" || block.Metadata["x-trace-id"] != "abc123" {
		t.Fatalf("parsed block mismatch: %+v", block)
	}
}

func TestMarshalTrailerFrame_CRLFInValueRejected(t *testing.T) {
	trailers := http.Header{}
	trailers.Set("Grpc-Status", "0")
	trailers.Set("x-upstream", "legit\r\ngrpc-status: 7\r\n")

	if _, err := MarshalTrailerFrame(trailers); !errors.Is(err, ErrControlChar) {
		t.Fatalf("error = %v, want ErrControlChar", err)
	}
}

func TestMarshalTrailerFrame_CRLFInKeyRejected(t *testing.T) {
	trailers := http.Header{}
	trailers.Set("Grpc-Status", "0")
	// Header names are normalized by net/http; construct the raw map directly
	// to simulate an upstream that smuggles a CRLF through a metadata key.
	trailers["x-bad\r\ngrpc-status: 13"] = []string{"x"}

	if _, err := MarshalTrailerFrame(trailers); !errors.Is(err, ErrControlChar) {
		t.Fatalf("error = %v, want ErrControlChar", err)
	}
}

func TestMarshalTrailerFrame_LFOnlyInValueRejected(t *testing.T) {
	trailers := http.Header{}
	trailers.Set("Grpc-Status", "0")
	trailers.Set("x-note", "line1\nline2")

	if _, err := MarshalTrailerFrame(trailers); !errors.Is(err, ErrControlChar) {
		t.Fatalf("error = %v, want ErrControlChar", err)
	}
}

func TestMarshalTrailerFrame_ReservedPrefixRejected(t *testing.T) {
	trailers := http.Header{}
	trailers.Set("Grpc-Status", "0")
	trailers.Set("grpc-status-details-bin", "Zm9v")

	if _, err := MarshalTrailerFrame(trailers); !errors.Is(err, ErrReservedKey) {
		t.Fatalf("error = %v, want ErrReservedKey", err)
	}
}

func TestMarshalTrailerFrame_IllegalKeyRejected(t *testing.T) {
	trailers := http.Header{}
	trailers.Set("Grpc-Status", "0")
	trailers["x-bad space"] = []string{"v"}

	if _, err := MarshalTrailerFrame(trailers); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("error = %v, want ErrInvalidKey", err)
	}
}

func TestStatusFromHTTP2Trailers_MissingAndInvalid(t *testing.T) {
	if _, err := StatusFromHTTP2Trailers(nil); !errors.Is(err, ErrMissingStatus) {
		t.Fatalf("nil trailers error = %v, want ErrMissingStatus", err)
	}

	empty := http.Header{}
	if _, err := StatusFromHTTP2Trailers(empty); !errors.Is(err, ErrMissingStatus) {
		t.Fatalf("empty trailers error = %v, want ErrMissingStatus", err)
	}

	bad := http.Header{http.CanonicalHeaderKey(StatusTrailer): []string{"not-a-number"}}
	if _, err := StatusFromHTTP2Trailers(bad); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("bad status error = %v, want ErrInvalidStatus", err)
	}

	dup := http.Header{http.CanonicalHeaderKey(StatusTrailer): []string{"0", "7"}}
	if _, err := StatusFromHTTP2Trailers(dup); !errors.Is(err, ErrDuplicateTrailer) {
		t.Fatalf("duplicate status error = %v, want ErrDuplicateTrailer", err)
	}
}

func TestMarshalTrailerFrame_MissingStatusRejected(t *testing.T) {
	trailers := http.Header{}
	trailers.Set("x-only", "v")

	if _, err := MarshalTrailerFrame(trailers); !errors.Is(err, ErrMissingStatus) {
		t.Fatalf("error = %v, want ErrMissingStatus", err)
	}
}

func TestParseTrailerBlock_ForgedCRLFRejected(t *testing.T) {
	// Attacker-controlled value containing an embedded CRLF that attempts to
	// introduce a second grpc-status line.
	forged := []byte("x-upstream: legit\r\ngrpc-status: 7\r\ngrpc-status: 0\r\n")

	block, err := ParseTrailerBlock(forged)
	if err == nil {
		t.Fatalf("parsing should fail, got block: %+v", block)
	}
	// The embedded CRLF produces an extra parsed line; either the duplicate
	// status or a structural/control-character failure is acceptable, but it
	// must never be silently accepted.
	if !errors.Is(err, ErrDuplicateTrailer) &&
		!errors.Is(err, ErrControlChar) &&
		!errors.Is(err, ErrMalformedTrailer) {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestParseTrailerBlock_RejectsInjectedControlChar(t *testing.T) {
	cases := [][]byte{
		[]byte("x-a: bad\rvalue\r\n"),
		[]byte("x-b: bad\nvalue\r\n"),
		[]byte("x-c: del\x7f\r\n"),
		[]byte("x-d\tx: v\r\n"),
	}
	for _, payload := range cases {
		if _, err := ParseTrailerBlock(payload); !errors.Is(err, ErrControlChar) {
			t.Fatalf("payload %q error = %v, want ErrControlChar", payload, err)
		}
	}
}

func TestParseTrailerBlock_MalformedLines(t *testing.T) {
	cases := map[string][]byte{
		"no trailing crlf":         []byte("grpc-status: 0"),
		"blank line":               []byte("\r\ngrpc-status: 0\r\n"),
		"missing colon":            []byte("grpc-status 0\r\n"),
		"missing separating space": []byte("grpc-status:0\r\n"),
		"reserved custom key":      []byte("grpc-fake: 1\r\ngrpc-status: 0\r\n"),
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTrailerBlock(payload); err == nil {
				t.Fatalf("payload %q parsed unexpectedly", payload)
			}
		})
	}
}

func TestParseTrailerBlock_MissingStatus(t *testing.T) {
	block, err := ParseTrailerBlock([]byte("x-only: v\r\n"))
	if err != nil {
		t.Fatalf("ParseTrailerBlock error = %v", err)
	}
	if block.HasStatus {
		t.Fatalf("block unexpectedly reports status: %+v", block)
	}
	if err := VerifyStatus(block, http.Header{}); !errors.Is(err, ErrMissingStatus) {
		t.Fatalf("VerifyStatus error = %v, want ErrMissingStatus", err)
	}
}

func TestVerifyStatus_RejectsForgedStatus(t *testing.T) {
	block, err := ParseTrailerBlock([]byte("grpc-status: 13\r\n"))
	if err != nil {
		t.Fatalf("ParseTrailerBlock error = %v", err)
	}

	real := http.Header{}
	real.Set("Grpc-Status", "0")
	if err := VerifyStatus(block, real); !errors.Is(err, ErrStatusMismatch) {
		t.Fatalf("VerifyStatus error = %v, want ErrStatusMismatch", err)
	}

	if err := VerifyStatus(block, real); !bytes.Contains([]byte(err.Error()), []byte("forged")) {
		t.Fatalf("error message should mention forgery: %v", err)
	}
}

func TestVerifyStatus_AcceptsMatchingStatus(t *testing.T) {
	block, err := ParseTrailerBlock([]byte("grpc-message: ok\r\ngrpc-status: 0\r\n"))
	if err != nil {
		t.Fatalf("ParseTrailerBlock error = %v", err)
	}
	real := http.Header{}
	real.Set("Grpc-Status", "0")
	if err := VerifyStatus(block, real); err != nil {
		t.Fatalf("VerifyStatus error = %v", err)
	}
}

func TestRoundTrip(t *testing.T) {
	trailers := http.Header{}
	trailers.Set("Grpc-Status", "3")
	trailers.Set("Grpc-Message", "bad request")
	trailers.Set("x-a", "1")
	trailers.Set("x-b", "2")

	frame := mustMarshal(t, trailers)
	if frame[0] != FlagTrailer {
		t.Fatalf("flag = 0x%x, want 0x%x", frame[0], FlagTrailer)
	}
	block, err := ParseTrailerBlock(frame[FrameHeaderSize:])
	if err != nil {
		t.Fatalf("ParseTrailerBlock error = %v", err)
	}
	if !block.HasStatus || block.Status != 3 || block.Message != "bad request" {
		t.Fatalf("round trip mismatch: %+v", block)
	}
	if err := VerifyStatus(block, trailers); err != nil {
		t.Fatalf("VerifyStatus error = %v", err)
	}
}
