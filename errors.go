package esp

import "errors"

var (
	// ErrAuthentication is returned for both a bad ICV/GCM tag and an invalid
	// (or badly padded) ESP trailer. Callers must not be able to tell the two
	// cases apart.
	ErrAuthentication = errors.New("esp: authentication failed")

	// ErrReplay is returned for a sequence number that is a replay or lies
	// outside the replay window.
	ErrReplay = errors.New("esp: replayed or out-of-window sequence number")

	// ErrSequenceZero is returned because sequence number zero is forbidden.
	ErrSequenceZero = errors.New("esp: sequence number zero is forbidden")

	// ErrRekeyRequired is returned when the 64-bit sequence number space would
	// wrap. Rekeying is mandatory.
	ErrRekeyRequired = errors.New("esp: sequence number exhausted; rekey required")

	// ErrSequenceOverflow is an alias for ErrRekeyRequired.
	ErrSequenceOverflow = ErrRekeyRequired

	ErrInvalidWindowSize = errors.New("esp: replay window must be 64, 256, or 1024 bits")
	ErrInvalidKey        = errors.New("esp: invalid AES or MAC key")
	ErrInvalidConfig     = errors.New("esp: invalid configuration")
	ErrMalformedPacket   = errors.New("esp: malformed ESP packet")
	ErrSPIMismatch       = errors.New("esp: unexpected SPI")
)
