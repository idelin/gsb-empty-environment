package netsec_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"example.org/netsec/q0387"
)

func authConfig(server string, hosts ...netsec.HostEntry) *netsec.Config {
	return &netsec.Config{Server: server, Hosts: hosts}
}

func sha256Digest(data string) string {
	sum := sha256.Sum256([]byte(data))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TestPlaintextMirror covers the "明文镜像" scenario: plaintext http and
// skip_verify must be rejected off loopback, with the loopback
// exception controlled by policy.
func TestPlaintextMirror(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *netsec.Config
		policy  netsec.Policy
		wantErr error
	}{
		{
			name: "plaintext mirror rejected",
			cfg: authConfig("https://registry.example.com",
				netsec.HostEntry{Host: "http://mirror.example.net"}),
			policy:  netsec.DefaultPolicy(),
			wantErr: netsec.ErrPlaintextNotAllowed,
		},
		{
			name: "https mirror accepted",
			cfg: authConfig("https://registry.example.com",
				netsec.HostEntry{Host: "https://mirror.example.net"}),
			policy:  netsec.DefaultPolicy(),
			wantErr: nil,
		},
		{
			name: "loopback http allowed by default policy",
			cfg: authConfig("https://registry.example.com",
				netsec.HostEntry{Host: "http://127.0.0.1:5000"}),
			policy:  netsec.DefaultPolicy(),
			wantErr: nil,
		},
		{
			name: "loopback http rejected when policy disallows it",
			cfg: authConfig("https://registry.example.com",
				netsec.HostEntry{Host: "http://localhost:5000"}),
			policy:  netsec.Policy{},
			wantErr: netsec.ErrPlaintextNotAllowed,
		},
		{
			name: "skip_verify rejected off loopback",
			cfg: authConfig("https://registry.example.com",
				netsec.HostEntry{Host: "https://mirror.example.net", SkipVerify: true}),
			policy:  netsec.DefaultPolicy(),
			wantErr: netsec.ErrSkipVerifyNotAllowed,
		},
		{
			name: "skip_verify allowed on loopback",
			cfg: authConfig("https://registry.example.com",
				netsec.HostEntry{Host: "https://127.0.0.1:5000", SkipVerify: true}),
			policy:  netsec.DefaultPolicy(),
			wantErr: nil,
		},
		{
			name: "skip_verify on loopback rejected by strict policy",
			cfg: authConfig("https://registry.example.com",
				netsec.HostEntry{Host: "https://[::1]:5000", SkipVerify: true}),
			policy:  netsec.Policy{},
			wantErr: netsec.ErrSkipVerifyNotAllowed,
		},
		{
			name:    "plaintext server rejected",
			cfg:     authConfig("http://registry.example.com"),
			policy:  netsec.DefaultPolicy(),
			wantErr: netsec.ErrPlaintextNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := netsec.Validate(tt.cfg, tt.policy)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate() error = %v, want errors.Is %v", err, tt.wantErr)
			}
		})
	}
}

// TestCredentialHeader covers the "凭证头外发" scenario: an
// Authorization header may only land on a host in the server's domain.
func TestCredentialHeader(t *testing.T) {
	t.Parallel()

	foreign := authConfig("https://registry.example.com",
		netsec.HostEntry{
			Host:   "https://mirror.example.net",
			Header: map[string]string{"Authorization": "Bearer token"},
		},
	)
	if err := netsec.Validate(foreign, netsec.DefaultPolicy()); !errors.Is(err, netsec.ErrCredentialLeak) {
		t.Fatalf("foreign Authorization header: got %v, want ErrCredentialLeak", err)
	}

	sameOrigin := authConfig("https://registry.example.com",
		netsec.HostEntry{
			Host:   "https://registry.example.com",
			Header: map[string]string{"authorization": "Bearer token"},
		},
	)
	if err := netsec.Validate(sameOrigin, netsec.DefaultPolicy()); err != nil {
		t.Fatalf("same-origin Authorization header: got %v, want nil", err)
	}

	// A non-Authorization header may still go to a foreign mirror.
	otherHeader := authConfig("https://registry.example.com",
		netsec.HostEntry{
			Host:   "https://mirror.example.net",
			Header: map[string]string{"X-Trace-Id": "abc"},
		},
	)
	if err := netsec.Validate(otherHeader, netsec.DefaultPolicy()); err != nil {
		t.Fatalf("foreign non-Authorization header: got %v, want nil", err)
	}

	// Same-domain check must normalize spelling: upper-case host and a
	// trailing dot still count as the server's domain.
	variant := authConfig("https://REGISTRY.Example.COM",
		netsec.HostEntry{
			Host:   "https://registry.example.com.",
			Header: map[string]string{"Authorization": "Bearer token"},
		},
	)
	if err := netsec.Validate(variant, netsec.DefaultPolicy()); err != nil {
		t.Fatalf("normalized same-domain Authorization header: got %v, want nil", err)
	}
}

// TestResolveOnlyPull covers the "仅 resolve 提供 pull" scenario: a
// resolve-only mirror must never be selected for pulls.
func TestResolveOnlyPull(t *testing.T) {
	t.Parallel()

	resolveOnly := netsec.HostEntry{
		Host:         "https://resolve-only.example.net",
		Capabilities: []netsec.Capability{netsec.CapabilityResolve},
	}
	origin := netsec.HostEntry{Host: "https://registry.example.com"}

	if !resolveOnly.IsResolveOnly() {
		t.Fatal("resolve-only host not detected")
	}
	if origin.IsResolveOnly() {
		t.Fatal("host without explicit capabilities defaults to all capabilities")
	}

	cfg := authConfig("https://registry.example.com", resolveOnly, origin)
	hosts, err := netsec.HostsForPull(cfg, netsec.DefaultPolicy())
	if err != nil {
		t.Fatalf("HostsForPull() unexpected error: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Host != origin.Host {
		t.Fatalf("HostsForPull() = %v, want only %q", hosts, origin.Host)
	}

	allResolveOnly := authConfig("https://registry.example.com", resolveOnly)
	if _, err := netsec.HostsForPull(allResolveOnly, netsec.DefaultPolicy()); !errors.Is(err, netsec.ErrNoPullHost) {
		t.Fatalf("all resolve-only: got %v, want ErrNoPullHost", err)
	}

	badCapability := authConfig("https://registry.example.com",
		netsec.HostEntry{
			Host:         "https://mirror.example.net",
			Capabilities: []netsec.Capability{"teleport"},
		},
	)
	if err := netsec.Validate(badCapability, netsec.DefaultPolicy()); !errors.Is(err, netsec.ErrUnknownCapability) {
		t.Fatalf("unknown capability: got %v, want ErrUnknownCapability", err)
	}
}

// TestSameDomainVariants covers the "同域写法变形" scenario: case,
// default port and trailing dot are normalized before comparison.
func TestSameDomainVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"case insensitive", "https://REGISTRY.Example.COM/v2/", "https://registry.example.com", true},
		{"trailing dot", "https://registry.example.com.", "https://registry.example.com:443", true},
		{"default https port", "https://registry.example.com", "https://registry.example.com:443", true},
		{"default http port", "http://registry.example.com:80", "http://registry.example.com", true},
		{"scheme implies different port", "https://registry.example.com", "http://registry.example.com", false},
		{"explicit non-default port", "https://registry.example.com:5000", "https://registry.example.com", false},
		{"different host", "https://registry.example.com", "https://mirror.example.com", false},
		{"loopback ip default port", "https://127.0.0.1", "https://127.0.0.1:443", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := netsec.SameDomain(tt.a, tt.b)
			if err != nil {
				t.Fatalf("SameDomain(%q, %q) unexpected error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Fatalf("SameDomain(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}

	if _, err := netsec.SameDomain("registry.example.com", "https://registry.example.com"); !errors.Is(err, netsec.ErrInvalidEndpoint) {
		t.Fatalf("relative URL: got %v, want ErrInvalidEndpoint", err)
	}
}
