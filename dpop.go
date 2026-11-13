package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DPoPProof is a parsed DPoP proof JWT. Its signature and request binding are
// checked by the middleware before it is returned to callers.
type DPoPProof struct {
	Raw        string
	Header     map[string]any
	Claims     map[string]any
	JWK        map[string]any
	Thumbprint string
	Signature  []byte

	signingInput string
}

// ParseDPoPProof parses a compact DPoP proof JWT without verifying it.
func ParseDPoPProof(raw string) (*DPoPProof, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty DPoP proof")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("DPoP proof is not a compact JWT")
	}

	headerBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode DPoP proof header: %w", err)
	}
	claimBytes, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode DPoP proof claims: %w", err)
	}
	signature, err := decodeBase64URL(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode DPoP proof signature: %w", err)
	}

	header := make(map[string]any)
	if err := decodeJSONObject(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("decode DPoP proof header JSON: %w", err)
	}
	claims := make(map[string]any)
	if err := decodeJSONObject(claimBytes, &claims); err != nil {
		return nil, fmt.Errorf("decode DPoP proof claims JSON: %w", err)
	}
	jwk, _ := header["jwk"].(map[string]any)
	if jwk == nil {
		return nil, errors.New("DPoP proof header has no jwk")
	}
	alg, _ := header["alg"].(string)
	if alg == "" {
		return nil, errors.New("DPoP proof has no alg header")
	}
	if strings.EqualFold(alg, "none") {
		return nil, errors.New("DPoP proof uses the none algorithm")
	}

	return &DPoPProof{
		Raw:          raw,
		Header:       header,
		Claims:       claims,
		JWK:          jwk,
		Signature:    signature,
		signingInput: parts[0] + "." + parts[1],
	}, nil
}

// Algorithm returns the proof's JWT alg header value.
func (p *DPoPProof) Algorithm() string {
	if p == nil {
		return ""
	}
	alg, _ := p.Header["alg"].(string)
	return alg
}

// JWKThumbprint computes the RFC 7638 SHA-256 thumbprint of a public JWK. The
// result is the value that appears in an access token's cnf.jkt member.
func JWKThumbprint(jwk map[string]any) (string, error) {
	if jwk == nil {
		return "", errors.New("nil JWK")
	}
	kty, _ := jwk["kty"].(string)

	var required map[string]string
	switch kty {
	case "EC":
		crv, ok := jwkString(jwk, "crv")
		if !ok {
			return "", errors.New("EC JWK has no crv")
		}
		x, ok := jwkString(jwk, "x")
		if !ok {
			return "", errors.New("EC JWK has no x")
		}
		y, ok := jwkString(jwk, "y")
		if !ok {
			return "", errors.New("EC JWK has no y")
		}
		required = map[string]string{"crv": crv, "kty": "EC", "x": x, "y": y}
	case "RSA":
		e, ok := jwkString(jwk, "e")
		if !ok {
			return "", errors.New("RSA JWK has no e")
		}
		n, ok := jwkString(jwk, "n")
		if !ok {
			return "", errors.New("RSA JWK has no n")
		}
		required = map[string]string{"e": e, "kty": "RSA", "n": n}
	case "OKP":
		crv, ok := jwkString(jwk, "crv")
		if !ok {
			return "", errors.New("OKP JWK has no crv")
		}
		x, ok := jwkString(jwk, "x")
		if !ok {
			return "", errors.New("OKP JWK has no x")
		}
		required = map[string]string{"crv": crv, "kty": "OKP", "x": x}
	default:
		return "", fmt.Errorf("unsupported JWK key type %q", kty)
	}

	canonical, err := json.Marshal(required)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return encodeBase64URL(sum[:]), nil
}

func jwkString(jwk map[string]any, name string) (string, bool) {
	value, ok := jwk[name].(string)
	if !ok || value == "" {
		return "", false
	}
	return value, true
}

func jwkPublicKey(jwk map[string]any) (crypto.PublicKey, error) {
	if jwk == nil {
		return nil, errors.New("nil JWK")
	}
	for _, privateMember := range []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"} {
		if _, ok := jwk[privateMember]; ok {
			return nil, errors.New("DPoP jwk must contain only public key material")
		}
	}

	kty, _ := jwk["kty"].(string)
	switch kty {
	case "EC":
		crv, ok := jwkString(jwk, "crv")
		if !ok {
			return nil, errors.New("EC JWK has no crv")
		}
		var curve elliptic.Curve
		switch crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported EC curve %q", crv)
		}
		x, ok := jwkString(jwk, "x")
		if !ok {
			return nil, errors.New("EC JWK has no x")
		}
		y, ok := jwkString(jwk, "y")
		if !ok {
			return nil, errors.New("EC JWK has no y")
		}
		xb, err := decodeBase64URL(x)
		if err != nil {
			return nil, fmt.Errorf("decode EC JWK x: %w", err)
		}
		yb, err := decodeBase64URL(y)
		if err != nil {
			return nil, fmt.Errorf("decode EC JWK y: %w", err)
		}
		pub := &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(xb),
			Y:     new(big.Int).SetBytes(yb),
		}
		if !curve.IsOnCurve(pub.X, pub.Y) {
			return nil, errors.New("EC JWK point is not on the curve")
		}
		return pub, nil

	case "RSA":
		n, ok := jwkString(jwk, "n")
		if !ok {
			return nil, errors.New("RSA JWK has no n")
		}
		e, ok := jwkString(jwk, "e")
		if !ok {
			return nil, errors.New("RSA JWK has no e")
		}
		nb, err := decodeBase64URL(n)
		if err != nil {
			return nil, fmt.Errorf("decode RSA JWK n: %w", err)
		}
		eb, err := decodeBase64URL(e)
		if err != nil {
			return nil, fmt.Errorf("decode RSA JWK e: %w", err)
		}
		modulus := new(big.Int).SetBytes(nb)
		exponent := new(big.Int).SetBytes(eb)
		if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > 1<<31-1 || exponent.Bit(0) == 0 {
			return nil, errors.New("RSA JWK has an invalid exponent")
		}
		if modulus.BitLen() < 2048 {
			return nil, errors.New("RSA JWK modulus is smaller than 2048 bits")
		}
		return &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}, nil

	case "OKP":
		crv, ok := jwkString(jwk, "crv")
		if !ok {
			return nil, errors.New("OKP JWK has no crv")
		}
		if crv != "Ed25519" {
			return nil, fmt.Errorf("unsupported OKP curve %q", crv)
		}
		x, ok := jwkString(jwk, "x")
		if !ok {
			return nil, errors.New("OKP JWK has no x")
		}
		xb, err := decodeBase64URL(x)
		if err != nil {
			return nil, fmt.Errorf("decode OKP JWK x: %w", err)
		}
		if len(xb) != ed25519.PublicKeySize {
			return nil, errors.New("Ed25519 JWK has an invalid public key length")
		}
		return ed25519.PublicKey(xb), nil

	default:
		return nil, fmt.Errorf("unsupported JWK key type %q", kty)
	}
}

func verifyAsymmetricJWT(alg string, key crypto.PublicKey, input string, signature []byte) error {
	switch {
	case strings.HasPrefix(alg, "RS"), strings.HasPrefix(alg, "PS"):
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("DPoP algorithm %s requires an RSA public key", alg)
		}
		h, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		digest := hashInput(h, input)
		if strings.HasPrefix(alg, "PS") {
			if err := rsa.VerifyPSS(pub, h, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}); err != nil {
				return errors.New("DPoP proof signature is invalid")
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(pub, h, digest, signature); err != nil {
			return errors.New("DPoP proof signature is invalid")
		}
		return nil

	case strings.HasPrefix(alg, "ES"):
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("DPoP algorithm %s requires an ECDSA public key", alg)
		}
		if err := checkECDSAAlgorithmKey(alg, pub); err != nil {
			return err
		}
		h, err := hashForAlgorithm(alg)
		if err != nil {
			return err
		}
		if !verifyECDSA(pub, h, input, signature) {
			return errors.New("DPoP proof signature is invalid")
		}
		return nil

	case alg == "EdDSA":
		pub, ok := key.(ed25519.PublicKey)
		if !ok {
			return errors.New("DPoP algorithm EdDSA requires an Ed25519 public key")
		}
		if !ed25519.Verify(pub, []byte(input), signature) {
			return errors.New("DPoP proof signature is invalid")
		}
		return nil

	default:
		return fmt.Errorf("unsupported DPoP algorithm %q", alg)
	}
}

// VerifyDPoPProof validates a DPoP proof for the request and access token and
// checks that its public key matches expectedThumbprint (the cnf.jkt value).
func VerifyDPoPProof(r *http.Request, accessToken, expectedThumbprint string, cfg Config) (*DPoPProof, error) {
	now := time.Now().UTC()
	if cfg.Now != nil {
		now = cfg.Now().UTC()
	}
	return verifyDPoPProof(r, accessToken, expectedThumbprint, cfg, now)
}

func verifyDPoPProof(r *http.Request, accessToken, expectedThumbprint string, cfg Config, now time.Time) (*DPoPProof, error) {
	if r == nil {
		return nil, errors.New("nil HTTP request")
	}
	raw := strings.TrimSpace(r.Header.Get("DPoP"))
	if raw == "" {
		return nil, errors.New("a DPoP proof is required for this access token")
	}
	proof, err := ParseDPoPProof(raw)
	if err != nil {
		return nil, err
	}

	if typ, ok := proof.Header["typ"].(string); ok && typ != "" && !strings.EqualFold(typ, "dpop+jwt") {
		return nil, errors.New("DPoP proof has an invalid typ header")
	}

	alg := proof.Algorithm()
	if strings.HasPrefix(strings.ToUpper(alg), "HS") {
		return nil, errors.New("DPoP proof must use an asymmetric signature")
	}
	pub, err := jwkPublicKey(proof.JWK)
	if err != nil {
		return nil, err
	}
	thumbprint, err := JWKThumbprint(proof.JWK)
	if err != nil {
		return nil, err
	}
	if expectedThumbprint == "" {
		return nil, errors.New("access token has no DPoP key thumbprint")
	}
	if subtle.ConstantTimeCompare([]byte(thumbprint), []byte(expectedThumbprint)) != 1 {
		return nil, errors.New("DPoP proof key does not match the access token cnf.jkt")
	}
	proof.Thumbprint = thumbprint

	if err := verifyAsymmetricJWT(alg, pub, proof.signingInput, proof.Signature); err != nil {
		return nil, err
	}

	htm, _ := proof.Claims["htm"].(string)
	if htm == "" || htm != r.Method {
		return nil, errors.New("DPoP proof htm does not match the request method")
	}
	htu, _ := proof.Claims["htu"].(string)
	if !htuMatches(htu, r) {
		return nil, errors.New("DPoP proof htu does not match the request URL")
	}

	if ath, ok := proof.Claims["ath"].(string); ok {
		want := sha256Base64URL([]byte(accessToken))
		if subtle.ConstantTimeCompare([]byte(ath), []byte(want)) != 1 {
			return nil, errors.New("DPoP proof ath does not match the access token")
		}
	} else if cfg.RequireATH {
		return nil, errors.New("DPoP proof is missing the ath claim")
	}

	leeway := cfg.DPoPLeeway
	if leeway <= 0 {
		leeway = time.Minute
	}
	maxAge := cfg.DPoPMaxAge
	if maxAge <= 0 {
		maxAge = 5 * time.Minute
	}
	if issuedAt, ok, err := numericClaim(proof.Claims, "iat"); err != nil {
		return nil, fmt.Errorf("DPoP proof iat: %w", err)
	} else if ok {
		if issuedAt.After(now.Add(leeway)) {
			return nil, errors.New("DPoP proof was issued in the future")
		}
		if issuedAt.Before(now.Add(-maxAge - leeway)) {
			return nil, errors.New("DPoP proof is too old")
		}
	}

	jti, _ := proof.Claims["jti"].(string)
	if jti == "" {
		jti = sha256Base64URL([]byte(proof.Raw))
	}
	if cfg.DPoPReplayCache != nil {
		replayKey := thumbprint + "\x00" + jti
		if !cfg.DPoPReplayCache.Consume(replayKey, now.Add(maxAge+leeway)) {
			return nil, errors.New("DPoP proof was already used")
		}
	}

	return proof, nil
}

func htuMatches(expected string, r *http.Request) bool {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return false
	}
	parsed, err := url.Parse(expected)
	if err != nil {
		return false
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""

	requestPath := r.URL.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	expectedPath := parsed.EscapedPath()
	if expectedPath == "" {
		expectedPath = "/"
	}

	if parsed.Scheme == "" && parsed.Host == "" {
		return expectedPath == requestPath
	}

	requestScheme, requestHost := requestSchemeAndHost(r)
	if !strings.EqualFold(parsed.Scheme, requestScheme) {
		return false
	}
	expectedHost, expectedPort := splitAuthority(parsed.Host, parsed.Scheme)
	actualHost, actualPort := splitAuthority(requestHost, requestScheme)
	return strings.EqualFold(expectedHost, actualHost) && expectedPort == actualPort && expectedPath == requestPath
}

func requestSchemeAndHost(r *http.Request) (string, string) {
	scheme := r.URL.Scheme
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := r.URL.Host
	if host == "" {
		host = r.Host
	}
	return scheme, host
}

func splitAuthority(authority, scheme string) (string, string) {
	authority = strings.TrimSpace(authority)
	if authority == "" {
		return "", defaultPort(scheme)
	}
	u, err := url.Parse("//" + authority)
	if err != nil || u.Hostname() == "" {
		return "", defaultPort(scheme)
	}
	port := u.Port()
	if port == "" {
		port = defaultPort(scheme)
	}
	return strings.ToLower(u.Hostname()), port
}

func defaultPort(scheme string) string {
	if strings.EqualFold(scheme, "https") {
		return "443"
	}
	return "80"
}

// ReplayCache records DPoP proof identifiers. Consume must atomically record
// key and report whether it was previously unseen.
type ReplayCache interface {
	Consume(key string, expires time.Time) bool
}

// NoReplayCache disables DPoP replay detection.
type NoReplayCache struct{}

// Consume always reports the key as fresh.
func (NoReplayCache) Consume(string, time.Time) bool { return true }

// MemoryReplayCache is a concurrency-safe in-memory ReplayCache. Its zero value
// is ready for use.
type MemoryReplayCache struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

// NewMemoryReplayCache returns an empty in-memory replay cache.
func NewMemoryReplayCache() *MemoryReplayCache {
	return &MemoryReplayCache{entries: make(map[string]time.Time)}
}

// Consume records key until expires and reports whether it was unseen.
func (c *MemoryReplayCache) Consume(key string, expires time.Time) bool {
	if c == nil {
		return true
	}
	now := time.Now()
	if expires.IsZero() {
		expires = now.Add(5 * time.Minute)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]time.Time)
	}
	if expiry, ok := c.entries[key]; ok && now.Before(expiry) {
		return false
	}
	c.entries[key] = expires

	if len(c.entries) > 4096 {
		for entry, expiry := range c.entries {
			if !now.Before(expiry) {
				delete(c.entries, entry)
			}
		}
	}
	return true
}
