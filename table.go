package conntrack

import (
	"sync"
	"time"
)

// Table is the concurrent-safe connection tracking table.
type Table struct {
	mu    sync.Mutex
	conns map[ConnKey]*Conn

	// HandshakeTimeout bounds connections stuck in SYN_SENT/SYN_RCVD.
	HandshakeTimeout time.Duration
	// EstablishedIdle bounds idle established and half-closed connections.
	EstablishedIdle time.Duration
	// CloseTimeout bounds TIME_WAIT and other closing states.
	CloseTimeout time.Duration
}

// NewTable creates an empty table with default timeouts.
func NewTable() *Table {
	return &Table{
		conns:            make(map[ConnKey]*Conn),
		HandshakeTimeout: defaultHandshakeTimeout,
		EstablishedIdle:  defaultEstablishedIdle,
		CloseTimeout:     defaultCloseTimeout,
	}
}

// Process feeds one segment to the tracker and returns the verdict. Segments
// that do not belong to any known connection are dropped; a bare SYN for a
// new four-tuple creates a connection in SYN_SENT.
func (t *Table) Process(p Packet) Verdict {
	return t.ProcessAt(p, time.Now())
}

// ProcessAt is Process with an explicit timestamp, which makes timeout and
// idle behavior deterministic in tests.
func (t *Table) ProcessAt(p Packet, now time.Time) Verdict {
	if !validFlags(p.Flags) || p.Payload < 0 {
		return VerdictDrop
	}

	k := key(p)
	t.mu.Lock()
	defer t.mu.Unlock()

	c, ok := t.conns[k]
	if !ok {
		// Only a bare SYN (no ACK, RST or FIN) may create state.
		if p.Flags != FlagSYN {
			return VerdictDrop
		}
		c = t.newConn(k, p, now)
		t.conns[k] = c
		return VerdictAccept
	}

	self, peer, _ := c.endpointFor(p.DstIP, p.DstPort)
	if self == nil {
		return VerdictDrop
	}

	verdict := c.process(p, self, peer, now)

	if c.state == StateUnknown {
		// An accepted RST tears the connection down.
		delete(t.conns, k)
	}
	return verdict
}

// newConn initializes a connection from its opening bare SYN.
func (t *Table) newConn(k ConnKey, p Packet, now time.Time) *Conn {
	c := &Conn{
		key:     k,
		state:   StateSynSent,
		created: now,
		side:    [2]*endpoint{{}, {}},
	}

	initiatorSide := 0
	if p.SrcIP == k.IP1 && p.SrcPort == k.Port1 {
		initiatorSide = 1
	}
	c.initiator = initiatorSide

	peer := c.side[initiatorSide]   // sender of the SYN
	self := c.side[initiatorSide^1] // receiver of the SYN
	c.registerSyn(p, self, peer)
	// registerSyn records the peer (SYN sender) state; the sender's own
	// send space is initialized there via isn/sndNxt. Nothing else to do.
	peer.lastSeen = now
	return c
}

// Len returns the number of currently tracked connections.
func (t *Table) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.conns)
}

// Lookup returns the state of the connection matching the four-tuple, or
// StateUnknown when no connection exists.
func (t *Table) Lookup(p Packet) State {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.conns[key(p)]
	if !ok {
		return StateUnknown
	}
	return c.state
}

// Sweep removes connections whose handshake, idle or close timeout has
// elapsed and returns how many were deleted.
func (t *Table) Sweep(now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	removed := 0
	for k, c := range t.conns {
		if t.expired(c, now) {
			delete(t.conns, k)
			removed++
		}
	}
	return removed
}

func (t *Table) expired(c *Conn, now time.Time) bool {
	latest := c.created
	for _, e := range c.side {
		if e.lastSeen.After(latest) {
			latest = e.lastSeen
		}
	}
	idle := now.Sub(latest)

	switch c.state {
	case StateSynSent, StateSynRcvd:
		return idle >= t.HandshakeTimeout
	case StateTimeWait, StateClosing, StateLastAck, StateFinWait1,
		StateFinWait2, StateCloseWait:
		return idle >= t.CloseTimeout
	default:
		return idle >= t.EstablishedIdle
	}
}
