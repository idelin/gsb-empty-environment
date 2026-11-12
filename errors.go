package netsec

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrFingerprintChanged is returned when a known host presents a
	// different certificate and neither of the permitted change conditions
	// holds.
	ErrFingerprintChanged = errors.New("netsec: server certificate fingerprint changed")

	// ErrFingerprintMismatch and ErrPinMismatch are aliases for
	// ErrFingerprintChanged.
	ErrFingerprintMismatch = ErrFingerprintChanged
	ErrPinMismatch         = ErrFingerprintChanged

	// ErrHostnameMismatch is returned when the leaf certificate is not valid
	// for the hostname that was connected to.
	ErrHostnameMismatch = errors.New("netsec: certificate hostname does not match connected host")

	// ErrHandshakeIncomplete is returned when pinning is attempted before
	// the TLS handshake has completed.
	ErrHandshakeIncomplete = errors.New("netsec: TLS handshake is not complete")

	// ErrNoPeerCertificate is returned when a completed connection has no
	// peer certificate.
	ErrNoPeerCertificate = errors.New("netsec: TLS connection has no peer certificate")

	// ErrInvalidHostPort is returned for an invalid hostname, port, or
	// connection state passed to the pinner.
	ErrInvalidHostPort = errors.New("netsec: invalid host or port")
)

// FingerprintChangedError describes a rejected certificate change. It matches
// ErrFingerprintChanged with errors.Is.
type FingerprintChangedError struct {
	Host            string
	Port            int
	Stored          Fingerprint
	Presented       Fingerprint
	StoredFirstSeen time.Time
	StoredNotAfter  time.Time
	ChainError      error
}

func (e *FingerprintChangedError) Error() string {
	msg := fmt.Sprintf(
		"netsec: certificate fingerprint changed for %s:%d (stored %s, presented %s)",
		e.Host, e.Port, e.Stored, e.Presented,
	)
	if e.ChainError != nil {
		msg += ": " + e.ChainError.Error()
	}
	return msg
}

func (e *FingerprintChangedError) Unwrap() error {
	return e.ChainError
}

func (e *FingerprintChangedError) Is(target error) bool {
	return target == ErrFingerprintChanged
}
