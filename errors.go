package netsec

import (
	"errors"
	"fmt"
)

// DeliveryError describes a failed outbound delivery. Permanent failures are
// suitable for generating a non-delivery report; the caller must not retry
// them, and in particular must not retry another MX over plaintext.
type DeliveryError struct {
	Stage     string
	Code      int
	Response  string
	Permanent bool
	Err       error
}

func (e *DeliveryError) Error() string {
	var msg string
	if e.Response != "" {
		msg = e.Response
	} else if e.Err != nil {
		msg = e.Err.Error()
	} else {
		msg = "delivery failed"
	}
	if e.Stage == "" {
		return msg
	}
	return fmt.Sprintf("%s: %s", e.Stage, msg)
}

func (e *DeliveryError) Unwrap() error {
	return e.Err
}

// IsPermanent reports whether err is a permanent delivery failure.
func IsPermanent(err error) bool {
	var derr *DeliveryError
	return errors.As(err, &derr) && derr.Permanent
}

var (
	// ErrTLSRequiredHeaderConflict means the envelope uses the REQUIRETLS
	// parameter but the message carries TLS-Required: No.
	ErrTLSRequiredHeaderConflict = errors.New("netsec: TLS-Required: No conflicts with the REQUIRETLS parameter")

	// ErrTLSRequiredWithoutParameter means the message declares
	// TLS-Required: Require but the envelope did not use REQUIRETLS.
	ErrTLSRequiredWithoutParameter = errors.New("netsec: TLS-Required: Require requires the REQUIRETLS parameter")

	// ErrMalformedTLSRequiredHeader means TLS-Required appeared more than
	// once or carried a value other than No or Require.
	ErrMalformedTLSRequiredHeader = errors.New("netsec: malformed TLS-Required header")
)
