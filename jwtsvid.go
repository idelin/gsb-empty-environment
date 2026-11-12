package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Supported JWT signature algorithms.
const (
	AlgES256 = "ES256"
	AlgRS256 = "RS256"
	AlgPS256 = "PS256"
)

// FutureIATLeeway is the maximum tolerated difference between a token's "iat"
// claim and the validation time. A token issued more than this in the future
// is rejected.
const FutureIATLeeway = 30 * time.Second

// Validation errors. Callers can use errors.Is to match them.
var (
	ErrMalformedToken    = errors.New("netsec: malformed JWT-SVID")
	ErrUnsupportedAlg    = errors.New("netsec: unsupported JWT algorithm")
	ErrInvalidSubject    = errors.New("netsec: invalid subject SPIFFE ID")
	ErrEmptyAudience     = errors.New("netsec: token audience must not be empty")
	ErrAudienceMismatch  = errors.New("netsec: token audience does not include the expected audience")
	ErrMissingExpiry     = errors.New("netsec: exp claim is required")
	ErrMissingIssuedAt   = errors.New("netsec: iat claim is required")
	ErrTokenExpired      = errors.New("netsec: token has expired")
	ErrFutureIssuedAt    = errors.New("netsec: token issued-at (iat) is too far in the future")
	ErrTrustDomainNotSet = errors.New("netsec: trust domain is not trusted")
	ErrNoVerificationKey = errors.New("netsec: no matching verification key in trust bundle")
	ErrInvalidSignature  = errors.New("netsec: invalid token signature")
	ErrInvalidClaim      = errors.New("netsec: invalid JWT claim")
)

// Validator validates JWT-SVIDs against a set of trust domain bundles.
//
// The verification key is always selected from the bundle of the trust
// domain named by the token's "sub" claim, so a token minted by another
// (federated) trust domain can never be verified against this service's
// local-domain bundle, even when the workload identity string collides.
type Validator struct {
	source   BundleSource
	audience string
	now      func() time.Time
}

// Option configures a Validator.
type Option func(*Validator)

// WithClock overrides the time source used for expiry and iat checks. It is
// mainly useful for tests.
func WithClock(now func() time.Time) Option {
	return func(v *Validator) {
		if now != nil {
			v.now = now
		}
	}
}

// NewValidator creates a JWT-SVID validator. audience is the expected
// "aud" value of this service; tokens not carrying that audience are
// rejected, which prevents cross-audience token replay.
func NewValidator(source BundleSource, audience string, opts ...Option) (*Validator, error) {
	if source == nil {
		return nil, errors.New("netsec: bundle source is required")
	}
	if audience == "" {
		return nil, errors.New("netsec: expected audience must not be empty")
	}
	v := &Validator{
		source:   source,
		audience: audience,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v, nil
}

// Claims is the validated set of JWT-SVID claims returned to callers.
type Claims struct {
	ID       SPIFFEID
	Subject  string
	Audience []string
	Expiry   time.Time
	IssuedAt time.Time
	Issuer   string
	Raw      map[string]any
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

type rawClaims struct {
	Sub string         `json:"sub"`
	Aud audience       `json:"aud"`
	Exp *numericDate   `json:"exp"`
	Iat *numericDate   `json:"iat"`
	Nbf *numericDate   `json:"nbf"`
	Iss string         `json:"iss"`
	Raw map[string]any `json:"-"`
}

// audience accepts both the single-string and string-array forms of the
// JWT "aud" claim.
type audience []string

func (a *audience) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = audience{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return fmt.Errorf("%w: aud must be a string or array of strings", ErrInvalidClaim)
	}
	*a = audience(many)
	return nil
}

// numericDate preserves the exact JSON number for JWT NumericDates.
type numericDate time.Time

func (n *numericDate) UnmarshalJSON(data []byte) error {
	var num json.Number
	if err := json.Unmarshal(data, &num); err != nil {
		return fmt.Errorf("%w: numeric date must be a JSON number", ErrInvalidClaim)
	}
	secs, err := num.Float64()
	if err != nil {
		return fmt.Errorf("%w: numeric date out of range", ErrInvalidClaim)
	}
	if secs < 0 {
		return fmt.Errorf("%w: numeric date must not be negative", ErrInvalidClaim)
	}
	whole := int64(secs)
	nanos := int64((secs - float64(whole)) * 1e9)
	*n = numericDate(time.Unix(whole, nanos).UTC())
	return nil
}

func (n numericDate) Time() time.Time { return time.Time(n) }

// Validate parses, verifies and validates a compact-serialization JWT-SVID.
func (v *Validator) Validate(token string) (*Claims, error) {
	if strings.Count(token, ".") != 2 {
		return nil, ErrMalformedToken
	}
	parts := strings.Split(token, ".")
	headerPart, payloadPart, signaturePart := parts[0], parts[1], parts[2]
	if headerPart == "" || payloadPart == "" || signaturePart == "" {
		return nil, ErrMalformedToken
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(headerPart)
	if err != nil {
		return nil, fmt.Errorf("%w: header is not base64url", ErrMalformedToken)
	}
	var header jwtHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, fmt.Errorf("%w: invalid header JSON", ErrMalformedToken)
	}
	if !isAllowedAlg(header.Alg) {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlg, header.Alg)
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not base64url", ErrMalformedToken)
	}
	var raw map[string]any
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return nil, fmt.Errorf("%w: invalid payload JSON", ErrMalformedToken)
	}
	var claims rawClaims
	dec := json.NewDecoder(bytes.NewReader(payloadBytes))
	dec.UseNumber()
	if err := dec.Decode(&claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidClaim, err)
	}
	claims.Raw = raw

	// Subject must be a fully validated SPIFFE ID.
	id, err := ParseSPIFFEID(claims.Sub)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSubject, err)
	}

	// Audience: non-empty and containing this service's audience.
	if len(claims.Aud) == 0 {
		return nil, ErrEmptyAudience
	}
	for _, aud := range claims.Aud {
		if aud == "" {
			return nil, ErrEmptyAudience
		}
	}
	if !containsString(claims.Aud, v.audience) {
		return nil, ErrAudienceMismatch
	}

	now := v.now()
	// Time claims. Both exp and iat are mandatory per JWT-SVID (RFC 8885).
	if claims.Exp == nil {
		return nil, ErrMissingExpiry
	}
	if claims.Iat == nil {
		return nil, ErrMissingIssuedAt
	}
	expiry := claims.Exp.Time()
	if !expiry.After(now) {
		return nil, fmt.Errorf("%w: exp %s", ErrTokenExpired, expiry.Format(time.RFC3339))
	}
	issuedAt := claims.Iat.Time()
	if issuedAt.Sub(now) > FutureIATLeeway {
		return nil, fmt.Errorf("%w: iat %s", ErrFutureIssuedAt, issuedAt.Format(time.RFC3339))
	}
	if claims.Nbf != nil && claims.Nbf.Time().After(now) {
		return nil, fmt.Errorf("%w: token not yet valid (nbf)", ErrInvalidClaim)
	}

	// Key selection is scoped strictly to the subject's trust domain.
	bundle, err := v.source.BundleForTrustDomain(id.TrustDomain())
	if err != nil {
		return nil, fmt.Errorf("netsec: bundle lookup failed: %w", err)
	}
	if bundle == nil || len(bundle.Keys()) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrTrustDomainNotSet, id.TrustDomain())
	}

	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return nil, fmt.Errorf("%w: signature is not base64url", ErrMalformedToken)
	}
	signingInput := headerPart + "." + payloadPart
	if err := verifyWithBundle(bundle, header.Alg, signingInput, signature); err != nil {
		return nil, err
	}

	return &Claims{
		ID:       id,
		Subject:  id.String(),
		Audience: []string(claims.Aud),
		Expiry:   expiry,
		IssuedAt: issuedAt,
		Issuer:   claims.Iss,
		Raw:      raw,
	}, nil
}

func isAllowedAlg(alg string) bool {
	switch alg {
	case AlgES256, AlgRS256, AlgPS256:
		return true
	default:
		return false
	}
}

func verifyWithBundle(bundle *Bundle, alg, signingInput string, signature []byte) error {
	digest := sha256.Sum256([]byte(signingInput))
	var matched bool
	for _, key := range bundle.Keys() {
		switch alg {
		case AlgES256:
			ecKey, ok := key.(*ecdsa.PublicKey)
			if !ok {
				continue
			}
			matched = true
			if verifyES256(ecKey, signature, digest[:]) {
				return nil
			}
		case AlgRS256, AlgPS256:
			rsaKey, ok := key.(*rsa.PublicKey)
			if !ok {
				continue
			}
			if rsaKey.Size()*8 < 2048 {
				continue
			}
			matched = true
			if verifyRSA(rsaKey, alg, digest[:], signature) {
				return nil
			}
		}
	}
	if !matched {
		return fmt.Errorf("%w: alg %s", ErrNoVerificationKey, alg)
	}
	return ErrInvalidSignature
}

// verifyES256 verifies an IEEE-P1363 (r||s) ECDSA P-256 signature, which is
// the JWT ES256 wire format.
func verifyES256(key *ecdsa.PublicKey, signature, digest []byte) bool {
	if key.Curve == nil || key.Params().BitSize != 256 || len(signature) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	return ecdsa.Verify(key, digest, r, s)
}

func verifyRSA(key *rsa.PublicKey, alg string, digest, signature []byte) bool {
	switch alg {
	case AlgRS256:
		return rsa.VerifyPKCS1v15(key, crypto.SHA256, digest, signature) == nil
	case AlgPS256:
		return rsa.VerifyPSS(key, crypto.SHA256, digest, signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
	default:
		return false
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
