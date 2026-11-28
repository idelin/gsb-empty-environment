package nestedjwt

import "errors"

// Sentinel errors returned by the verifier. All operational errors wrap one
// of these values, so callers can use errors.Is to make policy decisions.
var (
	// ErrMalformedToken is returned when the compact serialization is not a
	// structurally valid JWS (3 segments) or JWE (5 segments).
	ErrMalformedToken = errors.New("nestedjwt: malformed token")

	// ErrBareJWSRejected is returned when the policy requires a nested JWE
	// envelope but the caller submitted an unencrypted 3-segment JWS.
	ErrBareJWSRejected = errors.New("nestedjwt: policy requires nested JWE, bare JWS rejected")

	// ErrProtectedHeader is returned for syntactically invalid JOSE protected
	// headers (not JSON objects, undecodable, etc.).
	ErrProtectedHeader = errors.New("nestedjwt: invalid protected header")

	// ErrMissingContentType is returned when the outer JWE protected header
	// does not carry cty="JWT".
	ErrMissingContentType = errors.New("nestedjwt: JWE protected header must carry cty=\"JWT\"")

	// ErrUnsupportedAlgorithm is returned when alg/enc is not on the policy
	// whitelist or cannot be served by this implementation.
	ErrUnsupportedAlgorithm = errors.New("nestedjwt: algorithm not allowed by policy")

	// ErrCompressionRejected is returned when the JWE header requests
	// compression (zip), which is forbidden to prevent decompression bombs.
	ErrCompressionRejected = errors.New("nestedjwt: JWE compression (zip) is forbidden")

	// ErrCriticalHeader is returned for a non-empty crit header naming an
	// extension the verifier does not understand.
	ErrCriticalHeader = errors.New("nestedjwt: unsupported critical header (crit)")

	// ErrDecryption is returned when JWE decryption or authentication fails.
	ErrDecryption = errors.New("nestedjwt: JWE decryption failed")

	// ErrInnerNotJWS is returned when the JWE plaintext is not a compact JWS,
	// e.g. raw JSON claims were encrypted directly (signature stripping).
	ErrInnerNotJWS = errors.New("nestedjwt: JWE plaintext must be a compact JWS")

	// ErrNoneAlgorithm is returned when a JWS uses the unsecured "none" alg.
	ErrNoneAlgorithm = errors.New("nestedjwt: JWS alg \"none\" is forbidden")

	// ErrSymmetricAlgorithm is returned when a JWS uses a symmetric MAC alg
	// such as HS256, which cannot prove client possession of a private key.
	ErrSymmetricAlgorithm = errors.New("nestedjwt: symmetric JWS algorithms are forbidden")

	// ErrMissingKeyID is returned when a required kid header is absent.
	ErrMissingKeyID = errors.New("nestedjwt: key id (kid) is required")

	// ErrUnknownKey is returned when the key resolver has no key for a kid.
	ErrUnknownKey = errors.New("nestedjwt: no key registered for kid")

	// ErrSignature is returned when JWS signature verification fails or the
	// resolved key type does not match the signing algorithm.
	ErrSignature = errors.New("nestedjwt: JWS signature verification failed")

	// ErrNestedDepth is returned when the token nesting depth exceeds the
	// policy maximum (default 2 layers: outer JWE + inner JWS).
	ErrNestedDepth = errors.New("nestedjwt: nested token depth exceeds policy maximum")

	// ErrPayloadNotJSON is returned when a JWS payload is not a JSON object.
	ErrPayloadNotJSON = errors.New("nestedjwt: JWS payload is not a JSON claims object")

	// ErrCrossLayerClaim is returned when iss/sub/aud/kid values in the outer
	// JWE protected header disagree with the inner JWS claims or header.
	ErrCrossLayerClaim = errors.New("nestedjwt: cross-layer claim mismatch")

	// ErrMissingReplayClaims is returned when replay protection is enabled but
	// the inner claims lack iss or jti.
	ErrMissingReplayClaims = errors.New("nestedjwt: iss and jti claims are required for replay protection")

	// ErrReplayed is returned when an (iss, jti) pair has already been seen.
	ErrReplayed = errors.New("nestedjwt: token (iss, jti) has already been consumed")

	// ErrTokenExpired is returned when exp (plus clock skew) is in the past.
	ErrTokenExpired = errors.New("nestedjwt: token has expired")

	// ErrTokenNotYetValid is returned when nbf (minus clock skew) is in the
	// future.
	ErrTokenNotYetValid = errors.New("nestedjwt: token nbf is in the future")
)
