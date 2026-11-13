package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Token is a parsed JWT access token. Header and Claims contain the decoded
// JSON objects. The signature is not verified by ParseToken.
type Token struct {
	Raw       string
	Header    map[string]any
	Claims    map[string]any
	Signature []byte

	signingInput string
}

// ParseToken parses a compact JWS-encoded JWT without verifying its signature.
// Callers must use Authenticator.Authenticate or the middleware to verify a
// token before trusting any of its fields.
func ParseToken(raw string) (*Token, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty access token")
	}

	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("access token is not a compact JWT")
	}

	headerBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode token header: %w", err)
	}
	claimBytes, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode token claims: %w", err)
	}
	signature, err := decodeBase64URL(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode token signature: %w", err)
	}

	header := make(map[string]any)
	if err := decodeJSONObject(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("decode token header JSON: %w", err)
	}
	claims := make(map[string]any)
	if err := decodeJSONObject(claimBytes, &claims); err != nil {
		return nil, fmt.Errorf("decode token claims JSON: %w", err)
	}

	alg, _ := header["alg"].(string)
	if alg == "" {
		return nil, errors.New("access token has no alg header")
	}
	if strings.EqualFold(alg, "none") {
		return nil, errors.New("access token uses the none algorithm")
	}

	return &Token{
		Raw:          raw,
		Header:       header,
		Claims:       claims,
		Signature:    signature,
		signingInput: parts[0] + "." + parts[1],
	}, nil
}

// Algorithm returns the JWT alg header value.
func (t *Token) Algorithm() string {
	if t == nil {
		return ""
	}
	alg, _ := t.Header["alg"].(string)
	return alg
}

// KeyID returns the JWT kid header value.
func (t *Token) KeyID() string {
	if t == nil {
		return ""
	}
	kid, _ := t.Header["kid"].(string)
	return kid
}

// Subject returns the sub claim when it is a string.
func (t *Token) Subject() string {
	if t == nil {
		return ""
	}
	sub, _ := t.Claims["sub"].(string)
	return sub
}

// Confirmation returns the token's cnf claim. present reports whether the cnf
// member exists, even when it is null or empty. A present confirmation must
// never be treated as an ordinary bearer token.
func (t *Token) Confirmation() (confirmation Confirmation, present bool, err error) {
	if t == nil {
		return Confirmation{}, false, nil
	}
	raw, present := t.Claims["cnf"]
	if !present {
		return Confirmation{}, false, nil
	}
	return parseConfirmation(raw)
}

// Confirmation is the proof-of-possession constraint carried by cnf.
type Confirmation struct {
	// JWKThumbprint is the RFC 7638 SHA-256 thumbprint in cnf.jkt.
	JWKThumbprint string
	// CertificateThumbprintSHA256 is cnf["x5t#S256"].
	CertificateThumbprintSHA256 string
	// CertificateThumbprintSHA1 is the older cnf.x5t value.
	CertificateThumbprintSHA1 string
	Raw                       map[string]any
}

// RequiresProof reports whether cnf actually constrains the token to a key or
// certificate. A cnf member without a supported constraint is rejected by the
// middleware rather than downgraded to bearer.
func (c Confirmation) RequiresProof() bool {
	return c.JWKThumbprint != "" || c.CertificateThumbprintSHA256 != "" || c.CertificateThumbprintSHA1 != ""
}

func parseConfirmation(raw any) (Confirmation, bool, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return Confirmation{}, true, errors.New("cnf claim must be a JSON object")
	}
	c := Confirmation{Raw: obj}
	c.JWKThumbprint, _ = obj["jkt"].(string)
	c.CertificateThumbprintSHA256, _ = obj["x5t#S256"].(string)
	c.CertificateThumbprintSHA1, _ = obj["x5t"].(string)
	return c, true, nil
}

func decodeBase64URL(s string) ([]byte, error) {
	// JWT uses unpadded base64url. Accept padded input as well so callers that
	// pass values from other JOSE tooling are not rejected unnecessarily.
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

func encodeBase64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeJSONObject(data []byte, dst *map[string]any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(dst)
}

func algorithmAllowed(cfg Config, alg string) bool {
	if len(cfg.Algorithms) == 0 {
		return true
	}
	for _, allowed := range cfg.Algorithms {
		if strings.EqualFold(allowed, alg) {
			return true
		}
	}
	return false
}

func hashForAlgorithm(alg string) (crypto.Hash, error) {
	switch alg {
	case "HS256", "RS256", "PS256", "ES256":
		return crypto.SHA256, nil
	case "HS384", "RS384", "PS384", "ES384":
		return crypto.SHA384, nil
	case "HS512", "RS512", "PS512", "ES512":
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("unsupported JWT algorithm %q", alg)
	}
}

func verifyTokenSignature(t *Token, key any) error {
	alg := t.Algorithm()
	normalized, err := normalizeVerificationKey(key)
	if err != nil {
		return err
	}

	switch {
	case strings.HasPrefix(alg, "HS"):
		secret, ok := normalized.([]byte)
		if !ok {
			return fmt.Errorf("JWT algorithm %s requires an HMAC secret", alg)
		}
		h, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		mac := hmac.New(h.New, secret)
		_, _ = mac.Write([]byte(t.signingInput))
		if !hmac.Equal(mac.Sum(nil), t.Signature) {
			return errors.New("access token signature is invalid")
		}
		return nil

	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "PS"):
		pub, ok := normalized.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("JWT algorithm %s requires an RSA public key", alg)
		}
		h, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		digest := hashInput(h, t.signingInput)
		if strings.HasPrefix(alg, "PS") {
			if err := rsa.VerifyPSS(pub, h, digest, t.Signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}); err != nil {
				return errors.New("access token signature is invalid")
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(pub, h, digest, t.Signature); err != nil {
			return errors.New("access token signature is invalid")
		}
		return nil

	case strings.HasPrefix(alg, "ES"):
		pub, ok := normalized.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("JWT algorithm %s requires an ECDSA public key", alg)
		}
		if err := checkECDSAAlgorithmKey(alg, pub); err != nil {
			return err
		}
		h, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		if !verifyECDSA(pub, h, t.signingInput, t.Signature) {
			return errors.New("access token signature is invalid")
		}
		return nil

	case alg == "EdDSA":
		pub, ok := normalized.(ed25519.PublicKey)
		if !ok {
			return errors.New("JWT algorithm EdDSA requires an Ed25519 public key")
		}
		if !ed25519.Verify(pub, []byte(t.signingInput), t.Signature) {
			return errors.New("access token signature is invalid")
		}
		return nil

	default:
		return fmt.Errorf("unsupported JWT algorithm %q", alg)
	}
}

func hashInput(h crypto.Hash, input string) []byte {
	digest := h.New()
	_, _ = digest.Write([]byte(input))
	return digest.Sum(nil)
}

func verifyECDSA(pub *ecdsa.PublicKey, h crypto.Hash, input string, signature []byte) bool {
	if pub == nil || pub.Curve == nil || pub.Curve.Params() == nil {
		return false
	}
	size := (pub.Curve.Params().BitSize + 7) / 8
	if len(signature) != 2*size {
		return false
	}
	r := new(big.Int).SetBytes(signature[:size])
	s := new(big.Int).SetBytes(signature[size:])
	digest := hashInput(h, input)
	return ecdsa.Verify(pub, digest, r, s)
}

func checkECDSAAlgorithmKey(alg string, pub *ecdsa.PublicKey) error {
	wantBits := 0
	switch alg {
	case "ES256":
		wantBits = 256
	case "ES384":
		wantBits = 384
	case "ES512":
		wantBits = 521
	}
	if wantBits != 0 && pub.Curve.Params().BitSize != wantBits {
		return fmt.Errorf("JWT algorithm %s does not match the ECDSA key curve", alg)
	}
	return nil
}

func normalizeVerificationKey(key any) (any, error) {
	switch k := key.(type) {
	case nil:
		return nil, errors.New("nil verification key")
	case []byte:
		if bytes.Contains(k, []byte("-----BEGIN")) {
			return parsePEMPublicKey(k)
		}
		if len(k) == 0 {
			return nil, errors.New("empty verification key")
		}
		return k, nil
	case string:
		if strings.Contains(k, "-----BEGIN") {
			return parsePEMPublicKey([]byte(k))
		}
		if k == "" {
			return nil, errors.New("empty verification key")
		}
		return []byte(k), nil
	case *rsa.PublicKey:
		return k, nil
	case *rsa.PrivateKey:
		return &k.PublicKey, nil
	case *ecdsa.PublicKey:
		return k, nil
	case *ecdsa.PrivateKey:
		return &k.PublicKey, nil
	case ed25519.PublicKey:
		return k, nil
	case ed25519.PrivateKey:
		return k.Public(), nil
	case *x509.Certificate:
		return k.PublicKey, nil
	case map[string]any:
		return jwkPublicKey(k)
	case crypto.Signer:
		return k.Public(), nil
	default:
		return nil, fmt.Errorf("unsupported verification key type %T", key)
	}
}

func parsePEMPublicKey(data []byte) (any, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid PEM verification key")
	}
	switch block.Type {
	case "PUBLIC KEY":
		return x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	case "CERTIFICATE":
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		return cert.PublicKey, nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return publicPartOfPrivateKey(key)
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return &key.PublicKey, nil
	case "EC PRIVATE KEY":
		key, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		return &key.PublicKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block %q", block.Type)
	}
}

func publicPartOfPrivateKey(key any) (any, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey, nil
	case *ecdsa.PrivateKey:
		return &k.PublicKey, nil
	case ed25519.PrivateKey:
		return k.Public(), nil
	default:
		return nil, fmt.Errorf("unsupported private key type %T", key)
	}
}

func validateTokenClaims(t *Token, cfg Config, now time.Time) error {
	skew := cfg.ClockSkew
	if skew < 0 {
		skew = 0
	}

	if exp, ok, err := numericClaim(t.Claims, "exp"); err != nil {
		return err
	} else if ok && now.After(exp.Add(skew)) {
		return errors.New("access token has expired")
	}

	if nbf, ok, err := numericClaim(t.Claims, "nbf"); err != nil {
		return err
	} else if ok && now.Before(nbf.Add(-skew)) {
		return errors.New("access token is not valid yet")
	}

	if iat, ok, err := numericClaim(t.Claims, "iat"); err != nil {
		return err
	} else if ok && iat.After(now.Add(skew)) {
		return errors.New("access token was issued in the future")
	}

	if cfg.Issuer != "" {
		iss, _ := t.Claims["iss"].(string)
		if iss != cfg.Issuer {
			return errors.New("access token issuer is not allowed")
		}
	}
	if cfg.Audience != "" {
		if !audienceMatches(t.Claims["aud"], cfg.Audience) {
			return errors.New("access token audience is not allowed")
		}
	}
	return nil
}

func numericClaim(claims map[string]any, name string) (time.Time, bool, error) {
	raw, ok := claims[name]
	if !ok {
		return time.Time{}, false, nil
	}
	var seconds float64
	switch v := raw.(type) {
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return time.Time{}, false, fmt.Errorf("claim %q is not a number", name)
		}
		seconds = f
	case float64:
		seconds = v
	case int64:
		seconds = float64(v)
	case int:
		seconds = float64(v)
	default:
		return time.Time{}, false, fmt.Errorf("claim %q is not a number", name)
	}
	sec := int64(seconds)
	nsec := int64((seconds - float64(sec)) * 1e9)
	return time.Unix(sec, nsec).UTC(), true, nil
}

func audienceMatches(raw any, want string) bool {
	switch v := raw.(type) {
	case string:
		return v == want
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == want {
				return true
			}
		}
	}
	return false
}

func sha256Base64URL(data []byte) string {
	sum := sha256.Sum256(data)
	return encodeBase64URL(sum[:])
}
