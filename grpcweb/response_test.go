package grpcweb

import (
	"errors"
	"net/http"
	"testing"
)

func okTrailers() http.Header {
	trailers := http.Header{}
	trailers.Set("Grpc-Status", "0")
	trailers.Set("Grpc-Message", "ok")
	return trailers
}

func TestBuildAndParseWebResponse(t *testing.T) {
	body, err := BuildWebResponse([][]byte{[]byte("msg1"), []byte("msg2")}, okTrailers())
	if err != nil {
		t.Fatalf("BuildWebResponse error = %v", err)
	}

	parsed, err := ParseWebResponse(body)
	if err != nil {
		t.Fatalf("ParseWebResponse error = %v", err)
	}
	if string(parsed.Data) != "msg1msg2" {
		t.Fatalf("data = %q, want %q", parsed.Data, "msg1msg2")
	}
	if !parsed.Trailer.HasStatus || parsed.Trailer.Status != 0 {
		t.Fatalf("trailer mismatch: %+v", parsed.Trailer)
	}
	if parsed.Trailer.Message != "ok" {
		t.Fatalf("message = %q, want ok", parsed.Trailer.Message)
	}
}

func TestBuildWebResponse_MissingTrailers(t *testing.T) {
	if _, err := BuildWebResponse(nil, nil); !errors.Is(err, ErrMissingStatus) {
		t.Fatalf("nil trailers error = %v, want ErrMissingStatus", err)
	}

	noStatus := http.Header{}
	noStatus.Set("x-only", "v")
	if _, err := BuildWebResponse(nil, noStatus); !errors.Is(err, ErrMissingStatus) {
		t.Fatalf("missing status error = %v, want ErrMissingStatus", err)
	}
}

func TestBuildWebResponse_CRLFInjectionRejected(t *testing.T) {
	trailers := okTrailers()
	trailers.Set("x-poison", "ok\r\ngrpc-status: 13\r\n")

	if _, err := BuildWebResponse([][]byte{[]byte("m")}, trailers); !errors.Is(err, ErrControlChar) {
		t.Fatalf("error = %v, want ErrControlChar", err)
	}
}

func TestBuildWebResponse_ReservedMetadataRejected(t *testing.T) {
	trailers := okTrailers()
	trailers.Set("grpc-fake", "13")

	if _, err := BuildWebResponse(nil, trailers); !errors.Is(err, ErrReservedKey) {
		t.Fatalf("error = %v, want ErrReservedKey", err)
	}
}

func TestParseWebResponse_MissingTrailerFrame(t *testing.T) {
	// Data frame only, no FlagTrailer frame.
	body := makeFrame(FlagData, []byte("orphan"))
	if _, err := ParseWebResponse(body); !errors.Is(err, ErrMissingTrailer) {
		t.Fatalf("error = %v, want ErrMissingTrailer", err)
	}

	if _, err := ParseWebResponse(nil); !errors.Is(err, ErrMissingTrailer) {
		t.Fatalf("empty body error = %v, want ErrMissingTrailer", err)
	}
}

func TestParseWebResponse_TrailerWithoutStatus(t *testing.T) {
	body := makeFrame(FlagTrailer, []byte("x-only: v\r\n"))
	if _, err := ParseWebResponse(body); !errors.Is(err, ErrMissingStatus) {
		t.Fatalf("error = %v, want ErrMissingStatus", err)
	}
}

func TestParseWebResponse_ForgedStatusRejectedByVerification(t *testing.T) {
	// The frame itself is structurally valid, but its grpc-status must not be
	// trusted until cross-checked against the real HTTP/2 trailers.
	body := makeFrame(FlagTrailer, []byte("grpc-status: 13\r\n"))
	parsed, err := ParseWebResponse(body)
	if err != nil {
		t.Fatalf("ParseWebResponse error = %v", err)
	}

	real := http.Header{}
	real.Set("Grpc-Status", "0")
	if err := VerifyStatus(parsed.Trailer, real); !errors.Is(err, ErrStatusMismatch) {
		t.Fatalf("VerifyStatus error = %v, want ErrStatusMismatch", err)
	}
}

func TestParseWebResponse_Truncated(t *testing.T) {
	body := makeFrame(FlagTrailer, []byte("grpc-status: 0\r\n"))
	if _, err := ParseWebResponse(body[:len(body)-2]); !errors.Is(err, ErrTruncatedFrame) {
		t.Fatalf("error = %v, want ErrTruncatedFrame", err)
	}
}
