package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestLegitimateResponderExchange(t *testing.T) {
	ex, responderKey, responderCert, _, _ := testExchange(t)

	input, err := ex.ResponderAuthInput()
	if err != nil {
		t.Fatalf("ResponderAuthInput() error = %v", err)
	}
	auth := signRSA(t, responderKey, input, crypto.SHA256)

	if err := ex.VerifyResponderAuth(AuthMethodSignature, nil, auth); err != nil {
		t.Fatalf("legitimate responder AUTH rejected: %v", err)
	}

	if got := ex.ResponderCertificate; got != responderCert {
		t.Fatal("test setup lost responder certificate")
	}
}

func TestLegitimateInitiatorExchange(t *testing.T) {
	ex, _, _, initiatorKey, initiatorCert := testExchange(t)

	input, err := ex.InitiatorAuthInput()
	if err != nil {
		t.Fatalf("InitiatorAuthInput() error = %v", err)
	}
	auth := signRSA(t, initiatorKey, input, crypto.SHA256)

	if err := ex.VerifyInitiatorAuth(AuthMethodSignature, nil, auth); err != nil {
		t.Fatalf("legitimate initiator AUTH rejected: %v", err)
	}
	if ex.InitiatorCertificate != initiatorCert {
		t.Fatal("test setup lost initiator certificate")
	}
}

func TestChangedIdentityRejected(t *testing.T) {
	ex, responderKey, _, _, _ := testExchange(t)

	p, err := ex.ResponderParams()
	if err != nil {
		t.Fatal(err)
	}
	input, err := p.AuthInput()
	if err != nil {
		t.Fatal(err)
	}
	auth := signRSA(t, responderKey, input, crypto.SHA256)

	// The replacement identity is another SAN in the genuine certificate, so
	// only the AUTH-covered identity check can reject it.
	replaced := FQDNIdentity("alt.example.com").PayloadBody()
	p.PeerIdentity = replaced
	if err := p.VerifySignature(auth); !errors.Is(err, ErrAuthMismatch) {
		t.Fatalf("changed ID error = %v, want ErrAuthMismatch", err)
	}

	// An identity absent from the certificate is rejected before signature
	// verification.
	p.PeerIdentity = FQDNIdentity("attacker.example.com").PayloadBody()
	if err := p.VerifySignature(auth); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("unknown SAN error = %v, want ErrIdentityMismatch", err)
	}

	// The signed octet string must actually change when the ID body changes.
	changedInput, err := p.AuthInput()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(input, changedInput) {
		t.Fatal("AUTH input did not cover the identity payload body")
	}
}

func TestReflectedNonceRejected(t *testing.T) {
	ex, _, _, _, _ := testExchange(t)
	p, err := ex.ResponderParams()
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an attacker reflecting the initiator's own nonce back as the
	// responder's nonce.
	p.PeerNonce = ex.InitiatorNonce
	if err := p.VerifySignature([]byte("unchecked-signature")); !errors.Is(err, ErrReflectedNonce) {
		t.Fatalf("reflected nonce error = %v, want ErrReflectedNonce", err)
	}
}

func TestReflectedIdentityRejected(t *testing.T) {
	ex, _, _, _, _ := testExchange(t)
	p, err := ex.ResponderParams()
	if err != nil {
		t.Fatal(err)
	}

	// A reflection may replace the responder ID with the initiator's own IDi.
	p.PeerIdentity = ex.InitiatorID
	if err := p.VerifySignature([]byte("unchecked-signature")); !errors.Is(err, ErrReflectedIdentity) {
		t.Fatalf("reflected identity error = %v, want ErrReflectedIdentity", err)
	}

	// Equivalent FQDN encodings must not bypass the check.
	p.PeerIdentity = FQDNIdentity("CLIENT.EXAMPLE.COM").PayloadBody()
	if err := p.VerifySignature([]byte("unchecked-signature")); !errors.Is(err, ErrReflectedIdentity) {
		t.Fatalf("case-varied reflected identity error = %v, want ErrReflectedIdentity", err)
	}
}

func TestReflectedRequestMessageRejected(t *testing.T) {
	ex, _, _, initiatorKey, _ := testExchange(t)

	// Build the AUTH that the initiator would send in its IKE_AUTH request.
	ip, err := ex.InitiatorParams()
	if err != nil {
		t.Fatal(err)
	}
	requestInput, err := ip.AuthInput()
	if err != nil {
		t.Fatal(err)
	}
	reflectedAuth := signRSA(t, initiatorKey, requestInput, crypto.SHA256)

	// Reflect it as if it were the responder's second-round message. The
	// nonce and ID are both the initiator's own, so it must fail fast.
	rp, err := ex.ResponderParams()
	if err != nil {
		t.Fatal(err)
	}
	rp.PeerNonce = ex.InitiatorNonce
	rp.PeerIdentity = ex.InitiatorID
	err = rp.VerifySignature(reflectedAuth)
	if !errors.Is(err, ErrReflectedNonce) {
		t.Fatalf("reflected request error = %v, want ErrReflectedNonce", err)
	}
}

func TestHMACAuth(t *testing.T) {
	ex, _, _, _, _ := testExchange(t)
	p, err := ex.ResponderParams()
	if err != nil {
		t.Fatal(err)
	}
	input, err := p.AuthInput()
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x42}, 32)
	auth := p.PRF(key, input)

	if err := p.VerifyHMAC(key, auth); err != nil {
		t.Fatalf("legitimate HMAC AUTH rejected: %v", err)
	}
	if err := p.VerifyHMAC(key, append([]byte(nil), auth...)); err != nil {
		t.Fatalf("copied HMAC AUTH rejected: %v", err)
	}
	bad := append([]byte(nil), auth...)
	bad[0] ^= 0xff
	if err := p.VerifyHMAC(key, bad); !errors.Is(err, ErrAuthMismatch) {
		t.Fatalf("tampered HMAC AUTH error = %v, want ErrAuthMismatch", err)
	}
	if err := p.VerifyAuthPayload(AuthMethodHMAC, key, append([]byte{2}, auth...)); err != nil {
		t.Fatalf("complete AUTH payload rejected: %v", err)
	}
}

func TestSupportedSignatureAlgorithms(t *testing.T) {
	ex, _, _, _, _ := testExchange(t)
	input, err := ex.ResponderAuthInput()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ECDSA P-256", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cert := selfSignedCertificate(t, key, &key.PublicKey, "vpn.example.com")
		digest := digestFor(crypto.SHA256, input)
		sig, err := ecdsa.SignASN1(rand.Reader, key, digest)
		if err != nil {
			t.Fatal(err)
		}
		ex.ResponderCertificate = cert
		if err := ex.VerifyResponderAuth(AuthMethodSignature, nil, sig); err != nil {
			t.Fatalf("ECDSA AUTH rejected: %v", err)
		}
	})

	t.Run("Ed25519", func(t *testing.T) {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cert := selfSignedCertificate(t, key, pub, "vpn.example.com")
		sig := ed25519.Sign(key, input)
		ex.ResponderCertificate = cert
		if err := ex.VerifyResponderAuth(AuthMethodSignature, nil, sig); err != nil {
			t.Fatalf("Ed25519 AUTH rejected: %v", err)
		}
	})
}

func TestTamperedSignedInputsRejected(t *testing.T) {
	ex, responderKey, _, _, _ := testExchange(t)
	input, err := ex.ResponderAuthInput()
	if err != nil {
		t.Fatal(err)
	}
	auth := signRSA(t, responderKey, input, crypto.SHA256)

	if err := ex.VerifyResponderAuth(AuthMethodSignature, nil, auth); err != nil {
		t.Fatalf("baseline AUTH rejected: %v", err)
	}

	cases := []struct {
		name       string
		mutate     func(*Exchange)
		checkError error
	}{
		{
			name: "changed RealMessage1",
			mutate: func(e *Exchange) {
				e.RealMessage1 = append(append([]byte(nil), e.RealMessage1...), 0)
			},
			checkError: ErrAuthMismatch,
		},
		{
			name: "changed responder nonce",
			mutate: func(e *Exchange) {
				e.ResponderNonce = bytes.Repeat([]byte{0x99}, len(e.ResponderNonce))
			},
			checkError: ErrAuthMismatch,
		},
		{
			name: "changed responder SKp",
			mutate: func(e *Exchange) {
				e.ResponderSKp = bytes.Repeat([]byte{0x77}, len(e.ResponderSKp))
			},
			checkError: ErrAuthMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modified := *ex
			modified.RealMessage1 = append([]byte(nil), ex.RealMessage1...)
			modified.ResponderNonce = append([]byte(nil), ex.ResponderNonce...)
			modified.ResponderSKp = append([]byte(nil), ex.ResponderSKp...)
			tc.mutate(&modified)
			if err := modified.VerifyResponderAuth(AuthMethodSignature, nil, auth); !errors.Is(err, tc.checkError) {
				t.Fatalf("error = %v, want %v", err, tc.checkError)
			}
		})
	}
}

func TestResponderCertificateRequired(t *testing.T) {
	ex, _, _, _, _ := testExchange(t)
	ex.ResponderCertificate = nil
	p, err := ex.ResponderParams()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); !errors.Is(err, ErrCertificateRequired) {
		t.Fatalf("error = %v, want ErrCertificateRequired", err)
	}
}

func TestMatchIdentityToCertificate(t *testing.T) {
	cert := testCertificate(t, "vpn.example.com", "alt.example.com")
	cert.IPAddresses = []net.IP{net.ParseIP("192.0.2.10"), net.ParseIP("2001:db8::10")}
	cert.EmailAddresses = []string{"admin@example.com"}

	ok := []Identity{
		FQDNIdentity("vpn.example.com"),
		FQDNIdentity("VPN.EXAMPLE.COM."),
		FQDNIdentity("alt.example.com"),
		mustIPv4(t, "192.0.2.10"),
		mustIPv6(t, "2001:db8::10"),
		NewIdentity(IDRFC822Addr, []byte("admin@EXAMPLE.COM")),
	}
	for _, id := range ok {
		if err := MatchIdentityToCertificate(id.PayloadBody(), cert); err != nil {
			t.Errorf("%s rejected: %v", id.Value, err)
		}
	}

	bad := []Identity{
		FQDNIdentity("other.example.com"),
		FQDNIdentity("vpn.example.com.attacker.test"),
		mustIPv4(t, "192.0.2.11"),
		mustIPv6(t, "2001:db8::11"),
		NewIdentity(IDRFC822Addr, []byte("admin@attacker.test")),
	}
	for _, id := range bad {
		if err := MatchIdentityToCertificate(id.PayloadBody(), cert); !errors.Is(err, ErrIdentityMismatch) {
			t.Errorf("%s error = %v, want ErrIdentityMismatch", id.Value, err)
		}
	}
}

func TestMatchWildcardSAN(t *testing.T) {
	cert := testCertificate(t, "*.vpn.example.com")

	if err := MatchIdentityToCertificate(FQDNIdentity("gw.vpn.example.com").PayloadBody(), cert); err != nil {
		t.Fatalf("single-label wildcard match failed: %v", err)
	}
	if err := MatchIdentityToCertificate(FQDNIdentity("a.b.vpn.example.com").PayloadBody(), cert); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("multi-label wildcard match error = %v, want ErrIdentityMismatch", err)
	}
	if err := MatchIdentityToCertificate(FQDNIdentity("vpn.example.com").PayloadBody(), cert); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("bare-domain wildcard match error = %v, want ErrIdentityMismatch", err)
	}
}

func TestParseIdentityPayloadBody(t *testing.T) {
	body := FQDNIdentity("vpn.example.com").PayloadBody()
	id, err := ParseIdentityPayloadBody(body)
	if err != nil {
		t.Fatal(err)
	}
	if id.Type != IDFQDN || string(id.Value) != "vpn.example.com" {
		t.Fatalf("identity = %#v", id)
	}
	if !bytes.Equal(id.PayloadBody(), body) {
		t.Fatal("identity round trip changed body")
	}

	for _, malformed := range [][]byte{
		nil,
		{IDFQDN, 0, 0},
		{0xfe, 0, 0, 0, 'x'},
		{IDFQDN, 0, 0, 0},
	} {
		if _, err := ParseIdentityPayloadBody(malformed); !errors.Is(err, ErrMalformedIdentity) {
			t.Errorf("malformed body %x error = %v, want ErrMalformedIdentity", malformed, err)
		}
	}
}

func TestBuildAuthInput(t *testing.T) {
	prf := HMACPRF(sha256.New)
	skp := bytes.Repeat([]byte{0x11}, 32)
	id := FQDNIdentity("vpn.example.com").PayloadBody()
	got, err := BuildAuthInput([]byte("real-message-1"), []byte("nonce-r"), skp, id, prf)
	if err != nil {
		t.Fatal(err)
	}

	inner := prf(skp, id)
	want := append([]byte("real-message-1"), []byte("nonce-r")...)
	want = append(want, inner...)
	if !bytes.Equal(got, want) {
		t.Fatalf("AUTH input mismatch\n got %x\nwant %x", got, want)
	}

	if _, err := BuildAuthInput(nil, nil, nil, nil, nil); !errors.Is(err, ErrInvalidAuthParams) {
		t.Fatalf("empty inputs error = %v, want ErrInvalidAuthParams", err)
	}
}

func testExchange(t *testing.T) (*Exchange, *rsa.PrivateKey, *x509.Certificate, *rsa.PrivateKey, *x509.Certificate) {
	t.Helper()

	responderKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	initiatorKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	responderCert := testCertificateWithKey(t, responderKey, "vpn.example.com", "alt.example.com")
	initiatorCert := testCertificateWithKey(t, initiatorKey, "client.example.com")

	ex := &Exchange{
		RealMessage1:         []byte("IKE_SA_INIT request bytes"),
		InitiatorNonce:       []byte("initiator-nonce-0123456789"),
		ResponderNonce:       []byte("responder-nonce-0123456789"),
		InitiatorID:          FQDNIdentity("client.example.com").PayloadBody(),
		ResponderID:          FQDNIdentity("vpn.example.com").PayloadBody(),
		InitiatorSKp:         bytes.Repeat([]byte{0x11}, 32),
		ResponderSKp:         bytes.Repeat([]byte{0x22}, 32),
		InitiatorCertificate: initiatorCert,
		ResponderCertificate: responderCert,
		PRF:                  HMACPRF(sha256.New),
	}
	return ex, responderKey, responderCert, initiatorKey, initiatorCert
}

func testCertificate(t *testing.T, dnsNames ...string) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return testCertificateWithKey(t, key, dnsNames...)
}

func testCertificateWithKey(t *testing.T, key *rsa.PrivateKey, dnsNames ...string) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "not-a-san"},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func selfSignedCertificate(t *testing.T, signer crypto.Signer, pub crypto.PublicKey, dnsNames ...string) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "not-a-san"},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func signRSA(t *testing.T, key *rsa.PrivateKey, data []byte, hash crypto.Hash) []byte {
	t.Helper()
	digest := digestFor(hash, data)
	if digest == nil {
		t.Fatalf("hash %s unavailable", hash)
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, hash, digest)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func mustIPv4(t *testing.T, value string) Identity {
	t.Helper()
	id, err := IPv4Identity(net.ParseIP(value))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustIPv6(t *testing.T, value string) Identity {
	t.Helper()
	id, err := IPv6Identity(net.ParseIP(value))
	if err != nil {
		t.Fatal(err)
	}
	return id
}
