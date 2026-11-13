package netsec

import (
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"strings"
)

// KeyFunc selects a verification key from the protected header, for example
// based on the kid parameter.
type KeyFunc func(Header) (any, error)

// VerifyOptions controls verification. The zero value verifies the token's
// own payload and accepts any implemented algorithm.
type VerifyOptions struct {
	Key      any
	KeyFunc  KeyFunc
	Detached bool

	// DetachedPayload is the externally supplied payload. It is only used
	// when Detached is true. Supplying it without Detached is an error.
	DetachedPayload []byte
	// Payload is an alias for DetachedPayload.
	Payload []byte
	// ExternalPayload is an alias for DetachedPayload.
	ExternalPayload []byte

	// ContentType is the caller's declared type for the external payload.
	// When non-empty, the protected header must carry a matching cty or
	// payload-type hint.
	ContentType string
	// PayloadType is an alias for ContentType.
	PayloadType string
	// ExternalType is an alias for ContentType.
	ExternalType string

	// RequireContentType requires a type hint even when ContentType is
	// empty.
	RequireContentType bool

	Algorithms        []string
	AllowedAlgorithms []string
}

// VerifyOption mutates VerifyOptions.
type VerifyOption func(*VerifyOptions)

// WithDetachedPayload declares detached mode and supplies the external payload.
func WithDetachedPayload(payload []byte) VerifyOption {
	return func(options *VerifyOptions) {
		options.Detached = true
		options.DetachedPayload = payload
	}
}

// WithDetached is an alias for WithDetachedPayload.
func WithDetached(payload []byte) VerifyOption {
	return WithDetachedPayload(payload)
}

// WithExternalPayload is an alias for WithDetachedPayload.
func WithExternalPayload(payload []byte) VerifyOption {
	return WithDetachedPayload(payload)
}

// WithPayload is an alias for WithDetachedPayload.
func WithPayload(payload []byte) VerifyOption {
	return WithDetachedPayload(payload)
}

// WithDetachedPayloadType declares detached mode and pins the external
// payload's type.
func WithDetachedPayloadType(payload []byte, contentType string) VerifyOption {
	return func(options *VerifyOptions) {
		options.Detached = true
		options.DetachedPayload = payload
		options.ContentType = contentType
	}
}

// WithContentType asserts the type of the authenticated payload.
func WithContentType(contentType string) VerifyOption {
	return func(options *VerifyOptions) {
		options.ContentType = contentType
	}
}

// WithPayloadType is an alias for WithContentType.
func WithPayloadType(contentType string) VerifyOption {
	return WithContentType(contentType)
}

// WithExternalType is an alias for WithContentType.
func WithExternalType(contentType string) VerifyOption {
	return WithContentType(contentType)
}

// WithRequireContentType requires the protected header to carry a type hint.
func WithRequireContentType(required bool) VerifyOption {
	return func(options *VerifyOptions) {
		options.RequireContentType = required
	}
}

// WithAlgorithms restricts verification to the supplied JWS algorithms.
func WithAlgorithms(algorithms ...string) VerifyOption {
	return func(options *VerifyOptions) {
		options.Algorithms = append(options.Algorithms, algorithms...)
	}
}

// WithAllowedAlgorithms is an alias for WithAlgorithms.
func WithAllowedAlgorithms(algorithms ...string) VerifyOption {
	return WithAlgorithms(algorithms...)
}

// WithKeyFunc selects the key from the protected header.
func WithKeyFunc(keyFunc KeyFunc) VerifyOption {
	return func(options *VerifyOptions) {
		options.KeyFunc = keyFunc
	}
}

// WithKey sets the verification key through an option.
func WithKey(key any) VerifyOption {
	return func(options *VerifyOptions) {
		options.Key = key
	}
}

// Verify parses and verifies a compact JWS.
//
// Arguments after the token may contain a verification key and/or options.
// A caller-supplied external payload is used only when detached mode is
// explicitly selected with WithDetachedPayload or VerifyOptions.Detached.
func Verify(compact string, args ...any) (*Token, error) {
	options, key, err := collectVerifyArgs(args)
	if err != nil {
		return nil, err
	}
	return verifyCompact(compact, key, options)
}

// VerifyCompact is an alias for Verify.
func VerifyCompact(compact string, args ...any) (*Token, error) {
	return Verify(compact, args...)
}

// VerifyJWS is an alias for Verify.
func VerifyJWS(compact string, args ...any) (*Token, error) {
	return Verify(compact, args...)
}

// VerifyCompactJWS is an alias for Verify.
func VerifyCompactJWS(compact string, args ...any) (*Token, error) {
	return Verify(compact, args...)
}

// VerifyToken is an alias for Verify.
func VerifyToken(compact string, args ...any) (*Token, error) {
	return Verify(compact, args...)
}

// VerifyDetached verifies a compact JWS whose payload is supplied externally.
//
// The first byte-slice argument is the external payload. An optional string
// argument is the external payload's type. A token that carries any non-empty
// payload segment is rejected. The type is checked against the protected
// header's cty or payload-type hint.
func VerifyDetached(compact string, args ...any) (*Token, error) {
	options, key, err := collectDetachedArgs(args)
	if err != nil {
		return nil, err
	}
	options.Detached = true
	return verifyCompact(compact, key, options)
}

// VerifyDetachedJWS is an alias for VerifyDetached.
func VerifyDetachedJWS(compact string, args ...any) (*Token, error) {
	return VerifyDetached(compact, args...)
}

// VerifyDetachedCompact is an alias for VerifyDetached.
func VerifyDetachedCompact(compact string, args ...any) (*Token, error) {
	return VerifyDetached(compact, args...)
}

// VerifyCompactDetached is an alias for VerifyDetached.
func VerifyCompactDetached(compact string, args ...any) (*Token, error) {
	return VerifyDetached(compact, args...)
}

// VerifyDetachedCompactJWS is an alias for VerifyDetached.
func VerifyDetachedCompactJWS(compact string, args ...any) (*Token, error) {
	return VerifyDetached(compact, args...)
}

// VerifyDetachedPayload is an alias for VerifyDetached.
func VerifyDetachedPayload(compact string, args ...any) (*Token, error) {
	return VerifyDetached(compact, args...)
}

// VerifyDetachedBytes verifies a detached JWS and returns only its payload.
func VerifyDetachedBytes(compact string, args ...any) ([]byte, error) {
	token, err := VerifyDetached(compact, args...)
	if err != nil {
		return nil, err
	}
	return token.Payload, nil
}

// VerifyPayload verifies a compact JWS and returns only its payload.
func VerifyPayload(compact string, args ...any) ([]byte, error) {
	token, err := Verify(compact, args...)
	if err != nil {
		return nil, err
	}
	return token.Payload, nil
}

// VerifyBytes is an alias for VerifyPayload.
func VerifyBytes(compact string, args ...any) ([]byte, error) {
	return VerifyPayload(compact, args...)
}

// Validate verifies a compact JWS and returns only an error.
func Validate(compact string, args ...any) error {
	_, err := Verify(compact, args...)
	return err
}

// Verify verifies the parsed token's signature. The token's own payload is
// used unless detached options are supplied.
func (t *Token) Verify(key any, args ...any) error {
	if t == nil {
		return fmt.Errorf("%w: nil token", ErrMalformed)
	}
	all := []any{key}
	if t.Detached {
		all = append(all, WithDetachedPayload(t.Payload))
	}
	all = append(all, args...)
	_, err := Verify(t.Compact(), all...)
	return err
}

// VerifyDetached verifies the parsed token with an external payload.
func (t *Token) VerifyDetached(payload []byte, args ...any) error {
	if t == nil {
		return fmt.Errorf("%w: nil token", ErrMalformed)
	}
	all := append([]any{payload}, args...)
	_, err := VerifyDetached(t.Compact(), all...)
	return err
}

// VerifyPayload verifies the parsed token and returns its payload.
func (t *Token) VerifyPayload(key any, args ...any) ([]byte, error) {
	if err := t.Verify(key, args...); err != nil {
		return nil, err
	}
	return t.Payload, nil
}

// Check is an alias for Validate.
func Check(compact string, args ...any) error {
	return Validate(compact, args...)
}

func verifyCompact(compact string, key any, options VerifyOptions) (*Token, error) {
	token, err := parseCompact(compact)
	if err != nil {
		return nil, err
	}

	if err := checkCriticalHeaders(token.Header); err != nil {
		return nil, err
	}

	if options.DetachedPayload != nil && !options.Detached {
		return nil, ErrDetachedNotDeclared
	}

	var payload []byte
	var signingInput []byte
	if options.Detached {
		if token.PayloadSegment != "" {
			return nil, fmt.Errorf("%w: payload segment must be empty", ErrEmbeddedPayload)
		}
		payload = options.DetachedPayload
		if payload == nil {
			payload = []byte{}
		}
		signingInput = detachedSigningInput(token.Protected, payload, token.Header.B64Encoded())
		token.Detached = true
	} else {
		payload = token.Payload
		signingInput = []byte(token.Protected + "." + token.PayloadSegment)
		token.Detached = false
	}

	if err := checkAlgorithmAllowed(token.Header.Alg, options); err != nil {
		return nil, err
	}
	if _, ok := supportedAlgorithms[token.Header.Alg]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, token.Header.Alg)
	}

	key, err = resolveVerificationKey(key, options, token.Header)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(token.Header.Alg, key, signingInput, token.Signature); err != nil {
		if !options.Detached && token.PayloadSegment == "" && errors.Is(err, ErrInvalidSignature) {
			return nil, errors.Join(ErrDetachedPayloadRequired, err)
		}
		return nil, err
	}
	if err := checkContentType(token.Header, options.ContentType, options.RequireContentType); err != nil {
		return nil, err
	}

	token.Payload = payload
	return token, nil
}

func resolveVerificationKey(key any, options VerifyOptions, header Header) (any, error) {
	if options.KeyFunc != nil {
		return options.KeyFunc(header)
	}
	if key == nil {
		key = options.Key
	}
	switch value := key.(type) {
	case KeyFunc:
		return value(header)
	case func(Header) (any, error):
		return value(header)
	case func(Header) any:
		return value(header), nil
	case nil:
		return nil, fmt.Errorf("%w: no verification key", ErrInvalidKey)
	default:
		return key, nil
	}
}

func checkAlgorithmAllowed(algorithm string, options VerifyOptions) error {
	allowed := options.Algorithms
	if len(options.AllowedAlgorithms) > 0 {
		allowed = options.AllowedAlgorithms
	}
	if len(allowed) == 0 {
		return nil
	}
	for _, candidate := range allowed {
		if candidate == algorithm {
			return nil
		}
	}
	return fmt.Errorf("%w: %q", ErrAlgorithmNotAllowed, algorithm)
}

func checkCriticalHeaders(header Header) error {
	for _, name := range header.Crit {
		if name != "b64" {
			return fmt.Errorf("%w: %q", ErrUnsupportedCritical, name)
		}
	}
	return nil
}

func detachedSigningInput(protected string, payload []byte, b64 bool) []byte {
	if b64 {
		return []byte(protected + "." + base64.RawURLEncoding.EncodeToString(payload))
	}

	input := make([]byte, 0, len(protected)+1+len(payload))
	input = append(input, protected...)
	input = append(input, '.')
	input = append(input, payload...)
	return input
}

func checkContentType(header Header, expected string, required bool) error {
	hint := header.TypeHint()
	if expected == "" {
		if required && hint == "" {
			return ErrMissingContentType
		}
		return nil
	}
	if hint == "" {
		return fmt.Errorf("%w: header has no cty or payload type hint", ErrMissingContentType)
	}
	if !contentTypeEqual(hint, expected) {
		return fmt.Errorf("%w: header has %q, external payload is %q", ErrContentTypeMismatch, hint, expected)
	}
	return nil
}

func contentTypeEqual(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if strings.EqualFold(left, right) {
		return true
	}

	leftMediaType, leftParameters, leftErr := mime.ParseMediaType(left)
	rightMediaType, rightParameters, rightErr := mime.ParseMediaType(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if !strings.EqualFold(leftMediaType, rightMediaType) {
		return false
	}
	if len(leftParameters) != len(rightParameters) {
		return false
	}
	for name, value := range leftParameters {
		other, ok := rightParameters[name]
		if !ok || !strings.EqualFold(value, other) {
			return false
		}
	}
	return true
}

func collectVerifyArgs(args []any) (VerifyOptions, any, error) {
	var options VerifyOptions
	var key any
	keySet := false
	boolFlagSeen := false

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			continue
		case bool:
			options.Detached = value
			boolFlagSeen = true
		case VerifyOption:
			value(&options)
		case []VerifyOption:
			for _, option := range value {
				if option != nil {
					option(&options)
				}
			}
		case VerifyOptions:
			mergeVerifyOptions(&options, value)
		case *VerifyOptions:
			if value != nil {
				mergeVerifyOptions(&options, *value)
			}
		case KeyFunc:
			options.KeyFunc = value
		case func(Header) (any, error):
			options.KeyFunc = value
		default:
			if options.Detached && options.DetachedPayload == nil && (boolFlagSeen || keySet) {
				if payload, ok := asPayloadBytes(value); ok {
					options.DetachedPayload = payload
					continue
				}
			}
			if options.Detached && options.DetachedPayload != nil && options.ContentType == "" {
				if contentType, ok := value.(string); ok && looksLikeContentType(contentType) {
					options.ContentType = contentType
					continue
				}
			}
			if keySet && options.ContentType == "" {
				if contentType, ok := value.(string); ok {
					options.ContentType = contentType
					continue
				}
			}
			if keySet {
				if _, ok := asPayloadBytes(value); ok {
					return options, nil, ErrDetachedNotDeclared
				}
				return options, nil, fmt.Errorf("%w: multiple verification keys", ErrInvalidKey)
			}
			key = value
			keySet = true
		}
	}

	if !keySet {
		key = options.Key
	}
	return options, key, nil
}

func collectDetachedArgs(args []any) (VerifyOptions, any, error) {
	var options VerifyOptions
	var positional []any

	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
			continue
		case bool:
			if !value {
				return options, nil, ErrDetachedNotDeclared
			}
		case VerifyOption:
			value(&options)
		case []VerifyOption:
			for _, option := range value {
				if option != nil {
					option(&options)
				}
			}
		case VerifyOptions:
			mergeVerifyOptions(&options, value)
		case *VerifyOptions:
			if value != nil {
				mergeVerifyOptions(&options, *value)
			}
		case KeyFunc:
			options.KeyFunc = value
		case func(Header) (any, error):
			options.KeyFunc = value
		default:
			positional = append(positional, value)
		}
	}

	if options.DetachedPayload == nil {
		payloadIndex := -1
		for index, value := range positional {
			if _, ok := asPayloadBytes(value); ok {
				payloadIndex = index
				break
			}
		}
		if payloadIndex < 0 {
			return options, nil, fmt.Errorf("%w: missing external payload", ErrDetachedPayloadRequired)
		}
		options.DetachedPayload, _ = asPayloadBytes(positional[payloadIndex])
		positional = append(positional[:payloadIndex], positional[payloadIndex+1:]...)
	}

	var stringsArgs []string
	var keyCandidates []any
	for _, value := range positional {
		if text, ok := value.(string); ok {
			stringsArgs = append(stringsArgs, text)
		} else {
			keyCandidates = append(keyCandidates, value)
		}
	}

	key := options.Key
	keySet := key != nil

	switch {
	case len(stringsArgs) == 0:
		if len(keyCandidates) > 1 {
			return options, nil, fmt.Errorf("%w: multiple verification keys", ErrInvalidKey)
		}
	case len(stringsArgs) == 1:
		if len(keyCandidates) > 0 {
			options.ContentType = stringsArgs[0]
		} else if keySet || options.KeyFunc != nil {
			options.ContentType = stringsArgs[0]
		} else if looksLikeContentType(stringsArgs[0]) {
			options.ContentType = stringsArgs[0]
		} else {
			key = stringsArgs[0]
			keySet = true
		}
	case len(stringsArgs) == 2:
		if len(keyCandidates) > 0 {
			return options, nil, fmt.Errorf("%w: too many detached verification arguments", ErrInvalidKey)
		}
		if looksLikeContentType(stringsArgs[1]) && !looksLikeContentType(stringsArgs[0]) {
			stringsArgs[0], stringsArgs[1] = stringsArgs[1], stringsArgs[0]
		}
		if options.ContentType != "" && keySet {
			return options, nil, fmt.Errorf("%w: too many detached verification arguments", ErrInvalidKey)
		}
		if options.ContentType == "" {
			options.ContentType = stringsArgs[0]
		}
		if !keySet && options.KeyFunc == nil {
			key = stringsArgs[1]
			keySet = true
		}
	default:
		return options, nil, fmt.Errorf("%w: too many detached verification arguments", ErrInvalidKey)
	}

	if !keySet && len(keyCandidates) > 0 {
		key = keyCandidates[0]
		keySet = true
	}
	if !keySet && options.KeyFunc == nil {
		return options, nil, fmt.Errorf("%w: no verification key", ErrInvalidKey)
	}
	return options, key, nil
}

// Verifier binds a key and default options for repeated verification.
type Verifier struct {
	Key     any
	KeyFunc KeyFunc
	Options VerifyOptions
}

// NewVerifier creates a Verifier for key. Options are applied as defaults.
func NewVerifier(key any, options ...VerifyOption) *Verifier {
	verifier := &Verifier{Key: key}
	for _, option := range options {
		if option != nil {
			option(&verifier.Options)
		}
	}
	return verifier
}

// Verify verifies a token using the verifier's key and options.
func (v *Verifier) Verify(compact string, args ...any) (*Token, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil verifier", ErrInvalidKey)
	}
	all := []any{v.Options}
	if v.KeyFunc != nil {
		all = append(all, WithKeyFunc(v.KeyFunc))
	}
	if v.Key != nil {
		all = append(all, WithKey(v.Key))
	}
	all = append(all, args...)
	return Verify(compact, all...)
}

// VerifyDetached verifies a detached token using the verifier's key and
// options. The first byte-slice argument is the external payload.
func (v *Verifier) VerifyDetached(compact string, args ...any) (*Token, error) {
	if v == nil {
		return nil, fmt.Errorf("%w: nil verifier", ErrInvalidKey)
	}
	all := []any{v.Options}
	if v.KeyFunc != nil {
		all = append(all, WithKeyFunc(v.KeyFunc))
	}
	if v.Key != nil {
		all = append(all, WithKey(v.Key))
	}
	all = append(all, args...)
	return VerifyDetached(compact, all...)
}

// VerifyDetachedPayload is an alias for Verifier.VerifyDetached.
func (v *Verifier) VerifyDetachedPayload(compact string, args ...any) (*Token, error) {
	return v.VerifyDetached(compact, args...)
}

func looksLikeContentType(value string) bool {
	if strings.Contains(value, "/") {
		return true
	}
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "JWT", "JOSE", "JWS", "JWE", "DOT", "CWT", "JSON", "CBOR":
		return true
	default:
		return false
	}
}

func mergeVerifyOptions(destination *VerifyOptions, source VerifyOptions) {
	if source.Key != nil {
		destination.Key = source.Key
	}
	if source.KeyFunc != nil {
		destination.KeyFunc = source.KeyFunc
	}
	if source.Detached {
		destination.Detached = true
	}
	if source.DetachedPayload != nil {
		destination.DetachedPayload = source.DetachedPayload
	}
	if source.Payload != nil {
		destination.DetachedPayload = source.Payload
	}
	if source.ExternalPayload != nil {
		destination.DetachedPayload = source.ExternalPayload
	}
	if source.ContentType != "" {
		destination.ContentType = source.ContentType
	}
	if source.PayloadType != "" {
		destination.ContentType = source.PayloadType
	}
	if source.ExternalType != "" {
		destination.ContentType = source.ExternalType
	}
	if source.RequireContentType {
		destination.RequireContentType = true
	}
	destination.Algorithms = append(destination.Algorithms, source.Algorithms...)
	destination.AllowedAlgorithms = append(destination.AllowedAlgorithms, source.AllowedAlgorithms...)
}

func verifySignature(algorithm string, key any, signingInput, signature []byte) error {
	info, ok := supportedAlgorithms[algorithm]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
	if info.family != familyEdDSA && !info.hash.Available() {
		return fmt.Errorf("%w: hash for %q is unavailable", ErrUnsupportedAlgorithm, algorithm)
	}

	switch info.family {
	case familyHMAC:
		secret, err := hmacSecret(key)
		if err != nil {
			return err
		}
		actual := signHMAC(secret, info.hash, signingInput)
		if !hmac.Equal(signature, actual) {
			return ErrInvalidSignature
		}
		return nil

	case familyRSA:
		publicKey, err := rsaVerificationKey(key)
		if err != nil {
			return err
		}
		digest := hashBytes(info.hash, signingInput)
		if info.pss {
			if err := rsa.VerifyPSS(publicKey, info.hash, digest, signature, &rsa.PSSOptions{
				SaltLength: rsa.PSSSaltLengthAuto,
				Hash:       info.hash,
			}); err != nil {
				return ErrInvalidSignature
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(publicKey, info.hash, digest, signature); err != nil {
			return ErrInvalidSignature
		}
		return nil

	case familyECDSA:
		publicKey, err := ecdsaVerificationKey(key, info.curveBits)
		if err != nil {
			return err
		}
		digest := hashBytes(info.hash, signingInput)
		if !verifyECDSASignature(publicKey, digest, signature, info.curveBits) {
			return ErrInvalidSignature
		}
		return nil

	case familyEdDSA:
		publicKey, err := ed25519VerificationKey(key)
		if err != nil {
			return err
		}
		if !verifyEd25519(publicKey, signingInput, signature) {
			return ErrInvalidSignature
		}
		return nil
	}

	return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
}

func hashBytes(hash crypto.Hash, data []byte) []byte {
	digest := hash.New()
	digest.Write(data)
	return digest.Sum(nil)
}

func signHMAC(key []byte, hash crypto.Hash, data []byte) []byte {
	digest := hmac.New(hash.New, key)
	digest.Write(data)
	return digest.Sum(nil)
}

type algorithmFamily uint8

const (
	familyHMAC algorithmFamily = iota + 1
	familyRSA
	familyECDSA
	familyEdDSA
)

type algorithmInfo struct {
	family    algorithmFamily
	hash      crypto.Hash
	pss       bool
	curveBits int
}

var supportedAlgorithms = map[string]algorithmInfo{
	"HS256": {family: familyHMAC, hash: crypto.SHA256},
	"HS384": {family: familyHMAC, hash: crypto.SHA384},
	"HS512": {family: familyHMAC, hash: crypto.SHA512},

	"RS256": {family: familyRSA, hash: crypto.SHA256},
	"RS384": {family: familyRSA, hash: crypto.SHA384},
	"RS512": {family: familyRSA, hash: crypto.SHA512},

	"PS256": {family: familyRSA, hash: crypto.SHA256, pss: true},
	"PS384": {family: familyRSA, hash: crypto.SHA384, pss: true},
	"PS512": {family: familyRSA, hash: crypto.SHA512, pss: true},

	"ES256": {family: familyECDSA, hash: crypto.SHA256, curveBits: 256},
	"ES384": {family: familyECDSA, hash: crypto.SHA384, curveBits: 384},
	"ES512": {family: familyECDSA, hash: crypto.SHA512, curveBits: 521},

	"EdDSA": {family: familyEdDSA},
}
