package netsec

import "errors"

var (
	// ErrInvalidPrefix reports a tenant prefix that cannot be used on the
	// Memcached wire (empty, too long, or containing whitespace/control bytes).
	ErrInvalidPrefix = errors.New("netsec: invalid tenant prefix")
	// ErrInvalidConfig reports an impossible limit configuration.
	ErrInvalidConfig = errors.New("netsec: invalid configuration")
	// ErrInvalidKey reports a key that is empty or contains whitespace or
	// control bytes.
	ErrInvalidKey = errors.New("netsec: invalid key")
	// ErrKeyTooLong reports a prefixed key longer than the protocol limit.
	ErrKeyTooLong = errors.New("netsec: prefixed key exceeds 250 bytes")
	// ErrMalformedRequest reports a request that does not follow the
	// selected protocol.
	ErrMalformedRequest = errors.New("netsec: malformed request")
	// ErrMalformedBody reports a storage command whose data block is not
	// exactly the declared size followed by CRLF.
	ErrMalformedBody = errors.New("netsec: data block length mismatch")
	// ErrMalformedResponse reports a response that cannot be framed safely.
	ErrMalformedResponse = errors.New("netsec: malformed response")
	// ErrTruncated reports EOF inside a request or response frame.
	ErrTruncated = errors.New("netsec: truncated frame")
	// ErrProtocolSwitch reports bytes of a second protocol on a connection
	// whose protocol has already been selected.
	ErrProtocolSwitch = errors.New("netsec: protocol changed after lock")
	// ErrBinaryUnsupported reports a binary request on a normalizer that is
	// configured for text-only forwarding.
	ErrBinaryUnsupported = errors.New("netsec: binary protocol is not enabled")
	// ErrUnsupportedCommand reports a command outside the allowlist.
	ErrUnsupportedCommand = errors.New("netsec: unsupported command")
	// ErrTooLarge reports a command line, value, or response above its limit.
	ErrTooLarge = errors.New("netsec: frame exceeds configured limit")
	// ErrTrailingData reports extra bytes after a complete request passed to
	// the one-shot normalization helpers.
	ErrTrailingData = errors.New("netsec: trailing data after request")
)
