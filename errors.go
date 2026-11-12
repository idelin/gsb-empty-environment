package netsec

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrEmptyChain means no generation was supplied.
	ErrEmptyChain = errors.New("netsec: chain has no generations")
	// ErrEvidenceMismatch means the supplied evidence does not hash to the
	// chain's immutable evidence digest.
	ErrEvidenceMismatch = errors.New("netsec: evidence digest does not match chain")
	// ErrCaptureTimeMismatch means the caller's externally anchored original
	// collection time differs from the chain header.
	ErrCaptureTimeMismatch = errors.New("netsec: original capture time does not match chain")
	// ErrParentMismatch means a generation does not reference the preceding
	// generation digest.
	ErrParentMismatch = errors.New("netsec: parent digest does not match previous generation")
	// ErrFork means two generations claim the same parent.
	ErrFork = errors.New("netsec: chain fork detected")
	// ErrSameTimeFork means the fork siblings also share an issuance time.
	ErrSameTimeFork = errors.New("netsec: fork siblings share the same issuance time")
	// ErrTimeRegression means a generation predates its parent's issuance.
	ErrTimeRegression = errors.New("netsec: issuance time went backwards")
	// ErrTimeBeforeCapture means a generation predates the original evidence.
	ErrTimeBeforeCapture = errors.New("netsec: issuance time predates original capture time")
	// ErrIssuerInvalid means issuer material is missing or malformed.
	ErrIssuerInvalid = errors.New("netsec: issuer certificate is missing or malformed")
	// ErrIssuerNotYetValid means the issuer certificate was not valid yet at
	// the generation's issuance time.
	ErrIssuerNotYetValid = errors.New("netsec: issuer certificate was not yet valid")
	// ErrIssuerExpired means the issuer certificate had expired at the
	// generation's issuance time.
	ErrIssuerExpired = errors.New("netsec: issuer certificate had expired")
	// ErrIssuerKeyMismatch means the supplied signer does not match the
	// certificate public key.
	ErrIssuerKeyMismatch = errors.New("netsec: signer does not match issuer certificate")
	// ErrIssuerSignature means a generation signature did not verify.
	ErrIssuerSignature = errors.New("netsec: generation signature is invalid")
	// ErrUntrustedIssuer means certificate-chain trust validation failed.
	ErrUntrustedIssuer = errors.New("netsec: issuer certificate is not trusted")
	// ErrDuplicateGeneration means the same generation digest occurs twice.
	ErrDuplicateGeneration = errors.New("netsec: duplicate generation")
)

// Violation is one independently detected problem in a chain. Generation is
// -1 for a chain-header problem and otherwise the zero-based generation index.
type Violation struct {
	Generation int
	Err        error
}

// Error implements error.
func (v Violation) Error() string {
	if v.Generation < 0 {
		return fmt.Sprintf("header: %v", v.Err)
	}
	return fmt.Sprintf("generation %d: %v", v.Generation, v.Err)
}

// Unwrap returns the underlying sentinel-wrapped error.
func (v Violation) Unwrap() error {
	return v.Err
}

// VerificationError reports every defect found in a chain, in generation order.
// The chain itself is never truncated or modified by verification.
type VerificationError struct {
	Violations []Violation
}

// Error implements error.
func (e *VerificationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "netsec: chain verification failed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "netsec: chain verification failed with %d violation(s): ", len(e.Violations))
	for i, v := range e.Violations {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(v.Error())
	}
	return b.String()
}

// Unwrap exposes all violations to errors.Is and errors.As.
func (e *VerificationError) Unwrap() []error {
	if e == nil {
		return nil
	}
	errs := make([]error, 0, len(e.Violations))
	for _, v := range e.Violations {
		errs = append(errs, v.Err)
	}
	return errs
}
