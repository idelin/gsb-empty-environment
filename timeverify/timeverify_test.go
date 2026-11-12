package timeverify

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testFixture struct {
	verifier *Verifier
	logKey   *ecdsa.PrivateKey
	logID    string
	leaf     *x509.Certificate
	now      time.Time
	payload  Payload
}

// newFixture builds a trusted log, a Fulcio-like leaf certificate and a
// signed bundle. Certificate and clock windows are parameterized so each
// attack scenario can be modeled directly.
func newFixture(t *testing.T, notBefore, notAfter, now time.Time) *testFixture {
	t.Helper()

	logKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate log key: %v", err)
	}
	trusted, err := NewLogKey(&logKey.PublicKey)
	if err != nil {
		t.Fatalf("build log key: %v", err)
	}

	leaf := issueLeaf(t, notBefore, notAfter)

	payload := Payload{
		Body:           EncodeBody([]byte(`{"apiVersion":"0.0.1","kind":"hashedrekord"}`)),
		IntegratedTime: now.Add(-time.Minute).Unix(),
		LogIndex:       42,
		LogID:          trusted.LogID,
	}

	verifier, err := NewVerifier(
		[]*LogKey{trusted},
		WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	return &testFixture{
		verifier: verifier,
		logKey:   logKey,
		logID:    trusted.LogID,
		leaf:     leaf,
		now:      now,
		payload:  payload,
	}
}

func mustLogKey(t *testing.T, pub crypto.PublicKey) *LogKey {
	t.Helper()
	key, err := NewLogKey(pub)
	if err != nil {
		t.Fatalf("build log key: %v", err)
	}
	return key
}

func issueLeaf(t *testing.T, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()

	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fulcio-test-leaf"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &signer.PublicKey, signer)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

func (f *testFixture) signedBundle(t *testing.T, payload Payload) *Bundle {
	t.Helper()
	set, err := SignSET(f.logKey, payload)
	if err != nil {
		t.Fatalf("sign SET: %v", err)
	}
	return &Bundle{Leaf: f.leaf, Payload: payload, SET: set}
}

// TestVerifyValidBundle confirms the happy path, including a historically
// logged entry whose certificate has since expired.
func TestVerifyValidBundle(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-2 * time.Hour)
	notAfter := now.Add(-1 * time.Hour) // expired now, but valid at inclusion
	f := newFixture(t, notBefore, notAfter, now)

	// Inclusion happened while the certificate was still valid.
	f.payload.IntegratedTime = notAfter.Add(-time.Minute).Unix()

	if err := f.verifier.Verify(f.signedBundle(t, f.payload)); err != nil {
		t.Fatalf("expected valid bundle, got %v", err)
	}
}

// TestExpiredCertForgedTime models the core attack: an expired Fulcio
// certificate with an integratedTime stamped after expiry. The SET is valid
// but the timestamp falls outside the certificate window.
func TestExpiredCertForgedTime(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-48 * time.Hour)
	notAfter := now.Add(-24 * time.Hour)
	f := newFixture(t, notBefore, notAfter, now)

	// Attacker claims the entry was logged "now", long after NotAfter.
	f.payload.IntegratedTime = now.Unix()

	err := f.verifier.Verify(f.signedBundle(t, f.payload))
	if !errors.Is(err, ErrIntegratedTimeAfterCert) {
		t.Fatalf("expected ErrIntegratedTimeAfterCert, got %v", err)
	}
}

// TestTamperedIntegratedTime ensures that rewriting the signed timestamp
// breaks the SET, because integratedTime is part of the canonical payload.
func TestTamperedIntegratedTime(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	f := newFixture(t, now.Add(-time.Hour), now.Add(time.Hour), now)

	original := f.payload
	bundle := f.signedBundle(t, original)

	// Mutate the timestamp after the SET was produced.
	bundle.Payload.IntegratedTime += 3600
	if err := f.verifier.Verify(bundle); !errors.Is(err, ErrSETVerification) {
		t.Fatalf("expected ErrSETVerification for tampered time, got %v", err)
	}

	// Tampering with the other covered fields must fail identically.
	for _, mutate := range []func(*Payload){
		func(p *Payload) { p.LogIndex++ },
		func(p *Payload) { p.Body = EncodeBody([]byte("tampered-body")) },
	} {
		b := f.signedBundle(t, original)
		mutate(&b.Payload)
		if err := f.verifier.Verify(b); !errors.Is(err, ErrSETVerification) {
			t.Fatalf("expected ErrSETVerification after payload mutation, got %v", err)
		}
	}
}

// TestSwappedLogID rejects a bundle whose logID points at a different log.
func TestSwappedLogID(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	f := newFixture(t, now.Add(-time.Hour), now.Add(time.Hour), now)

	otherLog, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate other log key: %v", err)
	}
	otherLogKey := mustLogKey(t, &otherLog.PublicKey)

	// Re-sign a payload carrying the attacker's logID with the original log.
	swapped := f.payload
	swapped.LogID = otherLogKey.LogID
	bundle := f.signedBundle(t, swapped)

	if err := f.verifier.Verify(bundle); !errors.Is(err, ErrInvalidLogID) {
		t.Fatalf("expected ErrInvalidLogID for unknown logID, got %v", err)
	}

	// Even with both logs trusted, the original log cannot sign for the
	// other log's logID, so the SET check must fail.
	verifier, err := NewVerifier(
		[]*LogKey{
			mustLogKey(t, &f.logKey.PublicKey),
			otherLogKey,
		},
		WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	if err := verifier.Verify(bundle); !errors.Is(err, ErrSETVerification) {
		t.Fatalf("expected ErrSETVerification for cross-log signature, got %v", err)
	}

	// A bundle properly signed by the other log under its own logID verifies.
	goodSet, err := SignSET(otherLog, swapped)
	if err != nil {
		t.Fatalf("sign with other log: %v", err)
	}
	good := &Bundle{Leaf: f.leaf, Payload: swapped, SET: goodSet}
	if err := verifier.Verify(good); err != nil {
		t.Fatalf("expected cross-log bundle to verify, got %v", err)
	}
}

// TestFutureSkew checks the clock-skew boundary on future timestamps.
func TestFutureSkew(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	f := newFixture(t, now.Add(-time.Hour), now.Add(2*time.Hour), now)

	withinSkew := f.payload
	withinSkew.IntegratedTime = now.Add(DefaultSkew - time.Second).Unix()
	if err := f.verifier.Verify(f.signedBundle(t, withinSkew)); err != nil {
		t.Fatalf("expected timestamp within skew to pass, got %v", err)
	}

	beyondSkew := f.payload
	beyondSkew.IntegratedTime = now.Add(DefaultSkew + time.Second).Unix()
	if err := f.verifier.Verify(f.signedBundle(t, beyondSkew)); !errors.Is(err, ErrIntegratedTimeInFuture) {
		t.Fatalf("expected ErrIntegratedTimeInFuture, got %v", err)
	}

	// A zero-skew verifier rejects any future timestamp.
	strict, err := NewVerifier(
		[]*LogKey{mustLogKey(t, &f.logKey.PublicKey)},
		WithClock(func() time.Time { return now }),
		WithSkew(0),
	)
	if err != nil {
		t.Fatalf("new strict verifier: %v", err)
	}
	oneSecondFuture := f.payload
	oneSecondFuture.IntegratedTime = now.Add(time.Second).Unix()
	if err := strict.Verify(f.signedBundle(t, oneSecondFuture)); !errors.Is(err, ErrIntegratedTimeInFuture) {
		t.Fatalf("expected strict verifier to reject future timestamp, got %v", err)
	}
}

// TestNotBeforeRejected ensures timestamps earlier than certificate issuance
// cannot be claimed.
func TestNotBeforeRejected(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	notBefore := now.Add(-time.Hour)
	f := newFixture(t, notBefore, now.Add(time.Hour), now)

	f.payload.IntegratedTime = notBefore.Add(-time.Second).Unix()
	err := f.verifier.Verify(f.signedBundle(t, f.payload))
	if !errors.Is(err, ErrIntegratedTimeBeforeCert) {
		t.Fatalf("expected ErrIntegratedTimeBeforeCert, got %v", err)
	}
}

// TestEd25519Log covers the Ed25519 SET variant used by newer Rekor logs.
func TestEd25519Log(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	logKey, err := NewLogKey(pub)
	if err != nil {
		t.Fatalf("build ed25519 log key: %v", err)
	}
	verifier, err := NewVerifier(
		[]*LogKey{logKey},
		WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	payload := Payload{
		Body:           EncodeBody([]byte("ed25519-body")),
		IntegratedTime: now.Unix(),
		LogIndex:       7,
		LogID:          logKey.LogID,
	}
	set, err := SignSET(priv, payload)
	if err != nil {
		t.Fatalf("sign ed25519 SET: %v", err)
	}
	leaf := issueLeaf(t, now.Add(-time.Hour), now.Add(time.Hour))
	if err := verifier.Verify(&Bundle{Leaf: leaf, Payload: payload, SET: set}); err != nil {
		t.Fatalf("expected ed25519 bundle to verify, got %v", err)
	}

	corrupted := append([]byte(nil), set...)
	corrupted[len(corrupted)-1] ^= 0xFF
	if err := verifier.Verify(&Bundle{Leaf: leaf, Payload: payload, SET: corrupted}); !errors.Is(err, ErrSETVerification) {
		t.Fatalf("expected corrupted ed25519 signature to fail, got %v", err)
	}
}

// TestNilInputs guards the defensive error paths.
func TestNilInputs(t *testing.T) {
	now := time.Now()
	f := newFixture(t, now.Add(-time.Hour), now.Add(time.Hour), now)

	if err := f.verifier.Verify(nil); !errors.Is(err, ErrNilBundle) {
		t.Fatalf("expected ErrNilBundle, got %v", err)
	}
	if err := f.verifier.Verify(&Bundle{}); !errors.Is(err, ErrNilCertificate) {
		t.Fatalf("expected ErrNilCertificate, got %v", err)
	}
	if _, err := NewVerifier(nil); err == nil {
		t.Fatal("expected error when no log keys are supplied")
	}
	if _, err := NewVerifier([]*LogKey{mustLogKey(t, &f.logKey.PublicKey)}, WithSkew(-1)); err == nil {
		t.Fatal("expected error for negative skew")
	}
}

// TestCanonicalJSON locks the exact canonical form and key ordering that the
// SET covers.
func TestCanonicalJSON(t *testing.T) {
	p := Payload{
		Body:           EncodeBody([]byte("hello")),
		IntegratedTime: 1699900000,
		LogIndex:       9,
		LogID:          "abcdef",
	}
	want := `{"body":"` + EncodeBody([]byte("hello")) +
		`","integratedTime":1699900000,"logID":"abcdef","logIndex":9}`

	if got := string(p.canonicalJSON()); got != want {
		t.Fatalf("canonical JSON mismatch\n got: %s\nwant: %s", got, want)
	}

	// The canonical form must itself be valid JSON round-trippable data.
	var decoded map[string]any
	if err := json.Unmarshal(p.canonicalJSON(), &decoded); err != nil {
		t.Fatalf("canonical payload is not valid JSON: %v", err)
	}
	if decoded["integratedTime"].(float64) != float64(p.IntegratedTime) {
		t.Fatalf("integratedTime lost in round trip: %v", decoded)
	}
}
