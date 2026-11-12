package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

const (
	tdA  = "a.example"
	tdB  = "b.example"
	audA = "spiffe://a.example/service-a"
	audB = "spiffe://b.example/service-b"
	subA = "spiffe://a.example/workload/web"
	subB = "spiffe://b.example/workload/web"
)

type signer struct {
	alg string
	key crypto.PrivateKey
}

func newSigner(t *testing.T, alg string) signer {
	t.Helper()
	switch alg {
	case AlgES256:
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return signer{alg: alg, key: key}
	case AlgRS256, AlgPS256:
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		return signer{alg: alg, key: key}
	default:
		t.Fatalf("unsupported test alg %q", alg)
		return signer{}
	}
}

func (s signer) public() crypto.PublicKey {
	switch k := s.key.(type) {
	case *ecdsa.PrivateKey:
		return &k.PublicKey
	case *rsa.PrivateKey:
		return &k.PublicKey
	default:
		return nil
	}
}

func b64(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

func (s signer) sign(t *testing.T, header map[string]any, claims map[string]any) string {
	t.Helper()
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := b64(headerBytes) + "." + b64(payloadBytes)
	digest := sha256.Sum256([]byte(signingInput))

	var signature []byte
	switch s.alg {
	case AlgES256:
		key := s.key.(*ecdsa.PrivateKey)
		r, ss, err := ecdsa.Sign(rand.Reader, key, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		signature = make([]byte, 64)
		r.FillBytes(signature[:32])
		ss.FillBytes(signature[32:])
	case AlgRS256:
		key := s.key.(*rsa.PrivateKey)
		signature, err = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if err != nil {
			t.Fatal(err)
		}
	case AlgPS256:
		key := s.key.(*rsa.PrivateKey)
		signature, err = rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:],
			&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported alg %q", s.alg)
	}
	return signingInput + "." + b64(signature)
}

func (s signer) token(t *testing.T, claims map[string]any) string {
	return s.sign(t, map[string]any{"alg": s.alg, "typ": "JWT"}, claims)
}

func baseClaims(sub string, aud []string, now time.Time) map[string]any {
	return map[string]any{
		"sub": sub,
		"aud": aud,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
}

func testValidator(t *testing.T, source BundleSource, audience string, now time.Time) *Validator {
	t.Helper()
	v, err := NewValidator(source, audience, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return v
}

func mapWith(t *testing.T, bundles ...*Bundle) MapBundleSource {
	t.Helper()
	source := MapBundleSource{}
	for _, b := range bundles {
		source.Add(b)
	}
	return source
}

func bundleFor(t *testing.T, td string, keys ...crypto.PublicKey) *Bundle {
	t.Helper()
	b, err := NewBundle(td, keys...)
	if err != nil {
		t.Fatalf("NewBundle(%q): %v", td, err)
	}
	return b
}

// A correctly minted JWT-SVID validates for every supported algorithm.
func TestValidate_AcceptedAlgorithms(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	for _, alg := range []string{AlgES256, AlgRS256, AlgPS256} {
		t.Run(alg, func(t *testing.T) {
			s := newSigner(t, alg)
			source := mapWith(t, bundleFor(t, tdA, s.public()))
			v := testValidator(t, source, audB, now)
			token := s.token(t, baseClaims(subA, []string{audA, audB}, now))

			claims, err := v.Validate(token)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if claims.Subject != subA {
				t.Fatalf("subject = %q, want %q", claims.Subject, subA)
			}
			if claims.ID.TrustDomain() != tdA {
				t.Fatalf("trust domain = %q, want %q", claims.ID.TrustDomain(), tdA)
			}
			if len(claims.Audience) != 2 {
				t.Fatalf("audience = %v", claims.Audience)
			}
			if !claims.Expiry.Equal(now.Add(time.Hour)) {
				t.Fatalf("expiry = %v", claims.Expiry)
			}
		})
	}
}

func TestValidate_AudienceSingleString(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	claims := baseClaims(subA, nil, now)
	claims["aud"] = audB // single-string aud form
	if _, err := v.Validate(s.token(t, claims)); err != nil {
		t.Fatalf("single-string aud should be accepted: %v", err)
	}
}

// Attacker replays a token minted for service A at service B.
func TestValidate_CrossAudienceReplay(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	token := s.token(t, baseClaims(subA, []string{audA}, now))
	_, err := v.Validate(token)
	if !errors.Is(err, ErrAudienceMismatch) {
		t.Fatalf("err = %v, want ErrAudienceMismatch", err)
	}
}

func TestValidate_EmptyAudienceRejected(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	if _, err := v.Validate(s.token(t, baseClaims(subA, []string{}, now))); !errors.Is(err, ErrEmptyAudience) {
		t.Fatalf("empty aud: err = %v, want ErrEmptyAudience", err)
	}

	missingAud := map[string]any{
		"sub": subA,
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	if _, err := v.Validate(s.token(t, missingAud)); !errors.Is(err, ErrEmptyAudience) {
		t.Fatalf("missing aud: err = %v, want ErrEmptyAudience", err)
	}

	blankEntry := baseClaims(subA, []string{audB, ""}, now)
	if _, err := v.Validate(s.token(t, blankEntry)); !errors.Is(err, ErrEmptyAudience) {
		t.Fatalf("blank aud entry: err = %v, want ErrEmptyAudience", err)
	}
}

// A federated trust domain's key must never verify a token whose sub belongs
// to another trust domain, even when the workload name collides.
func TestValidate_FederatedKeyIsolation(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	signerA := newSigner(t, AlgES256)
	signerB := newSigner(t, AlgES256)
	source := mapWith(t,
		bundleFor(t, tdA, signerA.public()),
		bundleFor(t, tdB, signerB.public()),
	)
	v := testValidator(t, source, audB, now)

	// Domain B key signs a token claiming an a.example identity.
	token := signerB.token(t, baseClaims(subA, []string{audB}, now))
	if _, err := v.Validate(token); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("foreign-key token: err = %v, want ErrInvalidSignature", err)
	}

	// Same workload name under the federated domain is accepted via B's
	// bundle; the trust domain keeps the identities distinct.
	token = signerB.token(t, baseClaims(subB, []string{audB}, now))
	claims, err := v.Validate(token)
	if err != nil {
		t.Fatalf("b.example token should validate against b bundle: %v", err)
	}
	if claims.ID.TrustDomain() != tdB {
		t.Fatalf("trust domain = %q, want %q", claims.ID.TrustDomain(), tdB)
	}

	// Domain C is not trusted at all.
	token = signerB.token(t, baseClaims("spiffe://c.example/workload/web", []string{audB}, now))
	if _, err := v.Validate(token); !errors.Is(err, ErrTrustDomainNotSet) {
		t.Fatalf("untrusted domain: err = %v, want ErrTrustDomainNotSet", err)
	}
}

// The advertised algorithm must match the key type found in the bundle.
func TestValidate_KeyAlgorithmMismatch(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	rsaSigner := newSigner(t, AlgRS256)
	source := mapWith(t, bundleFor(t, tdA, rsaSigner.public()))
	v := testValidator(t, source, audB, now)

	header := map[string]any{"alg": AlgES256, "typ": "JWT"}
	token := rsaSigner.sign(t, header, baseClaims(subA, []string{audB}, now))
	if _, err := v.Validate(token); !errors.Is(err, ErrNoVerificationKey) {
		t.Fatalf("err = %v, want ErrNoVerificationKey", err)
	}
}

func TestValidate_UpperCaseTrustDomain(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	for _, sub := range []string{
		"spiffe://A.Example/workload/web",
		"SPIFFE://a.example/workload/web",
		"spiffe://a.Example/workload/web",
	} {
		token := s.token(t, baseClaims(sub, []string{audB}, now))
		if _, err := v.Validate(token); !errors.Is(err, ErrInvalidSubject) {
			t.Fatalf("sub %q: err = %v, want ErrInvalidSubject", sub, err)
		}
	}
}

func TestValidate_MalformedSubjects(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	malformed := []string{
		"",
		"a.example/workload/web",       // missing scheme
		"https://a.example/workload/x", // wrong scheme
		"spiffe://a.example:443/x",     // port
		"spiffe://user@a.example/x",    // user info
		"spiffe://a.example/x?query=1", // query
		"spiffe://a.example/x#frag",    // fragment
		"spiffe://a.example/x/",        // trailing slash
		"spiffe://a.example/x/./y",     // dot segment
		"spiffe://a.example/x/../y",    // parent segment
		"spiffe://a.example/./x",       // leading dot segment
		"spiffe://a.example/x//y",      // empty segment
		"spiffe:///x",                  // empty trust domain
		"spiffe://a.example/x y",       // whitespace in path
		"spiffe://-a.example/x",        // leading hyphen label
		"spiffe://a..example/x",        // empty label
	}
	for _, sub := range malformed {
		t.Run(sub, func(t *testing.T) {
			token := s.token(t, baseClaims(sub, []string{audB}, now))
			if _, err := v.Validate(token); !errors.Is(err, ErrInvalidSubject) {
				t.Fatalf("sub %q: err = %v, want ErrInvalidSubject", sub, err)
			}
		})
	}

	// An ID without a workload path is still legal per SPIFFE.
	token := s.token(t, baseClaims("spiffe://a.example", []string{audB}, now))
	claims, err := v.Validate(token)
	if err != nil {
		t.Fatalf("no-path SPIFFE ID should be valid: %v", err)
	}
	if claims.ID.Path() != "" {
		t.Fatalf("path = %q, want empty", claims.ID.Path())
	}
}

func TestValidate_DisallowedAlgorithms(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	for _, alg := range []string{"none", "HS256", "ES384", "ES512", "RS512", "EdDSA", ""} {
		token := s.sign(t, map[string]any{"alg": alg, "typ": "JWT"},
			baseClaims(subA, []string{audB}, now))
		if _, err := v.Validate(token); !errors.Is(err, ErrUnsupportedAlg) {
			t.Fatalf("alg %q: err = %v, want ErrUnsupportedAlg", alg, err)
		}
	}
}

func TestValidate_TimeClaims(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	t.Run("exp required", func(t *testing.T) {
		claims := map[string]any{"sub": subA, "aud": []string{audB}, "iat": now.Unix()}
		if _, err := v.Validate(s.token(t, claims)); !errors.Is(err, ErrMissingExpiry) {
			t.Fatalf("err = %v, want ErrMissingExpiry", err)
		}
	})

	t.Run("iat required", func(t *testing.T) {
		claims := map[string]any{"sub": subA, "aud": []string{audB}, "exp": now.Add(time.Hour).Unix()}
		if _, err := v.Validate(s.token(t, claims)); !errors.Is(err, ErrMissingIssuedAt) {
			t.Fatalf("err = %v, want ErrMissingIssuedAt", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		claims := baseClaims(subA, []string{audB}, now)
		claims["exp"] = now.Add(-time.Second).Unix()
		if _, err := v.Validate(s.token(t, claims)); !errors.Is(err, ErrTokenExpired) {
			t.Fatalf("err = %v, want ErrTokenExpired", err)
		}
	})

	t.Run("iat within leeway", func(t *testing.T) {
		claims := baseClaims(subA, []string{audB}, now)
		claims["iat"] = now.Add(FutureIATLeeway).Unix()
		if _, err := v.Validate(s.token(t, claims)); err != nil {
			t.Fatalf("iat exactly +30s must be accepted: %v", err)
		}
	})

	t.Run("iat beyond leeway", func(t *testing.T) {
		claims := baseClaims(subA, []string{audB}, now)
		claims["iat"] = now.Add(FutureIATLeeway + time.Second).Unix()
		if _, err := v.Validate(s.token(t, claims)); !errors.Is(err, ErrFutureIssuedAt) {
			t.Fatalf("err = %v, want ErrFutureIssuedAt", err)
		}
	})

	t.Run("nbf in future", func(t *testing.T) {
		claims := baseClaims(subA, []string{audB}, now)
		claims["nbf"] = now.Add(time.Minute).Unix()
		if _, err := v.Validate(s.token(t, claims)); !errors.Is(err, ErrInvalidClaim) {
			t.Fatalf("err = %v, want ErrInvalidClaim", err)
		}
	})
}

func TestValidate_StructureAndSignature(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	for _, token := range []string{
		"",
		"a.b",
		"a.b.c.d", // JWE-style or corrupted compact token
		"abc.def.ghi",
		"aaaaa.bbbbb.",
	} {
		if _, err := v.Validate(token); !errors.Is(err, ErrMalformedToken) {
			t.Fatalf("token %q: err = %v, want ErrMalformedToken", token, err)
		}
	}

	tamperClaims := baseClaims(subA, []string{audB}, now)
	tamperClaims["jti"] = "original-jti"
	good := s.token(t, tamperClaims)
	parts := strings.Split(good, ".")

	// Tamper with the payload; any claim change invalidates the signature.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(payload), "original-jti", "forged-jti--", 1)
	token := parts[0] + "." + b64([]byte(tampered)) + "." + parts[2]
	if _, err := v.Validate(token); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered payload: err = %v, want ErrInvalidSignature", err)
	}

	// Tamper with the signature bytes.
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sig[0] ^= 0xff
	token = parts[0] + "." + parts[1] + "." + b64(sig)
	if _, err := v.Validate(token); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered signature: err = %v, want ErrInvalidSignature", err)
	}

	// Unknown key in bundle position: signing key removed, replacement cannot verify.
	other := newSigner(t, AlgES256)
	vNoKey := testValidator(t, mapWith(t, bundleFor(t, tdA, other.public())), audB, now)
	if _, err := vNoKey.Validate(good); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("foreign key: err = %v, want ErrInvalidSignature", err)
	}
}

func TestParseSPIFFEID_Table(t *testing.T) {
	type want struct {
		td   string
		path string
	}
	valid := map[string]want{
		"spiffe://example.com":              {"example.com", ""},
		"spiffe://example.org/path/service": {"example.org", "/path/service"},
		"spiffe://my-ns.domain/a/b_c.d-e~f": {"my-ns.domain", "/a/b_c.d-e~f"},
		"spiffe://td/a%20b":                 {"td", "/a%20b"},
	}
	for raw, expected := range valid {
		id, err := ParseSPIFFEID(raw)
		if err != nil {
			t.Fatalf("ParseSPIFFEID(%q): %v", raw, err)
		}
		if id.TrustDomain() != expected.td || id.Path() != expected.path {
			t.Fatalf("ParseSPIFFEID(%q) = %q,%q; want %q,%q",
				raw, id.TrustDomain(), id.Path(), expected.td, expected.path)
		}
		if id.String() != raw {
			t.Fatalf("round trip = %q, want %q", id.String(), raw)
		}
	}

	invalid := []string{
		"spiffe://EXAMPLE.com/x",
		"spiffe://example.com:8080/x",
		"spiffe://u:p@example.com/x",
		"spiffe://example.com/x?a=b",
		"spiffe://example.com/x#f",
		"spiffe://example.com/x/",
		"spiffe://example.com/./x",
		"spiffe://example.com/x/../y",
		"spiffe://example.com/x//y",
		"spiffe://example.com/x%2fy", // encoded slash
		"spiffe://exa mple.com/x",
		"spiffe://example.com/x|y",
		"spiffe://",
		"spiffe:/example.com/x",
		"spiffe//example.com/x",
		"://example.com/x",
	}
	for _, raw := range invalid {
		if _, err := ParseSPIFFEID(raw); err == nil {
			t.Fatalf("ParseSPIFFEID(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestParsePublicKeyPEM(t *testing.T) {
	ecSigner := newSigner(t, AlgES256)
	ecDER, err := x509.MarshalPKIXPublicKey(ecSigner.public())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: ecDER}))
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM(ec): %v", err)
	}
	if _, ok := parsed.(*ecdsa.PublicKey); !ok {
		t.Fatalf("parsed EC key type = %T", parsed)
	}

	rsaSigner := newSigner(t, AlgRS256)
	rsaKey := rsaSigner.key.(*rsa.PrivateKey)
	rsaDER := x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey)
	parsed, err = ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: rsaDER}))
	if err != nil {
		t.Fatalf("ParsePublicKeyPEM(rsa): %v", err)
	}
	if _, ok := parsed.(*rsa.PublicKey); !ok {
		t.Fatalf("parsed RSA key type = %T", parsed)
	}

	if _, err := ParsePublicKeyPEM([]byte("not a pem")); err == nil {
		t.Fatal("expected error for non-PEM input")
	}
}

func TestNewValidator_RejectsBadConfig(t *testing.T) {
	if _, err := NewValidator(nil, audB); err == nil {
		t.Fatal("nil bundle source should be rejected")
	}
	if _, err := NewValidator(MapBundleSource{}, ""); err == nil {
		t.Fatal("empty audience should be rejected")
	}
	if _, err := NewBundle("Bad.Example"); err == nil {
		t.Fatal("upper-case bundle trust domain should be rejected")
	}
	if _, err := NewBundle(tdA, big.NewInt(1)); err == nil {
		t.Fatal("unsupported key type should be rejected")
	}
}
