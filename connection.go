package conntrack

import (
	"net/netip"
	"time"
)

// Default timeouts used by Table when the Table fields are left zero.
const (
	defaultHandshakeTimeout = 30 * time.Second
	defaultEstablishedIdle  = 30 * time.Minute
	defaultCloseTimeout     = 60 * time.Second
)

// endpoint holds the state owned by one side of a connection.
type endpoint struct {
	// Sequence numbers owned by this endpoint (its send space).
	isn    uint32 // initial sequence number (seq of its SYN)
	sndNxt uint32 // one past the highest sequence ever sent by it

	// Receive state for segments arriving at this endpoint.
	rcvNxt uint32 // next expected sequence (left edge of receive window)
	rcvWnd uint32 // current receive window, already scaled
	wscale uint8  // scale factor this endpoint advertised in its SYN

	synSeen  bool // a SYN from this endpoint was observed
	synAcked bool // its SYN was acknowledged by the peer
	finSeen  bool // a FIN from this endpoint was accepted
	finAcked bool // its FIN was acknowledged by the peer
	lastSeen time.Time
}

// scaledWindow applies the window scale advertised by the receiving endpoint
// to the raw window field of an incoming non-SYN segment.
func (e *endpoint) scaledWindow(raw uint16) uint32 {
	return uint32(raw) << e.wscale
}

// Conn is one tracked TCP connection. The zero value is not usable; obtain
// instances through Table.
type Conn struct {
	key   ConnKey
	state State

	// side[0] is the endpoint at key position 0, side[1] the endpoint at
	// key position 1. The initiator (first observed SYN sender) is recorded
	// separately because it may occupy either position.
	side      [2]*endpoint
	initiator int

	created time.Time
}

// Key returns the canonical connection key.
func (c *Conn) Key() ConnKey { return c.key }

// State returns the current connection state.
func (c *Conn) State() State { return c.state }

// endpointFor returns the endpoint with the given socket address and the
// peer endpoint, or nils when the address belongs to neither side.
func (c *Conn) endpointFor(ip netip.Addr, port uint16) (self, peer *endpoint, side int) {
	switch {
	case ip == c.key.IP0 && port == c.key.Port0:
		return c.side[0], c.side[1], 0
	case ip == c.key.IP1 && port == c.key.Port1:
		return c.side[1], c.side[0], 1
	default:
		return nil, nil, -1
	}
}

// endpoints returns the initiator and responder endpoints.
func (c *Conn) endpoints() (initiator, responder *endpoint) {
	return c.side[c.initiator], c.side[c.initiator^1]
}
