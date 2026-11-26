package spiffefed

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
)

type jwtVerificationKey struct {
	publicKey  crypto.PublicKey
	algorithms map[string]struct{}
}

// JWKS is the verification-key set belonging to exactly one trust domain.
type JWKS struct {
	domain string
	keys   map[string]jwtVerificationKey
}

// NewJWKS derives the allowable JOSE algorithms from each public key.
func NewJWKS(trustDomain string, keys map[string]crypto.PublicKey) (*JWKS, error) {
	if err := ValidateTrustDomain(trustDomain); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJWKS, err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: JWKS for %q has no keys", ErrInvalidJWKS, trustDomain)
	}
	set := &JWKS{domain: trustDomain, keys: make(map[string]jwtVerificationKey, len(keys))}
	for kid, publicKey := range keys {
		if err := validateKeyID(kid); err != nil {
			return nil, err
		}
		algorithms, err := algorithmsForKey(publicKey)
		if err != nil {
			return nil, fmt.Errorf("%w: JWKS %q kid %q: %w", ErrInvalidJWKS, trustDomain, kid, err)
		}
		set.keys[kid] = jwtVerificationKey{publicKey: publicKey, algorithms: algorithms}
	}
	return set, nil
}

// ParseJWKS parses an RFC 7517 JWK set and binds every key to trustDomain.
func ParseJWKS(trustDomain string, data []byte) (*JWKS, error) {
	if err := ValidateTrustDomain(trustDomain); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidJWKS, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var doc struct {
		Keys []struct {
			Kty    string   `json:"kty"`
			Kid    string   `json:"kid"`
			Use    string   `json:"use"`
			KeyOps []string `json:"key_ops"`
			Alg    string   `json:"alg"`
			Crv    string   `json:"crv"`
			X      string   `json:"x"`
			Y      string   `json:"y"`
			N      string   `json:"n"`
			E      string   `json:"e"`
			D      string   `json:"d"`
		} `json:"keys"`
	}
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrInvalidJWKS, err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJWKS, err)
	}
	if len(doc.Keys) == 0 {
		return nil, fmt.Errorf("%w: JWKS for %q has no keys", ErrInvalidJWKS, trustDomain)
	}

	set := &JWKS{domain: trustDomain, keys: make(map[string]jwtVerificationKey, len(doc.Keys))}
	for i, key := range doc.Keys {
		if err := validateKeyID(key.Kid); err != nil {
			return nil, fmt.Errorf("key %d: %w", i, err)
		}
		if _, exists := set.keys[key.Kid]; exists {
			return nil, fmt.Errorf("%w: duplicate kid %q in trust domain %q", ErrInvalidJWKS, key.Kid, trustDomain)
		}
		if key.Use != "" && key.Use != "sig" {
			return nil, fmt.Errorf("%w: key %q use is %q, want sig", ErrInvalidJWKS, key.Kid, key.Use)
		}
		if len(key.KeyOps) > 0 {
			hasVerify := false
			for _, op := range key.KeyOps {
				switch op {
				case "verify":
					hasVerify = true
				case "sign", "encrypt", "decrypt", "wrapKey", "unwrapKey", "deriveKey", "deriveBits":
					return nil, fmt.Errorf("%w: key %q has non-verification key_ops entry %q", ErrInvalidJWKS, key.Kid, op)
				default:
					return nil, fmt.Errorf("%w: key %q has unsupported key_ops entry %q", ErrInvalidJWKS, key.Kid, op)
				}
			}
			if !hasVerify {
				return nil, fmt.Errorf("%w: key %q key_ops does not allow verify", ErrInvalidJWKS, key.Kid)
			}
		}
		if key.D != "" {
			return nil, fmt.Errorf("%w: key %q contains private key material", ErrInvalidJWKS, key.Kid)
		}

		publicKey, algorithms, err := parseJWKPublicKey(key.Kty, key.Crv, key.X, key.Y, key.N, key.E)
		if err != nil {
			return nil, fmt.Errorf("%w: key %q: %v", ErrInvalidJWKS, key.Kid, err)
		}
		if key.Alg != "" {
			if !supportedJOSEAlgorithm(key.Alg) {
				return nil, fmt.Errorf("%w: key %q uses unsupported alg %q", ErrInvalidJWKS, key.Kid, key.Alg)
			}
			if _, ok := algorithms[key.Alg]; !ok {
				return nil, fmt.Errorf("%w: key %q alg %q does not match its key type or curve", ErrInvalidJWKS, key.Kid, key.Alg)
			}
			algorithms = map[string]struct{}{key.Alg: {}}
		}
		set.keys[key.Kid] = jwtVerificationKey{publicKey: publicKey, algorithms: algorithms}
	}
	return set, nil
}

// TrustDomain returns the domain to which every key in this set is bound.
func (s *JWKS) TrustDomain() string {
	if s == nil {
		return ""
	}
	return s.domain
}

// KeyIDs returns the sorted key identifiers.
func (s *JWKS) KeyIDs() []string {
	if s == nil {
		return nil
	}
	ids := make([]string, 0, len(s.keys))
	for kid := range s.keys {
		ids = append(ids, kid)
	}
	sort.Strings(ids)
	return ids
}

// PublicKey returns the verification key for kid. It does not expose mutable
// internal maps and cannot be used to select a key from another domain.
func (s *JWKS) PublicKey(kid string) (crypto.PublicKey, bool) {
	if s == nil {
		return nil, false
	}
	key, ok := s.keys[kid]
	return key.publicKey, ok
}

// JWTKeyRing holds isolated JWKS instances keyed by trust domain.
type JWTKeyRing struct {
	sets map[string]*JWKS
}

// NewJWTKeyRing constructs a key ring. Duplicate domains are rejected.
func NewJWTKeyRing(sets ...*JWKS) (*JWTKeyRing, error) {
	if len(sets) == 0 {
		return nil, fmt.Errorf("%w: key ring is empty", ErrInvalidJWKS)
	}
	ring := &JWTKeyRing{sets: make(map[string]*JWKS, len(sets))}
	for _, set := range sets {
		if set == nil {
			return nil, fmt.Errorf("%w: nil JWKS", ErrInvalidJWKS)
		}
		if _, exists := ring.sets[set.domain]; exists {
			return nil, fmt.Errorf("%w: duplicate JWKS for trust domain %q", ErrInvalidJWKS, set.domain)
		}
		ring.sets[set.domain] = set
	}
	return ring, nil
}

// Domains returns the sorted trust domains present in the ring.
func (r *JWTKeyRing) Domains() []string {
	if r == nil {
		return nil
	}
	domains := make([]string, 0, len(r.sets))
	for domain := range r.sets {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}

// SingleDomain returns the only domain in the ring, if there is exactly one.
func (r *JWTKeyRing) SingleDomain() (string, bool) {
	if r == nil || len(r.sets) != 1 {
		return "", false
	}
	for domain := range r.sets {
		return domain, true
	}
	return "", false
}

func (r *JWTKeyRing) key(trustDomain, kid string) (jwtVerificationKey, bool) {
	if r == nil {
		return jwtVerificationKey{}, false
	}
	set, ok := r.sets[trustDomain]
	if !ok {
		return jwtVerificationKey{}, false
	}
	key, ok := set.keys[kid]
	return key, ok
}

func parseJWKPublicKey(kty, crv, x, y, n, e string) (crypto.PublicKey, map[string]struct{}, error) {
	switch kty {
	case "EC":
		var curve elliptic.Curve
		var alg string
		var coordinateSize int
		switch crv {
		case "P-256":
			curve, alg, coordinateSize = elliptic.P256(), "ES256", 32
		case "P-384":
			curve, alg, coordinateSize = elliptic.P384(), "ES384", 48
		case "P-521":
			curve, alg, coordinateSize = elliptic.P521(), "ES512", 66
		default:
			return nil, nil, fmt.Errorf("unsupported EC curve %q", crv)
		}
		xBytes, err := decodeJWKBase64URL("x", x)
		if err != nil {
			return nil, nil, err
		}
		yBytes, err := decodeJWKBase64URL("y", y)
		if err != nil {
			return nil, nil, err
		}
		if len(xBytes) != coordinateSize || len(yBytes) != coordinateSize {
			return nil, nil, fmt.Errorf("EC coordinates must be %d bytes", coordinateSize)
		}
		publicKey := &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(xBytes), Y: new(big.Int).SetBytes(yBytes)}
		if !curve.IsOnCurve(publicKey.X, publicKey.Y) {
			return nil, nil, fmt.Errorf("EC public key is not on curve %s", crv)
		}
		return publicKey, map[string]struct{}{alg: {}}, nil

	case "RSA":
		nBytes, err := decodeJWKBase64URL("n", n)
		if err != nil {
			return nil, nil, err
		}
		eBytes, err := decodeJWKBase64URL("e", e)
		if err != nil {
			return nil, nil, err
		}
		modulus := new(big.Int).SetBytes(nBytes)
		exponent := new(big.Int).SetBytes(eBytes)
		if modulus.Sign() <= 0 || modulus.BitLen() < 2048 {
			return nil, nil, fmt.Errorf("RSA modulus must be at least 2048 bits")
		}
		if !exponent.IsInt64() || exponent.Int64() < 3 || exponent.Int64() > int64(^uint(0)>>1) {
			return nil, nil, fmt.Errorf("invalid RSA exponent")
		}
		if exponent.Int64()%2 == 0 {
			return nil, nil, fmt.Errorf("RSA exponent must be odd")
		}
		publicKey := &rsa.PublicKey{N: modulus, E: int(exponent.Int64())}
		return publicKey, map[string]struct{}{
			"RS256": {}, "RS384": {}, "RS512": {},
			"PS256": {}, "PS384": {}, "PS512": {},
		}, nil

	case "OKP":
		if crv != "Ed25519" {
			return nil, nil, fmt.Errorf("unsupported OKP curve %q", crv)
		}
		xBytes, err := decodeJWKBase64URL("x", x)
		if err != nil {
			return nil, nil, err
		}
		if len(xBytes) != ed25519.PublicKeySize {
			return nil, nil, fmt.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
		}
		return ed25519.PublicKey(append([]byte(nil), xBytes...)), map[string]struct{}{"EdDSA": {}}, nil

	default:
		return nil, nil, fmt.Errorf("unsupported kty %q", kty)
	}
}

func decodeJWKBase64URL(field, value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("missing %s", field)
	}
	if strings.ContainsAny(value, "=+/ \t\r\n") {
		return nil, fmt.Errorf("%s is not unpadded base64url", field)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64url: %v", field, err)
	}
	return decoded, nil
}

func algorithmsForKey(publicKey crypto.PublicKey) (map[string]struct{}, error) {
	switch key := publicKey.(type) {
	case *ecdsa.PublicKey:
		if key == nil || key.Curve == nil || key.X == nil || key.Y == nil || !key.Curve.IsOnCurve(key.X, key.Y) {
			return nil, fmt.Errorf("invalid ECDSA public key")
		}
		switch key.Curve {
		case elliptic.P256():
			return map[string]struct{}{"ES256": {}}, nil
		case elliptic.P384():
			return map[string]struct{}{"ES384": {}}, nil
		case elliptic.P521():
			return map[string]struct{}{"ES512": {}}, nil
		default:
			return nil, fmt.Errorf("unsupported ECDSA curve")
		}
	case *rsa.PublicKey:
		if key == nil || key.N == nil || key.N.BitLen() < 2048 || key.E < 3 || key.E%2 == 0 {
			return nil, fmt.Errorf("invalid or weak RSA public key")
		}
		return map[string]struct{}{
			"RS256": {}, "RS384": {}, "RS512": {},
			"PS256": {}, "PS384": {}, "PS512": {},
		}, nil
	case ed25519.PublicKey:
		if len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid Ed25519 public key")
		}
		return map[string]struct{}{"EdDSA": {}}, nil
	default:
		return nil, fmt.Errorf("unsupported public key type %T", publicKey)
	}
}

func validateKeyID(kid string) error {
	if kid == "" {
		return fmt.Errorf("%w: JWK kid is empty", ErrInvalidJWKS)
	}
	if len(kid) > 256 {
		return fmt.Errorf("%w: JWK kid is too long", ErrInvalidJWKS)
	}
	if !isASCII(kid) {
		return fmt.Errorf("%w: JWK kid must be ASCII", ErrInvalidJWKS)
	}
	for i := 0; i < len(kid); i++ {
		if kid[i] < 0x21 || kid[i] > 0x7e {
			return fmt.Errorf("%w: JWK kid contains whitespace or control bytes", ErrInvalidJWKS)
		}
	}
	return nil
}

func supportedJOSEAlgorithm(alg string) bool {
	switch alg {
	case "ES256", "ES384", "ES512", "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "EdDSA":
		return true
	default:
		return false
	}
}

func jsonObject(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if token != json.Delim('{') {
		return nil, fmt.Errorf("expected a JSON object")
	}
	object := make(map[string]json.RawMessage)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		if _, duplicate := object[key]; duplicate {
			return nil, fmt.Errorf("duplicate object member %q", key)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		object[key] = value
	}
	if token, err = dec.Token(); err != nil {
		return nil, err
	}
	if token != json.Delim('}') {
		return nil, fmt.Errorf("unterminated JSON object")
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, err
	}
	return object, nil
}

func jsonString(object map[string]json.RawMessage, name string, required bool) (string, error) {
	raw, ok := object[name]
	if !ok {
		if required {
			return "", fmt.Errorf("missing %q", name)
		}
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("%q must be a string", name)
	}
	return value, nil
}

func jsonNumericDate(object map[string]json.RawMessage, name string) (int64, bool, error) {
	raw, ok := object[name]
	if !ok {
		return 0, false, nil
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil || value <= 0 || value > 253402300799 {
		return 0, true, fmt.Errorf("%q must be a positive integer NumericDate", name)
	}
	return value, true, nil
}

func jsonAudience(object map[string]json.RawMessage, name string) ([]string, error) {
	raw, ok := object[name]
	if !ok {
		return nil, fmt.Errorf("missing %q", name)
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single == "" {
			return nil, fmt.Errorf("%q is empty", name)
		}
		return []string{single}, nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, fmt.Errorf("%q must be a string or string array", name)
	}
	if len(multiple) == 0 {
		return nil, fmt.Errorf("%q is empty", name)
	}
	for _, value := range multiple {
		if value == "" {
			return nil, fmt.Errorf("%q contains an empty audience", name)
		}
	}
	return multiple, nil
}
