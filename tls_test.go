package workloadid

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net/url"
	"testing"
	"time"
)

// TestSessionResumptionReevaluatesPolicy ensures a resumed session is
// re-authorized against the current generation and revocation state rather than
// inheriting a stale decision.
func TestSessionResumptionReevaluatesPolicy(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)
	leaf := f.prodLeaf(leafOptions{serial: 70, uris: []*url.URL{mustURI(spiffeAPI)}})
	claim := attestationClaim()

	if _, err := f.verifier.Authorize(AuthRequest{
		RawCerts:     derChain(leaf.cert, f.inter.cert),
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attestation:  claim,
	}); err != nil {
		t.Fatalf("fresh authorize: %v", err)
	}

	// Simulate resumption: crypto/tls only retains the leaf DER. Policy is still
	// re-evaluated against the live generation.
	resumed, err := f.verifier.Authorize(AuthRequest{
		RawCerts:     [][]byte{leaf.cert.Raw},
		Resumed:      true,
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attestation:  claim,
	})
	if err != nil {
		t.Fatalf("resumed authorize should pass while valid: %v", err)
	}
	if !resumed.Resumed || resumed.Generation != f.manager.MustCurrent().Generation {
		t.Fatal("resumed decision must bind to current generation")
	}

	// Revoke the leaf in a newer generation. On a resumed handshake only the
	// leaf is available, so the CRL path (keyed by leaf issuer) demonstrates the
	// fail-closed guarantee deterministically.
	crlDER, err := makeCRL(f.inter, []*big.Int{leaf.cert.SerialNumber},
		f.clock.Now(), f.clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	crlState := &RevocationState{
		OCSP: map[string]OCSPRecord{},
		CRLs: map[string]CRLRecord{
			issuerKey(leaf.cert): {RawDER: crlDER, Issuer: f.inter.cert},
		},
	}
	f.activateRevocation(f.manager.MustCurrent().Generation+1, crlState)

	if _, err := f.verifier.Authorize(AuthRequest{
		RawCerts:     [][]byte{leaf.cert.Raw},
		Resumed:      true,
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attestation:  attestationClaim(),
	}); !errors.Is(err, ErrCertRevoked) {
		t.Fatalf("resumed connection after revocation must be denied, got %v", err)
	}
}

// TestResumptionPolicyChangeRejected ensures a tightened policy blocks even a
// previously-allowed resumed session.
func TestResumptionPolicyChangeRejected(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)
	leaf := f.prodLeaf(leafOptions{serial: 71, uris: []*url.URL{mustURI(spiffeWeb)}})

	if _, err := f.verifier.Authorize(AuthRequest{
		RawCerts:     [][]byte{leaf.cert.Raw},
		Resumed:      true,
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
	}); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("resumed policy re-evaluation must deny, got %v", err)
	}
}

// TestTLSConfigDisablesResumptionByDefault verifies the default tls.Config turns
// off tickets so every connection performs full re-authentication.
func TestTLSConfigDisablesResumptionByDefault(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)

	authorizer := &TLSAuthorizer{
		Verifier:     f.verifier,
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attest:       attestationClaim,
	}
	cfg, err := authorizer.TLSConfig(f.manager)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SessionTicketsDisabled {
		t.Fatal("session tickets must be disabled by default")
	}
	if cfg.VerifyConnection == nil {
		t.Fatal("VerifyConnection must be wired")
	}

	err = authorizer.verifyConnection(tls.ConnectionState{DidResume: true})
	if !errors.Is(err, ErrResumeNotAllowed) {
		t.Fatalf("expected ErrResumeNotAllowed, got %v", err)
	}
}

// TestTLSVerifyConnectionAuthorizes drives the full VerifyConnection path for a
// fresh, valid mTLS handshake.
func TestTLSVerifyConnectionAuthorizes(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{
		serial: 101, ekus: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		uris: []*url.URL{mustURI(spiffeAPI)},
	})
	f.setLocalCredential(local)
	leaf := f.prodLeaf(leafOptions{serial: 72, uris: []*url.URL{mustURI(spiffeAPI)}})

	nonceUsed := ""
	authorizer := &TLSAuthorizer{
		Verifier:     f.verifier,
		CallerPolicy: callerPolicy(),
		TargetPolicy: targetPolicy(),
		Attest: func() *NodeAttestationClaim {
			claim := attestationClaim()
			nonceUsed = claim.Nonce
			return claim
		},
	}
	cfg, err := authorizer.TLSConfig(f.manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.VerifyConnection(tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{leaf.cert},
	}); err != nil {
		t.Fatalf("VerifyConnection should authorize: %v", err)
	}
	if nonceUsed == "" {
		t.Fatal("attestation hook must be invoked")
	}
}
