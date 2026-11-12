package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/base64"
	"math/big"
	"strings"
)

type parsedJWS struct {
	protected    map[string]any
	payload      []byte
	signature    []byte
	signingInput string
}

func parseJWS(token string) (*parsedJWS, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errorf(ErrMalformedJOSE, "a JWS must have three segments")
	}
	headerBytes, err := decodeBase64URL(parts[0])
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, "invalid protected header encoding")
	}
	header, err := decodeJSONObject(headerBytes)
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, err.Error())
	}
	payload, err := decodeBase64URL(parts[1])
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, "invalid payload encoding")
	}
	signature, err := decodeBase64URL(parts[2])
	if err != nil {
		return nil, errorf(ErrMalformedJOSE, "invalid signature encoding")
	}
	return &parsedJWS{
		protected:    header,
		payload:      payload,
		signature:    signature,
		signingInput: parts[0] + "." + parts[1],
	}, nil
}

func decodeBase64URL(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

func encodeBase64URL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func (p *Parser) verifyJWS(token string, client *Client) ([]byte, map[string]any, error) {
	jws, err := parseJWS(token)
	if err != nil {
		return nil, nil, err
	}

	algValue, ok := jws.protected["alg"]
	if !ok {
		return nil, nil, errorf(ErrMalformedJOSE, "protected header has no alg")
	}
	alg, ok := algValue.(string)
	if !ok || alg == "" {
		return nil, nil, errorf(ErrMalformedJOSE, "alg is not a string")
	}
	if alg == "none" {
		return nil, nil, errorf(ErrAlgorithmNotAllowed, "alg=none is forbidden")
	}
	if !supportedJWSAlgorithms[alg] {
		return nil, nil, errorf(ErrUnsupportedJWSAlg, alg)
	}
	if !clientAllowsAlgorithm(client, alg) {
		return nil, nil, errorf(ErrAlgorithmNotAllowed, alg+" is not registered for client "+client.ClientID)
	}
	if _, exists := jws.protected["crit"]; exists {
		return nil, nil, errorf(ErrMalformedJOSE, "critical header extensions are not supported")
	}
	if _, exists := jws.protected["b64"]; exists {
		return nil, nil, errorf(ErrMalformedJOSE, "unencoded JWS payloads are not supported")
	}

	var kid string
	if kidValue, exists := jws.protected["kid"]; exists {
		var ok bool
		kid, ok = kidValue.(string)
		if !ok {
			return nil, nil, errorf(ErrMalformedJOSE, "kid is not a string")
		}
	}

	keys := selectKeys(client.RequestObjectSigningKeys, kid)
	if len(keys) == 0 {
		return nil, nil, ErrInvalidSigningKey
	}
	for _, key := range keys {
		if verifySignature(alg, keyValue(key), []byte(jws.signingInput), jws.signature) {
			return jws.payload, jws.protected, nil
		}
	}
	return nil, nil, ErrSignatureInvalid
}

func clientAllowsAlgorithm(client *Client, alg string) bool {
	if client == nil {
		return false
	}
	for _, allowed := range client.RequestObjectSigningAlgorithms {
		if strings.TrimSpace(allowed) == alg {
			return true
		}
	}
	return false
}

func keyValue(key any) any {
	if named, ok := key.(Key); ok {
		return named.Value
	}
	if named, ok := key.(*Key); ok && named != nil {
		return named.Value
	}
	return key
}

func keyID(key any) string {
	switch named := key.(type) {
	case Key:
		return named.ID
	case *Key:
		if named != nil {
			return named.ID
		}
	}
	return ""
}

// selectKeys honors kid when the registration carries key identifiers. Raw
// keys are tried as a set because they have no identifier of their own.
func selectKeys(keys []any, kid string) []any {
	if kid == "" {
		return keys
	}
	var matched []any
	hasNamed := false
	for _, key := range keys {
		if id := keyID(key); id != "" {
			hasNamed = true
			if id == kid {
				matched = append(matched, key)
			}
		}
	}
	if len(matched) > 0 {
		return matched
	}
	if hasNamed {
		return nil
	}
	return keys
}

func verifySignature(alg string, key any, signingInput, signature []byte) bool {
	switch alg {
	case "HS256", "HS384", "HS512":
		secret, ok := keyAsBytes(key)
		if !ok {
			return false
		}
		hash, minKeySize := hmacParameters(alg)
		if len(secret) < minKeySize {
			return false
		}
		mac := hmac.New(hash.New, secret)
		mac.Write(signingInput)
		return hmac.Equal(mac.Sum(nil), signature)
	case "RS256", "RS384", "RS512":
		publicKey, ok := keyAsRSAPublic(key)
		if !ok {
			return false
		}
		hash := rsaHash(alg)
		digest, err := hashBytes(hash, signingInput)
		if err != nil {
			return false
		}
		return rsa.VerifyPKCS1v15(publicKey, hash, digest, signature) == nil
	case "PS256", "PS384", "PS512":
		publicKey, ok := keyAsRSAPublic(key)
		if !ok {
			return false
		}
		hash := rsaHash(alg)
		digest, err := hashBytes(hash, signingInput)
		if err != nil {
			return false
		}
		return rsa.VerifyPSS(publicKey, hash, digest, signature, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       hash,
		}) == nil
	case "ES256", "ES384", "ES512":
		publicKey, ok := keyAsECDSAPublic(key)
		if !ok {
			return false
		}
		curve, size, hash := ecdsaParameters(alg)
		if publicKey.Curve != curve {
			return false
		}
		der, ok := ecdsaSignatureToDER(signature, size)
		if !ok {
			return false
		}
		digest, err := hashBytes(hash, signingInput)
		if err != nil {
			return false
		}
		return ecdsa.VerifyASN1(publicKey, digest, der)
	case "EdDSA":
		publicKey, ok := keyAsEd25519Public(key)
		if !ok {
			return false
		}
		return ed25519.Verify(publicKey, signingInput, signature)
	default:
		return false
	}
}

func keyAsBytes(key any) ([]byte, bool) {
	switch typed := key.(type) {
	case []byte:
		return typed, true
	case string:
		return []byte(typed), true
	}
	return nil, false
}

func keyAsRSAPublic(key any) (*rsa.PublicKey, bool) {
	switch typed := key.(type) {
	case *rsa.PublicKey:
		return typed, typed != nil
	case rsa.PublicKey:
		return &typed, true
	case *rsa.PrivateKey:
		if typed == nil {
			return nil, false
		}
		return &typed.PublicKey, true
	}
	return nil, false
}

func keyAsECDSAPublic(key any) (*ecdsa.PublicKey, bool) {
	switch typed := key.(type) {
	case *ecdsa.PublicKey:
		return typed, typed != nil
	case ecdsa.PublicKey:
		return &typed, true
	case *ecdsa.PrivateKey:
		if typed == nil {
			return nil, false
		}
		return &typed.PublicKey, true
	}
	return nil, false
}

func keyAsEd25519Public(key any) (ed25519.PublicKey, bool) {
	switch typed := key.(type) {
	case ed25519.PublicKey:
		return typed, len(typed) == ed25519.PublicKeySize
	case *ed25519.PrivateKey:
		if typed == nil {
			return nil, false
		}
		public, ok := typed.Public().(ed25519.PublicKey)
		return public, ok
	case ed25519.PrivateKey:
		public, ok := typed.Public().(ed25519.PublicKey)
		return public, ok
	}
	return nil, false
}

func hmacParameters(alg string) (crypto.Hash, int) {
	switch alg {
	case "HS256":
		return crypto.SHA256, sha256.Size
	case "HS384":
		return crypto.SHA384, sha512.Size384
	default:
		return crypto.SHA512, sha512.Size
	}
}

func rsaHash(alg string) crypto.Hash {
	switch alg {
	case "RS256", "PS256":
		return crypto.SHA256
	case "RS384", "PS384":
		return crypto.SHA384
	default:
		return crypto.SHA512
	}
}

func hashBytes(hash crypto.Hash, data []byte) ([]byte, error) {
	h := hash.New()
	h.Write(data)
	return h.Sum(nil), nil
}

func ecdsaParameters(alg string) (elliptic.Curve, int, crypto.Hash) {
	switch alg {
	case "ES256":
		return elliptic.P256(), 32, crypto.SHA256
	case "ES384":
		return elliptic.P384(), 48, crypto.SHA384
	default:
		return elliptic.P521(), 66, crypto.SHA512
	}
}

func ecdsaSignatureToDER(signature []byte, coordinateSize int) ([]byte, bool) {
	if len(signature) != coordinateSize*2 {
		return nil, false
	}
	r := new(big.Int).SetBytes(signature[:coordinateSize])
	s := new(big.Int).SetBytes(signature[coordinateSize:])
	if r.Sign() <= 0 || s.Sign() <= 0 {
		return nil, false
	}
	der, err := asn1.Marshal(struct {
		R *big.Int
		S *big.Int
	}{R: r, S: s})
	if err != nil {
		return nil, false
	}
	return der, true
}
