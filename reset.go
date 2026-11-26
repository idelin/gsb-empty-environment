package quicguard

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
)

// Stateless reset constants from RFC 9000 section 10.3.
const (
	statelessResetTokenLen = 16
	minStatelessResetSize  = 21
	maxStatelessResetSize  = 64
	// resetFirstByte uses the short-header form with both reserved bits set;
	// the fixed bit is still set as required by RFC 9000 section 17.4, and
	// recipients discard the packet before interpreting the remaining bits.
	resetFirstByte byte = 0x50
)

// StatelessResetToken derives the 16-byte token associated with a connection
// ID using HMAC-SHA256 keyed by the server-private ResetHMACKey. The token is
// deterministic per CID so the host can detect resets, but it is never exposed
// except inside a stateless reset packet for the owning peer.
func (g *Guard) StatelessResetToken(connectionID []byte) [statelessResetTokenLen]byte {
	mac := hmac.New(sha256.New, g.resetHMAC)
	mac.Write(connectionID)
	sum := mac.Sum(nil)
	var token [statelessResetTokenLen]byte
	copy(token[:], sum[:statelessResetTokenLen])
	return token
}

// CanReset reports whether a triggering packet of triggerLen bytes can be
// answered with a stateless reset: the reset must be at least 21 bytes and
// strictly shorter than the trigger (RFC 9000 section 10.3). A trigger of 21
// bytes or less leaves no legal reset size and is therefore ignored, which is
// the structural defense against infinite reset loops.
func CanReset(triggerLen int) bool {
	return triggerLen > minStatelessResetSize
}

func resetLength(triggerLen int) int {
	length := triggerLen - 1
	if length > maxStatelessResetSize {
		length = maxStatelessResetSize
	}
	return length
}

// BuildStatelessReset constructs a stateless reset packet for a triggering
// datagram. The result is shorter than the trigger and at least 21 bytes.
// Random padding is drawn from the guard's RNG so the bytes after the first
// byte carry no exploitable fixed fingerprint; the final 16 bytes are the
// CID-bound token.
func (g *Guard) BuildStatelessReset(connectionID []byte, triggerLen int) ([]byte, error) {
	if !CanReset(triggerLen) {
		return nil, fmt.Errorf("%w: trigger=%d", ErrResetTriggerTooSmall, triggerLen)
	}
	length := resetLength(triggerLen)
	packet := make([]byte, length)
	packet[0] = resetFirstByte
	padding := packet[1 : length-statelessResetTokenLen]
	if _, err := g.readRandom(padding); err != nil {
		return nil, err
	}
	token := g.StatelessResetToken(connectionID)
	copy(packet[length-statelessResetTokenLen:], token[:])
	return packet, nil
}

// IsStatelessReset reports whether packet looks like a stateless reset whose
// last 16 bytes match the token registered for connectionID. A candidate must
// be at least 21 bytes and use the short-header first-byte pattern, otherwise
// it cannot be a valid reset (RFC 9000 section 10.3.4).
func (g *Guard) IsStatelessReset(packet, connectionID []byte) bool {
	if len(packet) < minStatelessResetSize {
		return false
	}
	if packet[0]&formBit != 0 || packet[0]&fixedBit == 0 {
		return false
	}
	got := packet[len(packet)-statelessResetTokenLen:]
	expected := g.StatelessResetToken(connectionID)
	return subtle.ConstantTimeCompare(got, expected[:]) == 1
}

// MaybeStatelessReset is the safe entry point for handling an unrecognized
// datagram. It refuses to answer another stateless reset (breaking reset
// amplification loops) and rejects triggers too small for a compliant reset
// before building the response. The host must send the returned packet only to
// the source address of the triggering datagram.
func (g *Guard) MaybeStatelessReset(packet, connectionID []byte) ([]byte, error) {
	if g.IsStatelessReset(packet, connectionID) {
		return nil, ErrResetLoop
	}
	if !CanReset(len(packet)) {
		return nil, fmt.Errorf("%w: trigger=%d", ErrResetTriggerTooSmall, len(packet))
	}
	return g.BuildStatelessReset(connectionID, len(packet))
}
