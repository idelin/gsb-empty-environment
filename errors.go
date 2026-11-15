package netsec

import (
	"errors"
	"fmt"
)

// Errors returned by this package. Errors caused by malformed or inconsistent
// certificate compression data also match ErrBadCertificate via errors.Is.
var (
	ErrInvalidExtension = errors.New("tls: invalid certificate compression extension")

	ErrUnsupportedAlgorithm = errors.New("tls: unsupported certificate compression algorithm")
	ErrAlgorithmNotOffered  = errors.New("tls: certificate compression algorithm not offered by client")

	ErrDictionaryNotFound    = errors.New("tls: certificate compression dictionary not found")
	ErrCrossTenantDictionary = errors.New("tls: certificate compression dictionary belongs to another tenant")
	ErrDictionaryIDConflict  = errors.New("tls: certificate compression dictionary ID conflict")
	ErrSNIConflict           = errors.New("tls: SNI is already bound to another tenant")

	ErrCompressedCertificateAlreadySent = errors.New("tls: compressed certificate already sent on this connection")

	ErrInvalidCertificateMessage = errors.New("tls: invalid certificate message")

	ErrUncompressedLengthTooLarge = errors.New("tls: uncompressed certificate length exceeds 2^24-1")
	ErrUncompressedLengthMismatch = errors.New("tls: uncompressed certificate length mismatch")
	ErrInvalidCompressedLength    = errors.New("tls: invalid compressed certificate length")

	// ErrBadCertificate is the TLS bad_certificate(42) alert condition.
	ErrBadCertificate = errors.New("tls: bad certificate")
)

// BadCertificateError carries a human-readable reason while keeping
// errors.Is(err, ErrBadCertificate) working.
type BadCertificateError struct {
	Detail string
	Cause  error
}

func (err *BadCertificateError) Error() string {
	if err.Detail != "" && err.Cause != nil {
		return fmt.Sprintf("%v: %s: %v", ErrBadCertificate, err.Detail, err.Cause)
	}
	if err.Detail != "" {
		return fmt.Sprintf("%v: %s", ErrBadCertificate, err.Detail)
	}
	if err.Cause != nil {
		return fmt.Sprintf("%v: %v", ErrBadCertificate, err.Cause)
	}
	return ErrBadCertificate.Error()
}

func (err *BadCertificateError) Unwrap() []error {
	causes := []error{ErrBadCertificate}
	if err.Cause != nil {
		causes = append(causes, err.Cause)
	}
	return causes
}

// IsBadCertificate reports whether err must be handled as bad_certificate(42).
func IsBadCertificate(err error) bool {
	return errors.Is(err, ErrBadCertificate)
}

func badCertificate(detail string, cause error) error {
	return &BadCertificateError{Detail: detail, Cause: cause}
}

// TLSAlertBadCertificate is the wire value of bad_certificate.
const TLSAlertBadCertificate uint8 = 42

// AlertForError maps an error produced by this package to a TLS alert. A nil
// error maps to 0. Non-certificate parsing errors map to internal_error(80).
func AlertForError(err error) uint8 {
	switch {
	case err == nil:
		return 0
	case IsBadCertificate(err):
		return TLSAlertBadCertificate
	default:
		return 80
	}
}
