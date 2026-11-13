package netsec

import (
	"errors"
	"net/http"
	"testing"
)

func TestForbiddenTrailer(t *testing.T) {
	forbidden := []string{
		"host",
		"Host",
		" HOST ",
		"content-length",
		"Content-Length",
		"transfer-encoding",
		"Transfer-Encoding",
		":path",
		":authority",
		":method",
		":scheme",
		":status",
		" :status",
	}
	for _, name := range forbidden {
		if !ForbiddenTrailer(name) {
			t.Errorf("ForbiddenTrailer(%q) = false, want true", name)
		}
	}

	allowed := []string{
		"x-checksum",
		"etag",
		"trailer",
		"te",
		"x-custom",
	}
	for _, name := range allowed {
		if ForbiddenTrailer(name) {
			t.Errorf("ForbiddenTrailer(%q) = true, want false", name)
		}
	}
}

func TestHostInTrailerFailsStream(t *testing.T) {
	// The core attack from the brief: host is written into the trailing part of
	// the request, after routing already happened. The gateway must fail the
	// stream instead of re-routing based on it.
	fields := []TrailerField{
		{Name: "X-Checksum", Value: "deadbeef"},
		{Name: "Host", Value: "evil.example"},
	}
	err := ValidateTrailerFields(fields)
	if err == nil {
		t.Fatal("expected stream failure for host trailer, got nil")
	}

	var streamErr *StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("error %v is not *StreamError", err)
	}
	if streamErr.Field != "Host" {
		t.Errorf("StreamError.Field = %q, want Host", streamErr.Field)
	}
	if !errors.Is(err, ErrForbiddenTrailer) {
		t.Errorf("errors.Is(err, ErrForbiddenTrailer) = false for %v", err)
	}
}

func TestForbiddenTrailerVariants(t *testing.T) {
	cases := []string{
		"host",
		"content-length",
		"transfer-encoding",
		":path",
		":authority",
	}
	for _, name := range cases {
		err := ValidateTrailerFields([]TrailerField{{Name: name, Value: "v"}})
		if !errors.Is(err, ErrForbiddenTrailer) {
			t.Errorf("ValidateTrailerFields(%q) err = %v, want ErrForbiddenTrailer", name, err)
		}
	}
}

func TestDeclaredCustomTrailersAccepted(t *testing.T) {
	validator := NewTrailerValidator([]string{"X-Checksum", "etag"})

	fields := []TrailerField{
		{Name: "x-checksum", Value: "deadbeef"},
		{Name: "ETag", Value: "\"abc\""},
	}
	if err := validator.ValidateFields(fields); err != nil {
		t.Fatalf("declared custom trailers rejected: %v", err)
	}
}

func TestForbiddenBeatsDeclaration(t *testing.T) {
	// Even if a peer (wrongly) announces host as a trailer, it is still rejected.
	validator := NewTrailerValidator([]string{"host"})
	err := validator.ValidateFields([]TrailerField{{Name: "Host", Value: "evil.example"}})
	if !errors.Is(err, ErrForbiddenTrailer) {
		t.Fatalf("err = %v, want ErrForbiddenTrailer", err)
	}
}

func TestStrictDeclarationEnforcement(t *testing.T) {
	validator := NewTrailerValidator(
		[]string{"X-Checksum"},
		WithRequireDeclared(true),
	)

	if err := validator.ValidateFields([]TrailerField{{Name: "X-Checksum", Value: "1"}}); err != nil {
		t.Fatalf("declared trailer rejected: %v", err)
	}

	err := validator.ValidateFields([]TrailerField{{Name: "X-Undeclared", Value: "1"}})
	if !errors.Is(err, ErrUndeclaredTrailer) {
		t.Fatalf("err = %v, want ErrUndeclaredTrailer", err)
	}

	// Forbidden fields take precedence over the undeclared check.
	err = validator.ValidateFields([]TrailerField{{Name: "Host", Value: "x"}})
	if !errors.Is(err, ErrForbiddenTrailer) {
		t.Fatalf("err = %v, want ErrForbiddenTrailer", err)
	}
}

func TestValidateHTTPServerTrailers(t *testing.T) {
	// net/http canonicalizes field names in http.Header; matching must still be
	// case-insensitive and catch the canonical Host/Content-Length spelling.
	header := http.Header{}
	header.Set("X-Checksum", "deadbeef")
	if err := ValidateTrailers(header); err != nil {
		t.Fatalf("safe trailer block rejected: %v", err)
	}

	header.Set("Host", "evil.example")
	if err := ValidateTrailers(header); !errors.Is(err, ErrForbiddenTrailer) {
		t.Fatalf("host via http.Header: err = %v, want ErrForbiddenTrailer", err)
	}

	cl := http.Header{}
	cl.Set("Content-Length", "12")
	if err := ValidateTrailers(cl); !errors.Is(err, ErrForbiddenTrailer) {
		t.Fatalf("content-length via http.Header: err = %v, want ErrForbiddenTrailer", err)
	}
}

func TestEmptyBlockIsSafe(t *testing.T) {
	if err := ValidateTrailerFields(nil); err != nil {
		t.Fatalf("nil trailer block rejected: %v", err)
	}
	if err := ValidateTrailers(http.Header{}); err != nil {
		t.Fatalf("empty trailer header rejected: %v", err)
	}
}
