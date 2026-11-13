package netsec

import (
	"context"
	"errors"
	"fmt"
)

// Verifier verifies JWS tokens with a fixed key source and policy.
type Verifier struct {
	// Keys may be any key supported by Verify, a KeyResolver, a resolver
	// function, or a map from protected kid to key.
	Keys any

	// KeyResolver and Resolver are alternative key source fields.
	KeyResolver any
	Resolver    any

	// AllowedAlgorithms restricts alg. Empty means all supported algorithms.
	AllowedAlgorithms []string
	// Algorithms is an alias for AllowedAlgorithms.
	Algorithms []string
	// RequireKeyID rejects tokens without a protected kid.
	RequireKeyID bool
}

// NewVerifier creates a Verifier with the given key source.
func NewVerifier(keySource any) *Verifier {
	return &Verifier{Keys: keySource}
}

func (v *Verifier) keySource() any {
	if v == nil {
		return nil
	}
	if v.Keys != nil {
		return v.Keys
	}
	if v.KeyResolver != nil {
		return v.KeyResolver
	}
	return v.Resolver
}

func (v *Verifier) allows(alg string) bool {
	if v == nil {
		return true
	}
	allowed := v.AllowedAlgorithms
	if len(allowed) == 0 {
		allowed = v.Algorithms
	}
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if candidate == alg {
			return true
		}
	}
	return false
}

// Verify parses a compact or JSON JWS and returns its authenticated payload.
func Verify(token any, keyOrResolver any) ([]byte, error) {
	return VerifyContext(context.Background(), token, keyOrResolver)
}

// VerifyContext is Verify with an explicit context for key resolution.
func VerifyContext(ctx context.Context, token any, keyOrResolver any) ([]byte, error) {
	verifier := &Verifier{Keys: keyOrResolver}
	return verifier.VerifyContext(ctx, token)
}

// VerifyResult is Verify but also returns details of the accepted signature.
func VerifyResult(token any, keyOrResolver any) (*Result, error) {
	return VerifyResultContext(context.Background(), token, keyOrResolver)
}

// VerifyResultContext is VerifyResult with an explicit context.
func VerifyResultContext(ctx context.Context, token any, keyOrResolver any) (*Result, error) {
	verifier := &Verifier{Keys: keyOrResolver}
	return verifier.VerifyResultContext(ctx, token)
}

// VerifyJWS is an alias for Verify.
func VerifyJWS(token any, keyOrResolver any) ([]byte, error) {
	return Verify(token, keyOrResolver)
}

// VerifyToken is an alias for Verify.
func VerifyToken(token any, keyOrResolver any) ([]byte, error) {
	return Verify(token, keyOrResolver)
}

// VerifySignature is an alias for Verify.
func VerifySignature(token any, keyOrResolver any) ([]byte, error) {
	return Verify(token, keyOrResolver)
}

// Validate is an alias for Verify.
func Validate(token any, keyOrResolver any) ([]byte, error) {
	return Verify(token, keyOrResolver)
}

// VerifyWithKey is an alias for Verify.
func VerifyWithKey(token any, key any) ([]byte, error) {
	return Verify(token, key)
}

// VerifyCompact parses a compact JWS and verifies it.
func VerifyCompact(token any, keyOrResolver any) ([]byte, error) {
	parsed, err := ParseCompact(token)
	if err != nil {
		return nil, err
	}
	return (&Verifier{Keys: keyOrResolver}).VerifyToken(parsed)
}

// VerifyJSON parses a JSON JWS and verifies it.
func VerifyJSON(token any, keyOrResolver any) ([]byte, error) {
	parsed, err := ParseJSON(token)
	if err != nil {
		return nil, err
	}
	return (&Verifier{Keys: keyOrResolver}).VerifyToken(parsed)
}

// VerifyToken verifies an already parsed token.
func (v *Verifier) VerifyToken(token *Token) ([]byte, error) {
	return v.VerifyTokenContext(context.Background(), token)
}

// VerifyTokenContext verifies an already parsed token with a context.
func (v *Verifier) VerifyTokenContext(ctx context.Context, token *Token) ([]byte, error) {
	result, err := v.VerifyTokenResultContext(ctx, token)
	if err != nil {
		return nil, err
	}
	return result.Payload, nil
}

// VerifyTokenResult verifies an already parsed token and returns details.
func (v *Verifier) VerifyTokenResult(token *Token) (*Result, error) {
	return v.VerifyTokenResultContext(context.Background(), token)
}

// VerifyTokenResultContext verifies an already parsed token and returns details.
func (v *Verifier) VerifyTokenResultContext(ctx context.Context, token *Token) (*Result, error) {
	if token == nil {
		return nil, fmt.Errorf("%w: nil token", ErrMalformedToken)
	}
	if len(token.Signatures) == 0 {
		return nil, fmt.Errorf("%w: no signatures", ErrMalformedToken)
	}
	if token.Detached {
		return nil, ErrDetachedPayload
	}

	var failures []error
	for index := range token.Signatures {
		result, err := v.verifySignature(ctx, token, index)
		if err == nil {
			return result, nil
		}
		failures = append(failures, err)
	}
	return nil, combineVerificationErrors(failures)
}

// Verify parses a compact or JSON JWS and returns its authenticated payload.
func (v *Verifier) Verify(token any) ([]byte, error) {
	return v.VerifyContext(context.Background(), token)
}

// VerifyContext parses a compact or JSON JWS and returns its authenticated payload.
func (v *Verifier) VerifyContext(ctx context.Context, token any) ([]byte, error) {
	result, err := v.VerifyResultContext(ctx, token)
	if err != nil {
		return nil, err
	}
	return result.Payload, nil
}

// VerifyResult parses a compact or JSON JWS and returns details of the accepted
// signature.
func (v *Verifier) VerifyResult(token any) (*Result, error) {
	return v.VerifyResultContext(context.Background(), token)
}

// VerifyResultContext parses a compact or JSON JWS and returns details of the
// accepted signature.
func (v *Verifier) VerifyResultContext(ctx context.Context, token any) (*Result, error) {
	if parsed, ok := token.(*Token); ok {
		return v.VerifyTokenResultContext(ctx, parsed)
	}
	parsed, err := Parse(token)
	if err != nil {
		return nil, err
	}
	return v.VerifyTokenResultContext(ctx, parsed)
}

// VerifyCompact verifies a compact JWS.
func (v *Verifier) VerifyCompact(token any) ([]byte, error) {
	parsed, err := ParseCompact(token)
	if err != nil {
		return nil, err
	}
	return v.VerifyToken(parsed)
}

// VerifyJSON verifies a JSON JWS.
func (v *Verifier) VerifyJSON(token any) ([]byte, error) {
	parsed, err := ParseJSON(token)
	if err != nil {
		return nil, err
	}
	return v.VerifyToken(parsed)
}

// VerifyDetached verifies a JWS whose payload is supplied out of band.
func VerifyDetached(token any, payload []byte, keyOrResolver any) ([]byte, error) {
	return VerifyDetachedContext(context.Background(), token, payload, keyOrResolver)
}

// VerifyDetachedContext verifies a JWS with a detached payload.
func VerifyDetachedContext(ctx context.Context, token any, payload []byte, keyOrResolver any) ([]byte, error) {
	verifier := &Verifier{Keys: keyOrResolver}
	return verifier.VerifyDetachedContext(ctx, token, payload)
}

// VerifyDetached verifies a JWS whose payload is supplied out of band.
func (v *Verifier) VerifyDetached(token any, payload []byte) ([]byte, error) {
	return v.VerifyDetachedContext(context.Background(), token, payload)
}

// VerifyDetachedContext verifies a JWS whose payload is supplied out of band.
func (v *Verifier) VerifyDetachedContext(ctx context.Context, token any, payload []byte) ([]byte, error) {
	parsed, err := Parse(token)
	if err != nil {
		return nil, err
	}
	if !parsed.Detached && parsed.PayloadEncoded != "" {
		return nil, fmt.Errorf("%w: token already contains a payload", ErrMalformedToken)
	}
	parsed.Detached = false
	parsed.Payload = append([]byte(nil), payload...)
	parsed.PayloadEncoded = encodeBase64URL(payload)
	return v.VerifyTokenContext(ctx, parsed)
}

func (v *Verifier) verifySignature(ctx context.Context, token *Token, index int) (*Result, error) {
	signature := &token.Signatures[index]

	if err := ensureProtectedExtensions(signature.Protected); err != nil {
		return nil, err
	}
	if err := checkHeaderConsistency(signature.Protected, signature.Unprotected); err != nil {
		return nil, err
	}

	alg := signature.Protected.Algorithm
	if alg == "" {
		return nil, ErrAlgorithmMissing
	}
	if alg == "none" || !isSupportedAlgorithm(alg) {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
	if !v.allows(alg) {
		return nil, fmt.Errorf("%w: %q", ErrAlgorithmNotAllowed, alg)
	}
	if v != nil && v.RequireKeyID && signature.Protected.KeyID == "" {
		return nil, ErrKeyIDRequired
	}

	key, err := resolveSigningKey(ctx, v.keySource(), signature.Protected)
	if err != nil {
		return nil, err
	}

	signingInput := []byte(signature.ProtectedEncoded + "." + token.PayloadEncoded)
	if err := verifyWithKeys(alg, key, signingInput, signature.Signature); err != nil {
		return nil, err
	}

	result := &Result{
		Payload:           append([]byte(nil), token.Payload...),
		PayloadEncoded:    token.PayloadEncoded,
		Signature:         append([]byte(nil), signature.Signature...),
		SignatureIndex:    index,
		Protected:         signature.Protected,
		ProtectedHeader:   signature.Protected,
		ProtectedEncoded:  signature.ProtectedEncoded,
		Unprotected:       signature.Unprotected,
		UnprotectedHeader: signature.Unprotected,
		Algorithm:         alg,
		KeyID:             signature.Protected.KeyID,
	}
	return result, nil
}

func combineVerificationErrors(failures []error) error {
	if len(failures) == 0 {
		return ErrInvalidSignature
	}
	if len(failures) == 1 {
		return fmt.Errorf("%w: %w", ErrInvalidSignature, failures[0])
	}
	return fmt.Errorf("%w: %w", ErrInvalidSignature, errors.Join(failures...))
}
