package netsec

import (
	"crypto/subtle"
	"errors"
)

var (
	// ErrCookieMissing is returned when the server issued a cookie in
	// HelloRetryRequest but the second ClientHello carries none.
	ErrCookieMissing = errors.New("netsec: second ClientHello missing required cookie echo")
	// ErrCookieMismatch is returned when the echoed cookie differs
	// from the one the server issued.
	ErrCookieMismatch = errors.New("netsec: cookie echo does not match issued cookie")
)

// VerifyEcho enforces the retry-cookie echo policy on the server:
//
//   - issued == nil: the server sent no cookie, so the handshake
//     proceeds regardless of what the second ClientHello carries.
//   - issued != nil, echoed == nil: the client dropped the cookie;
//     the handshake fails with ErrCookieMissing.
//   - issued != nil, echoed != issued: the cookie was altered;
//     the handshake fails with ErrCookieMismatch.
//   - issued == echoed (byte-for-byte): the handshake proceeds.
//
// issued is the cookie the TLS stack placed into HelloRetryRequest;
// echoed is the cookie extension of the second ClientHello.
func VerifyEcho(issued, echoed []byte) error {
	if len(issued) == 0 {
		return nil
	}
	if len(echoed) == 0 {
		return ErrCookieMissing
	}
	if subtle.ConstantTimeCompare(issued, echoed) != 1 {
		return ErrCookieMismatch
	}
	return nil
}
