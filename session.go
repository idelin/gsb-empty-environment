package cid

import "net"

// Session is the state the router needs from a DTLS session. SetPeerAddress is
// called only after the record has passed CID length checks, AEAD verification,
// and replay checks.
type Session interface {
	ConnectionID() []byte
	PeerAddress() net.Addr
	SetPeerAddress(net.Addr)
}

// MemorySession is a small Session implementation useful for adapters and
// tests.
type MemorySession struct {
	CID  []byte
	Peer net.Addr
}

// NewMemorySession creates a session with a copied connection ID.
func NewMemorySession(connectionID []byte, peer net.Addr) *MemorySession {
	return &MemorySession{
		CID:  append([]byte(nil), connectionID...),
		Peer: peer,
	}
}

// ConnectionID returns the session's negotiated connection ID.
func (s *MemorySession) ConnectionID() []byte {
	return s.CID
}

// PeerAddress returns the session's current peer address.
func (s *MemorySession) PeerAddress() net.Addr {
	return s.Peer
}

// SetPeerAddress updates the session's current peer address.
func (s *MemorySession) SetPeerAddress(addr net.Addr) {
	s.Peer = addr
}
