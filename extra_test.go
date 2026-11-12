package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestValidate_MalformedJSONAndClaims(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	source := mapWith(t, bundleFor(t, tdA, s.public()))
	v := testValidator(t, source, audB, now)

	encode := func(header, payload any) string {
		hb, _ := json.Marshal(header)
		pb, _ := json.Marshal(payload)
		input := b64(hb) + "." + b64(pb)
		digest := sha256.Sum256([]byte(input))
		key := s.key.(*ecdsa.PrivateKey)
		r, ss, _ := ecdsa.Sign(rand.Reader, key, digest[:])
		sig := make([]byte, 64)
		r.FillBytes(sig[:32])
		ss.FillBytes(sig[32:])
		return input + "." + b64(sig)
	}

	// Header that is not JSON.
	token := b64([]byte("{not-json")) + "." + b64([]byte("{}")) + "." + b64([]byte("sig"))
	if _, err := v.Validate(token); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("bad header json: err = %v, want ErrMalformedToken", err)
	}

	// Payload that is not JSON.
	token = encode(map[string]any{"alg": AlgES256}, nil)
	parts := strings.Split(token, ".")
	token = parts[0] + "." + b64([]byte("{nope")) + "." + parts[2]
	if _, err := v.Validate(token); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("bad payload json: err = %v, want ErrMalformedToken", err)
	}

	// aud with a non-string element.
	token = encode(map[string]any{"alg": AlgES256},
		map[string]any{"sub": subA, "aud": []int{1}, "exp": now.Unix(), "iat": now.Unix()})
	if _, err := v.Validate(token); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("bad aud type: err = %v, want ErrInvalidClaim", err)
	}

	// exp that is not a number.
	token = encode(map[string]any{"alg": AlgES256},
		map[string]any{"sub": subA, "aud": []string{audB}, "exp": "soon", "iat": now.Unix()})
	if _, err := v.Validate(token); !errors.Is(err, ErrInvalidClaim) {
		t.Fatalf("bad exp type: err = %v, want ErrInvalidClaim", err)
	}

	// Not base64url.
	if _, err := v.Validate("@@@.bbb.ccc"); !errors.Is(err, ErrMalformedToken) {
		t.Fatalf("bad base64: err = %v, want ErrMalformedToken", err)
	}
}

func TestValidate_EmptyBundleAndShortSignature(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)

	emptyBundle, err := NewBundle(tdA)
	if err != nil {
		t.Fatal(err)
	}
	v := testValidator(t, mapWith(t, emptyBundle), audB, now)
	if _, err := v.Validate(s.token(t, baseClaims(subA, []string{audB}, now))); !errors.Is(err, ErrTrustDomainNotSet) {
		t.Fatalf("empty bundle: err = %v, want ErrTrustDomainNotSet", err)
	}

	// A non-P-256 key cannot satisfy ES256.
	p384Key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v = testValidator(t, mapWith(t, bundleFor(t, tdA, &p384Key.PublicKey)), audB, now)
	if _, err := v.Validate(s.token(t, baseClaims(subA, []string{audB}, now))); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("p384 key for ES256: err = %v, want ErrInvalidSignature", err)
	}

	// Truncated signature.
	good := s.token(t, baseClaims(subA, []string{audB}, now))
	parts := strings.Split(good, ".")
	short := parts[0] + "." + parts[1] + "." + b64([]byte{1, 2, 3})
	v = testValidator(t, mapWith(t, bundleFor(t, tdA, s.public())), audB, now)
	if _, err := v.Validate(short); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("short signature: err = %v, want ErrInvalidSignature", err)
	}

	// RSA key smaller than 2048 bits cannot be used.
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner := newSigner(t, AlgRS256)
	v = testValidator(t, mapWith(t, bundleFor(t, tdA, &small.PublicKey)), audB, now)
	if _, err := v.Validate(rsaSigner.token(t, baseClaims(subA, []string{audB}, now))); !errors.Is(err, ErrNoVerificationKey) {
		t.Fatalf("1024-bit RSA: err = %v, want ErrNoVerificationKey", err)
	}
}

func TestParsePublicKeyPEM_Certificate(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if err != nil {
		t.Fatalf("certificate PEM: %v", err)
	}
	if _, ok := parsed.(*ecdsa.PublicKey); !ok {
		t.Fatalf("type = %T", parsed)
	}

	if _, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "EC PUBLIC KEY", Bytes: der})); err == nil {
		t.Fatal("EC PUBLIC KEY block should error with guidance")
	}
	if _, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "UNKNOWN", Bytes: der})); err == nil {
		t.Fatal("unknown PEM block type should error")
	}
	if _, err := ParsePublicKeyPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("bad")})); err == nil {
		t.Fatal("malformed certificate should error")
	}
}

func TestNewBundle_RejectsPathAndBadDomain(t *testing.T) {
	if _, err := NewBundle("a.example/path"); err == nil {
		t.Fatal("bundle domain with path should be rejected")
	}
	if _, err := NewBundle("a.example:443"); err == nil {
		t.Fatal("bundle domain with port should be rejected")
	}
	if _, err := NewBundle("A.Example"); err == nil {
		t.Fatal("upper-case bundle domain should be rejected")
	}
}

func TestBundleSource_ErrorPropagates(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	s := newSigner(t, AlgES256)
	boom := errors.New("bundle backend unavailable")
	source := failingSource{err: boom}
	v := testValidator(t, source, audB, now)

	_, err := v.Validate(s.token(t, baseClaims(subA, []string{audB}, now)))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped %v", err, boom)
	}
}

type failingSource struct{ err error }

func (f failingSource) BundleForTrustDomain(string) (*Bundle, error) {
	return nil, f.err
}
