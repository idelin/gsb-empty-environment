package workloadid

import (
	"crypto/x509"
	"errors"
	"math/big"
	"net/url"
	"testing"
	"time"

	"prescreen.local/ps-174fa6fd5f/internal/ocsp"
)

func (f *fixture) activateRevocation(generation uint64, state *RevocationState) {
	f.t.Helper()
	snap := f.manager.MustCurrent()
	if _, err := f.manager.Activate(generation, snap.Config, RootBundle{
		Roots:         []*x509.Certificate{f.root.cert},
		Intermediates: []*x509.Certificate{f.inter.cert},
		Revocation:    state,
	}); err != nil {
		f.t.Fatal(err)
	}
}

func ocspState(f *fixture, leaf *x509.Certificate, der []byte) *RevocationState {
	return &RevocationState{
		OCSP: map[string]OCSPRecord{
			serialKey(leaf.SerialNumber): {RawDER: der, Responder: f.inter.cert},
		},
		CRLs: map[string]CRLRecord{},
	}
}

// TestOCSPRevokedConnectionDenied verifies a revoked leaf cannot establish a
// connection even while its certificate is otherwise valid.
func TestOCSPRevokedConnectionDenied(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{serial: 30, uris: []*url.URL{mustURI(spiffeAPI)}})

	der, err := makeOCSP(f.inter, leaf.cert, ocsp.Revoked,
		f.clock.Now(), f.clock.Now().Add(time.Hour), f.clock.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	f.activateRevocation(3, ocspState(f, leaf.cert, der))

	_, _, err = f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert))
	if !errors.Is(err, ErrCertRevoked) {
		t.Fatalf("expected ErrCertRevoked, got %v", err)
	}
}

// TestOCSPGoodConnectionAllowed verifies a good OCSP response lets a valid
// workload through.
func TestOCSPGoodConnectionAllowed(t *testing.T) {
	f := newFixture(t)
	local := f.prodLeaf(leafOptions{serial: 101, uris: []*url.URL{mustURI(spiffeAPI)}})
	f.setLocalCredential(local)
	leaf := f.prodLeaf(leafOptions{serial: 31, uris: []*url.URL{mustURI(spiffeAPI)}})

	der, err := makeOCSP(f.inter, leaf.cert, ocsp.Good,
		f.clock.Now(), f.clock.Now().Add(time.Hour), Time{})
	if err != nil {
		t.Fatal(err)
	}
	f.activateRevocation(3, ocspState(f, leaf.cert, der))

	if _, err := f.authorize(leaf, callerPolicy(), targetPolicy(), attestationClaim()); err != nil {
		t.Fatalf("good OCSP should authorize, got %v", err)
	}
}

// TestOCSPStaleAndGrace exercises the grace window: within grace the response is
// flagged, past grace it is stale.
func TestOCSPStaleAndGrace(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{serial: 32, uris: []*url.URL{mustURI(spiffeAPI)}})

	nextUpdate := f.clock.Now().Add(-10 * time.Second)
	der, err := makeOCSP(f.inter, leaf.cert, ocsp.Good, f.clock.Now().Add(-time.Minute), nextUpdate, Time{})
	if err != nil {
		t.Fatal(err)
	}
	f.activateRevocation(3, ocspState(f, leaf.cert, der))

	// Inside the 30s grace window.
	_, _, err = f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert))
	if !errors.Is(err, ErrRevocationGrace) {
		t.Fatalf("expected ErrRevocationGrace, got %v", err)
	}

	// Beyond grace: stale.
	f.clock.Advance(40 * time.Second)
	_, _, err = f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert))
	if !errors.Is(err, ErrRevocationStale) {
		t.Fatalf("expected ErrRevocationStale, got %v", err)
	}
}

// TestOCSPUnknownTreatedAsRevoked honors UnknownIsRevoked.
func TestOCSPUnknownTreatedAsRevoked(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{serial: 33, uris: []*url.URL{mustURI(spiffeAPI)}})
	der, err := makeOCSP(f.inter, leaf.cert, ocsp.Unknown,
		f.clock.Now(), f.clock.Now().Add(time.Hour), Time{})
	if err != nil {
		t.Fatal(err)
	}
	f.activateRevocation(3, ocspState(f, leaf.cert, der))
	_, _, err = f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert))
	if !errors.Is(err, ErrOCSPUnknown) {
		t.Fatalf("expected ErrOCSPUnknown, got %v", err)
	}
}

// TestCRLRevokedAndFresh covers CRL based revocation.
func TestCRLRevokedAndFresh(t *testing.T) {
	f := newFixture(t)
	revokedLeaf := f.prodLeaf(leafOptions{serial: 40, uris: []*url.URL{mustURI(spiffeAPI)}})
	goodLeaf := f.prodLeaf(leafOptions{serial: 41, uris: []*url.URL{mustURI(spiffeAPI)}})

	crlDER, err := makeCRL(f.inter, []*big.Int{revokedLeaf.cert.SerialNumber},
		f.clock.Now(), f.clock.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	state := &RevocationState{
		OCSP: map[string]OCSPRecord{},
		CRLs: map[string]CRLRecord{
			issuerKey(revokedLeaf.cert): {RawDER: crlDER, Issuer: f.inter.cert},
		},
	}
	f.activateRevocation(3, state)

	if _, _, err := f.verifier.Authenticate(derChain(revokedLeaf.cert, f.inter.cert)); !errors.Is(err, ErrCertRevoked) {
		t.Fatalf("CRL revoked cert must be denied, got %v", err)
	}
	if _, _, err := f.verifier.Authenticate(derChain(goodLeaf.cert, f.inter.cert)); err != nil {
		t.Fatalf("non-revoked cert should pass revocation: %v", err)
	}
}

// TestCRLStalePastGrace ensures an expired CRL (past grace) fails closed.
func TestCRLStalePastGrace(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{serial: 42, ttl: 24 * time.Hour, uris: []*url.URL{mustURI(spiffeAPI)}})
	crlDER, err := makeCRL(f.inter, nil,
		f.clock.Now().Add(-2*time.Hour), f.clock.Now().Add(-90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	state := &RevocationState{
		OCSP: map[string]OCSPRecord{},
		CRLs: map[string]CRLRecord{
			issuerKey(leaf.cert): {RawDER: crlDER, Issuer: f.inter.cert},
		},
	}
	f.activateRevocation(3, state)
	f.clock.Advance(2 * time.Minute)
	_, _, err = f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert))
	if !errors.Is(err, ErrRevocationStale) {
		t.Fatalf("expected ErrRevocationStale, got %v", err)
	}
}

// TestRevocationClockRollback rejects a response whose ThisUpdate is in the
// future beyond the configured skew.
func TestRevocationClockRollback(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{serial: 43, uris: []*url.URL{mustURI(spiffeAPI)}})
	der, err := makeOCSP(f.inter, leaf.cert, ocsp.Good,
		f.clock.Now().Add(2*time.Minute), f.clock.Now().Add(3*time.Minute), Time{})
	if err != nil {
		t.Fatal(err)
	}
	f.activateRevocation(3, ocspState(f, leaf.cert, der))
	_, _, err = f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert))
	if !errors.Is(err, ErrRevocationRolledBack) {
		t.Fatalf("expected ErrRevocationRolledBack, got %v", err)
	}
}

// TestOCSPWrongResponderRejected ensures an OCSP response signed by an
// untrusted responder cannot salvage revocation status.
func TestOCSPWrongResponderRejected(t *testing.T) {
	f := newFixture(t)
	leaf := f.prodLeaf(leafOptions{serial: 44, uris: []*url.URL{mustURI(spiffeAPI)}})
	der, err := makeOCSP(f.testInter, leaf.cert, ocsp.Good,
		f.clock.Now(), f.clock.Now().Add(time.Hour), Time{})
	if err != nil {
		t.Fatal(err)
	}
	f.activateRevocation(3, ocspState(f, leaf.cert, der))
	_, _, err = f.verifier.Authenticate(derChain(leaf.cert, f.inter.cert))
	if err == nil {
		t.Fatal("OCSP response from unrelated responder must be rejected")
	}
}
