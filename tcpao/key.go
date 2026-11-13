package tcpao

import (
	"fmt"
	"time"
)

// KeyID is the 8-bit TCP-AO key identifier. Zero is reserved by TCP-AO and is
// never a valid KeyID or RNextKeyID.
type KeyID uint8

// NoKey is the TCP-AO value used when no next key is requested.
const NoKey KeyID = 0

// Valid reports whether id can identify a TCP-AO key.
func (id KeyID) Valid() bool {
	return id != NoKey
}

// Algorithm identifies a TCP-AO MAC algorithm. An empty Algorithm selects the
// default, HMAC-SHA1-96, for compatibility with TCP-AO's mandatory algorithm.
type Algorithm string

const (
	// HMACSHA1 is HMAC-SHA1 truncated to 96 bits.
	HMACSHA1 Algorithm = "hmac-sha1-96"

	// HMACSHA256 is HMAC-SHA-256 truncated to 128 bits.
	HMACSHA256 Algorithm = "hmac-sha256-128"
)

func (a Algorithm) normalized() (Algorithm, error) {
	switch a {
	case "", HMACSHA1:
		return HMACSHA1, nil
	case HMACSHA256:
		return HMACSHA256, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, a)
	}
}

// MACSize returns the number of bytes produced by the algorithm. It returns
// zero for an unsupported algorithm.
func (a Algorithm) MACSize() int {
	switch a {
	case "", HMACSHA1:
		return 12
	case HMACSHA256:
		return 16
	default:
		return 0
	}
}

// Validity is a half-open time interval [NotBefore, NotAfter). A zero field
// means that side of the interval is unbounded. The interval is evaluated in
// the caller's time base, so tests and transports can supply a deterministic
// clock without changing the selector.
type Validity struct {
	NotBefore time.Time
	NotAfter  time.Time
}

// Contains reports whether at is inside the validity interval.
func (v Validity) Contains(at time.Time) bool {
	if !v.NotBefore.IsZero() && at.Before(v.NotBefore) {
		return false
	}
	if !v.NotAfter.IsZero() && !at.Before(v.NotAfter) {
		return false
	}
	return true
}

func (v Validity) validate() error {
	if !v.NotBefore.IsZero() && !v.NotAfter.IsZero() && !v.NotBefore.Before(v.NotAfter) {
		return fmt.Errorf("%w: empty validity interval", ErrInvalidKey)
	}
	return nil
}

type validityState uint8

const (
	validityOK validityState = iota
	validityFuture
	validityExpired
)

func (v Validity) state(at time.Time) validityState {
	if !v.NotBefore.IsZero() && at.Before(v.NotBefore) {
		return validityFuture
	}
	if !v.NotAfter.IsZero() && !at.Before(v.NotAfter) {
		return validityExpired
	}
	return validityOK
}

// Key is one registered TCP-AO key. SendKey authenticates outbound segments and
// ReceiveKey verifies inbound segments. They are separate values to support
// directional keys as required by TCP-AO.
//
// SendValidity controls whether this key may become, or remain, the selected
// send key. ReceiveValidity controls whether inbound segments carrying this
// KeyID are accepted. The two intervals may differ, for example to keep a key
// receivable while it is no longer permitted for transmission.
type Key struct {
	ID              KeyID
	SendKey         []byte
	ReceiveKey      []byte
	Algorithm       Algorithm
	SendValidity    Validity
	ReceiveValidity Validity
}

// Validate checks the key without registering or retaining it.
func (k Key) Validate() error {
	_, err := k.normalized()
	return err
}

func (k Key) normalized() (Key, error) {
	if !k.ID.Valid() {
		return Key{}, fmt.Errorf("%w: zero key id", ErrInvalidKey)
	}
	if len(k.SendKey) == 0 {
		return Key{}, fmt.Errorf("%w: key %d has no send key", ErrInvalidKey, k.ID)
	}
	if len(k.ReceiveKey) == 0 {
		return Key{}, fmt.Errorf("%w: key %d has no receive key", ErrInvalidKey, k.ID)
	}

	alg, err := k.Algorithm.normalized()
	if err != nil {
		return Key{}, fmt.Errorf("%w: key %d: %w", ErrInvalidKey, k.ID, err)
	}
	if err := k.SendValidity.validate(); err != nil {
		return Key{}, fmt.Errorf("key %d send validity: %w", k.ID, err)
	}
	if err := k.ReceiveValidity.validate(); err != nil {
		return Key{}, fmt.Errorf("key %d receive validity: %w", k.ID, err)
	}

	k.Algorithm = alg
	k.SendKey = cloneBytes(k.SendKey)
	k.ReceiveKey = cloneBytes(k.ReceiveKey)
	return k, nil
}

func (k Key) clone() Key {
	k.SendKey = cloneBytes(k.SendKey)
	k.ReceiveKey = cloneBytes(k.ReceiveKey)
	return k
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}
