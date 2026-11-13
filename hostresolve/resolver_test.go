package hostresolve

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func trustLoopback(peer net.IP) bool {
	return peer.IsLoopback()
}

func TestResolve(t *testing.T) {
	untrusted := &Resolver{}

	tests := []struct {
		name      string
		resolver  *Resolver
		in        *Request
		wantKey   string
		wantSrc   Source
		wantHost  string
		wantPort  string
		wantError error
	}{
		{
			name:     "origin form uses Host header",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/v1/items",
				HostHeader:    []string{"tenant-a.example"},
			},
			wantKey:  "tenant-a.example",
			wantSrc:  SourceHost,
			wantHost: "tenant-a.example",
		},
		{
			name:     "origin form preserves and parses port",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/",
				HostHeader:    []string{"Tenant-A.Example:8443"},
			},
			wantKey:  "tenant-a.example:8443",
			wantSrc:  SourceHost,
			wantHost: "tenant-a.example",
			wantPort: "8443",
		},
		{
			name:     "absolute form target wins over conflicting Host header",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "https://tenant-b.example/v1/items",
				HostHeader:    []string{"tenant-a.example"},
			},
			wantKey:  "tenant-b.example",
			wantSrc:  SourceRequestTarget,
			wantHost: "tenant-b.example",
		},
		{
			name:     "absolute form target wins even with conflicting host:port",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "http://tenant-b.example:8080/c",
				HostHeader:    []string{"tenant-a.example:9000"},
			},
			wantKey:  "tenant-b.example:8080",
			wantSrc:  SourceRequestTarget,
			wantHost: "tenant-b.example",
			wantPort: "8080",
		},
		{
			name:     "missing Host rejected",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/",
			},
			wantError: ErrMissingHost,
		},
		{
			name:     "empty Host rejected",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/",
				HostHeader:    []string{""},
			},
			wantError: ErrMissingHost,
		},
		{
			name:     "duplicate Host field lines rejected",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/",
				HostHeader:    []string{"a.example", "b.example"},
			},
			wantError: ErrDuplicateHost,
		},
		{
			name:     "comma list in one Host line rejected",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/",
				HostHeader:    []string{"a.example, b.example"},
			},
			wantError: ErrDuplicateHost,
		},
		{
			name:     "Host with userinfo rejected",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/",
				HostHeader:    []string{"user@tenant-a.example"},
			},
			wantError: ErrInvalidHost,
		},
		{
			name:     "Host with path rejected",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/",
				HostHeader:    []string{"tenant-a.example/internal"},
			},
			wantError: ErrInvalidHost,
		},
		{
			name:     "Host with query rejected",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "/",
				HostHeader:    []string{"tenant-a.example?x=1"},
			},
			wantError: ErrInvalidHost,
		},
		{
			name:     "malformed Host rejected even when absolute target is valid",
			resolver: untrusted,
			in: &Request{
				RequestTarget: "https://tenant-b.example/x",
				HostHeader:    []string{"tenant-a.example/evil"},
			},
			wantError: ErrInvalidHost,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.resolver.Resolve(tc.in)
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("Resolve() error = %v, want errors.Is %v", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() unexpected error: %v", err)
			}
			if got.Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tc.wantKey)
			}
			if got.Source != tc.wantSrc {
				t.Errorf("Source = %v, want %v", got.Source, tc.wantSrc)
			}
			if got.Host != tc.wantHost {
				t.Errorf("Host = %q, want %q", got.Host, tc.wantHost)
			}
			if got.Port != tc.wantPort {
				t.Errorf("Port = %q, want %q", got.Port, tc.wantPort)
			}
			if got.RouteKey() != got.Key || got.CacheKey() != got.Key {
				t.Errorf("RouteKey/CacheKey must equal Key: route=%q cache=%q key=%q",
					got.RouteKey(), got.CacheKey(), got.Key)
			}
		})
	}
}

func TestForwardedHostTrust(t *testing.T) {
	trusted := &Resolver{IsTrustedProxy: trustLoopback}
	untrusted := &Resolver{}

	t.Run("untrusted peer forwarding header is ignored", func(t *testing.T) {
		res, err := untrusted.Resolve(&Request{
			RequestTarget: "/",
			HostHeader:    []string{"tenant-a.example"},
			ForwardedHost: []string{"evil.example"},
			RemoteAddr:    "203.0.113.7:5100",
		})
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if res.Key != "tenant-a.example" || res.Source != SourceHost {
			t.Fatalf("got key=%q source=%v, want tenant-a.example/SourceHost", res.Key, res.Source)
		}
	})

	t.Run("zero value resolver trusts nobody", func(t *testing.T) {
		var zero Resolver
		res, err := zero.Resolve(&Request{
			RequestTarget: "/",
			HostHeader:    []string{"tenant-a.example"},
			ForwardedHost: []string{"evil.example"},
			RemoteAddr:    "127.0.0.1:5100",
		})
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if res.Key != "tenant-a.example" {
			t.Fatalf("zero-value resolver honored XFH: %q", res.Key)
		}
	})

	t.Run("trusted proxy overrides origin-form host", func(t *testing.T) {
		res, err := trusted.Resolve(&Request{
			RequestTarget: "/",
			HostHeader:    []string{"proxy.internal"},
			ForwardedHost: []string{"tenant-a.example"},
			RemoteAddr:    "127.0.0.1:5100",
		})
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if res.Key != "tenant-a.example" || res.Source != SourceForwardedHost {
			t.Fatalf("got key=%q source=%v", res.Key, res.Source)
		}
	})

	t.Run("trusted proxy overrides absolute-form authority", func(t *testing.T) {
		res, err := trusted.Resolve(&Request{
			RequestTarget: "https://tenant-b.example/x",
			HostHeader:    []string{"proxy.internal"},
			ForwardedHost: []string{"tenant-a.example:443"},
			RemoteAddr:    "127.0.0.1:5100",
		})
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if res.Key != "tenant-a.example:443" || res.Source != SourceForwardedHost {
			t.Fatalf("got key=%q source=%v", res.Key, res.Source)
		}
		if res.Host != "tenant-a.example" || res.Port != "443" {
			t.Fatalf("got host=%q port=%q", res.Host, res.Port)
		}
	})

	t.Run("trusted proxy without forwarding header keeps host", func(t *testing.T) {
		res, err := trusted.Resolve(&Request{
			RequestTarget: "/",
			HostHeader:    []string{"tenant-a.example"},
			RemoteAddr:    "127.0.0.1:5100",
		})
		if err != nil {
			t.Fatalf("Resolve() error: %v", err)
		}
		if res.Key != "tenant-a.example" || res.Source != SourceHost {
			t.Fatalf("got key=%q source=%v", res.Key, res.Source)
		}
	})

	t.Run("malformed forwarding header from trusted proxy rejected", func(t *testing.T) {
		_, err := trusted.Resolve(&Request{
			RequestTarget: "/",
			HostHeader:    []string{"proxy.internal"},
			ForwardedHost: []string{"attacker@tenant-a.example"},
			RemoteAddr:    "127.0.0.1:5100",
		})
		if !errors.Is(err, ErrInvalidHost) {
			t.Fatalf("error = %v, want ErrInvalidHost", err)
		}
	})

	t.Run("duplicate forwarding header from trusted proxy rejected", func(t *testing.T) {
		_, err := trusted.Resolve(&Request{
			RequestTarget: "/",
			HostHeader:    []string{"proxy.internal"},
			ForwardedHost: []string{"a.example", "b.example"},
			RemoteAddr:    "127.0.0.1:5100",
		})
		if !errors.Is(err, ErrInvalidHost) {
			t.Fatalf("error = %v, want ErrInvalidHost", err)
		}
	})

	t.Run("malformed forwarding header from untrusted peer ignored", func(t *testing.T) {
		res, err := untrusted.Resolve(&Request{
			RequestTarget: "/",
			HostHeader:    []string{"tenant-a.example"},
			ForwardedHost: []string{"attacker@evil.example"},
			RemoteAddr:    "203.0.113.7:5100",
		})
		if err != nil {
			t.Fatalf("untrusted malformed XFH should be ignored, got %v", err)
		}
		if res.Key != "tenant-a.example" {
			t.Fatalf("key = %q, want tenant-a.example", res.Key)
		}
	})
}

func TestRequestTargetRejections(t *testing.T) {
	r := &Resolver{}
	tests := []struct {
		name   string
		target string
		host   []string
		want   error
	}{
		{"userinfo in absolute target", "https://bob@tenant-b.example/x", []string{"tenant-b.example"}, ErrInvalidRequestTarget},
		{"fragment in absolute target", "https://tenant-b.example/x#frag", []string{"tenant-b.example"}, ErrInvalidRequestTarget},
		{"fragment in origin target", "/x#frag", []string{"tenant-b.example"}, ErrInvalidRequestTarget},
		{"non-http scheme", "ftp://tenant-b.example/x", []string{"tenant-b.example"}, ErrInvalidRequestTarget},
		{"asterisk form", "*", []string{"tenant-a.example"}, ErrInvalidRequestTarget},
		{"empty target", "", []string{"tenant-a.example"}, ErrInvalidRequestTarget},
		{"absolute target without authority", "https:///x", []string{"tenant-a.example"}, ErrInvalidRequestTarget},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Resolve(&Request{RequestTarget: tc.target, HostHeader: tc.host})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAuthorityEdgeCases(t *testing.T) {
	r := &Resolver{}
	tests := []struct {
		name    string
		host    string
		wantKey string
		wantErr error
	}{
		{name: "IPv6 literal with port", host: "[2001:DB8::1]:443", wantKey: "[2001:db8::1]:443"},
		{name: "IPv6 literal without port", host: "[2001:db8::1]", wantKey: "2001:db8::1"},
		{name: "IPv4 address", host: "203.0.113.7", wantKey: "203.0.113.7"},
		{name: "out of range port", host: "a.example:99999", wantErr: ErrInvalidHost},
		{name: "non numeric port", host: "a.example:http", wantErr: ErrInvalidHost},
		{name: "unbracketed IPv6", host: "2001:db8::1", wantErr: ErrInvalidHost},
		{name: "unterminated bracket", host: "[2001:db8::1", wantErr: ErrInvalidHost},
		{name: "trailing dot refused", host: "tenant-a.example.", wantErr: ErrInvalidHost},
		{name: "leading dot refused", host: ".tenant-a.example", wantErr: ErrInvalidHost},
		{name: "percent encoding refused", host: "tenant%2da.example", wantErr: ErrInvalidHost},
		{name: "empty authority", host: "", wantErr: ErrMissingHost},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := r.Resolve(&Request{RequestTarget: "/", HostHeader: []string{tc.host}})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Key != tc.wantKey {
				t.Fatalf("key = %q, want %q", res.Key, tc.wantKey)
			}
		})
	}
}

func TestRouteAndCacheKeysAreIdenticalAcrossConflictingInputs(t *testing.T) {
	// The poisoning scenario: one request says tenant A in Host but tenant B
	// in the absolute target. A single resolved key must feed both routing and
	// caching instead of letting each subsystem pick its own header.
	r := &Resolver{}
	res, err := r.Resolve(&Request{
		RequestTarget: "https://tenant-b.example/objects/1",
		HostHeader:    []string{"tenant-a.example"},
	})
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if res.Source != SourceRequestTarget {
		t.Fatalf("Source = %v, want SourceRequestTarget", res.Source)
	}
	if res.RouteKey() != "tenant-b.example" || res.CacheKey() != "tenant-b.example" {
		t.Fatalf("routing/cache keys diverged: route=%q cache=%q", res.RouteKey(), res.CacheKey())
	}

	upper, err := r.Resolve(&Request{
		RequestTarget: "https://TENANT-B.example/objects/1",
		HostHeader:    []string{"tenant-a.example"},
	})
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if upper.CacheKey() != res.CacheKey() {
		t.Fatalf("cache key not canonical: %q vs %q", upper.CacheKey(), res.CacheKey())
	}
}

func TestMiddlewareAcceptsStoresAndRejects(t *testing.T) {
	r := &Resolver{IsTrustedProxy: trustLoopback}
	var seen *Result
	handler := r.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = FromContext(req.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://tenant-a.example/x", nil)
	req.RemoteAddr = "127.0.0.1:5100"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if seen == nil || seen.Key != "tenant-a.example" {
		t.Fatalf("context result = %+v", seen)
	}

	badRec := httptest.NewRecorder()
	badReq := httptest.NewRequest(http.MethodGet, "http://tenant-a.example/x", nil)
	badReq.Host = "tenant-a.example/evil"
	handler.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", badRec.Code)
	}
	if FromContext(badReq.Context()) != nil {
		t.Fatal("rejected request must not carry a resolution result")
	}
}

func TestFromContextWithoutMiddleware(t *testing.T) {
	if got := FromContext(t.Context()); got != nil {
		t.Fatalf("FromContext without middleware = %+v, want nil", got)
	}
}
