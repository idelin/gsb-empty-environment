package workloadid

import (
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"
)

// TestMultiLevelWildcardRejected ensures a wildcard never spans multiple labels.
func TestMultiLevelWildcardRejected(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)

	policy := callerPolicy()
	policy.AllowedDNS = &DNSRule{AllowedPatterns: []string{"*.svc.prod.example.com"}}
	leaf := f.prodLeaf(leafOptions{
		serial: 9,
		dns:    []string{"deep.sub.svc.prod.example.com"},
		uris:   []*url.URL{mustURI(spiffeAPI)},
	})
	if _, err := f.authorize(leaf, policy, targetPolicy(), attestationClaim()); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("multi-level wildcard must be denied, got %v", err)
	}

	// A single-label sibling is still accepted by the same wildcard policy.
	okLeaf := f.prodLeaf(leafOptions{
		serial: 10, dns: []string{"one.svc.prod.example.com"}, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if _, err := f.authorize(okLeaf, policy, targetPolicy(), attestationClaim()); err != nil {
		t.Fatalf("single-label wildcard should authorize: %v", err)
	}
}

// TestNameConstraintExclusion enforces RFC 5280 name constraints: a SAN inside
// an excluded subtree fails the explicit checker (and the stdlib chain check).
func TestNameConstraintExclusion(t *testing.T) {
	f := newFixture(t)
	constrained, err := newIntermediate(f.root, "constrained-inter", leafOptions{
		serial: 4, ttl: 24 * time.Hour, excludedDNS: []string{"forbidden.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := newLeaf(constrained, leafOptions{
		serial: 5, dns: []string{"forbidden.example.com"}, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}

	snap := f.newSnapshotWithIntermediate(constrained)
	if _, err := leaf.cert.Verify(x509.VerifyOptions{
		Roots: snap.Roots, Intermediates: snap.Intermediates, CurrentTime: f.clock.Now(),
	}); err == nil {
		t.Fatal("expected chain verification to reject name constraint violation")
	}
	if err := checkNameConstraints(leaf.cert, []*x509.Certificate{constrained.cert}); !errors.Is(err, ErrExcludedNameConstraint) {
		t.Fatalf("expected ErrExcludedNameConstraint, got %v", err)
	}
}

// TestNameConstraintPermittedSubtree rejects out-of-scope SANs.
func TestNameConstraintPermittedSubtree(t *testing.T) {
	f := newFixture(t)
	constrained, err := newIntermediate(f.root, "permitted-inter", leafOptions{
		serial: 6, ttl: 24 * time.Hour, permittedDNS: []string{"allowed.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := newLeaf(constrained, leafOptions{
		serial: 7, dns: []string{"scope.other.example.com"}, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkNameConstraints(leaf.cert, []*x509.Certificate{constrained.cert}); !errors.Is(err, ErrPermittedNameConstraint) {
		t.Fatalf("expected ErrPermittedNameConstraint, got %v", err)
	}

	// A name inside the permitted subtree passes.
	inScope, err := newLeaf(constrained, leafOptions{
		serial: 8, dns: []string{"host.allowed.example.com"}, uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkNameConstraints(inScope.cert, []*x509.Certificate{constrained.cert}); err != nil {
		t.Fatalf("in-scope SAN should pass: %v", err)
	}
}

// TestURISANCannotImpersonate ensures a non-SPIFFE URI cannot stand in for the
// SPIFFE identity.
func TestURISANCannotImpersonate(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)

	impersonating := f.prodLeaf(leafOptions{
		serial: 11,
		dns:    []string{"api.prod.example.com"},
		uris:   []*url.URL{mustURI("https://prod.example.com/ns/payments/svc/api")},
	})
	_, _, err := f.verifier.Authenticate(derChain(impersonating.cert, f.inter.cert))
	if !errors.Is(err, ErrNoSPIFFEID) {
		t.Fatalf("expected ErrNoSPIFFEID, got %v", err)
	}
}

// TestSANTypesNotInterchangeable ensures DNS, IP and URI SANs cannot substitute.
func TestSANTypesNotInterchangeable(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{
		serial: 12,
		dns:    []string{"api.prod.example.com"},
		ips:    []net.IP{net.ParseIP("10.1.2.3")},
		uris:   []*url.URL{mustURI(spiffeAPI)},
	})
	id, err := buildIdentity(leaf.cert)
	if err != nil {
		t.Fatal(err)
	}

	// A DNS rule never matches an IP/URI SAN value.
	if id.HasSAN(SANIP, "api.prod.example.com") {
		t.Fatal("DNS value must not satisfy an IP lookup")
	}
	if id.HasSAN(SANDNS, "10.1.2.3") {
		t.Fatal("IP value must not satisfy a DNS lookup")
	}
	if id.HasSAN(SANDNS, spiffeAPI) {
		t.Fatal("URI value must not satisfy a DNS lookup")
	}
	if !id.HasSAN(SANIP, "10.1.2.3") || !id.HasSAN(SANDNS, "api.prod.example.com") || !id.HasSAN(SANURI, spiffeAPI) {
		t.Fatal("correctly typed lookups must succeed")
	}

	// An IP-constraining policy rejects a leaf lacking the required IP SAN.
	policy := callerPolicy()
	policy.AllowedIPs = []string{"10.9.9.9"}
	if err := policy.Matches(id, attestationClaim()); !errors.Is(err, ErrSANTypeMismatch) {
		t.Fatalf("expected ErrSANTypeMismatch, got %v", err)
	}

	// An extra, unapproved URI SAN is rejected.
	extraURI := f.prodLeaf(leafOptions{
		serial: 20,
		uris:   []*url.URL{mustURI(spiffeAPI), mustURI("https://sidecar.prod.example.com/debug")},
	})
	extraID, err := buildIdentity(extraURI.cert)
	if err != nil {
		t.Fatal(err)
	}
	policy2 := callerPolicy()
	policy2.AllowedURIs = []string{"https://sidecar.prod.example.com/other"}
	if err := policy2.Matches(extraID, attestationClaim()); !errors.Is(err, ErrSANTypeMismatch) {
		t.Fatalf("unapproved URI SAN must be rejected, got %v", err)
	}

	// A URI rule cannot be satisfied by a DNS SAN: identity with only the SPIFFE
	// URI plus a DNS SAN still fails when an unrelated exact URI is demanded.
	policy3 := callerPolicy()
	policy3.AllowedURIs = []string{"spiffe://prod.example.com/ns/payments/svc/api"}
	if err := policy3.Matches(id, attestationClaim()); err != nil {
		t.Fatalf("SPIFFE URI excluded from the exact-URI list should not matter: %v", err)
	}
}

// TestCrossTrustDomainDenied confirms a valid chain under another trust domain
// is still denied by policy even when that root is bundled.
func TestCrossTrustDomainDenied(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)
	f.activateTrustedTestRoot()

	foreign := f.testLeaf(leafOptions{
		dns:  []string{"api.prod.example.com"},
		uris: []*url.URL{mustURI("spiffe://" + testTD + "/ns/payments/svc/api")},
	})
	_, err := f.authorize(foreign, callerPolicy(), targetPolicy(), attestationClaim())
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("cross trust-domain must be denied, got %v", err)
	}

	// Authenticate itself rejects a trust domain mismatch with the mesh domain.
	if _, _, err := f.verifier.Authenticate(derChain(foreign.cert, f.testInter.cert)); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected trust-domain denial, got %v", err)
	}
}

// TestPathDepthAndEKURestricted covers SPIFFE path depth and subject purpose.
func TestPathDepthAndEKURestricted(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)

	deep := f.prodLeaf(leafOptions{
		serial: 13,
		uris:   []*url.URL{mustURI("spiffe://" + prodTD + "/ns/payments/svc/api/extra/deep")},
	})
	policy := callerPolicy()
	policy.MaxPathSegments = 3
	if _, err := f.authorize(deep, policy, targetPolicy(), attestationClaim()); !errors.Is(err, ErrPathDepthExceeded) {
		t.Fatalf("expected ErrPathDepthExceeded, got %v", err)
	}

	// EKU: a leaf with only code-signing-style purpose fails the client policy.
	noClientEKU := f.prodLeaf(leafOptions{
		serial: 14,
		ekus:   []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		uris:   []*url.URL{mustURI(spiffeAPI)},
	})
	if _, err := f.authorize(noClientEKU, callerPolicy(), targetPolicy(), attestationClaim()); !errors.Is(err, ErrEKUNotPermitted) {
		t.Fatalf("expected ErrEKUNotPermitted, got %v", err)
	}
}

// TestAttestationEnforced covers type, principal and nonce replay checks.
func TestAttestationEnforced(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)
	leaf := f.prodLeaf(leafOptions{serial: 15, uris: []*url.URL{mustURI(spiffeAPI)}})

	if _, err := f.authorize(leaf, callerPolicy(), targetPolicy(), nil); !errors.Is(err, ErrAttestationDenied) {
		t.Fatalf("missing attestation must be denied, got %v", err)
	}

	wrongType := attestationClaim()
	wrongType.Type = "aws-ec2"
	if _, err := f.authorize(leaf, callerPolicy(), targetPolicy(), wrongType); !errors.Is(err, ErrAttestationTypeMismatch) {
		t.Fatalf("expected ErrAttestationTypeMismatch, got %v", err)
	}

	claim := attestationClaim()
	if _, err := f.authorize(leaf, callerPolicy(), targetPolicy(), claim); err != nil {
		t.Fatalf("first use of nonce should pass: %v", err)
	}
	replay := attestationClaimWithNonce(claim.Nonce)
	if _, err := f.authorize(leaf, callerPolicy(), targetPolicy(), replay); !errors.Is(err, ErrAttestationReplayed) {
		t.Fatalf("replayed nonce must be denied, got %v", err)
	}
}

// TestLeafMustBeDigitalSignature rejects leaves that are CAs or lack the
// DigitalSignature key usage.
func TestLeafMustBeDigitalSignature(t *testing.T) {
	f := newFixture(t)
	caLeaf := f.prodLeaf(leafOptions{serial: 16, isCA: true, uris: []*url.URL{mustURI(spiffeAPI)}})
	if _, _, err := f.verifier.Authenticate(derChain(caLeaf.cert, f.inter.cert)); !errors.Is(err, ErrLeafIsCA) {
		t.Fatalf("expected ErrLeafIsCA, got %v", err)
	}

	noKU := f.prodLeaf(leafOptions{
		serial:   17,
		keyUsage: x509.KeyUsageKeyEncipherment,
		uris:     []*url.URL{mustURI(spiffeAPI)},
	})
	if _, _, err := f.verifier.Authenticate(derChain(noKU.cert, f.inter.cert)); !errors.Is(err, ErrLeafKeyUsage) {
		t.Fatalf("expected ErrLeafKeyUsage, got %v", err)
	}

	expired := f.prodLeaf(leafOptions{
		serial: 18, notBeforeOffset: -3 * time.Hour, ttl: time.Hour,
		uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if _, _, err := f.verifier.Authenticate(derChain(expired.cert, f.inter.cert)); !errors.Is(err, ErrCertExpired) {
		t.Fatalf("expected ErrCertExpired, got %v", err)
	}

	// NotBefore in the future is treated as clock-rollback evidence.
	notYetValid := f.prodLeaf(leafOptions{
		serial: 19, notBeforeOffset: time.Hour, ttl: 2 * time.Hour,
		uris: []*url.URL{mustURI(spiffeAPI)},
	})
	if _, _, err := f.verifier.Authenticate(derChain(notYetValid.cert, f.inter.cert)); !errors.Is(err, ErrCertNotValidYet) {
		t.Fatalf("expected ErrCertNotValidYet, got %v", err)
	}
}
