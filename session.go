package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const SessionIDSize = 16

// SessionID is an unguessable cache session identifier. A change of session
// invalidates all incremental history and requires a complete reset.
type SessionID [SessionIDSize]byte

func (s SessionID) IsZero() bool {
	var zero SessionID
	return s == zero
}

func (s SessionID) String() string { return hex.EncodeToString(s[:]) }

func ParseSessionID(text string) (SessionID, error) {
	var s SessionID
	b, err := hex.DecodeString(text)
	if err != nil {
		return s, fmt.Errorf("%w: invalid session ID: %v", ErrMalformedMessage, err)
	}
	if len(b) != len(s) {
		return s, fmt.Errorf("%w: session ID is %d bytes, want %d", ErrMalformedMessage, len(b), len(s))
	}
	copy(s[:], b)
	if s.IsZero() {
		return s, fmt.Errorf("%w: zero session ID", ErrMalformedMessage)
	}
	return s, nil
}

func SessionIDFromBytes(b []byte) (SessionID, error) {
	var s SessionID
	if len(b) != len(s) {
		return s, fmt.Errorf("%w: session ID is %d bytes, want %d", ErrMalformedMessage, len(b), len(s))
	}
	copy(s[:], b)
	if s.IsZero() {
		return s, fmt.Errorf("%w: zero session ID", ErrMalformedMessage)
	}
	return s, nil
}

// NewSessionID returns a non-zero random session identifier.
func NewSessionID() (SessionID, error) {
	for {
		var s SessionID
		if _, err := rand.Read(s[:]); err != nil {
			return s, fmt.Errorf("netsec: generate session ID: %w", err)
		}
		if !s.IsZero() {
			return s, nil
		}
	}
}
