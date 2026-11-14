package netsec

import "errors"

var (
	// ErrHeaderInjection is returned when untrusted text contains a line break,
	// a control character, or another byte sequence that could alter the
	// structure of a message header.
	ErrHeaderInjection = errors.New("netsec: unsafe header value")

	// ErrInvalidAddress is returned for an address that is not a single,
	// ASCII-only RFC 5322 mailbox.
	ErrInvalidAddress = errors.New("netsec: invalid address")

	// ErrInvalidHeader is returned when a header cannot be represented without
	// violating the message format.
	ErrInvalidHeader = errors.New("netsec: invalid header")

	// ErrInvalidMIME is returned for a caller-supplied MIME value that is not a
	// bare media type.
	ErrInvalidMIME = errors.New("netsec: invalid MIME value")

	// ErrEncodedInput is returned when raw display text already contains an
	// RFC 2047 encoded-word. Accepting encoded input would make decoding
	// ambiguous and could enable a second decoding pass to expose line breaks.
	ErrEncodedInput = errors.New("netsec: pre-encoded text is not accepted")
)
