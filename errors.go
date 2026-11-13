package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
)

var (
	// ErrInvalidChain is returned when the supplied certificates do not form
	// a usable leaf-to-root path.
	ErrInvalidChain = errors.New("netsec: invalid certificate chain")

	// ErrNameConstraintsOnNonCA is returned when a certificate that is not a
	// CA carries the name constraints extension. RFC 5280, section 4.2.1.10,
	// restricts this extension to CA certificates.
	ErrNameConstraintsOnNonCA = errors.New("netsec: name constraints are only permitted on CA certificates")

	// ErrNameNotPermitted is returned when a name falls outside every
	// permitted subtree of an applicable name constraint.
	ErrNameNotPermitted = errors.New("netsec: name is not permitted by name constraints")

	// ErrNameExcluded is returned when a name falls inside an excluded
	// subtree of an applicable name constraint.
	ErrNameExcluded = errors.New("netsec: name is excluded by name constraints")

	// ErrUnmatchableName is returned when a constrained name cannot be
	// compared with the constraint, for example a URI without a host.
	ErrUnmatchableName = errors.New("netsec: name cannot be matched against constraints")

	// ErrTooManyConstraints is returned when a pathological certificate
	// contains too many names or subtrees to process safely.
	ErrTooManyConstraints = errors.New("netsec: too many name constraint comparisons")

	// ErrUnhandledCriticalExtension is returned for a certificate with a
	// critical extension that the verifier does not understand.
	ErrUnhandledCriticalExtension = errors.New("netsec: unhandled critical extension")

	// ErrCertificateExpired is returned for a certificate outside its
	// validity period.
	ErrCertificateExpired = errors.New("netsec: certificate is outside its validity period")

	// ErrNotAuthorizedToSign is returned when a non-CA certificate is used
	// to issue another certificate.
	ErrNotAuthorizedToSign = errors.New("netsec: certificate is not authorized to sign other certificates")

	// ErrPathLengthExceeded is returned when a basic constraints path length
	// is violated.
	ErrPathLengthExceeded = errors.New("netsec: path length constraint exceeded")

	// ErrSignature is returned when a certificate signature cannot be
	// verified with its issuer's public key.
	ErrSignature = errors.New("netsec: invalid certificate signature")
)

// ValidationError describes why a certificate in a path was rejected.
type ValidationError struct {
	// Cert is the certificate that caused the failure.
	Cert *x509.Certificate
	// Index is the position of Cert in the leaf-to-root chain.
	Index int
	// Err is the underlying sentinel error.
	Err error
	// Detail provides human-readable context.
	Detail string
}

func (e *ValidationError) Error() string {
	subject := ""
	if e.Cert != nil {
		subject = e.Cert.Subject.String()
	}

	switch {
	case e.Detail != "" && subject != "":
		return fmt.Sprintf("%v (certificate %d, subject %q: %s)", e.Err, e.Index, subject, e.Detail)
	case e.Detail != "":
		return fmt.Sprintf("%v (certificate %d: %s)", e.Err, e.Index, e.Detail)
	case subject != "":
		return fmt.Sprintf("%v (certificate %d, subject %q)", e.Err, e.Index, subject)
	default:
		return fmt.Sprintf("%v (certificate %d)", e.Err, e.Index)
	}
}

// Unwrap makes errors.Is and errors.As work with the sentinel errors above.
func (e *ValidationError) Unwrap() error { return e.Err }

func validationError(index int, cert *x509.Certificate, err error, detail string) error {
	return &ValidationError{Cert: cert, Index: index, Err: err, Detail: detail}
}
