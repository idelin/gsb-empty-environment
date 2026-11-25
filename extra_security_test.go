package workloadid

import (
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"
)

// TestIPNameConstraints verifies IP SANs are checked against IP constraint
// ranges in their own name space and cannot bypass via DNS.
func TestIPNameConstraints(t *testing.T) {
	f := newFixture(t)
	_, allowed, _ := net.ParseCIDR("10.0.0.0/8")
	constrained, err := newIntermediate(f.root, "ip-constrained", leafOptions{
		serial: 90, ttl: 24 * time.Hour, permittedIP: []*net.IPNet{allowed},
	})
	if err != nil {
		t.Fatal(err)
	}

	outside, err := newLeaf(constrained, leafOptions{
		serial: 91, ips: []net.IP{net.ParseIP("192.0.2.5")},
		uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkNameConstraints(outside.cert, []*x509.Certificate{constrained.cert}); !errors.Is(err, ErrPermittedNameConstraint) {
		t.Fatalf("out-of-range IP must be denied, got %v", err)
	}

	inside, err := newLeaf(constrained, leafOptions{
		serial: 92, ips: []net.IP{net.ParseIP("10.1.2.3")},
		uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkNameConstraints(inside.cert, []*x509.Certificate{constrained.cert}); err != nil {
		t.Fatalf("in-range IP should pass, got %v", err)
	}

	// Excluded range.
	_, excluded, _ := net.ParseCIDR("10.5.0.0/16")
	excludedCA, err := newIntermediate(f.root, "ip-excluded", leafOptions{
		serial: 93, ttl: 24 * time.Hour, excludedIP: []*net.IPNet{excluded},
	})
	if err != nil {
		t.Fatal(err)
	}
	excludedLeaf, err := newLeaf(excludedCA, leafOptions{
		serial: 94, ips: []net.IP{net.ParseIP("10.5.9.9")},
		uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkNameConstraints(excludedLeaf.cert, []*x509.Certificate{excludedCA.cert}); !errors.Is(err, ErrExcludedNameConstraint) {
		t.Fatalf("excluded IP must be denied, got %v", err)
	}
}

// TestURINameConstraints verifies URI host-based constraints.
func TestURINameConstraints(t *testing.T) {
	f := newFixture(t)
	constrained, err := newIntermediate(f.root, "uri-constrained", leafOptions{
		serial: 95, ttl: 24 * time.Hour, permittedURI: []string{"prod.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The SPIFFE URI host is prod.example.com, so it is in scope.
	inScope, err := newLeaf(constrained, leafOptions{
		serial: 96, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkNameConstraints(inScope.cert, []*x509.Certificate{constrained.cert}); err != nil {
		t.Fatalf("in-scope URI should pass: %v", err)
	}

	outCA, err := newIntermediate(f.root, "uri-out", leafOptions{
		serial: 97, ttl: 24 * time.Hour, permittedURI: []string{"other.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	outLeaf, err := newLeaf(outCA, leafOptions{
		serial: 98, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkNameConstraints(outLeaf.cert, []*x509.Certificate{outCA.cert}); !errors.Is(err, ErrPermittedNameConstraint) {
		t.Fatalf("out-of-scope URI must be denied, got %v", err)
	}
}

// TestMultipleSPIFFEIDsRejected ensures ambiguous identity (two SPIFFE SANs)
// fails closed.
func TestMultipleSPIFFEIDsRejected(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{
		serial: 99,
		uris: []*url.URL{
			mustURI(spiffeAPI),
			mustURI("spiffe://prod.example.com/ns/other/svc/x"),
		},
	})
	if _, _, err := f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert)); !errors.Is(err, ErrMultipleSPIFFEID) {
		t.Fatalf("expected ErrMultipleSPIFFEID, got %v", err)
	}
}

// TestRequireRevocationFailsClosed verifies that with RequireRevocation a
// certificate lacking revocation data is denied rather than allowed.
func TestRequireRevocationFailsClosed(t *testing.T) {
	f := newFixture(t)
	snap := f.manager.MustCurrent()
	cfg := snap.Config
	cfg.Revocation.RequireRevocation = true
	if _, err := f.manager.Activate(snap.Generation+1, cfg, RootBundle{
		Roots:         []*x509.Certificate{f.root.cert},
		Intermediates: []*x509.Certificate{f.inter.cert},
	}); err != nil {
		t.Fatal(err)
	}
	leaf := f.prodLeaf(leafOptions{serial: 110, uris: []*url.URL{mustURI(spiffeAPI)}})
	_, _, err := f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert))
	if !errors.Is(err, ErrCRLMissing) {
		t.Fatalf("expected ErrCRLMissing fail-closed, got %v", err)
	}
}

// TestPolicyValidation covers the configuration-time guards.
func TestPolicyValidation(t *testing.T) {
	cases := []struct {
		name string
		p    *Policy
	}{
		{"empty trust domain", &Policy{AllowedPathPrefixes: [][]string{{"x"}}}},
		{"no prefixes", &Policy{TrustDomain: prodTD}},
		{"bad prefix segment", &Policy{TrustDomain: prodTD, AllowedPathPrefixes: [][]string{{".."}}}},
		{"bad attestation", &Policy{
			TrustDomain: prodTD, AllowedPathPrefixes: [][]string{{"x"}},
			RequireAttestation: &NodeAttestation{Type: "", AllowedPrincipals: []string{"a"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.p.Validate(); !errors.Is(err, ErrInvalidPolicy) &&
				!errors.Is(err, ErrInvalidAttestation) {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}

	good := &Policy{
		TrustDomain: prodTD, AllowedPathPrefixes: [][]string{{"ns"}},
		AllowedIPs: []string{"10.0.0.1"},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid policy should pass: %v", err)
	}
	badIP := &Policy{TrustDomain: prodTD, AllowedPathPrefixes: [][]string{{"ns"}}, AllowedIPs: []string{"not-ip"}}
	if err := badIP.Validate(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("expected ErrInvalidPolicy for bad IP, got %v", err)
	}
}

// TestNoPeerCertsAndNoPolicies covers request validation guards.
func TestNoPeerCertsAndNoPolicies(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.verifier.Authenticate(nil); !errors.Is(err, ErrNoPeerCertificates) {
		t.Fatalf("expected ErrNoPeerCertificates, got %v", err)
	}
	if _, err := f.verifier.Authorize(AuthRequest{RawCerts: [][]byte{{1}}}); !errors.Is(err, ErrNoPolicies) {
		t.Fatalf("expected ErrNoPolicies, got %v", err)
	}
}
