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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"strings"
)

// Claims contains the decoded JWT claims.
type Claims map[string]any

var (
	// ErrInvalidToken reports a token that is malformed or cannot be verified.
	ErrInvalidToken = errors.New("netsec: invalid JWT")
	// ErrAlgorithmNotAllowed reports a missing, empty, "none", or unlisted alg.
	ErrAlgorithmNotAllowed = errors.New("netsec: JWT algorithm not allowed")
	// ErrSignatureInvalid reports a signature that does not match the token.
	ErrSignatureInvalid = errors.New("netsec: invalid JWT signature")
	// ErrInvalidKey reports a key that is unsuitable for the configured algorithm.
	ErrInvalidKey = errors.New("netsec: invalid verification key")
	// ErrInvalidConfiguration reports an unsafe verifier configuration.
	ErrInvalidConfiguration = errors.New("netsec: invalid verifier configuration")
)

// Verifier checks JWT signatures using an explicit, non-"none" algorithm
// allowlist and a single verification key.
type Verifier struct {
	key        any
	algorithms map[string]struct{}
}

// NewVerifier creates a verifier. At least one non-"none" algorithm must be
// listed explicitly; there are no default algorithms. The key must be:
//
//   - []byte for HS256, HS384, or HS512;
//   - *rsa.PublicKey for RS* or PS* algorithms;
//   - *ecdsa.PublicKey for the matching ES* algorithm; or
//   - ed25519.PublicKey for EdDSA.
//
// Algorithm names are case-sensitive. "none" is rejected in every
// capitalization.
func NewVerifier(key any, algorithms []string) (*Verifier, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil key", ErrInvalidConfiguration)
	}
	if len(algorithms) == 0 {
		return nil, fmt.Errorf("%w: no algorithms listed", ErrInvalidConfiguration)
	}

	allowed := make(map[string]struct{}, len(algorithms))
	for _, algorithm := range algorithms {
		if algorithm == "" || isNoneAlgorithm(algorithm) {
			return nil, fmt.Errorf(
				"%w: %w: %q",
				ErrInvalidConfiguration,
				ErrAlgorithmNotAllowed,
				algorithm,
			)
		}
		if !isSupportedAlgorithm(algorithm) {
			return nil, fmt.Errorf(
				"%w: unsupported algorithm %q",
				ErrInvalidConfiguration,
				algorithm,
			)
		}
		if err := validateKeyForAlgorithm(key, algorithm); err != nil {
			return nil, fmt.Errorf(
				"%w: algorithm %s: %w",
				ErrInvalidConfiguration,
				algorithm,
				err,
			)
		}
		allowed[algorithm] = struct{}{}
	}

	return &Verifier{key: key, algorithms: allowed}, nil
}

// Verify checks the JWT signature and returns its claims. It never accepts an
// empty signature, even when the header says alg is "none".
func (v *Verifier) Verify(token string) (Claims, error) {
	if v == nil || v.key == nil || len(v.algorithms) == 0 {
		return nil, fmt.Errorf("%w: uninitialized verifier", ErrInvalidConfiguration)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("%w: expected three non-empty segments", ErrInvalidToken)
	}

	headerSegment, err := decodeSegment(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid header encoding", ErrInvalidToken)
	}
	header, err := decodeJSONObject(headerSegment)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid header", ErrInvalidToken)
	}

	rawAlgorithm, ok := header["alg"]
	if !ok {
		return nil, fmt.Errorf(
			"%w: %w: alg is missing",
			ErrInvalidToken,
			ErrAlgorithmNotAllowed,
		)
	}

	var algorithm string
	if err := json.Unmarshal(rawAlgorithm, &algorithm); err != nil || algorithm == "" {
		return nil, fmt.Errorf(
			"%w: %w: alg is empty or not a string",
			ErrInvalidToken,
			ErrAlgorithmNotAllowed,
		)
	}
	if isNoneAlgorithm(algorithm) {
		return nil, fmt.Errorf(
			"%w: %w: alg %q is forbidden",
			ErrInvalidToken,
			ErrAlgorithmNotAllowed,
			algorithm,
		)
	}
	if _, ok := v.algorithms[algorithm]; !ok {
		return nil, fmt.Errorf(
			"%w: %w: alg %q was not listed",
			ErrInvalidToken,
			ErrAlgorithmNotAllowed,
			algorithm,
		)
	}

	signature, err := decodeSegment(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid signature encoding", ErrInvalidToken)
	}
	if len(signature) == 0 {
		return nil, fmt.Errorf(
			"%w: %w: empty signature",
			ErrInvalidToken,
			ErrSignatureInvalid,
		)
	}

	signingInput := parts[0] + "." + parts[1]
	if err := verifySignature(algorithm, v.key, []byte(signingInput), signature); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	payloadSegment, err := decodeSegment(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid payload encoding", ErrInvalidToken)
	}
	payload, err := decodeJSONObject(payloadSegment)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid payload", ErrInvalidToken)
	}

	claims := make(Claims, len(payload))
	for name, rawValue := range payload {
		var value any
		if err := json.Unmarshal(rawValue, &value); err != nil {
			return nil, fmt.Errorf("%w: invalid claim %q", ErrInvalidToken, name)
		}
		claims[name] = value
	}
	return claims, nil
}

// Verify creates a verifier and checks the token in one call. allowedAlgorithms
// must explicitly list every non-"none" algorithm the caller is willing to
// accept.
func Verify(token string, key any, allowedAlgorithms []string) (Claims, error) {
	verifier, err := NewVerifier(key, allowedAlgorithms)
	if err != nil {
		return nil, err
	}
	return verifier.Verify(token)
}

// VerifyJWT is an alias for Verify for callers that prefer an explicit name.
func VerifyJWT(token string, key any, allowedAlgorithms []string) (Claims, error) {
	return Verify(token, key, allowedAlgorithms)
}

func isNoneAlgorithm(algorithm string) bool {
	return strings.EqualFold(algorithm, "none")
}

func isSupportedAlgorithm(algorithm string) bool {
	switch algorithm {
	case "HS256", "HS384", "HS512",
		"RS256", "RS384", "RS512",
		"PS256", "PS384", "PS512",
		"ES256", "ES384", "ES512",
		"EdDSA":
		return true
	default:
		return false
	}
}

func validateKeyForAlgorithm(key any, algorithm string) error {
	switch algorithm {
	case "HS256", "HS384", "HS512":
		secret, ok := key.([]byte)
		if !ok || len(secret) == 0 {
			return fmt.Errorf("%w: HMAC requires a non-empty []byte", ErrInvalidKey)
		}
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		publicKey, ok := key.(*rsa.PublicKey)
		if !ok || publicKey == nil || publicKey.N == nil || publicKey.N.Sign() <= 0 || publicKey.E < 2 {
			return fmt.Errorf("%w: RSA requires a valid *rsa.PublicKey", ErrInvalidKey)
		}
	case "ES256", "ES384", "ES512":
		publicKey, ok := key.(*ecdsa.PublicKey)
		if !ok || publicKey == nil || publicKey.Curve == nil ||
			publicKey.X == nil || publicKey.Y == nil ||
			!publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
			return fmt.Errorf("%w: ECDSA requires a valid *ecdsa.PublicKey", ErrInvalidKey)
		}
		if publicKey.Curve != curveForAlgorithm(algorithm) {
			return fmt.Errorf(
				"%w: %s requires curve %s",
				ErrInvalidKey,
				algorithm,
				curveForAlgorithm(algorithm).Params().Name,
			)
		}
	case "EdDSA":
		publicKey, ok := key.(ed25519.PublicKey)
		if !ok || len(publicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: EdDSA requires an ed25519.PublicKey", ErrInvalidKey)
		}
	default:
		return fmt.Errorf("%w: unsupported algorithm %q", ErrAlgorithmNotAllowed, algorithm)
	}
	return nil
}

func verifySignature(algorithm string, key any, signingInput, signature []byte) error {
	switch algorithm {
	case "HS256", "HS384", "HS512":
		secret, ok := key.([]byte)
		if !ok || len(secret) == 0 {
			return fmt.Errorf("%w: HMAC requires a non-empty []byte", ErrInvalidKey)
		}
		mac := hmac.New(hmacHashForAlgorithm(algorithm), secret)
		mac.Write(signingInput)
		if !hmac.Equal(signature, mac.Sum(nil)) {
			return ErrSignatureInvalid
		}
		return nil
	case "RS256", "RS384", "RS512":
		publicKey, ok := key.(*rsa.PublicKey)
		if !ok || publicKey == nil {
			return fmt.Errorf("%w: RSA requires a *rsa.PublicKey", ErrInvalidKey)
		}
		digest, hashID, err := digestForAlgorithm(algorithm, signingInput)
		if err != nil {
			return err
		}
		if err := rsa.VerifyPKCS1v15(publicKey, hashID, digest, signature); err != nil {
			return ErrSignatureInvalid
		}
		return nil
	case "PS256", "PS384", "PS512":
		publicKey, ok := key.(*rsa.PublicKey)
		if !ok || publicKey == nil {
			return fmt.Errorf("%w: RSA requires a *rsa.PublicKey", ErrInvalidKey)
		}
		digest, hashID, err := digestForAlgorithm(algorithm, signingInput)
		if err != nil {
			return err
		}
		options := &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hashID,
		}
		if err := rsa.VerifyPSS(publicKey, hashID, digest, signature, options); err != nil {
			return ErrSignatureInvalid
		}
		return nil
	case "ES256", "ES384", "ES512":
		publicKey, ok := key.(*ecdsa.PublicKey)
		if !ok || publicKey == nil {
			return fmt.Errorf("%w: ECDSA requires a *ecdsa.PublicKey", ErrInvalidKey)
		}
		if publicKey.Curve != curveForAlgorithm(algorithm) {
			return fmt.Errorf("%w: wrong ECDSA curve for %s", ErrInvalidKey, algorithm)
		}
		digest, _, err := digestForAlgorithm(algorithm, signingInput)
		if err != nil {
			return err
		}
		coordinateSize := (publicKey.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*coordinateSize {
			return ErrSignatureInvalid
		}
		r := new(big.Int).SetBytes(signature[:coordinateSize])
		s := new(big.Int).SetBytes(signature[coordinateSize:])
		if !ecdsa.Verify(publicKey, digest, r, s) {
			return ErrSignatureInvalid
		}
		return nil
	case "EdDSA":
		publicKey, ok := key.(ed25519.PublicKey)
		if !ok || len(publicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: EdDSA requires an ed25519.PublicKey", ErrInvalidKey)
		}
		if !ed25519.Verify(publicKey, signingInput, signature) {
			return ErrSignatureInvalid
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported algorithm %q", ErrAlgorithmNotAllowed, algorithm)
	}
}

func hmacHashForAlgorithm(algorithm string) func() hash.Hash {
	switch algorithm {
	case "HS256":
		return sha256.New
	case "HS384":
		return sha512.New384
	case "HS512":
		return sha512.New
	default:
		panic("netsec: hmacHashForAlgorithm called with unsupported algorithm")
	}
}

func digestForAlgorithm(algorithm string, data []byte) ([]byte, crypto.Hash, error) {
	switch algorithm {
	case "RS256", "PS256", "ES256":
		sum := sha256.Sum256(data)
		return sum[:], crypto.SHA256, nil
	case "RS384", "PS384", "ES384":
		sum := sha512.Sum384(data)
		return sum[:], crypto.SHA384, nil
	case "RS512", "PS512", "ES512":
		sum := sha512.Sum512(data)
		return sum[:], crypto.SHA512, nil
	default:
		return nil, 0, fmt.Errorf(
			"%w: unsupported digest algorithm %q",
			ErrAlgorithmNotAllowed,
			algorithm,
		)
	}
}

func curveForAlgorithm(algorithm string) elliptic.Curve {
	switch algorithm {
	case "ES256":
		return elliptic.P256()
	case "ES384":
		return elliptic.P384()
	case "ES512":
		return elliptic.P521()
	default:
		return nil
	}
}

func decodeSegment(segment string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(segment)
}

func decodeJSONObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))

	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, errors.New("not a JSON object")
	}

	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("object key is not a string")
		}
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate member %q", name)
		}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields[name] = value
	}

	token, err = decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok = token.(json.Delim)
	if !ok || delimiter != '}' {
		return nil, errors.New("unterminated object")
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing JSON value")
		}
		return nil, err
	}
	return fields, nil
}
