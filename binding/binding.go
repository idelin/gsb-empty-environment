// Package binding exposes the CCA realm/platform token binding checks as a
// dedicated subpackage. It is a thin, type-compatible wrapper around the
// module's root netsec package.
package binding

import netsec "example.org/netsec/q0095"

type (
	Claims        = netsec.Claims
	HashAlgorithm = netsec.HashAlgorithm
	Option        = netsec.Option
	Policy        = netsec.Policy
)

const (
	SHA256 HashAlgorithm = netsec.SHA256
	SHA384 HashAlgorithm = netsec.SHA384
	SHA512 HashAlgorithm = netsec.SHA512
)

const (
	ClaimKeyNonce                       = netsec.ClaimKeyNonce
	ClaimKeyPlatformHashAlgorithm       = netsec.ClaimKeyPlatformHashAlgorithm
	ClaimKeyRealmHashAlgorithm          = netsec.ClaimKeyRealmHashAlgorithm
	ClaimKeyRealmPublicKey              = netsec.ClaimKeyRealmPublicKey
	ClaimKeyRealmInitialMeasurement     = netsec.ClaimKeyRealmInitialMeasurement
	ClaimKeyRealmExtensibleMeasurements = netsec.ClaimKeyRealmExtensibleMeasurements
	ClaimKeyRealmPublicKeyHashAlgorithm = netsec.ClaimKeyRealmPublicKeyHashAlgorithm
	ClaimKeyRealmDelegatedToken         = netsec.ClaimKeyRealmDelegatedToken
	ClaimKeyPlatformToken               = netsec.ClaimKeyPlatformToken
)

var (
	ErrMalformedToken            = netsec.ErrMalformedToken
	ErrTokenFormat               = netsec.ErrTokenFormat
	ErrInvalidToken              = netsec.ErrInvalidToken
	ErrInvalidArgument           = netsec.ErrInvalidArgument
	ErrMissingClaim              = netsec.ErrMissingClaim
	ErrInvalidClaim              = netsec.ErrInvalidClaim
	ErrChallengeMismatch         = netsec.ErrChallengeMismatch
	ErrRealmChallengeMismatch    = netsec.ErrRealmChallengeMismatch
	ErrPlatformChallengeMismatch = netsec.ErrPlatformChallengeMismatch
	ErrNonceMismatch             = netsec.ErrNonceMismatch
	ErrAlgorithmNotAllowed       = netsec.ErrAlgorithmNotAllowed
	ErrHashAlgorithmNotAllowed   = netsec.ErrHashAlgorithmNotAllowed
	ErrHashAlgNotAllowed         = netsec.ErrHashAlgNotAllowed
	ErrUnsupportedHashAlgorithm  = netsec.ErrUnsupportedHashAlgorithm
	ErrMeasurementLength         = netsec.ErrMeasurementLength
	ErrMeasurementLengthMismatch = netsec.ErrMeasurementLengthMismatch
)

var DefaultAllowedHashAlgorithms = netsec.DefaultAllowedHashAlgorithms

func ParseHashAlgorithm(v any) (HashAlgorithm, error) { return netsec.ParseHashAlgorithm(v) }

func DefaultPolicy() Policy { return netsec.DefaultPolicy() }

func WithAllowedAlgorithms(algorithms ...HashAlgorithm) Option {
	return netsec.WithAllowedAlgorithms(algorithms...)
}

func WithAllowedAlgorithmNames(names ...string) Option {
	return netsec.WithAllowedAlgorithmNames(names...)
}

func RequireMeasurements() Option { return netsec.RequireMeasurements() }

func VerifyBinding(args ...any) error      { return netsec.VerifyBinding(args...) }
func CheckBinding(args ...any) error       { return netsec.CheckBinding(args...) }
func VerifyTokenBinding(args ...any) error { return netsec.VerifyTokenBinding(args...) }
func CheckTokenBinding(args ...any) error  { return netsec.CheckTokenBinding(args...) }
func ValidateBinding(args ...any) error    { return netsec.ValidateBinding(args...) }
func ValidateTokenBinding(args ...any) error {
	return netsec.ValidateTokenBinding(args...)
}
func VerifyCCABinding(args ...any) error { return netsec.VerifyCCABinding(args...) }
func Check(realm, platform, nonce any, options ...any) error {
	return netsec.Check(realm, platform, nonce, options...)
}
func Validate(realm, platform, nonce any, options ...any) error {
	return netsec.Validate(realm, platform, nonce, options...)
}

func Verify(realm, platform, nonce any, options ...any) error {
	return netsec.Verify(realm, platform, nonce, options...)
}
