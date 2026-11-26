package quicguard

import (
	"crypto/subtle"
	"fmt"
	"net/netip"
)

const (
	framePathChallenge = 0x1a
	framePathResponse  = 0x1b
	pathDataLen        = 8
)

// BuildPathChallengeFrame returns a PATH_CHALLENGE frame carrying an 8-byte
// random value (RFC 9000 section 19.17). The same value is stored as the
// outstanding challenge for the new path.
func BuildPathChallengeFrame(data [pathDataLen]byte) []byte {
	return append([]byte{framePathChallenge}, data[:]...)
}

// BuildPathResponseFrame returns the PATH_RESPONSE frame echoing a received
// PATH_CHALLENGE value (RFC 9000 section 19.18).
func BuildPathResponseFrame(data [pathDataLen]byte) []byte {
	return append([]byte{framePathResponse}, data[:]...)
}

// ParsePathFrame extracts the 8-byte value from a PATH_CHALLENGE or
// PATH_RESPONSE frame.
func ParsePathFrame(frame []byte) (frameType byte, data [pathDataLen]byte, err error) {
	if len(frame) != 1+pathDataLen ||
		(frame[0] != framePathChallenge && frame[0] != framePathResponse) {
		return 0, data, ErrPathFrame
	}
	copy(data[:], frame[1:])
	return frame[0], data, nil
}

// resetPathLocked installs a fresh unvalidated record for src, zeroing the
// anti-amplification budget and discarding any old queue or challenge.
func (g *Guard) resetPathLocked(src netip.AddrPort) *pathState {
	p := &pathState{}
	g.paths[src] = p
	return p
}

// BeginPathValidation arms a new path: the record is reset so the new path
// starts with rxBytes=txBytes=0 and a fresh 3x limit, and an 8-byte random
// PATH_CHALLENGE is generated and recorded. The datagram containing the
// PATH_CHALLENGE frame must still pass Send's budget check before emission.
func (g *Guard) BeginPathValidation(src netip.AddrPort) ([pathDataLen]byte, []byte, error) {
	var challenge [pathDataLen]byte
	if _, err := g.readRandom(challenge[:]); err != nil {
		return challenge, nil, err
	}
	g.mu.Lock()
	p := g.resetPathLocked(src)
	p.pending = challenge
	p.hasPending = true
	g.mu.Unlock()
	return challenge, BuildPathChallengeFrame(challenge), nil
}

// HandlePathResponse verifies a PATH_RESPONSE frame against the outstanding
// PATH_CHALLENGE for src. On success the path becomes validated, its pending
// challenge is cleared and any suspended datagrams are released.
func (g *Guard) HandlePathResponse(src netip.AddrPort, frame []byte) ([]QueuedPacket, error) {
	frameType, response, err := ParsePathFrame(frame)
	if err != nil {
		return nil, err
	}
	if frameType != framePathResponse {
		return nil, fmt.Errorf("%w: frame type 0x%02x", ErrPathFrame, frameType)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	p, ok := g.paths[src]
	if !ok || !p.hasPending {
		return nil, ErrNoPendingChallenge
	}
	if subtle.ConstantTimeCompare(response[:], p.pending[:]) != 1 {
		return nil, ErrPathChallengeMismatch
	}
	p.hasPending = false
	p.validated = true
	p.reason = "path-challenge"
	return g.drainQueueLocked(p), nil
}

// HandlePathChallenge echoes an inbound PATH_CHALLENGE as a PATH_RESPONSE
// frame, per RFC 9000 section 8.2.3. It does not change validation state.
func HandlePathChallenge(frame []byte) ([]byte, error) {
	frameType, data, err := ParsePathFrame(frame)
	if err != nil {
		return nil, err
	}
	if frameType != framePathChallenge {
		return nil, fmt.Errorf("%w: frame type 0x%02x", ErrPathFrame, frameType)
	}
	return BuildPathResponseFrame(data), nil
}
