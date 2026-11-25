package workloadid

import (
	"crypto/x509"
	"errors"
	"net/url"
	"strconv"
	"testing"
	"time"
)

const (
	prodTD    = "prod.example.com"
	testTD    = "test.example.com"
	spiffeAPI = "spiffe://prod.example.com/ns/payments/svc/api"
	spiffeWeb = "spiffe://prod.example.com/ns/shop/svc/web"
)

type fixture struct {
	t         *testing.T
	clock     *fakeClock
	manager   *Manager
	verifier  *Verifier
	root      *ca
	inter     *ca
	testRoot  *ca
	testInter *ca
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	clock := newFakeClock(base)
	testNow = clock.Now

	root, err := newRoot("prod-root")
	if err != nil {
		t.Fatal(err)
	}
	inter, err := newIntermediate(root, "prod-intermediate", leafOptions{serial: 2, ttl: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	testRoot, err := newRoot("test-root")
	if err != nil {
		t.Fatal(err)
	}
	testInter, err := newIntermediate(testRoot, "test-intermediate", leafOptions{serial: 3, ttl: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	manager := NewManager(clock)
	cfg := BundleConfig{
		TrustDomain:   prodTD,
		MaxChainDepth: 5,
		MaxLeafTTL:    24 * time.Hour,
		Revocation: RevocationConfig{
			OCSPGrace:        30 * time.Second,
			CRLGrace:         time.Minute,
			UnknownIsRevoked: true,
			MaxSkew:          time.Minute,
		},
	}
	if _, err := manager.Activate(1, cfg, RootBundle{
		Roots:         []*x509.Certificate{root.cert},
		Intermediates: []*x509.Certificate{inter.cert},
	}); err != nil {
		t.Fatal(err)
	}
	verifier := NewVerifier(manager, WithClock(clock))
	return &fixture{
		t: t, clock: clock, manager: manager, verifier: verifier,
		root: root, inter: inter, testRoot: testRoot, testInter: testInter,
	}
}

func (f *fixture) prodLeaf(opts leafOptions) *leafBundle {
	f.t.Helper()
	if opts.ttl == 0 {
		opts.ttl = time.Hour
	}
	if opts.serial == 0 {
		opts.serial = 100
	}
	bundle, err := newLeaf(f.inter, opts)
	if err != nil {
		f.t.Fatal(err)
	}
	return bundle
}

func (f *fixture) testLeaf(opts leafOptions) *leafBundle {
	f.t.Helper()
	if opts.ttl == 0 {
		opts.ttl = time.Hour
	}
	if opts.serial == 0 {
		opts.serial = 200
	}
	bundle, err := newLeaf(f.testInter, opts)
	if err != nil {
		f.t.Fatal(err)
	}
	return bundle
}

func (f *fixture) setLocalCredential(bundle *leafBundle) {
	f.t.Helper()
	if _, err := f.manager.RotateCredential(2, WorkloadCredential{
		Leaf: bundle.cert, PrivateKey: bundle.key,
	}); err != nil {
		f.t.Fatal(err)
	}
}

func callerPolicy() *Policy {
	return &Policy{
		Name:                "caller-payments",
		TrustDomain:         prodTD,
		AllowedPathPrefixes: [][]string{{"ns", "payments"}},
		MaxPathSegments:     6,
		AllowedEKUs:         []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		RequireAttestation: &NodeAttestation{
			Type:              "k8s-sa",
			AllowedPrincipals: []string{"payments-sa"},
			MaxAge:            5 * time.Minute,
		},
	}
}

func targetPolicy() *Policy {
	return &Policy{
		Name:                "target-payments",
		TrustDomain:         prodTD,
		AllowedPathPrefixes: [][]string{{"ns", "payments"}},
		MaxPathSegments:     6,
		AllowedEKUs:         []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
}

var nonceCounter int64

func attestationClaimWithNonce(nonce string) *NodeAttestationClaim {
	return &NodeAttestationClaim{
		Type:      "k8s-sa",
		Principal: "payments-sa",
		IssuedAt:  time.Now(),
		Nonce:     nonce,
	}
}

func attestationClaim() *NodeAttestationClaim {
	nonceCounter++
	return attestationClaimWithNonce("nonce-" + time.Now().Format("150405.000000") + "-" +
		strconv.FormatInt(nonceCounter, 10))
}

func (f *fixture) authorize(bundle *leafBundle, caller, target *Policy, claim *NodeAttestationClaim) (*Decision, error) {
	return f.verifier.Authorize(AuthRequest{
		RawCerts:     derChain(bundle.cert, f.inter.cert),
		CallerPolicy: caller,
		TargetPolicy: target,
		Attestation:  claim,
	})
}

func (f *fixture) activateTrustedTestRoot() {
	f.t.Helper()
	snap := f.manager.MustCurrent()
	if _, err := f.manager.Activate(snap.Generation+1, snap.Config, RootBundle{
		Roots:         []*x509.Certificate{f.root.cert, f.testRoot.cert},
		Intermediates: []*x509.Certificate{f.inter.cert, f.testInter.cert},
		Revocation:    snap.Revocation,
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) newSnapshotWithIntermediate(intermediate *ca) *Snapshot {
	f.t.Helper()
	snap := f.manager.MustCurrent()
	pool := x509.NewCertPool()
	pool.AddCert(intermediate.cert)
	return &Snapshot{
		Generation:    snap.Generation,
		Config:        snap.Config,
		Roots:         snap.Roots,
		Intermediates: pool,
		Revocation:    snap.Revocation,
	}
}

// TestLegitSameDomainServiceAllowed is the positive control: a properly issued
// same-trust-domain workload with matching attestation is authorized and its
// identity is not amplified beyond the policy.
func TestLegitSameDomainServiceAllowed(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{
		dns:  []string{"api.prod.example.com"},
		uris: []*url.URL{mustURI(spiffeAPI)},
	})
	local := f.prodLeaf(leafOptions{
		serial: 101,
		dns:    []string{"api.prod.example.com"},
		uris:   []*url.URL{mustURI(spiffeAPI)},
		ekus:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	})
	f.setLocalCredential(local)

	decision, err := f.authorize(leaf, callerPolicy(), targetPolicy(), attestationClaim())
	if err != nil {
		t.Fatalf("expected authorization, got %v", err)
	}
	if !decision.CallerMatched || !decision.TargetMatched {
		t.Fatalf("both policies must match: %+v", decision)
	}
	if decision.Identity.TrustDomain != prodTD {
		t.Fatalf("trust domain = %q", decision.Identity.TrustDomain)
	}
	if decision.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", decision.Generation)
	}
}

// TestEvilProdCannotInheritProductionIdentity reproduces the original attack: a
// test-issued certificate carrying "evil.prod" must never be treated as a
// production workload. Identity is anchored on the SPIFFE trust domain plus an
// explicit policy, not on a loose "*.prod" DNS suffix.
func TestEvilProdCannotInheritProductionIdentity(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)

	evil := f.testLeaf(leafOptions{
		dns:  []string{"evil.prod"},
		uris: []*url.URL{mustURI("spiffe://" + testTD + "/ns/anything/svc/evil")},
	})

	// The test root is not trusted, so chain verification fails first.
	if _, _, err := f.verifier.Authenticate(derChain(evil.cert, f.testInter.cert)); !errors.Is(err, ErrChainVerify) {
		t.Fatalf("expected chain failure for untrusted test cert, got %v", err)
	}

	// Even if the test root were trusted, cross trust-domain policy must deny.
	f.activateTrustedTestRoot()
	_, err := f.verifier.Authorize(AuthRequest{
		RawCerts:     derChain(evil.cert, f.testInter.cert),
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attestation:  attestationClaim(),
	})
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}

	// The DNS wildcard machinery itself never accepts evil.prod.
	matcher := DNSMatcher{AllowedPatterns: []string{"*.prod"}}
	if ok, _ := matcher.Match("evil.prod"); ok {
		t.Fatal("*.prod must not grant evil.prod")
	}
}
