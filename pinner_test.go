package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

var testSerial uint64

func nextSerial() *big.Int {
	return big.NewInt(int64(atomic.AddUint64(&testSerial, 1)))
}

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T, commonName string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) issue(t *testing.T, commonName string, dnsNames []string, notBefore, notAfter time.Time) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	return ca.issueWithKey(t, key, commonName, dnsNames, notBefore, notAfter)
}

func (ca *testCA) issueWithKey(t *testing.T, key *ecdsa.PrivateKey, commonName string, dnsNames []string, notBefore, notAfter time.Time) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	template := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             notBefore.UTC().Truncate(time.Second),
		NotAfter:              notAfter.UTC().Truncate(time.Second),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf certificate: %v", err)
	}
	return cert, key
}

func poolOf(certs ...*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool
}

func stateOf(leaf *x509.Certificate, handshakeComplete bool, chain ...*x509.Certificate) tls.ConnectionState {
	certs := make([]*x509.Certificate, 0, len(chain)+1)
	certs = append(certs, leaf)
	certs = append(certs, chain...)
	return tls.ConnectionState{
		PeerCertificates:  certs,
		HandshakeComplete: handshakeComplete,
	}
}

func mustPin(t *testing.T, store PinStore, host string, port int) Pin {
	t.Helper()
	pin, ok, err := store.Get(host, port)
	if err != nil {
		t.Fatalf("get pin: %v", err)
	}
	if !ok {
		t.Fatalf("expected pin for %s:%d", host, port)
	}
	return pin
}

func TestFirstConnectionRecordsSPKIFingerprint(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	ca := newTestCA(t, "trusted root")
	leaf, _ := ca.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(24*time.Hour))
	store := NewMemoryStore()
	pinner := NewPinner(poolOf(ca.cert), WithStore(store))

	if err := pinner.CheckState("pin.test", 443, stateOf(leaf, true), now); err != nil {
		t.Fatalf("first connection: %v", err)
	}

	pin := mustPin(t, store, "pin.test", 443)
	wantFingerprint := Fingerprint(sha256.Sum256(leaf.RawSubjectPublicKeyInfo))
	if pin.Fingerprint != wantFingerprint {
		t.Fatalf("fingerprint = %s, want %s", pin.Fingerprint, wantFingerprint)
	}
	if pin.Fingerprint != SPKIFingerprint(leaf) {
		t.Fatalf("SPKIFingerprint mismatch: %s != %s", pin.Fingerprint, SPKIFingerprint(leaf))
	}
	if !pin.FirstSeen.Equal(now) {
		t.Fatalf("first seen = %v, want %v", pin.FirstSeen, now)
	}
	if !pin.NotAfter.Equal(leaf.NotAfter) {
		t.Fatalf("recorded expiry = %v, want %v", pin.NotAfter, leaf.NotAfter)
	}

	if err := pinner.CheckState("pin.test", 443, stateOf(leaf, true), now.Add(time.Hour)); err != nil {
		t.Fatalf("same fingerprint should pass: %v", err)
	}
	after := mustPin(t, store, "pin.test", 443)
	if !after.FirstSeen.Equal(pin.FirstSeen) {
		t.Fatalf("same certificate changed first-seen: %v -> %v", pin.FirstSeen, after.FirstSeen)
	}
	if after.Fingerprint != pin.Fingerprint {
		t.Fatalf("same certificate changed fingerprint")
	}
}

func TestChangedUntrustedCertificateIsRejectedWithoutUpdatingPin(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	trusted := newTestCA(t, "trusted root")
	rogue := newTestCA(t, "rogue root")
	oldLeaf, _ := trusted.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(2*time.Hour))
	newLeaf, _ := rogue.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(24*time.Hour))
	store := NewMemoryStore()
	pinner := NewPinner(poolOf(trusted.cert), WithStore(store))

	if err := pinner.CheckState("pin.test", 443, stateOf(oldLeaf, true), now); err != nil {
		t.Fatalf("first connection: %v", err)
	}
	original := mustPin(t, store, "pin.test", 443)

	at := now.Add(time.Hour)
	err := pinner.CheckState("pin.test", 443, stateOf(newLeaf, true), at)
	if !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("error = %v, want ErrFingerprintChanged", err)
	}
	var changed *FingerprintChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("error %T does not expose change details", err)
	}
	if changed.Stored != original.Fingerprint || changed.Presented != SPKIFingerprint(newLeaf) {
		t.Fatalf("change details do not identify both fingerprints")
	}

	current := mustPin(t, store, "pin.test", 443)
	if current != original {
		t.Fatalf("pin was updated after rejected change: %+v -> %+v", original, current)
	}
}

func TestRenewalSignedByTrustedCAIsAcceptedAndUpdatesPin(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	ca := newTestCA(t, "trusted root")
	oldLeaf, _ := ca.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(2*time.Hour))
	newLeaf, _ := ca.issue(t, "pin.test", []string{"pin.test"}, now.Add(30*time.Minute), now.Add(48*time.Hour))
	store := NewMemoryStore()
	pinner := NewPinner(poolOf(ca.cert), WithStore(store))

	if err := pinner.CheckState("pin.test", 443, stateOf(oldLeaf, true), now); err != nil {
		t.Fatalf("first connection: %v", err)
	}

	renewedAt := now.Add(time.Hour)
	if err := pinner.CheckState("pin.test", 443, stateOf(newLeaf, true), renewedAt); err != nil {
		t.Fatalf("trusted renewal should pass: %v", err)
	}
	pin := mustPin(t, store, "pin.test", 443)
	if pin.Fingerprint != SPKIFingerprint(newLeaf) {
		t.Fatalf("renewed fingerprint = %s, want %s", pin.Fingerprint, SPKIFingerprint(newLeaf))
	}
	if !pin.FirstSeen.Equal(renewedAt) {
		t.Fatalf("renewed first seen = %v, want %v", pin.FirstSeen, renewedAt)
	}
	if !pin.NotAfter.Equal(newLeaf.NotAfter) {
		t.Fatalf("renewed expiry = %v, want %v", pin.NotAfter, newLeaf.NotAfter)
	}

	if err := pinner.CheckState("pin.test", 443, stateOf(newLeaf, true), renewedAt.Add(time.Minute)); err != nil {
		t.Fatalf("renewed certificate should now match the pin: %v", err)
	}
}

func TestExpiryBoundaryAllowsUntrustedChange(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	trusted := newTestCA(t, "trusted root")
	rogue := newTestCA(t, "rogue root")
	oldLeaf, _ := trusted.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(time.Hour))
	newLeaf, _ := rogue.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(24*time.Hour))
	store := NewMemoryStore()
	pinner := NewPinner(poolOf(trusted.cert), WithStore(store))

	if err := pinner.CheckState("pin.test", 443, stateOf(oldLeaf, true), now.Add(-30*time.Minute)); err != nil {
		t.Fatalf("first connection: %v", err)
	}
	original := mustPin(t, store, "pin.test", 443)

	justBefore := oldLeaf.NotAfter.Add(-time.Second)
	err := pinner.CheckState("pin.test", 443, stateOf(newLeaf, true), justBefore)
	if !errors.Is(err, ErrFingerprintChanged) {
		t.Fatalf("just before expiry error = %v, want ErrFingerprintChanged", err)
	}
	if current := mustPin(t, store, "pin.test", 443); current != original {
		t.Fatalf("pin changed before expiry")
	}

	if err := pinner.CheckState("pin.test", 443, stateOf(newLeaf, true), oldLeaf.NotAfter); err != nil {
		t.Fatalf("NotAfter instant should count as expired: %v", err)
	}
	pin := mustPin(t, store, "pin.test", 443)
	if pin.Fingerprint != SPKIFingerprint(newLeaf) {
		t.Fatalf("expired-pin replacement was not recorded")
	}
	if !pin.FirstSeen.Equal(oldLeaf.NotAfter) {
		t.Fatalf("replacement first seen = %v, want %v", pin.FirstSeen, oldLeaf.NotAfter)
	}

	if oldLeaf.NotAfter.Equal(time.Date(2026, 11, 13, 1, 0, 0, 0, time.UTC)) != true {
		t.Fatalf("test certificate expiry was truncated unexpectedly: %v", oldLeaf.NotAfter)
	}
}

func TestHostnameMismatchIsRejectedRegardlessOfPin(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	ca := newTestCA(t, "trusted root")
	leaf, key := ca.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(2*time.Hour))
	store := NewMemoryStore()
	pinner := NewPinner(poolOf(ca.cert), WithStore(store))

	if err := pinner.CheckState("pin.test", 443, stateOf(leaf, true), now); err != nil {
		t.Fatalf("first connection: %v", err)
	}
	original := mustPin(t, store, "pin.test", 443)

	otherHost, _ := ca.issueWithKey(t, key, "other.test", []string{"other.test"}, now.Add(-time.Hour), now.Add(24*time.Hour))
	if SPKIFingerprint(otherHost) != original.Fingerprint {
		t.Fatalf("test setup: expected the replacement to reuse the same SPKI")
	}
	err := pinner.CheckState("pin.test", 443, stateOf(otherHost, true), now.Add(time.Hour))
	if !errors.Is(err, ErrHostnameMismatch) {
		t.Fatalf("known pin, wrong hostname error = %v, want ErrHostnameMismatch", err)
	}
	if current := mustPin(t, store, "pin.test", 443); current != original {
		t.Fatalf("pin changed after hostname mismatch")
	}

	expiredStore := NewMemoryStore()
	expiredPinner := NewPinner(poolOf(ca.cert), WithStore(expiredStore))
	expiredLeaf, _ := ca.issue(t, "pin.test", []string{"pin.test"}, now.Add(-2*time.Hour), now.Add(-time.Hour))
	if err := expiredPinner.CheckState("pin.test", 443, stateOf(expiredLeaf, true), now.Add(-90*time.Minute)); err != nil {
		t.Fatalf("seed expired pin: %v", err)
	}
	if err := expiredPinner.CheckState("pin.test", 443, stateOf(otherHost, true), now); !errors.Is(err, ErrHostnameMismatch) {
		t.Fatalf("expired pin, wrong hostname error = %v, want ErrHostnameMismatch", err)
	}

	freshStore := NewMemoryStore()
	freshPinner := NewPinner(poolOf(ca.cert), WithStore(freshStore))
	wrongHost, _ := ca.issue(t, "other.test", []string{"other.test"}, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err := freshPinner.CheckState("pin.test", 443, stateOf(wrongHost, true), now); !errors.Is(err, ErrHostnameMismatch) {
		t.Fatalf("first connection, wrong hostname error = %v, want ErrHostnameMismatch", err)
	}
	if freshStore.Len() != 0 {
		t.Fatalf("recorded a pin for a hostname-mismatched certificate")
	}
}

func TestIncompleteHandshakeNeverWritesPin(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	ca := newTestCA(t, "trusted root")
	leaf, _ := ca.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(2*time.Hour))
	newLeaf, _ := ca.issue(t, "pin.test", []string{"pin.test"}, now.Add(30*time.Minute), now.Add(24*time.Hour))
	store := NewMemoryStore()
	pinner := NewPinner(poolOf(ca.cert), WithStore(store))

	err := pinner.CheckState("pin.test", 443, stateOf(leaf, false), now)
	if !errors.Is(err, ErrHandshakeIncomplete) {
		t.Fatalf("incomplete handshake error = %v, want ErrHandshakeIncomplete", err)
	}
	if store.Len() != 0 {
		t.Fatalf("incomplete handshake wrote a pin")
	}

	if err := pinner.CheckState("pin.test", 443, stateOf(leaf, true), now); err != nil {
		t.Fatalf("complete handshake should write pin: %v", err)
	}
	original := mustPin(t, store, "pin.test", 443)

	err = pinner.CheckState("pin.test", 443, stateOf(newLeaf, false), now.Add(time.Hour))
	if !errors.Is(err, ErrHandshakeIncomplete) {
		t.Fatalf("incomplete handshake error = %v, want ErrHandshakeIncomplete", err)
	}
	if current := mustPin(t, store, "pin.test", 443); current != original {
		t.Fatalf("incomplete handshake updated the pin")
	}
}

func TestFailedRealHandshakeDoesNotWritePin(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	clientSide, serverSide := net.Pipe()
	client := tls.Client(clientSide, &tls.Config{
		ServerName:         "pin.test",
		InsecureSkipVerify: true,
	})
	done := make(chan error, 1)
	go func() {
		done <- client.Handshake()
	}()
	_ = serverSide.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("handshake unexpectedly succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("handshake did not fail")
	}
	_ = clientSide.Close()

	store := NewMemoryStore()
	pinner := NewPinner(nil, WithStore(store))
	if err := pinner.CheckConn(client, now); !errors.Is(err, ErrHandshakeIncomplete) {
		t.Fatalf("failed handshake check error = %v, want ErrHandshakeIncomplete", err)
	}
	if store.Len() != 0 {
		t.Fatalf("failed handshake wrote a pin")
	}
}

func TestSuccessfulRealHandshakeRecordsPin(t *testing.T) {
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	ca := newTestCA(t, "trusted root")
	leaf, key := ca.issue(t, "pin.test", []string{"pin.test"}, now.Add(-time.Hour), now.Add(24*time.Hour))

	serverConfig := &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{leaf.Raw},
			PrivateKey:  key,
		}},
	}
	clientSide, serverSide := net.Pipe()
	server := tls.Server(serverSide, serverConfig)
	client := tls.Client(clientSide, &tls.Config{
		ServerName:         "pin.test",
		InsecureSkipVerify: true,
	})
	defer serverSide.Close()
	defer clientSide.Close()

	serverDone := make(chan error, 1)
	clientDone := make(chan error, 1)
	go func() { serverDone <- server.Handshake() }()
	go func() { clientDone <- client.Handshake() }()

	timeout := time.After(5 * time.Second)
	for range 2 {
		select {
		case err := <-serverDone:
			if err != nil {
				t.Fatalf("server handshake: %v", err)
			}
			serverDone = nil
		case err := <-clientDone:
			if err != nil {
				t.Fatalf("client handshake: %v", err)
			}
			clientDone = nil
		case <-timeout:
			t.Fatalf("handshake timed out")
		}
	}

	store := NewMemoryStore()
	pinner := NewPinner(poolOf(ca.cert), WithStore(store))
	if err := pinner.CheckConnection(client, "pin.test", 443, now); err != nil {
		t.Fatalf("check real connection: %v", err)
	}

	pin := mustPin(t, store, "pin.test", 443)
	if pin.Fingerprint != SPKIFingerprint(leaf) {
		t.Fatalf("real handshake recorded wrong fingerprint")
	}
}
