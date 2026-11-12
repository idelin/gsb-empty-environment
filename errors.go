package netsec

import "errors"

var (
	// ErrMalformedToken indicates that a token is not a compact JWT.
	ErrMalformedToken = errors.New("netsec: malformed token")
	// ErrUnsupportedAlgorithm indicates an algorithm other than Ed25519.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported JWT algorithm")
	// ErrInvalidSignature indicates that the compact JWT signature is invalid.
	ErrInvalidSignature = errors.New("netsec: invalid JWT signature")
	// ErrUntrustedIssuer indicates that an account JWT was not signed by a
	// trusted operator or operator signing key.
	ErrUntrustedIssuer = errors.New("netsec: untrusted account issuer")
	// ErrUntrustedSigningKey indicates that a user JWT was signed by a key
	// that is neither the account key nor one of its registered signing keys.
	ErrUntrustedSigningKey = errors.New("netsec: user JWT signing key is not registered to the account")
	// ErrMissingIssuerAccount indicates that a registered account signing key
	// issued a user JWT without the required issuer_account claim.
	ErrMissingIssuerAccount = errors.New("netsec: issuer_account is required for account signing keys")
	// ErrIssuerAccountMismatch indicates issuer_account does not identify the
	// account whose signing key issued the user JWT.
	ErrIssuerAccountMismatch = errors.New("netsec: issuer_account does not match the account")
	// ErrWrongTokenType indicates the JWT is not the expected account or user
	// token type.
	ErrWrongTokenType = errors.New("netsec: wrong JWT type")
	// ErrExpired indicates that the JWT expiry time has been reached.
	ErrExpired = errors.New("netsec: JWT is expired")
	// ErrNotYetValid indicates that the JWT issue time is in the future.
	ErrNotYetValid = errors.New("netsec: JWT is not yet valid")
	// ErrMissingTimeClaim indicates that iat or exp is absent.
	ErrMissingTimeClaim = errors.New("netsec: JWT is missing iat or exp")
	// ErrRevoked indicates that the user JWT was issued at or before its
	// revocation time.
	ErrRevoked = errors.New("netsec: user JWT is revoked")
	// ErrInvalidAccountKey indicates that an account key cannot be resolved.
	ErrInvalidAccountKey = errors.New("netsec: invalid account key")
	// ErrInvalidUserKey indicates that a user subject cannot be resolved.
	ErrInvalidUserKey = errors.New("netsec: invalid user key")
	// ErrInvalidNonceSignature indicates that the nonce signature does not
	// verify with the user's public key.
	ErrInvalidNonceSignature = errors.New("netsec: invalid nonce signature")
	// ErrNonceReplay indicates that the nonce has already been consumed.
	ErrNonceReplay = errors.New("netsec: nonce replay")
	// ErrNonceExpired indicates that an issued challenge has expired.
	ErrNonceExpired = errors.New("netsec: nonce challenge expired")
	// ErrInvalidKey indicates a malformed Ed25519 key or key identifier.
	ErrInvalidKey = errors.New("netsec: invalid Ed25519 key")
)
