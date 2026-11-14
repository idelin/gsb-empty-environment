package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"sync"
	"testing"
	"time"
)

var testNow = time.Date(2030, 1, 15, 12, 0, 0, 0, time.UTC)

type testCA struct {
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	roots  *x509.CertPool
	nextSN int64
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "EAP-TLS Test CA", Organization: []string{"Example"}},
		NotBefore:             testNow.Add(-time.Hour),
		NotAfter:              testNow.Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return &testCA{cert: cert, key: key, roots: roots, nextSN: 2}
}

func (ca *testCA) issueClient(t *testing.T, identity string) ([][]byte, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	ca.nextSN++
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(ca.nextSN),
		Subject:               pkix.Name{CommonName: identity, Organization: []string{"Example"}},
		EmailAddresses:        []string{identity},
		NotBefore:             testNow.Add(-time.Hour),
		NotAfter:              testNow.Add(48 * time.Hour),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create client certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse client certificate: %v", err)
	}
	return [][]byte{der, ca.cert.Raw}, cert
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(now time.Time) *testClock {
	return &testClock{now: now}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func guestPolicy() Policy {
	return Policy{
		DomainID:                "guest-domain",
		Revision:                1,
		SSID:                    "Guest",
		AllowedBSSIDs:           []string{"aa:bb:cc:00:00:01", "aa:bb:cc:00:00:02"},
		AllowedServerIdentities: []string{"aaa.example.org"},
		AllowedTLSVersions:      []uint16{TLSVersion13},
		AllowedCipherSuites:     []uint16{TLSAES128GCMSHA256},
		AllowedVLANs:            []uint16{10, 20},
	}
}

func corpPolicy() Policy {
	return Policy{
		DomainID:                "corp-domain",
		Revision:                3,
		SSID:                    "Corp",
		AllowedBSSIDs:           []string{"aa:bb:cc:00:01:01"},
		AllowedServerIdentities: []string{"aaa.example.org"},
		AllowedTLSVersions:      []uint16{TLSVersion13},
		AllowedCipherSuites:     []uint16{TLSAES128GCMSHA256},
		AllowedVLANs:            []uint16{100, 200},
	}
}

func guestAP(n int) AccessPoint {
	switch n {
	case 1:
		return AccessPoint{DomainID: "guest-domain", SSID: "Guest", BSSID: "aa:bb:cc:00:00:01"}
	case 2:
		return AccessPoint{DomainID: "guest-domain", SSID: "Guest", BSSID: "aa:bb:cc:00:00:02"}
	default:
		return AccessPoint{DomainID: "guest-domain", SSID: "Guest", BSSID: "aa:bb:cc:00:00:03"}
	}
}

func corpAP() AccessPoint {
	return AccessPoint{DomainID: "corp-domain", SSID: "Corp", BSSID: "aa:bb:cc:00:01:01"}
}

func testTLS(bindingSeed string) TLSState {
	binding := sha256.Sum256([]byte("channel-binding:" + bindingSeed))
	msk := sha256.Sum256([]byte("exported-msk-identity"))
	return TLSState{
		Version:                TLSVersion13,
		CipherSuite:            TLSAES128GCMSHA256,
		Finished:               true,
		ChannelBindingType:     "tls-exporter",
		ChannelBinding:         binding[:],
		ChannelBindingVerified: true,
		MSKIdentity:            msk[:],
	}
}

func resumedTLS(bindingSeed string, ticket Ticket) TLSState {
	tls := testTLS(bindingSeed)
	tls.Resumed = true
	tls.PSKIdentity = append([]byte(nil), ticket.ID[:]...)
	return tls
}

func fastRoamTLS(initial TLSState) TLSState {
	out := initial
	out.Resumed = false
	out.PSKIdentity = nil
	out.ChannelBinding = append([]byte(nil), initial.ChannelBinding...)
	out.MSKIdentity = append([]byte(nil), initial.MSKIdentity...)
	return out
}

type testRig struct {
	auth       *Authenticator
	keyring    *Keyring
	replay     *MemoryReplayStore
	revocation *MemoryRevocationChecker
	clock      *testClock
	ca         *testCA
	chain      [][]byte
	leaf       *x509.Certificate
}

func newTestRig(t *testing.T) *testRig {
	t.Helper()
	ca := newTestCA(t)
	chain, leaf := ca.issueClient(t, "alice@corp.example")
	clock := newTestClock(testNow)
	key := sha256.Sum256([]byte("fixed test ticket master key"))
	keyring, err := NewKeyring(key[:])
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	replay := NewMemoryReplayStore()
	revocation := NewMemoryRevocationChecker()
	auth, err := NewAuthenticator(AuthenticatorConfig{
		Keyring:           keyring,
		ReplayStore:       replay,
		RevocationChecker: revocation,
		TrustRoots:        ca.roots,
		Clock:             clock.Now,
		MaxTicketLifetime: time.Hour,
	})
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	return &testRig{
		auth:       auth,
		keyring:    keyring,
		replay:     replay,
		revocation: revocation,
		clock:      clock,
		ca:         ca,
		chain:      chain,
		leaf:       leaf,
	}
}

func (r *testRig) issue(t *testing.T, policy Policy, ap AccessPoint, tls TLSState, vlan uint16) *Issued {
	t.Helper()
	issued, err := r.auth.Issue(t.Context(), IssueRequest{
		OuterEAPIdentity:         "anonymous@example.org",
		InnerEAPIdentity:         "alice@corp.example",
		ServerVirtualIdentity:    "aaa.example.org",
		AccessPoint:              ap,
		Policy:                   policy,
		ClientCertificateChain:   r.chain,
		CertValidationGeneration: 7,
		TLS:                      tls,
		RequestedVLAN:            vlan,
		ACL: []ACLRule{
			{Action: "allow", Protocol: "udp", Destination: "10.10.0.0/16", Ports: "53"},
			{Action: "allow", Protocol: "tcp", Destination: "10.10.0.0/16", Ports: "443"},
		},
		Lifetime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	return issued
}

func (r *testRig) resumeRequest(ticket Ticket, policy Policy, ap AccessPoint, tls TLSState) ResumeRequest {
	return ResumeRequest{
		Ticket:                   ticket.Bytes(),
		OuterEAPIdentity:         "anonymous@example.org",
		InnerEAPIdentity:         "alice@corp.example",
		ServerVirtualIdentity:    "aaa.example.org",
		AccessPoint:              ap,
		Policy:                   policy,
		ClientCertificateChain:   r.chain,
		CertValidationGeneration: 7,
		TLS:                      tls,
		Mode:                     ResumeConsume,
	}
}
