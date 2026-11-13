package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"hash"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// MaxProofSize is the largest compact proof JWT accepted by ParseProof.
const MaxProofSize = 64 << 10

// Proof is a parsed and structurally validated JWT. Parsing alone does not
// authenticate the token; Verifier.ValidateProof also verifies the signature.
type Proof struct {
	Raw       string
	Header    map[string]any
	Claims    map[string]any
	Algorithm string

	Audience  string
	Audiences []string
	Nonce     string

	IssuedAt  time.Time
	NotBefore time.Time
	ExpiresAt time.Time

	HasIssuedAt  bool
	HasNotBefore bool
	HasExpiresAt bool

	Subject string
	Issuer  string
	JWTID   string

	signingInput []byte
	signature    []byte
}

// ParseProof parses a compact JWT/JWS and validates its registered
// audience/nonce claims. It deliberately does not verify the signature; callers
// must use a Verifier for authentication. The returned claims remain available
// for application-specific checks.
func ParseProof(raw string) (*Proof, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, proofError(ErrMalformedProof, "empty token")
	}
	if len(raw) > MaxProofSize {
		return nil, proofError(ErrMalformedProof, "token exceeds %d bytes", MaxProofSize)
	}

	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, proofError(ErrMalformedProof, "expected three compact JWS segments, got %d", len(parts))
	}
	for i, part := range parts {
		if part == "" {
			return nil, proofError(ErrMalformedProof, "segment %d is empty", i+1)
		}
	}

	headerBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return nil, proofErrorWithCause(ErrMalformedProof, err, "invalid header encoding")
	}
	header, err := decodeJSONObject(headerBytes)
	if err != nil {
		return nil, proofErrorWithCause(ErrMalformedProof, err, "invalid header JSON")
	}

	alg, ok := header["alg"].(string)
	if !ok || alg == "" {
		return nil, proofError(ErrInvalidClaim, `"alg" header is required`)
	}

	payloadBytes, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, proofErrorWithCause(ErrMalformedProof, err, "invalid payload encoding")
	}
	claims, err := decodeJSONObject(payloadBytes)
	if err != nil {
		return nil, proofErrorWithCause(ErrMalformedProof, err, "invalid payload JSON")
	}

	signature, err := decodeBase64URL(parts[2])
	if err != nil {
		return nil, proofErrorWithCause(ErrMalformedProof, err, "invalid signature encoding")
	}

	proof := &Proof{
		Raw:          raw,
		Header:       header,
		Claims:       claims,
		Algorithm:    alg,
		signingInput: []byte(parts[0] + "." + parts[1]),
		signature:    signature,
	}
	if err := parseRegisteredClaims(proof); err != nil {
		return nil, err
	}
	return proof, nil
}

// ParseUnverifiedProof is an explicit alias for ParseProof.
func ParseUnverifiedProof(raw string) (*Proof, error) {
	return ParseProof(raw)
}

func decodeBase64URL(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("empty base64url value")
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func decodeJSONObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}

	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON value is not an object")
	}
	return object, nil
}

func parseRegisteredClaims(proof *Proof) error {
	audValue, ok := proof.Claims["aud"]
	if !ok {
		return proofError(ErrInvalidClaim, `"aud" claim is required`)
	}
	audiences, err := parseAudience(audValue)
	if err != nil {
		return proofErrorWithCause(ErrInvalidClaim, err, `invalid "aud" claim`)
	}
	proof.Audiences = audiences
	if len(audiences) == 1 {
		proof.Audience = audiences[0]
	}

	nonceValue, ok := proof.Claims["nonce"]
	if !ok {
		return proofError(ErrInvalidClaim, `"nonce" claim is required`)
	}
	nonce, ok := nonceValue.(string)
	if !ok || nonce == "" {
		return proofError(ErrInvalidClaim, `"nonce" claim must be a non-empty string`)
	}
	proof.Nonce = nonce

	if value, ok := proof.Claims["iat"]; ok {
		issuedAt, err := parseNumericDate(value)
		if err != nil {
			return proofErrorWithCause(ErrInvalidClaim, err, `invalid "iat" claim`)
		}
		proof.IssuedAt = issuedAt
		proof.HasIssuedAt = true
	}
	if value, ok := proof.Claims["nbf"]; ok {
		notBefore, err := parseNumericDate(value)
		if err != nil {
			return proofErrorWithCause(ErrInvalidClaim, err, `invalid "nbf" claim`)
		}
		proof.NotBefore = notBefore
		proof.HasNotBefore = true
	}
	if value, ok := proof.Claims["exp"]; ok {
		expiresAt, err := parseNumericDate(value)
		if err != nil {
			return proofErrorWithCause(ErrInvalidClaim, err, `invalid "exp" claim`)
		}
		proof.ExpiresAt = expiresAt
		proof.HasExpiresAt = true
	}

	proof.Subject = optionalString(proof.Claims, "sub")
	proof.Issuer = optionalString(proof.Claims, "iss")
	proof.JWTID = optionalString(proof.Claims, "jti")
	return nil
}

func optionalString(claims map[string]any, name string) string {
	value, _ := claims[name].(string)
	return value
}

func parseAudience(value any) ([]string, error) {
	switch audience := value.(type) {
	case string:
		if audience == "" {
			return nil, fmt.Errorf("audience is empty")
		}
		return []string{audience}, nil
	case []any:
		if len(audience) == 0 {
			return nil, fmt.Errorf("audience array is empty")
		}
		result := make([]string, 0, len(audience))
		for i, item := range audience {
			text, ok := item.(string)
			if !ok || text == "" {
				return nil, fmt.Errorf("audience element %d is not a non-empty string", i)
			}
			result = append(result, text)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("audience must be a string or string array")
	}
}

func parseNumericDate(value any) (time.Time, error) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		if err != nil {
			return time.Time{}, err
		}
		number = parsed
	case float64:
		number = typed
	case int64:
		number = float64(typed)
	case int:
		number = float64(typed)
	default:
		return time.Time{}, fmt.Errorf("numeric date is not a number")
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return time.Time{}, fmt.Errorf("numeric date is not finite")
	}

	seconds, fraction := math.Modf(number)
	nanoseconds := int64(fraction * float64(time.Second))
	if nanoseconds < 0 {
		seconds--
		nanoseconds += int64(time.Second)
	}
	return time.Unix(int64(seconds), nanoseconds), nil
}

func validateHeader(proof *Proof, allowedAlgorithms map[string]bool, requiredType string) error {
	algorithm := proof.Algorithm
	if strings.EqualFold(algorithm, "none") {
		return proofError(ErrUnsupportedAlgorithm, `"alg" must not be "none"`)
	}
	if allowedAlgorithms != nil && !allowedAlgorithms[algorithm] {
		return proofError(ErrUnsupportedAlgorithm, "algorithm %q is not allowed", algorithm)
	}

	if value, ok := proof.Header["crit"]; ok {
		return proofError(ErrUnsupportedCriticalHeader, `"crit" header is present: %v`, value)
	}
	if value, ok := proof.Header["b64"]; ok {
		encoded, ok := value.(bool)
		if !ok || !encoded {
			return proofError(ErrUnsupportedCriticalHeader, `"b64": false is not supported`)
		}
	}
	if requiredType != "" {
		typ, _ := proof.Header["typ"].(string)
		if typ != requiredType {
			return proofError(ErrProofType, "required %q, got %q", requiredType, typ)
		}
	}
	return nil
}

func verifySignature(proof *Proof, key any) error {
	key, err := normalizeVerificationKey(key)
	if err != nil {
		return err
	}
	if key == nil {
		return proofError(ErrMissingKey, "")
	}

	algorithm := proof.Algorithm
	switch algorithm {
	case "HS256", "HS384", "HS512":
		secret, ok := key.([]byte)
		if !ok {
			return proofError(ErrInvalidKey, "%s requires an HMAC secret, got %T", algorithm, key)
		}
		if len(secret) == 0 {
			return proofError(ErrInvalidKey, "HMAC secret must not be empty")
		}
		mac := hmac.New(hashConstructor(hashFor(algorithm)), secret)
		mac.Write(proof.signingInput)
		if !hmac.Equal(mac.Sum(nil), proof.signature) {
			return proofError(ErrInvalidSignature, "HMAC mismatch")
		}
		return nil
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		publicKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return proofError(ErrInvalidKey, "%s requires an RSA public key, got %T", algorithm, key)
		}
		if publicKey.N == nil || publicKey.N.Sign() <= 0 || publicKey.E < 3 {
			return proofError(ErrInvalidKey, "invalid RSA public key")
		}
		hash := hashFor(algorithm)
		digest := hashBytes(hash, proof.signingInput)
		if strings.HasPrefix(algorithm, "PS") {
			err = rsa.VerifyPSS(publicKey, hash, digest, proof.signature, &rsa.PSSOptions{
				SaltLength: rsa.PSSSaltLengthEqualsHash,
				Hash:       hash,
			})
		} else {
			err = rsa.VerifyPKCS1v15(publicKey, hash, digest, proof.signature)
		}
		if err != nil {
			return proofErrorWithCause(ErrInvalidSignature, err, "RSA signature mismatch")
		}
		return nil
	case "ES256", "ES384", "ES512":
		publicKey, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return proofError(ErrInvalidKey, "%s requires an ECDSA public key, got %T", algorithm, key)
		}
		if publicKey.Curve == nil || publicKey.X == nil || publicKey.Y == nil {
			return proofError(ErrInvalidKey, "invalid ECDSA public key")
		}
		expectedCurve, size := curveForAlgorithm(algorithm)
		if publicKey.Curve != expectedCurve {
			return proofError(ErrInvalidKey, "%s requires curve %s", algorithm, expectedCurve.Params().Name)
		}
		if len(proof.signature) != 2*size {
			return proofError(ErrInvalidSignature, "ECDSA signature must be %d bytes, got %d", 2*size, len(proof.signature))
		}
		r := new(big.Int).SetBytes(proof.signature[:size])
		s := new(big.Int).SetBytes(proof.signature[size:])
		digest := hashBytes(hashFor(algorithm), proof.signingInput)
		if !ecdsa.Verify(publicKey, digest, r, s) {
			return proofError(ErrInvalidSignature, "ECDSA signature mismatch")
		}
		return nil
	case "EdDSA":
		publicKey, ok := key.(ed25519.PublicKey)
		if !ok {
			return proofError(ErrInvalidKey, "EdDSA requires an Ed25519 public key, got %T", key)
		}
		if len(proof.signature) != ed25519.SignatureSize {
			return proofError(ErrInvalidSignature, "Ed25519 signature must be %d bytes", ed25519.SignatureSize)
		}
		if !ed25519.Verify(publicKey, proof.signingInput, proof.signature) {
			return proofError(ErrInvalidSignature, "Ed25519 signature mismatch")
		}
		return nil
	default:
		return proofError(ErrUnsupportedAlgorithm, "algorithm %q is not supported", algorithm)
	}
}

func hashFor(algorithm string) crypto.Hash {
	switch algorithm {
	case "HS256", "RS256", "PS256", "ES256":
		return crypto.SHA256
	case "HS384", "RS384", "PS384", "ES384":
		return crypto.SHA384
	case "HS512", "RS512", "PS512", "ES512":
		return crypto.SHA512
	default:
		return 0
	}
}

func hashBytes(algorithmHash crypto.Hash, value []byte) []byte {
	hasher := newHash(algorithmHash)
	hasher.Write(value)
	return hasher.Sum(nil)
}

func newHash(algorithmHash crypto.Hash) hash.Hash {
	switch algorithmHash {
	case crypto.SHA256:
		return sha256.New()
	case crypto.SHA384:
		return sha512.New384()
	case crypto.SHA512:
		return sha512.New()
	default:
		panic("netsec: unsupported hash")
	}
}

func hashConstructor(algorithmHash crypto.Hash) func() hash.Hash {
	return func() hash.Hash {
		return newHash(algorithmHash)
	}
}

func curveForAlgorithm(algorithm string) (elliptic.Curve, int) {
	switch algorithm {
	case "ES256":
		return elliptic.P256(), 32
	case "ES384":
		return elliptic.P384(), 48
	default:
		return elliptic.P521(), 66
	}
}

func normalizeVerificationKey(key any) (any, error) {
	switch typed := key.(type) {
	case nil:
		return nil, nil
	case []byte:
		if bytes.HasPrefix(bytes.TrimSpace(typed), []byte("-----BEGIN")) {
			return parsePEMKey(typed)
		}
		return append([]byte(nil), typed...), nil
	case string:
		if strings.Contains(typed, "-----BEGIN") {
			return parsePEMKey([]byte(typed))
		}
		return []byte(typed), nil
	case json.RawMessage:
		var object map[string]any
		if err := json.Unmarshal(typed, &object); err != nil {
			return nil, proofErrorWithCause(ErrInvalidKey, err, "invalid JWK JSON")
		}
		return parseJWK(object, "")
	case map[string]any:
		return parseJWK(typed, "")
	case *x509.Certificate:
		return typed.PublicKey, nil
	case crypto.Signer:
		return typed.Public(), nil
	case *rsa.PublicKey:
		return typed, nil
	case rsa.PublicKey:
		return &typed, nil
	case *rsa.PrivateKey:
		return &typed.PublicKey, nil
	case *ecdsa.PublicKey:
		return typed, nil
	case ecdsa.PublicKey:
		return &typed, nil
	case *ecdsa.PrivateKey:
		return &typed.PublicKey, nil
	case ed25519.PublicKey:
		if len(typed) != ed25519.PublicKeySize {
			return nil, proofError(ErrInvalidKey, "Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
		}
		return typed, nil
	case ed25519.PrivateKey:
		return typed.Public().(ed25519.PublicKey), nil
	default:
		return nil, proofError(ErrInvalidKey, "unsupported key type %T", key)
	}
}

func parsePEMKey(data []byte) (any, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, proofError(ErrInvalidKey, "no PEM block found")
	}

	if certificate, err := x509.ParseCertificate(block.Bytes); err == nil {
		return certificate.PublicKey, nil
	}
	if publicKey, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return publicKey, nil
	}
	if publicKey, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return publicKey, nil
	}
	if privateKey, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return publicFromPrivate(privateKey)
	}
	if privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return &privateKey.PublicKey, nil
	}
	if privateKey, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return &privateKey.PublicKey, nil
	}
	return nil, proofError(ErrInvalidKey, "unrecognised PEM key")
}

func publicFromPrivate(privateKey any) (any, error) {
	switch typed := privateKey.(type) {
	case *rsa.PrivateKey:
		return &typed.PublicKey, nil
	case *ecdsa.PrivateKey:
		return &typed.PublicKey, nil
	case ed25519.PrivateKey:
		return typed.Public().(ed25519.PublicKey), nil
	default:
		return nil, proofError(ErrInvalidKey, "unsupported private key type %T", privateKey)
	}
}

func parseJWK(object map[string]any, expectedAlgorithm string) (any, error) {
	if object == nil {
		return nil, proofError(ErrInvalidKey, "empty JWK")
	}
	for _, privateName := range []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"} {
		if _, ok := object[privateName]; ok {
			return nil, proofError(ErrInvalidKey, "JWK must not contain private key material %q", privateName)
		}
	}

	kty, _ := object["kty"].(string)
	if kty == "" {
		return nil, proofError(ErrInvalidKey, `JWK "kty" is required`)
	}
	if use, ok := object["use"]; ok {
		if use != "sig" {
			return nil, proofError(ErrInvalidKey, `JWK "use" must be "sig"`)
		}
	}
	if operations, ok := object["key_ops"]; ok {
		allowed, err := jwkAllowsVerify(operations)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, proofError(ErrInvalidKey, `JWK "key_ops" does not allow verify`)
		}
	}
	if algorithm, ok := object["alg"]; ok {
		value, ok := algorithm.(string)
		if !ok {
			return nil, proofError(ErrInvalidKey, `JWK "alg" must be a string`)
		}
		if expectedAlgorithm != "" && value != expectedAlgorithm {
			return nil, proofError(ErrInvalidKey, "JWK algorithm %q does not match %q", value, expectedAlgorithm)
		}
	}

	switch kty {
	case "RSA":
		modulusBytes, err := jwkBytes(object, "n")
		if err != nil {
			return nil, err
		}
		exponentBytes, err := jwkBytes(object, "e")
		if err != nil {
			return nil, err
		}
		exponent := new(big.Int).SetBytes(exponentBytes)
		if exponent.Sign() <= 0 || exponent.BitLen() > 31 {
			return nil, proofError(ErrInvalidKey, "invalid RSA exponent")
		}
		modulus := new(big.Int).SetBytes(modulusBytes)
		if modulus.Sign() <= 0 {
			return nil, proofError(ErrInvalidKey, "invalid RSA modulus")
		}
		return &rsa.PublicKey{
			N: modulus,
			E: int(exponent.Int64()),
		}, nil
	case "EC":
		curveName, _ := object["crv"].(string)
		curve, size, ok := jwkCurve(curveName)
		if !ok {
			return nil, proofError(ErrInvalidKey, "unsupported EC curve %q", curveName)
		}
		xBytes, err := jwkBytes(object, "x")
		if err != nil {
			return nil, err
		}
		yBytes, err := jwkBytes(object, "y")
		if err != nil {
			return nil, err
		}
		if len(xBytes) != size || len(yBytes) != size {
			return nil, proofError(ErrInvalidKey, "EC coordinates for %s must be %d bytes", curveName, size)
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if !curve.IsOnCurve(x, y) {
			return nil, proofError(ErrInvalidKey, "EC point is not on curve %s", curveName)
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	case "OKP":
		curveName, _ := object["crv"].(string)
		if curveName != "Ed25519" {
			return nil, proofError(ErrInvalidKey, "unsupported OKP curve %q", curveName)
		}
		xBytes, err := jwkBytes(object, "x")
		if err != nil {
			return nil, err
		}
		if len(xBytes) != ed25519.PublicKeySize {
			return nil, proofError(ErrInvalidKey, "Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
		}
		return ed25519.PublicKey(xBytes), nil
	default:
		return nil, proofError(ErrInvalidKey, "unsupported JWK key type %q", kty)
	}
}

func jwkBytes(object map[string]any, name string) ([]byte, error) {
	encoded, ok := object[name].(string)
	if !ok || encoded == "" {
		return nil, proofError(ErrInvalidKey, "JWK %q is required", name)
	}
	decoded, err := decodeBase64URL(encoded)
	if err != nil {
		return nil, proofErrorWithCause(ErrInvalidKey, err, "invalid JWK %q", name)
	}
	return decoded, nil
}

func jwkAllowsVerify(value any) (bool, error) {
	var operations []string
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			operation, ok := item.(string)
			if !ok {
				return false, proofError(ErrInvalidKey, `JWK "key_ops" entries must be strings`)
			}
			operations = append(operations, operation)
		}
	case []string:
		operations = typed
	default:
		return false, proofError(ErrInvalidKey, `JWK "key_ops" must be an array`)
	}
	for _, operation := range operations {
		if operation == "verify" {
			return true, nil
		}
	}
	return false, nil
}

func jwkCurve(name string) (elliptic.Curve, int, bool) {
	switch name {
	case "P-256":
		return elliptic.P256(), 32, true
	case "P-384":
		return elliptic.P384(), 48, true
	case "P-521":
		return elliptic.P521(), 66, true
	default:
		return nil, 0, false
	}
}

func equalConstantTime(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
