package netsec

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math/big"
	"strings"
)

// Header is the decoded JWS header. Raw retains every header member so
// callers can inspect key-selection metadata such as kid.
type Header struct {
	Algorithm string
	Type      string
	KeyID     string
	Raw       map[string]json.RawMessage
}

// Token is the unverified token representation supplied to a KeyFunc. Claims
// MUST NOT be trusted until Validate has verified the signature and claims.
type Token struct {
	Header  Header
	Claims  map[string]any
	raw     string
	signing string
}

func parseToken(raw string) (*Token, []byte, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, nil, fmt.Errorf("%w: JWT must have three segments", ErrMalformedToken)
	}

	headerObject, err := decodeBase64Object(parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid header: %v", ErrMalformedToken, err)
	}

	claimsObject, err := decodeBase64Claims(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid claims: %v", ErrMalformedToken, err)
	}

	algorithm, err := stringMember(headerObject, "alg")
	if err != nil || algorithm == "" {
		return nil, nil, fmt.Errorf("%w: alg header is missing or not a string", ErrMalformedToken)
	}
	if strings.EqualFold(algorithm, "none") {
		return nil, nil, fmt.Errorf("%w: alg none is forbidden", ErrUnsupportedAlgorithm)
	}

	typ, _ := stringMember(headerObject, "typ")
	keyID, _ := stringMember(headerObject, "kid")
	if _, present := headerObject["crit"]; present {
		return nil, nil, fmt.Errorf("%w: crit is not supported", ErrUnsupportedCritical)
	}
	if _, present := headerObject["b64"]; present {
		return nil, nil, fmt.Errorf("%w: b64 is not supported", ErrUnsupportedCritical)
	}

	var signature []byte
	signature, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: invalid signature encoding: %v", ErrMalformedToken, err)
	}

	token := &Token{
		Header: Header{
			Algorithm: algorithm,
			Type:      typ,
			KeyID:     keyID,
			Raw:       headerObject,
		},
		Claims:  claimsObject,
		raw:     raw,
		signing: parts[0] + "." + parts[1],
	}
	return token, signature, nil
}

func decodeBase64Object(segment string) (map[string]json.RawMessage, error) {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return nil, err
	}
	if err := rejectDuplicateMembers(data); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("segment is not a JSON object")
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	return object, nil
}

func decodeBase64Claims(segment string) (map[string]any, error) {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return nil, err
	}
	if err := rejectDuplicateMembers(data); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, fmt.Errorf("segment is not a JSON object")
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	return object, nil
}

func stringMember(object map[string]json.RawMessage, name string) (string, error) {
	raw, ok := object[name]
	if !ok {
		return "", fmt.Errorf("member %q is absent", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("member %q is not a string", name)
	}
	return value, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("trailing JSON value")
	}
	return err
}

func rejectDuplicateMembers(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSON(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func walkJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON object member %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSON(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder); err != nil {
				return err
			}
		}
		_, err := decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

// SupportedAlgorithms returns the signature algorithms this package can
// verify with the Go standard library.
func SupportedAlgorithms() []string {
	return []string{
		"RS256", "RS384", "RS512",
		"PS256", "PS384", "PS512",
		"ES256", "ES384", "ES512",
		"EdDSA",
		"HS256", "HS384", "HS512",
	}
}

func verifySignature(algorithm string, key any, signingInput, signature []byte) error {
	if key == nil {
		return fmt.Errorf("%w: KeyFunc returned nil", ErrKeyUnavailable)
	}

	switch algorithm {
	case "RS256":
		return verifyRSA(key, crypto.SHA256, signingInput, signature, false)
	case "RS384":
		return verifyRSA(key, crypto.SHA384, signingInput, signature, false)
	case "RS512":
		return verifyRSA(key, crypto.SHA512, signingInput, signature, false)
	case "PS256":
		return verifyRSA(key, crypto.SHA256, signingInput, signature, true)
	case "PS384":
		return verifyRSA(key, crypto.SHA384, signingInput, signature, true)
	case "PS512":
		return verifyRSA(key, crypto.SHA512, signingInput, signature, true)
	case "ES256":
		return verifyECDSA(key, crypto.SHA256, 256, signingInput, signature)
	case "ES384":
		return verifyECDSA(key, crypto.SHA384, 384, signingInput, signature)
	case "ES512":
		return verifyECDSA(key, crypto.SHA512, 521, signingInput, signature)
	case "EdDSA":
		return verifyEd25519(key, signingInput, signature)
	case "HS256":
		return verifyHMAC(key, crypto.SHA256, signingInput, signature)
	case "HS384":
		return verifyHMAC(key, crypto.SHA384, signingInput, signature)
	case "HS512":
		return verifyHMAC(key, crypto.SHA512, signingInput, signature)
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
}

func verifyRSA(key any, hashAlgorithm crypto.Hash, signingInput, signature []byte, pss bool) error {
	var publicKey *rsa.PublicKey
	switch typed := key.(type) {
	case *rsa.PublicKey:
		publicKey = typed
	case *rsa.PrivateKey:
		publicKey = &typed.PublicKey
	default:
		return fmt.Errorf("%w: RSA algorithm requires an RSA key", ErrInvalidSignature)
	}

	digest, err := hashInput(hashAlgorithm, signingInput)
	if err != nil {
		return err
	}
	if pss {
		err = rsa.VerifyPSS(publicKey, hashAlgorithm, digest, signature, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hashAlgorithm,
		})
	} else {
		err = rsa.VerifyPKCS1v15(publicKey, hashAlgorithm, digest, signature)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return nil
}

func verifyECDSA(key any, hashAlgorithm crypto.Hash, bits int, signingInput, signature []byte) error {
	var publicKey *ecdsa.PublicKey
	switch typed := key.(type) {
	case *ecdsa.PublicKey:
		publicKey = typed
	case *ecdsa.PrivateKey:
		publicKey = &typed.PublicKey
	default:
		return fmt.Errorf("%w: ECDSA algorithm requires an ECDSA key", ErrInvalidSignature)
	}
	if publicKey.Curve == nil || publicKey.Curve.Params() == nil || publicKey.Curve.Params().BitSize != bits {
		return fmt.Errorf("%w: ECDSA key does not match algorithm curve", ErrInvalidSignature)
	}

	digest, err := hashInput(hashAlgorithm, signingInput)
	if err != nil {
		return err
	}

	partSize := (bits + 7) / 8
	if len(signature) != 2*partSize {
		return ErrInvalidSignature
	}
	r := new(big.Int).SetBytes(signature[:partSize])
	s := new(big.Int).SetBytes(signature[partSize:])
	if !ecdsa.Verify(publicKey, digest, r, s) {
		return ErrInvalidSignature
	}
	return nil
}

func verifyEd25519(key any, signingInput, signature []byte) error {
	var publicKey ed25519.PublicKey
	switch typed := key.(type) {
	case ed25519.PublicKey:
		publicKey = typed
	case ed25519.PrivateKey:
		publicKey = typed.Public().(ed25519.PublicKey)
	default:
		return fmt.Errorf("%w: EdDSA requires an Ed25519 key", ErrInvalidSignature)
	}
	if len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	if !ed25519.Verify(publicKey, signingInput, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func verifyHMAC(key any, hashAlgorithm crypto.Hash, signingInput, signature []byte) error {
	var secret []byte
	switch typed := key.(type) {
	case []byte:
		secret = typed
	case string:
		secret = []byte(typed)
	default:
		return fmt.Errorf("%w: HMAC algorithm requires []byte or string key", ErrInvalidSignature)
	}

	hashConstructor, err := hashFor(hashAlgorithm)
	if err != nil {
		return err
	}
	mac := hmac.New(hashConstructor, secret)
	_, _ = mac.Write(signingInput)
	if !hmac.Equal(mac.Sum(nil), signature) {
		return ErrInvalidSignature
	}
	return nil
}

func hashInput(hashAlgorithm crypto.Hash, input []byte) ([]byte, error) {
	constructor, err := hashFor(hashAlgorithm)
	if err != nil {
		return nil, err
	}
	hasher := constructor()
	_, _ = hasher.Write(input)
	return hasher.Sum(nil), nil
}

func hashFor(hashAlgorithm crypto.Hash) (func() hash.Hash, error) {
	switch hashAlgorithm {
	case crypto.SHA256:
		return sha256.New, nil
	case crypto.SHA384:
		return sha512.New384, nil
	case crypto.SHA512:
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("%w: hash %v", ErrUnsupportedAlgorithm, hashAlgorithm)
	}
}

// ContextKeyFunc is the key resolution callback used during validation. The
// token has not been authenticated when the callback is invoked; only use its
// header and claims to choose a key, never to authorize the request.
type ContextKeyFunc func(ctx context.Context, token *Token) (any, error)
