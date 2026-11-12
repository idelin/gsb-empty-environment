package idbind

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// testFixture simulates one IKE_AUTH exchange from the responder's view.
type testFixture struct {
	key      *ecdsa.PrivateKey
	cert     *x509.Certificate
	skPi     []byte
	nonceR   []byte
	message1 []byte
	ownID    *IDPayload
	store    *PolicyStore
	aliceIDi *IDPayload
	alicePol *Policy
}

func newFixture(t *testing.T) *testFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:   big.NewInt(1),
		Subject:        pkix.Name{CommonName: "alice@example.com"},
		NotBefore:      time.Now().Add(-time.Hour),
		NotAfter:       time.Now().Add(time.Hour),
		EmailAddresses: []string{"alice@example.com"},
		DNSNames:       []string{"alice.vpn.example.com"},
		IPAddresses:    []net.IP{net.ParseIP("192.0.2.10")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	skPi := make([]byte, 32)
	nonceR := make([]byte, 32)
	if _, err := rand.Read(skPi); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(nonceR); err != nil {
		t.Fatal(err)
	}
	// Stand-in for the initiator's IKE_SA_INIT request as received.
	message1 := make([]byte, 256)
	if _, err := rand.Read(message1); err != nil {
		t.Fatal(err)
	}
	ownID := &IDPayload{Type: ID_FQDN, Data: []byte("vpn.example.com")}
	aliceIDi := &IDPayload{Type: ID_RFC822_ADDR, Data: []byte("alice@example.com")}
	// Alice may reach the 10.1.0.0/16 client net over TCP, any port.
	alicePol := &Policy{Allowed: []TrafficSelector{{
		Type:      TS_IPV4_ADDR_RANGE,
		Protocol:  6,
		StartPort: 0,
		EndPort:   65535,
		StartAddr: net.ParseIP("10.1.0.0"),
		EndAddr:   net.ParseIP("10.1.255.255"),
	}}}
	store := NewPolicyStore()
	store.Set(aliceIDi, alicePol)
	return &testFixture{
		key:      key,
		cert:     cert,
		skPi:     skPi,
		nonceR:   nonceR,
		message1: message1,
		ownID:    ownID,
		store:    store,
		aliceIDi: aliceIDi,
		alicePol: alicePol,
	}
}

// sign builds the initiator's AUTH signature exactly as a real peer
// would: reconstruct the signed octets and sign with the cert key.
func (f *testFixture) sign(t *testing.T, message1 []byte, idi *IDPayload) []byte {
	t.Helper()
	signed := BuildInitiatorSignedOctets(message1, f.nonceR, idi.Body(), f.skPi, PRFHMACSHA256)
	digest := sha256.Sum256(signed)
	sig, err := ecdsa.SignASN1(rand.Reader, f.key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func (f *testFixture) request(idi, idr *IDPayload, sig []byte, tsi, tsr []TrafficSelector) *AuthRequest {
	return &AuthRequest{
		RealMessage1:  f.message1,
		NonceR:        f.nonceR,
		IDiPayload:    idi.Marshal(0),
		IDrPayload:    idr.Marshal(0),
		SKPi:          f.skPi,
		PRF:           PRFHMACSHA256,
		AuthMethod:    AuthECDSASha256,
		AuthSignature: sig,
		PeerCert:      f.cert,
		OwnID:         f.ownID,
		TSi:           tsi,
		TSr:           tsr,
	}
}

// narrowedTS is strictly inside Alice's policy.
func narrowedTS() []TrafficSelector {
	return []TrafficSelector{{
		Type:      TS_IPV4_ADDR_RANGE,
		Protocol:  6,
		StartPort: 443,
		EndPort:   443,
		StartAddr: net.ParseIP("10.1.2.0"),
		EndAddr:   net.ParseIP("10.1.2.255"),
	}}
}

func TestLegitimateNegotiation(t *testing.T) {
	f := newFixture(t)
	sig := f.sign(t, f.message1, f.aliceIDi)
	req := f.request(f.aliceIDi, f.ownID, sig, narrowedTS(), narrowedTS())
	id, policy, err := Authorize(req, f.store)
	if err != nil {
		t.Fatalf("legitimate negotiation rejected: %v", err)
	}
	if string(id.Data) != "alice@example.com" {
		t.Fatalf("unexpected identity %q", id.Data)
	}
	if policy != f.alicePol {
		t.Fatalf("unexpected policy returned")
	}
}

func TestMismatchedIDi(t *testing.T) {
	f := newFixture(t)
	// Attacker holds Alice's certificate and key but claims to be Bob,
	// hoping to inherit Bob's broader policy. The AUTH signature over
	// the forged IDi is valid — only the cert binding can catch this.
	bobIDi := &IDPayload{Type: ID_RFC822_ADDR, Data: []byte("bob@example.com")}
	sig := f.sign(t, f.message1, bobIDi)
	req := f.request(bobIDi, f.ownID, sig, narrowedTS(), narrowedTS())
	_, _, err := Authorize(req, f.store)
	if err == nil {
		t.Fatal("mismatched IDi accepted")
	}
	if !strings.Contains(err.Error(), "not bound to certificate") {
		t.Fatalf("expected cert binding failure, got: %v", err)
	}
}

func TestTamperedIDr(t *testing.T) {
	f := newFixture(t)
	sig := f.sign(t, f.message1, f.aliceIDi)
	// IDr names a different responder than the one processing IKE_AUTH.
	tamperedIDr := &IDPayload{Type: ID_FQDN, Data: []byte("other-vpn.example.com")}
	req := f.request(f.aliceIDi, tamperedIDr, sig, narrowedTS(), narrowedTS())
	_, _, err := Authorize(req, f.store)
	if err == nil {
		t.Fatal("tampered IDr accepted")
	}
	if !strings.Contains(err.Error(), "IDr") {
		t.Fatalf("expected IDr failure, got: %v", err)
	}
}

func TestIdentityOverreach(t *testing.T) {
	f := newFixture(t)
	sig := f.sign(t, f.message1, f.aliceIDi)
	// Alice's certificate and IDi are genuine, but the proposed CHILD_SA
	// reaches outside her 10.1.0.0/16 policy.
	overreach := []TrafficSelector{{
		Type:      TS_IPV4_ADDR_RANGE,
		Protocol:  6,
		StartPort: 0,
		EndPort:   65535,
		StartAddr: net.ParseIP("10.2.0.0"),
		EndAddr:   net.ParseIP("10.2.255.255"),
	}}
	req := f.request(f.aliceIDi, f.ownID, sig, overreach, narrowedTS())
	_, _, err := Authorize(req, f.store)
	if err == nil {
		t.Fatal("overreaching traffic selectors accepted")
	}
	if !strings.Contains(err.Error(), "exceeds identity policy") {
		t.Fatalf("expected policy overreach failure, got: %v", err)
	}
}

func TestProtocolOverreach(t *testing.T) {
	f := newFixture(t)
	sig := f.sign(t, f.message1, f.aliceIDi)
	// Same addresses as the policy but UDP instead of TCP.
	udp := []TrafficSelector{{
		Type:      TS_IPV4_ADDR_RANGE,
		Protocol:  17,
		StartPort: 0,
		EndPort:   65535,
		StartAddr: net.ParseIP("10.1.0.0"),
		EndAddr:   net.ParseIP("10.1.255.255"),
	}}
	req := f.request(f.aliceIDi, f.ownID, sig, udp, narrowedTS())
	if _, _, err := Authorize(req, f.store); err == nil {
		t.Fatal("protocol overreach accepted")
	}
}

func TestTamperedFirstMessage(t *testing.T) {
	f := newFixture(t)
	sig := f.sign(t, f.message1, f.aliceIDi)
	// The IKE_SA_INIT request seen by the responder differs from what
	// the initiator signed (e.g. modified in flight).
	tampered := make([]byte, len(f.message1))
	copy(tampered, f.message1)
	tampered[10] ^= 0xff
	req := f.request(f.aliceIDi, f.ownID, sig, narrowedTS(), narrowedTS())
	req.RealMessage1 = tampered
	_, _, err := Authorize(req, f.store)
	if err == nil {
		t.Fatal("tampered first message accepted")
	}
	if !strings.Contains(err.Error(), "signature") {
		t.Fatalf("expected signature failure, got: %v", err)
	}
}

func TestSignedOctetsLayout(t *testing.T) {
	// RFC 7296 Section 2.15: InitiatorSignedOctets =
	// RealMessage1 | NonceRData | prf(SK_pi, IDi').
	msg1 := []byte{0x01, 0x02, 0x03}
	nonceR := []byte{0x0a, 0x0b}
	idi := &IDPayload{Type: ID_FQDN, Data: []byte("a.example.com")}
	skPi := []byte{0xff, 0xee}
	got := BuildInitiatorSignedOctets(msg1, nonceR, idi.Body(), skPi, PRFHMACSHA256)
	mac := PRFHMACSHA256(skPi, idi.Body())
	want := append(append(append([]byte{}, msg1...), nonceR...), mac...)
	if string(got) != string(want) {
		t.Fatal("signed octets layout mismatch")
	}
	if len(got) != len(msg1)+len(nonceR)+32 {
		t.Fatalf("unexpected length %d", len(got))
	}
}

func TestParseIDPayloadRoundTrip(t *testing.T) {
	id := &IDPayload{Type: ID_RFC822_ADDR, Data: []byte("alice@example.com")}
	raw := id.Marshal(38)
	parsed, err := ParseIDPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Type != id.Type || string(parsed.Data) != string(id.Data) {
		t.Fatalf("round trip mismatch: %+v", parsed)
	}
	// Body excludes the 4-byte generic header but keeps type+reserved.
	if len(parsed.Body()) != 4+len(id.Data) {
		t.Fatalf("body length %d", len(parsed.Body()))
	}
	if parsed.Body()[0] != byte(ID_RFC822_ADDR) {
		t.Fatal("body does not start with ID type")
	}
}

func TestParseIDPayloadRejectsTruncated(t *testing.T) {
	if _, err := ParseIDPayload([]byte{0, 0, 0, 8, 2}); err == nil {
		t.Fatal("truncated payload accepted")
	}
	bad := (&IDPayload{Type: ID_FQDN, Data: []byte("x")}).Marshal(0)
	bad[3] = 99 // declared length disagrees with actual
	if _, err := ParseIDPayload(bad); err == nil {
		t.Fatal("length mismatch accepted")
	}
}

func TestBindIdentityVariants(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		id   *IDPayload
		ok   bool
	}{
		{"email SAN", &IDPayload{Type: ID_RFC822_ADDR, Data: []byte("alice@example.com")}, true},
		{"email SAN case-insensitive", &IDPayload{Type: ID_RFC822_ADDR, Data: []byte("ALICE@example.com")}, true},
		{"DNS SAN", &IDPayload{Type: ID_FQDN, Data: []byte("alice.vpn.example.com")}, true},
		{"IP SAN", &IDPayload{Type: ID_IPV4_ADDR, Data: net.ParseIP("192.0.2.10").To4()}, true},
		{"subject DN", &IDPayload{Type: ID_DER_ASN1_DN, Data: f.cert.RawSubject}, true},
		{"wrong email", &IDPayload{Type: ID_RFC822_ADDR, Data: []byte("mallory@example.com")}, false},
		{"wrong DNS", &IDPayload{Type: ID_FQDN, Data: []byte("mallory.example.com")}, false},
		{"wrong IP", &IDPayload{Type: ID_IPV4_ADDR, Data: net.ParseIP("192.0.2.11").To4()}, false},
		{"KEY_ID not cert-bound", &IDPayload{Type: ID_KEY_ID, Data: []byte("opaque")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := BindIdentity(tc.id, f.cert)
			if tc.ok && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestCNFallbackWhenNoSAN(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "carol@example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	id := &IDPayload{Type: ID_RFC822_ADDR, Data: []byte("carol@example.com")}
	if err := BindIdentity(id, cert); err != nil {
		t.Fatalf("CN fallback failed: %v", err)
	}
	id.Data = []byte("dave@example.com")
	if err := BindIdentity(id, cert); err == nil {
		t.Fatal("CN fallback accepted wrong identity")
	}
}
