package appstorejws

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testBundleID = "com.example.app"
)

type testChain struct {
	leafDER, intermediateDER, rootDER []byte
	leafCert                          *x509.Certificate
	rootCert                          *x509.Certificate
	leafKey                           *ecdsa.PrivateKey
}

func mustExt(t *testing.T, oid string) pkix.Extension {
	t.Helper()
	der, err := asn1.Marshal(true)
	if err != nil {
		t.Fatalf("marshal extension %s: %v", oid, err)
	}
	return pkix.Extension{Id: asn1.ObjectIdentifier(parseOID(t, oid)), Value: der}
}

func parseOID(t *testing.T, s string) []int {
	t.Helper()
	parts := strings.Split(s, ".")
	oid := make(asn1.ObjectIdentifier, 0, len(parts))
	for _, part := range parts {
		arc, err := strconv.Atoi(part)
		if err != nil {
			t.Fatalf("parse oid %s: %v", s, err)
		}
		oid = append(oid, arc)
	}
	return []int(oid)
}

// buildTestChain creates an in-memory root -> intermediate -> leaf P-256
// chain. The marker OIDs can be omitted to simulate malformed certificates.
func buildTestChain(t *testing.T, notBefore, notAfter time.Time, leafOID, intermediateOID bool) testChain {
	t.Helper()

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate root key: %v", err)
	}
	rootSerial := big.NewInt(1)
	rootTmpl := &x509.Certificate{
		SerialNumber:          rootSerial,
		Subject:               pkix.Name{CommonName: "Test Apple Root CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}

	intermediateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate intermediate key: %v", err)
	}
	intermediateTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Test WWDR Intermediate CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	if intermediateOID {
		intermediateTmpl.ExtraExtensions = []pkix.Extension{mustExt(t, OIDIntermediateMarker)}
	}
	intermediateDER, err := x509.CreateCertificate(rand.Reader, intermediateTmpl, rootCert, &intermediateKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create intermediate: %v", err)
	}
	intermediateCert, err := x509.ParseCertificate(intermediateDER)
	if err != nil {
		t.Fatalf("parse intermediate: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "Test Transaction Signing"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if leafOID {
		leafTmpl.ExtraExtensions = []pkix.Extension{mustExt(t, OIDLeafMarker)}
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, intermediateCert, &leafKey.PublicKey, intermediateKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}

	return testChain{
		leafDER:         leafDER,
		intermediateDER: intermediateDER,
		rootDER:         rootDER,
		leafCert:        leafCert,
		rootCert:        rootCert,
		leafKey:         leafKey,
	}
}

// signJWS produces a compact ES256 JWS whose x5c header carries the chain's
// DER certificates (standard base64, as used in App Store headers).
func signJWS(t *testing.T, chain testChain, payload map[string]any, alg string) string {
	t.Helper()
	header := map[string]any{
		"alg": alg,
		"x5c": []string{
			base64.StdEncoding.EncodeToString(chain.leafDER),
			base64.StdEncoding.EncodeToString(chain.intermediateDER),
			base64.StdEncoding.EncodeToString(chain.rootDER),
		},
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	head := base64.RawURLEncoding.EncodeToString(headerJSON)
	body := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := head + "." + body

	digest := sha256.Sum256([]byte(signingInput))
	rr, ss, err := ecdsa.Sign(rand.Reader, chain.leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	signature := make([]byte, 64)
	rr.FillBytes(signature[:32])
	ss.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func samplePayload(environment string, signedDate int64) map[string]any {
	return map[string]any{
		"appTransactionId":      int64(99_000_111_222),
		"bundleId":              testBundleID,
		"environment":           environment,
		"originalTransactionId": "1000000123456789",
		"productId":             "com.example.app.pro.monthly",
		"signedDate":            signedDate,
		"transactionId":         "2000000987654321",
		"type":                  "Auto-Renewable Subscription",
		"webOrderLineItemId":    "3000000555444333",
	}
}

func newTestVerifier(t *testing.T, chain testChain, environment string) *Verifier {
	t.Helper()
	verifier, err := New(Config{
		RootDER:     chain.rootDER,
		BundleID:    testBundleID,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("New verifier: %v", err)
	}
	return verifier
}

func wantErrIs(t *testing.T, got error, target error) {
	t.Helper()
	if !errors.Is(got, target) {
		t.Fatalf("want error %v, got %v", target, got)
	}
}

func signWithHeader(t *testing.T, chain testChain, header map[string]any, payload map[string]any) string {
	t.Helper()
	head := base64.RawURLEncoding.EncodeToString(mustJSON(t, header))
	body := base64.RawURLEncoding.EncodeToString(mustJSON(t, payload))
	signingInput := head + "." + body
	digest := sha256.Sum256([]byte(signingInput))
	rr, ss, err := ecdsa.Sign(rand.Reader, chain.leafKey, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	signature := make([]byte, 64)
	rr.FillBytes(signature[:32])
	ss.FillBytes(signature[32:])
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}

func splitTestCompact(token string) []string {
	var out []string
	start := 0
	for i := 0; i < len(token); i++ {
		if token[i] == '.' {
			out = append(out, token[start:i])
			start = i + 1
		}
	}
	return append(out, token[start:])
}

func joinCompact(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += "."
		}
		out += part
	}
	return out
}

var (
	windowStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	windowEnd   = time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	signedAt    = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
)

func validChain(t *testing.T) testChain {
	t.Helper()
	return buildTestChain(t, windowStart, windowEnd, true, true)
}

func TestVerify_ValidTransaction(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)
	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())

	tx, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tx.BundleID != testBundleID {
		t.Fatalf("bundle id = %q", tx.BundleID)
	}
	if tx.Environment != EnvironmentProduction {
		t.Fatalf("environment = %q", tx.Environment)
	}
	if tx.Revoked() {
		t.Fatal("fresh transaction must not be revoked")
	}
	if tx.Raw["productId"] != "com.example.app.pro.monthly" {
		t.Fatalf("raw payload not retained: %v", tx.Raw)
	}
}

func TestVerify_ValidityBoundaries(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)

	cases := []struct {
		name string
		when time.Time
		want error
	}{
		{"signed at NotBefore is valid", windowStart, nil},
		{"signed at NotAfter is valid", windowEnd, nil},
		{"one second before NotBefore", windowStart.Add(-time.Second), ErrCertExpired},
		{"one second after NotAfter", windowEnd.Add(time.Second), ErrCertExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := samplePayload(EnvironmentProduction, tc.when.UnixMilli())
			_, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
			if tc.want == nil && err != nil {
				t.Fatalf("want success, got %v", err)
			}
			if tc.want != nil {
				wantErrIs(t, err, tc.want)
			}
		})
	}
}

func TestVerify_ExpiredChainReportsPosition(t *testing.T) {
	chain := buildTestChain(t, windowStart, windowEnd, true, true)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)
	payload := samplePayload(EnvironmentProduction, windowEnd.Add(time.Second).UnixMilli())

	_, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
	wantErrIs(t, err, ErrCertExpired)
}

func TestVerify_MissingMarkerOIDs(t *testing.T) {
	cases := []struct {
		name            string
		leafOID         bool
		intermediateOID bool
	}{
		{"leaf missing OID", false, true},
		{"intermediate missing OID", true, false},
		{"both missing OID", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chain := buildTestChain(t, windowStart, windowEnd, tc.leafOID, tc.intermediateOID)
			verifier := newTestVerifier(t, chain, EnvironmentProduction)
			payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())

			_, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
			wantErrIs(t, err, ErrMissingOID)
		})
	}
}

func TestVerify_AttackerSelfSignedChainRejected(t *testing.T) {
	// The pinned anchor is one root; the attacker builds an entirely separate
	// self-signed chain that still carries both marker OIDs.
	pinned := validChain(t)
	verifier := newTestVerifier(t, pinned, EnvironmentProduction)

	attacker := buildTestChain(t, windowStart, windowEnd, true, true)
	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())
	token := signJWS(t, attacker, payload, "ES256")

	_, err := verifier.Verify(token)
	wantErrIs(t, err, ErrRootMismatch)
}

func TestVerify_ChainSignatureForgedRejected(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)

	// A second attacker chain shares the pinned root but has an intermediate
	// that the real root never signed.
	rogue := buildTestChain(t, windowStart, windowEnd, true, true)
	rogue.rootDER = chain.rootDER

	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())
	_, err := verifier.Verify(signJWS(t, rogue, payload, "ES256"))
	wantErrIs(t, err, ErrInvalidChain)
}

func TestVerify_SandboxTransaction(t *testing.T) {
	chain := validChain(t)

	t.Run("sandbox verifier accepts sandbox token", func(t *testing.T) {
		verifier := newTestVerifier(t, chain, EnvironmentSandbox)
		payload := samplePayload(EnvironmentSandbox, signedAt.UnixMilli())

		tx, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if tx.Environment != EnvironmentSandbox {
			t.Fatalf("environment = %q", tx.Environment)
		}
	})

	t.Run("production verifier rejects sandbox token", func(t *testing.T) {
		verifier := newTestVerifier(t, chain, EnvironmentProduction)
		payload := samplePayload(EnvironmentSandbox, signedAt.UnixMilli())

		_, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
		wantErrIs(t, err, ErrEnvironmentMismatch)
	})

	t.Run("sandbox verifier rejects production token", func(t *testing.T) {
		verifier := newTestVerifier(t, chain, EnvironmentSandbox)
		payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())

		_, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
		wantErrIs(t, err, ErrEnvironmentMismatch)
	})
}

func TestVerify_RevocationDate(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)

	t.Run("revocationDate marks transaction revoked", func(t *testing.T) {
		payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())
		payload["revocationDate"] = signedAt.Add(24 * time.Hour).UnixMilli()

		tx, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
		wantErrIs(t, err, ErrRevoked)
		if tx == nil || !tx.Revoked() {
			t.Fatalf("revoked transaction must still be returned: %+v", tx)
		}
	})

	t.Run("explicit null revocationDate is not revoked", func(t *testing.T) {
		payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())
		payload["revocationDate"] = nil

		if _, err := verifier.Verify(signJWS(t, chain, payload, "ES256")); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	})
}

func TestVerify_BundleIDMismatch(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)
	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())
	payload["bundleId"] = "com.evil.other"

	_, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
	wantErrIs(t, err, ErrBundleIDMismatch)
}

func TestVerify_Algorithm(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)
	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())

	for _, alg := range []string{"none", "RS256", "ES384", "es256", ""} {
		t.Run(alg, func(t *testing.T) {
			_, err := verifier.Verify(signJWS(t, chain, payload, alg))
			wantErrIs(t, err, ErrUnsupportedAlg)
		})
	}
}

func TestVerify_X5CStructure(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)
	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())

	t.Run("two certificates rejected", func(t *testing.T) {
		header := map[string]any{
			"alg": "ES256",
			"x5c": []string{
				base64.StdEncoding.EncodeToString(chain.leafDER),
				base64.StdEncoding.EncodeToString(chain.intermediateDER),
			},
		}
		token := signWithHeader(t, chain, header, payload)
		_, err := verifier.Verify(token)
		wantErrIs(t, err, ErrInvalidX5C)
	})

	t.Run("four certificates rejected", func(t *testing.T) {
		header := map[string]any{
			"alg": "ES256",
			"x5c": []string{
				base64.StdEncoding.EncodeToString(chain.leafDER),
				base64.StdEncoding.EncodeToString(chain.intermediateDER),
				base64.StdEncoding.EncodeToString(chain.rootDER),
				base64.StdEncoding.EncodeToString(chain.rootDER),
			},
		}
		token := signWithHeader(t, chain, header, payload)
		_, err := verifier.Verify(token)
		wantErrIs(t, err, ErrInvalidX5C)
	})

	t.Run("non-DER entry rejected", func(t *testing.T) {
		header := map[string]any{
			"alg": "ES256",
			"x5c": []string{
				base64.StdEncoding.EncodeToString([]byte("not a certificate")),
				base64.StdEncoding.EncodeToString(chain.intermediateDER),
				base64.StdEncoding.EncodeToString(chain.rootDER),
			},
		}
		token := signWithHeader(t, chain, header, payload)
		_, err := verifier.Verify(token)
		wantErrIs(t, err, ErrInvalidX5C)
	})
}

func TestVerify_SignatureTampering(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)
	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())

	t.Run("payload altered after signing", func(t *testing.T) {
		token := signJWS(t, chain, payload, "ES256")
		tampered := samplePayload(EnvironmentProduction, signedAt.UnixMilli())
		tampered["productId"] = "com.evil.fake"
		parts := splitTestCompact(token)
		parts[1] = base64.RawURLEncoding.EncodeToString(mustJSON(t, tampered))
		_, err := verifier.Verify(joinCompact(parts))
		wantErrIs(t, err, ErrInvalidSignature)
	})

	t.Run("signature bytes flipped", func(t *testing.T) {
		token := signJWS(t, chain, payload, "ES256")
		parts := splitTestCompact(token)
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			t.Fatalf("decode signature: %v", err)
		}
		sig[0] ^= 0xff
		parts[2] = base64.RawURLEncoding.EncodeToString(sig)
		_, err = verifier.Verify(joinCompact(parts))
		wantErrIs(t, err, ErrInvalidSignature)
	})
}

func TestVerify_MalformedToken(t *testing.T) {
	verifier := newTestVerifier(t, validChain(t), EnvironmentProduction)

	for _, token := range []string{
		"",
		"abc",
		"abc.def",
		"abc.def.ghi.jkl",
		".abc.def",
		"abc..def",
		"abc.def.",
	} {
		if _, err := verifier.Verify(token); !errors.Is(err, ErrMalformedJWS) {
			t.Fatalf("token %q: want ErrMalformedJWS, got %v", token, err)
		}
	}
}

func TestVerify_MissingSignedDate(t *testing.T) {
	chain := validChain(t)
	verifier := newTestVerifier(t, chain, EnvironmentProduction)
	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())
	delete(payload, "signedDate")

	_, err := verifier.Verify(signJWS(t, chain, payload, "ES256"))
	wantErrIs(t, err, ErrMissingClaim)
}

func TestNew_RejectsInvalidConfig(t *testing.T) {
	chain := validChain(t)

	if _, err := New(Config{RootDER: []byte("not-der"), BundleID: testBundleID, Environment: EnvironmentProduction}); err == nil {
		t.Fatal("bad root DER must fail")
	}
	if _, err := New(Config{RootDER: chain.rootDER, BundleID: "", Environment: EnvironmentProduction}); err == nil {
		t.Fatal("empty bundle id must fail")
	}
	if _, err := New(Config{RootDER: chain.rootDER, BundleID: testBundleID, Environment: ""}); err == nil {
		t.Fatal("empty environment must fail")
	}
}

func TestNew_DefensiveRootCopy(t *testing.T) {
	chain := validChain(t)
	rootDER := append([]byte(nil), chain.rootDER...)
	verifier, err := New(Config{RootDER: rootDER, BundleID: testBundleID, Environment: EnvironmentProduction})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rootDER[0] ^= 0xff

	payload := samplePayload(EnvironmentProduction, signedAt.UnixMilli())
	if _, err := verifier.Verify(signJWS(t, chain, payload, "ES256")); err != nil {
		t.Fatalf("verifier must keep its own root copy: %v", err)
	}
}
