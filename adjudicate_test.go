package netsec

import (
	"errors"
	"net/http"
	"testing"
)

func legalRequest() []HeaderField {
	return []HeaderField{
		{Name: ":method", Value: "POST"},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: "/gateway"},
		{Name: ":authority", Value: "example.org"},
		{Name: "content-type", Value: "application/json"},
		{Name: "x-custom-header", Value: "keep-me"},
	}
}

func TestAdjudicateRequest_LegalRequestPasses(t *testing.T) {
	fields := legalRequest()
	if err := AdjudicateRequest(fields); err != nil {
		t.Fatalf("legal request rejected: %v", err)
	}
}

func TestAdjudicateRequest_ForbiddenHeaders(t *testing.T) {
	forbidden := []string{"connection", "keep-alive", "transfer-encoding", "upgrade"}
	for _, name := range forbidden {
		t.Run(name, func(t *testing.T) {
			fields := append(legalRequest(), HeaderField{Name: name, Value: "smuggle"})
			err := AdjudicateRequest(fields)
			if err == nil {
				t.Fatalf("header %q must fail adjudication", name)
			}
			streamErr := AsStreamError(err)
			if streamErr == nil {
				t.Fatalf("expected *StreamError, got %T: %v", err, err)
			}
			if streamErr.Code != ErrorForbiddenHeader {
				t.Fatalf("expected code %q, got %q", ErrorForbiddenHeader, streamErr.Code)
			}
			if streamErr.HeaderName != name {
				t.Fatalf("expected header %q, got %q", name, streamErr.HeaderName)
			}
		})
	}
}

func TestAdjudicateRequest_CaseInsensitive(t *testing.T) {
	variants := []string{"Connection", "TRANSFER-ENCODING", "Keep-Alive", "UpGrAdE"}
	for _, name := range variants {
		fields := append(legalRequest(), HeaderField{Name: name, Value: "x"})
		err := AdjudicateRequest(fields)
		if err == nil {
			t.Fatalf("header %q must be rejected case-insensitively", name)
		}
		streamErr := AsStreamError(err)
		if streamErr == nil || streamErr.Code != ErrorForbiddenHeader {
			t.Fatalf("expected forbidden-header stream error for %q, got %v", name, err)
		}
	}
}

func TestAdjudicateRequest_DoesNotMutateFields(t *testing.T) {
	fields := append(legalRequest(), HeaderField{Name: "x-other", Value: "1"})
	original := make([]HeaderField, len(fields))
	copy(original, fields)

	if err := AdjudicateRequest(fields); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range fields {
		if fields[i] != original[i] {
			t.Fatalf("field %d mutated: got %+v want %+v", i, fields[i], original[i])
		}
	}
}

func TestAdjudicateRequest_PseudoAfterRegular(t *testing.T) {
	fields := []HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: "x-custom", Value: "1"},
		{Name: ":path", Value: "/"},
	}
	err := AdjudicateRequest(fields)
	streamErr := AsStreamError(err)
	if streamErr == nil || streamErr.Code != ErrorPseudoAfterRegular {
		t.Fatalf("expected pseudo-after-regular stream error, got %v", err)
	}
}

func TestIsForbiddenHeader(t *testing.T) {
	for _, name := range []string{"connection", "keep-alive", "transfer-encoding", "upgrade"} {
		if !IsForbiddenHeader(name) {
			t.Errorf("IsForbiddenHeader(%q) = false, want true", name)
		}
	}
	for _, name := range []string{":method", ":path", "x-custom", "content-length", "te"} {
		if IsForbiddenHeader(name) {
			t.Errorf("IsForbiddenHeader(%q) = true, want false", name)
		}
	}
}

func TestAdjudicateHTTPHeader(t *testing.T) {
	legal := http.Header{}
	legal.Set("Content-Type", "application/json")
	legal.Set("X-Custom-Header", "keep-me")
	if err := AdjudicateHTTPHeader(legal); err != nil {
		t.Fatalf("legal http.Header rejected: %v", err)
	}

	forbidden := []string{"Connection", "Keep-Alive", "Transfer-Encoding", "Upgrade"}
	for _, name := range forbidden {
		t.Run(name, func(t *testing.T) {
			header := http.Header{}
			header.Set("X-Custom-Header", "keep-me")
			header.Set(name, "smuggle")

			err := AdjudicateHTTPHeader(header)
			var streamErr *StreamError
			if !errors.As(err, &streamErr) || streamErr.Code != ErrorForbiddenHeader {
				t.Fatalf("expected forbidden-header stream error for %q, got %v", name, err)
			}

			if got := header.Get("X-Custom-Header"); got != "keep-me" {
				t.Fatalf("custom header must be retained, got %q", got)
			}
		})
	}
}
