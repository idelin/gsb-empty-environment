package rtsp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// SessionIDSize is the size of a session identifier in bytes.
const SessionIDSize = 16

// SessionID is a 128-bit cryptographically random session identifier.
type SessionID [SessionIDSize]byte

// newSessionID draws a fresh 128-bit identifier from crypto/rand.
func newSessionID() (SessionID, error) {
	var id SessionID
	if _, err := rand.Read(id[:]); err != nil {
		return SessionID{}, err
	}
	return id, nil
}

// String renders the identifier as lowercase hexadecimal, matching the
// textual form carried in the RTSP Session header.
func (id SessionID) String() string {
	return hex.EncodeToString(id[:])
}

// ParseSessionID decodes a 32-character hexadecimal Session header value.
func ParseSessionID(text string) (SessionID, error) {
	var id SessionID
	raw, err := hex.DecodeString(text)
	if err != nil || len(raw) != SessionIDSize {
		return SessionID{}, errBadRequest
	}
	copy(id[:], raw)
	return id, nil
}

// equal compares two identifiers in constant time.
func (id SessionID) equal(other SessionID) bool {
	return subtle.ConstantTimeCompare(id[:], other[:]) == 1
}

// Principal is the authenticated identity (for example a user name) that the
// session is bound to. Requests on a session are only accepted from the
// principal that created it.
type Principal string

// sessionState tracks the RTSP lifecycle of a single session.
type sessionState int

const (
	stateReady   sessionState = iota // SETUP accepted, waiting for PLAY
	statePlaying                     // PLAY accepted, media delivery active
)

// Session is a single RTSP presentation session. Manager methods return
// value copies, so callers cannot mutate live session state directly.
type Session struct {
	ID       SessionID
	Owner    Principal
	Target   Target
	state    sessionState
	lastCSeq uint32
}

// Target is the only network address media for a session may ever be sent
// to. It is pinned at SETUP time to the request source and the validated
// client_port pair, so a rewritten Transport header cannot redirect the
// stream elsewhere.
type Target struct {
	// Addr is the pinned request source (and validated Transport destination).
	Addr [16]byte
	// IsV4 reports whether Addr holds an IPv4-mapped IPv6 representation.
	IsV4 bool
	// RTPPort is the even first port of the client_port pair.
	RTPPort uint16
	// RTCPPort is the consecutive odd second port.
	RTCPPort uint16
}
