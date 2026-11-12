package netsec

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
)

const (
	curveMessageName       = "MESSAGE"
	curveMessageNameLength = len(curveMessageName)
	curveShortNonceSize    = 8
	curveMessageBoxMinSize = 17
	curveMessageMinSize    = 1 + curveMessageNameLength + curveShortNonceSize + curveMessageBoxMinSize
)

// CurveMessage is a decoded CURVE MESSAGE command. The short nonce is
// decoded from the eight-octet big-endian field used by libzmq.
type CurveMessage struct {
	Nonce uint64
	Box   []byte
}

// ParseCurveMessage decodes a CURVE MESSAGE command body. ZMTP command
// bodies begin with a one-octet command-name length (0x07) followed by
// "MESSAGE". For interoperability with tests and older senders, the
// length prefix may be omitted; the nonce and box layout is otherwise
// identical.
func ParseCurveMessage(body []byte) (CurveMessage, error) {
	nonceOffset := -1
	switch {
	case len(body) >= 1+curveMessageNameLength &&
		body[0] == byte(curveMessageNameLength) &&
		string(body[1:1+curveMessageNameLength]) == curveMessageName:
		nonceOffset = 1 + curveMessageNameLength
	case len(body) >= curveMessageNameLength &&
		string(body[:curveMessageNameLength]) == curveMessageName:
		nonceOffset = curveMessageNameLength
	default:
		return CurveMessage{}, fmt.Errorf("%w: command body does not start with MESSAGE", ErrNotCurveMessage)
	}

	if len(body) < nonceOffset+curveShortNonceSize+curveMessageBoxMinSize {
		return CurveMessage{}, fmt.Errorf(
			"%w: body length %d, need at least %d",
			ErrMalformedCurveMessage, len(body), nonceOffset+curveShortNonceSize+curveMessageBoxMinSize,
		)
	}

	nonceEnd := nonceOffset + curveShortNonceSize
	nonce := binary.BigEndian.Uint64(body[nonceOffset:nonceEnd])
	box := make([]byte, len(body)-nonceEnd)
	copy(box, body[nonceEnd:])

	return CurveMessage{Nonce: nonce, Box: box}, nil
}

// EncodeCurveMessage builds a CURVE MESSAGE command body with a
// big-endian short nonce and the supplied message box. The box must be at
// least 17 octets.
func EncodeCurveMessage(nonce uint64, box []byte) ([]byte, error) {
	if len(box) < curveMessageBoxMinSize {
		return nil, fmt.Errorf("%w: box length %d", ErrMalformedCurveMessage, len(box))
	}
	out := make([]byte, 1+curveMessageNameLength+curveShortNonceSize+len(box))
	out[0] = byte(curveMessageNameLength)
	copy(out[1:], curveMessageName)
	nonceOffset := 1 + curveMessageNameLength
	binary.BigEndian.PutUint64(out[nonceOffset:nonceOffset+curveShortNonceSize], nonce)
	copy(out[nonceOffset+curveShortNonceSize:], box)
	return out, nil
}

// NonceTracker enforces strictly increasing CURVE short nonces for one
// inbound direction of one connection. It is safe for concurrent use.
type NonceTracker struct {
	mu       sync.Mutex
	last     uint64
	hasNonce bool
}

// Check accepts nonce only when it is greater than every previously
// accepted nonce. Replayed and rolled-back values return ErrNonceReplay.
func (t *NonceTracker) Check(nonce uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.hasNonce {
		if nonce <= t.last {
			return fmt.Errorf("%w: got %d after %d", ErrNonceReplay, nonce, t.last)
		}
	}

	t.last = nonce
	t.hasNonce = true
	return nil
}

// Accept is an alias for Check.
func (t *NonceTracker) Accept(nonce uint64) error {
	return t.Check(nonce)
}

// Last returns the most recently accepted nonce.
func (t *NonceTracker) Last() (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last, t.hasNonce
}

// Exhausted reports whether the tracker has accepted math.MaxUint64 and
// therefore cannot accept any further value.
func (t *NonceTracker) Exhausted() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.hasNonce && t.last == math.MaxUint64
}

// Reset clears the tracker so it can be reused for a new connection.
func (t *NonceTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last = 0
	t.hasNonce = false
}

// CheckCurveNonce decodes a CURVE MESSAGE body, checks its nonce against
// tracker, and returns the decoded message.
func CheckCurveNonce(body []byte, tracker *NonceTracker) (CurveMessage, error) {
	if tracker == nil {
		return CurveMessage{}, fmt.Errorf("%w: nil nonce tracker", ErrNonceReplay)
	}
	message, err := ParseCurveMessage(body)
	if err != nil {
		return CurveMessage{}, err
	}
	if err := tracker.Check(message.Nonce); err != nil {
		return CurveMessage{}, err
	}
	return message, nil
}
