package netsec

import (
	"errors"
	"testing"
)

func TestSameHost(t *testing.T) {
	tests := []struct {
		name       string
		issuer     string
		statusList string
		wantSame   bool
	}{
		{
			name:       "same host different paths",
			issuer:     "https://issuer.example/tenant/one",
			statusList: "https://issuer.example/status/1",
			wantSame:   true,
		},
		{
			name:       "host comparison is case insensitive",
			issuer:     "HTTPS://ISSUER.EXAMPLE",
			statusList: "https://Issuer.Example/status/1",
			wantSame:   true,
		},
		{
			name:       "trailing dot is the same DNS host",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example./status/1",
			wantSame:   true,
		},
		{
			name:       "port is not part of the host",
			issuer:     "https://issuer.example:8443",
			statusList: "https://issuer.example/status/1",
			wantSame:   true,
		},
		{
			name:       "different host",
			issuer:     "https://issuer.example",
			statusList: "https://evil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "lookalike suffix",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example.evil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "userinfo cannot smuggle another host",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example@evil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "fragment does not change the host",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example/status/1#@evil.example",
			wantSame:   true,
		},
		{
			name:       "relative URI is not trusted",
			issuer:     "https://issuer.example",
			statusList: "/status/1",
			wantSame:   false,
		},
		{
			name:       "scheme relative URI is not trusted",
			issuer:     "https://issuer.example",
			statusList: "//evil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "empty URI is not trusted",
			issuer:     "https://issuer.example",
			statusList: "",
			wantSame:   false,
		},
		{
			name:       "malformed port is not trusted",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example:bad/status/1",
			wantSame:   false,
		},
		{
			name:       "unicode lookalike host is different",
			issuer:     "https://issuer.example",
			statusList: "https://іssuer.example/status/1",
			wantSame:   false,
		},
		{
			name:       "backslash cannot hide another host",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example\\@evil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "encoded slash cannot hide another host",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example%2Fevil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "encoded at sign cannot hide another host",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example%40evil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "encoded dot cannot extend the host",
			issuer:     "https://issuer.example",
			statusList: "https://issuer.example%2Eevil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "did web issuer same host",
			issuer:     "did:web:issuer.example",
			statusList: "https://issuer.example/status/1",
			wantSame:   true,
		},
		{
			name:       "did web issuer with port",
			issuer:     "did:web:issuer.example%3A8443",
			statusList: "https://issuer.example:8443/status/1",
			wantSame:   true,
		},
		{
			name:       "did web issuer different host",
			issuer:     "did:web:issuer.example",
			statusList: "https://evil.example/status/1",
			wantSame:   false,
		},
		{
			name:       "bare issuer host",
			issuer:     "issuer.example",
			statusList: "https://issuer.example/status/1",
			wantSame:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameHost(tt.issuer, tt.statusList); got != tt.wantSame {
				t.Fatalf("SameHost(%q, %q) = %v, want %v", tt.issuer, tt.statusList, got, tt.wantSame)
			}
			if got := StatusListURIAllowed(tt.issuer, tt.statusList); got != tt.wantSame {
				t.Fatalf("StatusListURIAllowed(%q, %q) = %v, want %v", tt.issuer, tt.statusList, got, tt.wantSame)
			}
		})
	}
}

func TestValidateStatusListURI(t *testing.T) {
	if err := ValidateStatusListURI("https://issuer.example", "https://issuer.example/status/1"); err != nil {
		t.Fatalf("same-host list rejected: %v", err)
	}

	err := ValidateStatusListURI("https://issuer.example", "https://evil.example/status/1")
	if !errors.Is(err, ErrStatusListHostMismatch) {
		t.Fatalf("different-host list error = %v, want ErrStatusListHostMismatch", err)
	}

	err = ValidateStatusListURI("https://issuer.example", "/status/1")
	if !errors.Is(err, ErrInvalidStatusListURI) {
		t.Fatalf("relative status list error = %v, want ErrInvalidStatusListURI", err)
	}
}

func TestCanonicalHostNormalizesIPv6(t *testing.T) {
	if !SameHost("https://[0:0:0:0:0:0:0:1]", "https://[::1]/status") {
		t.Fatal("canonical IPv6 hosts should compare equal")
	}
}

func TestIssuerHostRejectsMalformedDIDWebHost(t *testing.T) {
	if _, err := IssuerHost("did:web:issuer.example%2Fevil.example"); !errors.Is(err, ErrInvalidIssuer) {
		t.Fatalf("IssuerHost error = %v, want ErrInvalidIssuer", err)
	}
}
