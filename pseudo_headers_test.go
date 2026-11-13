package netsec

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

func TestValidatePseudoHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers []Header
		wantErr error
	}{
		{
			name: "ordinary request",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "example.test"},
				{Name: ":path", Value: "/index.html"},
				{Name: "user-agent", Value: "gateway"},
			},
		},
		{
			name: "authority may follow path",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
				{Name: ":authority", Value: "example.test"},
			},
		},
		{
			name: "authority is optional",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
			},
		},
		{
			name: "duplicate path",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/public"},
				{Name: ":path", Value: "/admin"},
			},
			wantErr: ErrDuplicatePseudoHeader,
		},
		{
			name: "duplicate path and swapped order",
			headers: []Header{
				{Name: ":path", Value: "/admin"},
				{Name: ":method", Value: "GET"},
				{Name: ":path", Value: "/public"},
				{Name: ":scheme", Value: "https"},
			},
			wantErr: ErrDuplicatePseudoHeader,
		},
		{
			name: "duplicate method",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":method", Value: "POST"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
			},
			wantErr: ErrDuplicatePseudoHeader,
		},
		{
			name: "duplicate scheme",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":scheme", Value: "http"},
				{Name: ":path", Value: "/"},
			},
			wantErr: ErrDuplicatePseudoHeader,
		},
		{
			name: "duplicate authority",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":authority", Value: "one.test"},
				{Name: ":authority", Value: "two.test"},
				{Name: ":path", Value: "/"},
			},
			wantErr: ErrDuplicatePseudoHeader,
		},
		{
			name: "path before method",
			headers: []Header{
				{Name: ":path", Value: "/"},
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
			},
			wantErr: ErrPseudoHeaderOrder,
		},
		{
			name: "path before scheme",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":path", Value: "/"},
				{Name: ":scheme", Value: "https"},
			},
			wantErr: ErrPseudoHeaderOrder,
		},
		{
			name: "pseudo-header after regular header",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: "user-agent", Value: "gateway"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
			},
			wantErr: ErrPseudoHeaderAfterField,
		},
		{
			name: "missing method",
			headers: []Header{
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
			},
			wantErr: ErrMissingPseudoHeader,
		},
		{
			name: "missing scheme",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":path", Value: "/"},
			},
			wantErr: ErrMissingPseudoHeader,
		},
		{
			name: "missing path",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
			},
			wantErr: ErrMissingPseudoHeader,
		},
		{
			name: "unknown pseudo-header",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
				{Name: ":status", Value: "200"},
			},
			wantErr: ErrUnknownPseudoHeader,
		},
		{
			name: "empty path",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: ""},
			},
			wantErr: ErrEmptyPseudoHeader,
		},
		{
			name: "duplicate regular header is allowed",
			headers: []Header{
				{Name: ":method", Value: "GET"},
				{Name: ":scheme", Value: "https"},
				{Name: ":path", Value: "/"},
				{Name: "x-forwarded-for", Value: "10.0.0.1"},
				{Name: "x-forwarded-for", Value: "10.0.0.2"},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePseudoHeaders(test.headers)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidatePseudoHeaders() error = %v, want nil", err)
				}
				if !CheckPseudoHeaders(test.headers) {
					t.Fatal("CheckPseudoHeaders() = false, want true")
				}
				if got := Decide(test.headers); got != Allow {
					t.Fatalf("Decide() = %v, want %v", got, Allow)
				}
				return
			}
			if err == nil {
				t.Fatal("ValidatePseudoHeaders() = nil, want error")
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidatePseudoHeaders() error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
			if CheckPseudoHeaders(test.headers) {
				t.Fatal("CheckPseudoHeaders() = true, want false")
			}
			if got := Decide(test.headers); got != Deny {
				t.Fatalf("Decide() = %v, want %v", got, Deny)
			}
		})
	}
}

func TestValidatePseudoHeadersInputForms(t *testing.T) {
	if err := ValidatePseudoHeaders("GET", "https", "example.test", "/"); err != nil {
		t.Fatalf("four-string form: %v", err)
	}
	if err := ValidatePseudoHeaders("GET", "https", "/"); err != nil {
		t.Fatalf("three-string form: %v", err)
	}
	if err := ValidatePseudoHeaders(RequestPseudoHeaders{
		Method:    "GET",
		Scheme:    "https",
		Authority: "example.test",
		Path:      "/",
	}); err != nil {
		t.Fatalf("struct form: %v", err)
	}
	if err := ValidatePseudoHeaders([][2]string{
		{":method", "GET"},
		{":scheme", "https"},
		{":path", "/"},
	}); err != nil {
		t.Fatalf("pair form: %v", err)
	}
	if err := ValidatePseudoHeaders([]string{
		":method", "GET",
		":scheme", "https",
		":path", "/",
		"user-agent", "gateway",
	}); err != nil {
		t.Fatalf("string pair form: %v", err)
	}
	if err := ValidatePseudoHeaders([]string{":method", ":scheme", ":path"}); err != nil {
		t.Fatalf("name-only form: %v", err)
	}
	if err := ValidatePseudoHeaders(HeaderList{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: "/"},
	}); err != nil {
		t.Fatalf("HeaderList form: %v", err)
	}
	if err := ValidatePseudoHeaders(&RequestPseudoHeaders{
		Method: "GET",
		Scheme: "https",
		Path:   "/",
	}); err != nil {
		t.Fatalf("pointer struct form: %v", err)
	}
	if err := ValidatePseudoHeaders([][]string{
		{":method", "GET"},
		{":scheme", "https"},
		{":path", "/"},
	}); err != nil {
		t.Fatalf("string pair slice form: %v", err)
	}
	if err := ValidatePseudoHeaders([]any{
		Header{Name: ":method", Value: "GET"},
		Header{Name: ":scheme", Value: "https"},
		Header{Name: ":path", Value: "/"},
	}); err != nil {
		t.Fatalf("interface slice form: %v", err)
	}
	if err := ValidatePseudoHeaders([][2]any{
		{":method", "GET"},
		{":scheme", "https"},
		{":path", "/"},
	}); err != nil {
		t.Fatalf("interface pair form: %v", err)
	}
	if err := ValidatePseudoHeaders(map[string]string{
		":method": "GET",
		":scheme": "https",
		":path":   "/",
	}); err != nil {
		t.Fatalf("string map form: %v", err)
	}
	if err := ValidatePseudoHeaders(url.Values{
		":method": {"GET"},
		":scheme": {"https"},
		":path":   {"/"},
	}); err != nil {
		t.Fatalf("url.Values form: %v", err)
	}
}

func TestValidatePseudoHeadersMap(t *testing.T) {
	valid := map[string][]string{
		":method":    {"GET"},
		":scheme":    {"https"},
		":authority": {"example.test"},
		":path":      {"/"},
	}
	if err := ValidatePseudoHeaders(valid); err != nil {
		t.Fatalf("valid map: %v", err)
	}

	duplicatePath := map[string][]string{
		":method": {"GET"},
		":scheme": {"https"},
		":path":   {"/public", "/admin"},
	}
	if err := ValidatePseudoHeaders(duplicatePath); !errors.Is(err, ErrDuplicatePseudoHeader) {
		t.Fatalf("duplicate path map error = %v, want %v", err, ErrDuplicatePseudoHeader)
	}

	httpHeaders := http.Header{
		":method":    {"GET"},
		":scheme":    {"https"},
		":authority": {"example.test"},
		":path":      {"/"},
		"user-agent": {"gateway"},
	}
	if err := ValidatePseudoHeaders(httpHeaders); err != nil {
		t.Fatalf("http.Header: %v", err)
	}

	httpHeaders[":path"] = []string{"/public", "/admin"}
	if err := ValidatePseudoHeaders(httpHeaders); !errors.Is(err, ErrDuplicatePseudoHeader) {
		t.Fatalf("http.Header duplicate path error = %v, want %v", err, ErrDuplicatePseudoHeader)
	}
}

type testHeader struct {
	Name  string
	Value string
}

type testHeaderKV struct {
	Key string
	Val string
}

func TestValidatePseudoHeadersReflection(t *testing.T) {
	headers := []testHeader{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "https"},
		{Name: ":path", Value: "/"},
	}
	if err := ValidatePseudoHeaders(headers); err != nil {
		t.Fatalf("reflection form: %v", err)
	}

	headers = append(headers, testHeader{Name: ":path", Value: "/admin"})
	if err := ValidatePseudoHeaders(headers); !errors.Is(err, ErrDuplicatePseudoHeader) {
		t.Fatalf("reflection duplicate error = %v, want %v", err, ErrDuplicatePseudoHeader)
	}

	if err := ValidatePseudoHeaders([]testHeaderKV{
		{Key: ":method", Val: "GET"},
		{Key: ":scheme", Val: "https"},
		{Key: ":path", Val: "/"},
	}); err != nil {
		t.Fatalf("key/value reflection form: %v", err)
	}
}

func TestValidatePseudoHeadersRejectsUnsupportedInput(t *testing.T) {
	if err := ValidatePseudoHeaders(42); !errors.Is(err, ErrUnsupportedHeaderInput) {
		t.Fatalf("error = %v, want %v", err, ErrUnsupportedHeaderInput)
	}
	if err := ValidatePseudoHeaders(); !errors.Is(err, ErrUnsupportedHeaderInput) {
		t.Fatalf("empty call error = %v, want %v", err, ErrUnsupportedHeaderInput)
	}
	if err := ValidatePseudoHeaders(map[int]string{1: "GET"}); !errors.Is(err, ErrUnsupportedHeaderInput) {
		t.Fatalf("map key error = %v, want %v", err, ErrUnsupportedHeaderInput)
	}
}

func TestGateway(t *testing.T) {
	gateway := NewGateway()
	if err := gateway.ValidatePseudoHeaders("GET", "https", "example.test", "/"); err != nil {
		t.Fatalf("gateway rejected ordinary request: %v", err)
	}
	if gateway.Allowed([]Header{
		{Name: ":path", Value: "/"},
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "https"},
	}) {
		t.Fatal("gateway allowed path before method")
	}
}
