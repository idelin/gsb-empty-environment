package spiffefed_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/url"
	"testing"
	"time"

	spiffefed "formal.local/w27-6accbac653"
)

type testAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestAuthority(t *testing.T, name string) *testAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA certificate: %v", err)
	}
	return &testAuthority{cert: cert, key: key}
}

func makeTestSVID(t *testing.T, issuer *testAuthority, uris []string, mutate func(*x509.Certificate)) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate SVID key: %v", err)
	}
	now := time.Now().UTC()
	parsedURIs := make([]*url.URL, 0, len(uris))
	for _, raw := range uris {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse URI %q: %v", raw, err)
		}
		parsedURIs = append(parsedURIs, parsed)
	}
	template := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  false,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		URIs:                  parsedURIs,
	}
	if mutate != nil {
		mutate(template)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer.cert, &key.PublicKey, issuer.key)
	if err != nil {
		t.Fatalf("create SVID certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse SVID certificate: %v", err)
	}
	return cert
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate serial: %v", err)
	}
	return serial
}

func testJWKS(t *testing.T, domain string, keys map[string]*ecdsa.PrivateKey) *spiffefed.JWKS {
	t.Helper()
	publicKeys := make(map[string]crypto.PublicKey, len(keys))
	for kid, key := range keys {
		publicKeys[kid] = &key.PublicKey
	}
	set, err := spiffefed.NewJWKS(domain, publicKeys)
	if err != nil {
		t.Fatalf("new JWKS: %v", err)
	}
	return set
}

func signTestJWT(t *testing.T, key *ecdsa.PrivateKey, kid string, header, claims map[string]any) string {
	t.Helper()
	if header == nil {
		header = map[string]any{"alg": "ES256", "typ": "JWT", "kid": kid}
	}
	if _, ok := header["kid"]; !ok {
		header["kid"] = kid
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal JWT claims: %v", err)
	}
	headerPart := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsPart := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerPart + "." + claimsPart
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	size := (key.Curve.Params().BitSize + 7) / 8
	signature := make([]byte, size*2)
	r.FillBytes(signature[:size])
	s.FillBytes(signature[size:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func testClaims(subject, audience string, now time.Time, lifetime time.Duration) map[string]any {
	return map[string]any{
		"sub": subject,
		"aud": []string{audience},
		"iat": now.Unix(),
		"exp": now.Add(lifetime).Unix(),
	}
}

func mustPolicy(t *testing.T, local string, services map[string]spiffefed.ServicePolicy) *spiffefed.FederationPolicy {
	t.Helper()
	policy, err := spiffefed.NewFederationPolicy(local, services)
	if err != nil {
		t.Fatalf("new federation policy: %v", err)
	}
	return policy
}
