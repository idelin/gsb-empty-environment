package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// SignOptions controls compact JWS creation.
type SignOptions struct {
	Algorithm   string
	KeyID       string
	ContentType string
	Type        string
	B64         *bool
	Detached    bool
	Header      Header
	Extra       map[string]json.RawMessage
}

// SignOption mutates SignOptions.
type SignOption func(*SignOptions)

// WithSignAlgorithm selects the JWS algorithm explicitly.
func WithSignAlgorithm(algorithm string) SignOption {
	return func(options *SignOptions) {
		options.Algorithm = algorithm
	}
}

// WithSignKeyID sets the kid protected header.
func WithSignKeyID(keyID string) SignOption {
	return func(options *SignOptions) {
		options.KeyID = keyID
	}
}

// WithSignContentType sets the cty protected header.
func WithSignContentType(contentType string) SignOption {
	return func(options *SignOptions) {
		options.ContentType = contentType
	}
}

// WithSignType sets the typ protected header.
func WithSignType(tokenType string) SignOption {
	return func(options *SignOptions) {
		options.Type = tokenType
	}
}

// WithSignB64 controls RFC 7797 base64url payload encoding.
func WithSignB64(encoded bool) SignOption {
	return func(options *SignOptions) {
		options.B64 = &encoded
	}
}

// WithSignDetached marks the created token as detached.
func WithSignDetached() SignOption {
	return func(options *SignOptions) {
		options.Detached = true
	}
}

// WithSignHeader supplies additional protected header parameters.
func WithSignHeader(header Header) SignOption {
	return func(options *SignOptions) {
		options.Header = header
	}
}

// WithSignExtra supplies extension protected header parameters.
func WithSignExtra(extra map[string]json.RawMessage) SignOption {
	return func(options *SignOptions) {
		options.Extra = extra
	}
}

// Sign creates a compact JWS over payload.
func Sign(payload []byte, key any, options ...SignOption) (string, error) {
	config := SignOptions{}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return signCompact(payload, key, config)
}

// SignDetached creates a compact JWS with an empty payload segment. The
// payload is still covered by the signature.
func SignDetached(payload []byte, key any, options ...SignOption) (string, error) {
	config := SignOptions{Detached: true}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return signCompact(payload, key, config)
}

// SignCompact is an alias for Sign.
func SignCompact(payload []byte, key any, options ...SignOption) (string, error) {
	return Sign(payload, key, options...)
}

// SignCompactDetached is an alias for SignDetached.
func SignCompactDetached(payload []byte, key any, options ...SignOption) (string, error) {
	return SignDetached(payload, key, options...)
}

// SignJWS is an alias for Sign.
func SignJWS(payload []byte, key any, options ...SignOption) (string, error) {
	return Sign(payload, key, options...)
}

// SignDetachedJWS is an alias for SignDetached.
func SignDetachedJWS(payload []byte, key any, options ...SignOption) (string, error) {
	return SignDetached(payload, key, options...)
}

func signCompact(payload []byte, key any, options SignOptions) (string, error) {
	algorithm := options.Algorithm
	if algorithm == "" {
		algorithm = options.Header.Alg
	}
	if algorithm == "" {
		inferred, err := inferAlgorithm(key)
		if err != nil {
			return "", err
		}
		algorithm = inferred
	}
	if options.Header.Alg != "" && options.Algorithm != "" && options.Header.Alg != options.Algorithm {
		return "", fmt.Errorf("%w: conflicting signing algorithms", ErrMalformed)
	}

	b64 := true
	if options.B64 != nil {
		b64 = *options.B64
	} else if options.Header.B64 != nil {
		b64 = *options.Header.B64
	} else if raw, ok := options.Extra["b64"]; ok {
		var encoded bool
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return "", fmt.Errorf("%w: b64 extension header is not a boolean", ErrMalformed)
		}
		b64 = encoded
	}

	headerMap := make(map[string]any)
	for name, value := range options.Extra {
		headerMap[name] = value
	}
	headerMap["alg"] = algorithm

	tokenType := options.Type
	if tokenType == "" {
		tokenType = options.Header.Typ
	}
	if tokenType != "" {
		headerMap["typ"] = tokenType
	}

	contentType := options.ContentType
	if contentType == "" {
		contentType = options.Header.Cty
	}
	if contentType != "" {
		headerMap["cty"] = contentType
	}

	keyID := options.KeyID
	if keyID == "" {
		keyID = options.Header.Kid
	}
	if keyID != "" {
		headerMap["kid"] = keyID
	}

	critical := append([]string(nil), options.Header.Crit...)
	if !b64 {
		critical = appendUnique(critical, "b64")
		headerMap["b64"] = false
	} else if options.B64 != nil || options.Header.B64 != nil {
		headerMap["b64"] = true
	}
	if len(critical) > 0 {
		headerMap["crit"] = critical
	}

	headerJSON, err := json.Marshal(headerMap)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	protected := base64.RawURLEncoding.EncodeToString(headerJSON)

	signingInput := detachedSigningInput(protected, payload, b64)
	signature, err := createSignature(algorithm, key, signingInput)
	if err != nil {
		return "", err
	}

	payloadSegment := ""
	if !options.Detached {
		if b64 {
			payloadSegment = base64.RawURLEncoding.EncodeToString(payload)
		} else {
			payloadSegment = string(payload)
		}
	}
	return protected + "." + payloadSegment + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func inferAlgorithm(key any) (string, error) {
	switch value := key.(type) {
	case []byte:
		if looksLikePEM(value) {
			parsed, err := parseAsymmetricKey(value)
			if err != nil {
				return "", err
			}
			return inferAlgorithm(parsed)
		}
		return "HS256", nil
	case string:
		if strings.Contains(value, "-----BEGIN ") {
			parsed, err := parseAsymmetricKey([]byte(value))
			if err != nil {
				return "", err
			}
			return inferAlgorithm(parsed)
		}
		return "HS256", nil
	case *rsa.PrivateKey, rsa.PrivateKey:
		return "RS256", nil
	case *ecdsa.PrivateKey, ecdsa.PrivateKey:
		privateKey, err := ecdsaPrivateKey(key)
		if err != nil {
			return "", err
		}
		switch privateKey.Curve.Params().BitSize {
		case 256:
			return "ES256", nil
		case 384:
			return "ES384", nil
		case 521:
			return "ES512", nil
		default:
			return "", fmt.Errorf("%w: unsupported ECDSA curve", ErrInvalidKey)
		}
	case ed25519.PrivateKey:
		return "EdDSA", nil
	case crypto.Signer:
		return inferAlgorithm(value.Public())
	case *rsa.PublicKey:
		return "RS256", nil
	case *ecdsa.PublicKey:
		if value == nil || value.Curve == nil {
			return "", fmt.Errorf("%w: invalid ECDSA public key", ErrInvalidKey)
		}
		switch value.Curve.Params().BitSize {
		case 256:
			return "ES256", nil
		case 384:
			return "ES384", nil
		case 521:
			return "ES512", nil
		}
		return "", fmt.Errorf("%w: unsupported ECDSA curve", ErrInvalidKey)
	case ed25519.PublicKey:
		return "EdDSA", nil
	default:
		return "", fmt.Errorf("%w: cannot infer a signing algorithm from %T", ErrInvalidKey, key)
	}
}

func createSignature(algorithm string, key any, signingInput []byte) ([]byte, error) {
	info, ok := supportedAlgorithms[algorithm]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
	if !info.hash.Available() && info.family != familyEdDSA {
		return nil, fmt.Errorf("%w: hash for %q is unavailable", ErrUnsupportedAlgorithm, algorithm)
	}
	if info.family != familyHMAC {
		switch value := key.(type) {
		case []byte:
			parsed, err := parseAsymmetricKey(value)
			if err != nil {
				return nil, err
			}
			key = parsed
		case string:
			parsed, err := parseAsymmetricKey([]byte(value))
			if err != nil {
				return nil, err
			}
			key = parsed
		}
	}

	switch info.family {
	case familyHMAC:
		secret, err := hmacSecret(key)
		if err != nil {
			return nil, err
		}
		return signHMAC(secret, info.hash, signingInput), nil

	case familyRSA:
		return signRSA(algorithm, info, key, signingInput)

	case familyECDSA:
		return signECDSA(info, key, signingInput)

	case familyEdDSA:
		return signEd25519(key, signingInput)
	}
	return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
}

func signRSA(algorithm string, info algorithmInfo, key any, signingInput []byte) ([]byte, error) {
	digest := hashBytes(info.hash, signingInput)

	if privateKey, ok := key.(*rsa.PrivateKey); ok {
		if privateKey == nil {
			return nil, fmt.Errorf("%w: invalid RSA private key", ErrInvalidKey)
		}
		if info.pss {
			return rsa.SignPSS(rand.Reader, privateKey, info.hash, digest, &rsa.PSSOptions{
				SaltLength: rsa.PSSSaltLengthEqualsHash,
				Hash:       info.hash,
			})
		}
		return rsa.SignPKCS1v15(rand.Reader, privateKey, info.hash, digest)
	}

	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("%w: %s requires an RSA private key", ErrInvalidKey, algorithm)
	}
	if _, ok := signer.Public().(*rsa.PublicKey); !ok {
		return nil, fmt.Errorf("%w: %s requires an RSA private key", ErrInvalidKey, algorithm)
	}
	if info.pss {
		return signer.Sign(rand.Reader, digest, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       info.hash,
		})
	}
	return signer.Sign(rand.Reader, digest, info.hash)
}

func signECDSA(info algorithmInfo, key any, signingInput []byte) ([]byte, error) {
	privateKey, err := ecdsaPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if privateKey.Curve == nil {
		return nil, fmt.Errorf("%w: invalid ECDSA private key", ErrInvalidKey)
	}
	if privateKey.Curve.Params().BitSize != info.curveBits {
		return nil, fmt.Errorf("%w: curve has %d bits, algorithm requires %d", ErrInvalidKey, privateKey.Curve.Params().BitSize, info.curveBits)
	}

	digest := hashBytes(info.hash, signingInput)
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}

	componentSize := (info.curveBits + 7) / 8
	signature := make([]byte, 2*componentSize)
	r.FillBytes(signature[:componentSize])
	s.FillBytes(signature[componentSize:])
	return signature, nil
}

func ecdsaPrivateKey(key any) (*ecdsa.PrivateKey, error) {
	switch value := key.(type) {
	case *ecdsa.PrivateKey:
		if value == nil || value.Curve == nil {
			return nil, fmt.Errorf("%w: invalid ECDSA private key", ErrInvalidKey)
		}
		return value, nil
	case ecdsa.PrivateKey:
		return &value, nil
	case crypto.Signer:
		if _, ok := value.Public().(*ecdsa.PublicKey); !ok {
			return nil, fmt.Errorf("%w: ECDSA signing requires an ECDSA private key", ErrInvalidKey)
		}
		return nil, fmt.Errorf("%w: generic ECDSA signer is not supported; supply *ecdsa.PrivateKey", ErrInvalidKey)
	default:
		return nil, fmt.Errorf("%w: ECDSA signing requires an ECDSA private key", ErrInvalidKey)
	}
}

func signEd25519(key any, message []byte) ([]byte, error) {
	switch value := key.(type) {
	case ed25519.PrivateKey:
		if len(value) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%w: invalid Ed25519 private key", ErrInvalidKey)
		}
		return ed25519.Sign(value, message), nil
	case crypto.Signer:
		if _, ok := value.Public().(ed25519.PublicKey); !ok {
			return nil, fmt.Errorf("%w: EdDSA signing requires an Ed25519 private key", ErrInvalidKey)
		}
		return value.Sign(rand.Reader, message, crypto.Hash(0))
	default:
		return nil, fmt.Errorf("%w: EdDSA signing requires an Ed25519 private key", ErrInvalidKey)
	}
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
