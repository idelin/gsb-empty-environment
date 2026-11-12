package netsec

import (
	"errors"
	"testing"
)

func validBlock() []HeaderField {
	return []HeaderField{
		{Name: ":method", Value: "GET"},
		{Name: ":scheme", Value: "https"},
		{Name: ":authority", Value: "acme.example"},
		{Name: ":path", Value: "/index"},
		{Name: "content-type", Value: "application/json"},
	}
}

func TestValidatePseudoHeader(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func([]HeaderField) []HeaderField
		wantErr error
	}{
		{
			name:    "valid block",
			mutate:  func(h []HeaderField) []HeaderField { return h },
			wantErr: nil,
		},
		{name: "missing method", mutate: func(h []HeaderField) []HeaderField {
			return h[1:]
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "duplicate method", mutate: func(h []HeaderField) []HeaderField {
			return append(h, HeaderField{Name: ":method", Value: "HEAD"})
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "unknown pseudo header", mutate: func(h []HeaderField) []HeaderField {
			h[0].Name = ":trace"
			return h
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "pseudo after regular", mutate: func(h []HeaderField) []HeaderField {
			return append([]HeaderField{h[4]}, h[:4]...)
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "unsafe method", mutate: func(h []HeaderField) []HeaderField {
			h[0].Value = "POST"
			return h
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "uppercase header name", mutate: func(h []HeaderField) []HeaderField {
			h[4].Name = "Content-Type"
			return h
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "connection specific", mutate: func(h []HeaderField) []HeaderField {
			return append(h, HeaderField{Name: "connection", Value: "keep-alive"})
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "te forbidden", mutate: func(h []HeaderField) []HeaderField {
			return append(h, HeaderField{Name: "te", Value: "trailers"})
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "host mismatch", mutate: func(h []HeaderField) []HeaderField {
			return append(h, HeaderField{Name: "host", Value: "evil.example"})
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "duplicate host", mutate: func(h []HeaderField) []HeaderField {
			return append(h,
				HeaderField{Name: "host", Value: "acme.example"},
				HeaderField{Name: "host", Value: "acme.example"})
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "empty authority", mutate: func(h []HeaderField) []HeaderField {
			h[2].Value = ""
			return h
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "path with fragment", mutate: func(h []HeaderField) []HeaderField {
			h[3].Value = "/index#frag"
			return h
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "relative path", mutate: func(h []HeaderField) []HeaderField {
			h[3].Value = "index"
			return h
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "protocol relative path", mutate: func(h []HeaderField) []HeaderField {
			h[3].Value = "//evil.example/index"
			return h
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "header value with newline", mutate: func(h []HeaderField) []HeaderField {
			h[4].Value = "application/json\r\nEvil: yes"
			return h
		}, wantErr: ErrInvalidPseudoHeader},
		{name: "empty block", mutate: func(h []HeaderField) []HeaderField {
			return nil
		}, wantErr: ErrInvalidPseudoHeader},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			block := tc.mutate(validBlock())
			got, err := ValidatePseudoHeader(block)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Origin.String() != "https://acme.example:443" {
					t.Fatalf("canonical origin: got %q", got.Origin.String())
				}
				if got.Method != "GET" || got.Path != "/index" {
					t.Fatalf("parsed request mismatch: %+v", got)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestOPTIONSStarPath(t *testing.T) {
	block := []HeaderField{
		{Name: ":method", Value: "OPTIONS"},
		{Name: ":scheme", Value: "https"},
		{Name: ":authority", Value: "acme.example"},
		{Name: ":path", Value: "*"},
	}
	if _, err := ValidatePseudoHeader(block); err != nil {
		t.Fatalf("OPTIONS * should be valid: %v", err)
	}
}

func TestSchemeAllowList(t *testing.T) {
	controller := NewController(nil, WithAllowedSchemes("https"))
	mustBind(t, controller, 1, "http://acme.example")

	_, err := controller.AdmitPromise(PushPromise{
		ParentStreamID: 1, PromisedStreamID: 2,
		Headers: promiseBlock(t, "http://acme.example"),
	})
	if !errors.Is(err, ErrInvalidPseudoHeader) {
		t.Fatalf("non-allow-listed scheme: want ErrInvalidPseudoHeader, got %v", err)
	}
}

func TestOriginParsing(t *testing.T) {
	valid := []string{
		"https://acme.example",
		"https://acme.example:8443",
		"http://127.0.0.1:8080",
		"https://[2001:db8::1]:443",
	}
	for _, raw := range valid {
		if _, err := ParseOrigin(raw); err != nil {
			t.Errorf("ParseOrigin(%q): unexpected error %v", raw, err)
		}
	}

	invalid := []string{
		"acme.example",
		"https://",
		"https://acme.example/path",
		"https://acme.example?q=1",
		"https://user@acme.example",
		"https://acme.example:99999",
		"https://acme.example:bad",
	}
	for _, raw := range invalid {
		if _, err := ParseOrigin(raw); !errors.Is(err, ErrInvalidOrigin) {
			t.Errorf("ParseOrigin(%q): want ErrInvalidOrigin, got %v", raw, err)
		}
	}

	origin, err := ParseOrigin("HTTPS://ACME.Example:443")
	if err != nil {
		t.Fatalf("canonical parse: %v", err)
	}
	if origin.String() != "https://acme.example:443" {
		t.Fatalf("normalization: got %q", origin.String())
	}

	defaultPort, err := ParseOrigin("https://acme.example")
	if err != nil {
		t.Fatalf("default port parse: %v", err)
	}
	if defaultPort.Port() != 443 {
		t.Fatalf("default port: got %d", defaultPort.Port())
	}
}
