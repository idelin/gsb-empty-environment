package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrMalformedProof indicates that the value is not a compact JWS/JWT.
	ErrMalformedProof = errors.New("netsec: malformed proof JWT")
	// ErrUnsupportedAlgorithm indicates that the JWS algorithm is not allowed.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported proof algorithm")
	// ErrUnsupportedCriticalHeader indicates an unsupported "crit" header.
	ErrUnsupportedCriticalHeader = errors.New("netsec: unsupported critical header")
	// ErrMissingKey indicates that no key was available to verify the proof.
	ErrMissingKey = errors.New("netsec: no proof verification key")
	// ErrInvalidKey indicates that the configured or embedded key is invalid.
	ErrInvalidKey = errors.New("netsec: invalid proof verification key")
	// ErrInvalidSignature indicates that the proof signature did not verify.
	ErrInvalidSignature = errors.New("netsec: invalid proof signature")
	// ErrInvalidClaim indicates a missing or malformed JWT claim.
	ErrInvalidClaim = errors.New("netsec: invalid proof claim")
	// ErrAudienceMismatch indicates that "aud" is not the credential endpoint.
	ErrAudienceMismatch = errors.New("netsec: proof audience mismatch")
	// ErrNonceMismatch indicates that "nonce" is not the current c_nonce.
	ErrNonceMismatch = errors.New("netsec: proof nonce mismatch")
	// ErrNonceReused indicates that a valid c_nonce was already consumed.
	ErrNonceReused = errors.New("netsec: proof nonce already used")
	// ErrNonceRejected indicates that a nonce store rejected the nonce.
	ErrNonceRejected = errors.New("netsec: proof nonce rejected")
	// ErrNonceStore indicates that the nonce store failed.
	ErrNonceStore = errors.New("netsec: nonce store failure")
	// ErrExpiredProof indicates that the proof is expired.
	ErrExpiredProof = errors.New("netsec: proof expired")
	// ErrProofNotYetValid indicates that the proof is not valid yet.
	ErrProofNotYetValid = errors.New("netsec: proof not yet valid")
	// ErrProofType indicates an unacceptable JWT "typ" header.
	ErrProofType = errors.New("netsec: unacceptable proof type")
)

// ProofError adds context to one of the package sentinel errors. Use errors.Is
// with the sentinel errors above to classify failures.
type ProofError struct {
	Kind   error
	Detail string
	Err    error
}

func (e *ProofError) Error() string {
	switch {
	case e.Detail != "" && e.Err != nil:
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Detail, e.Err)
	case e.Detail != "":
		return fmt.Sprintf("%s: %s", e.Kind, e.Detail)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Kind, e.Err)
	default:
		return e.Kind.Error()
	}
}

// Unwrap exposes the underlying error.
func (e *ProofError) Unwrap() error {
	return e.Err
}

// Is makes errors.Is(err, ErrAudienceMismatch) work through ProofError.
func (e *ProofError) Is(target error) bool {
	return target == e.Kind
}

func proofError(kind error, format string, args ...any) error {
	detail := ""
	if format != "" {
		detail = fmt.Sprintf(format, args...)
	}
	return &ProofError{Kind: kind, Detail: detail}
}

func proofErrorWithCause(kind error, cause error, format string, args ...any) error {
	detail := ""
	if format != "" {
		detail = fmt.Sprintf(format, args...)
	}
	return &ProofError{Kind: kind, Detail: detail, Err: cause}
}
