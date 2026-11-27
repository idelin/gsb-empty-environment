package nat64

import "time"

// TCP control flag bits.
const (
	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpACK = 0x10
)

type tcpEvent int

const (
	evOther tcpEvent = iota
	evSYN
	evSYNACK
	evFIN
	evRST
)

// classifyTCP derives the RFC 6146 event from the TCP flags.
func classifyTCP(flags byte) tcpEvent {
	switch {
	case flags&tcpRST != 0:
		return evRST
	case flags&tcpSYN != 0 && flags&tcpACK == 0:
		return evSYN
	case flags&tcpSYN != 0 && flags&tcpACK != 0:
		return evSYNACK
	case flags&tcpFIN != 0:
		return evFIN
	default:
		return evOther
	}
}

// closeSession is returned by tcpTransition when the session must be
// deleted and the BIB reference released.
const closeSession = TCPState(-1)

// tcpTransition implements RFC 6146 section 3.5.2 table 2. The V4_INIT
// simultaneous-open pairing itself is driven by the engine; this table
// covers every event for an existing session.
func tcpTransition(state TCPState, ev tcpEvent) TCPState {
	switch state {
	case StateV6Init:
		switch ev {
		case evSYN:
			return StateV6Init // SYN retransmit from either side
		case evSYNACK:
			// SYN-ACK arriving from the IPv4 peer answers the v6 SYN.
			return StateV6Established
		case evFIN:
			return StateV6Trans
		case evRST:
			return closeSession
		}
	case StateV4Init:
		switch ev {
		case evSYN:
			return StateV4Init // cached-SYN retransmit
		case evSYNACK:
			return StateV4Established
		case evRST:
			return closeSession
		}
	case StateV6Established, StateV4Established:
		switch ev {
		case evFIN:
			// The side initiating close is resolved by the caller through
			// direction; unreachable events leave the state untouched.
			return state
		case evRST:
			return closeSession
		case evSYNACK:
			return state
		}
	case StateV6Trans:
		switch ev {
		case evFIN:
			return StateV4Trans
		case evOther, evSYNACK:
			return StateV4Established // final ACK from V4 half-closes the v6 side
		case evRST:
			return closeSession
		}
	case StateV4Trans:
		switch ev {
		case evFIN:
			return StateV6Trans
		case evOther, evSYNACK:
			return StateV6Established // final ACK from V6
		case evRST:
			return closeSession
		}
	}
	return state
}

// tcpFinTransition resolves FIN events, which need the packet direction.
func tcpFinTransition(state TCPState, dir direction) TCPState {
	switch state {
	case StateV6Init:
		if dir == dirV6 {
			return StateV6Trans
		}
	case StateV6Established, StateV4Established:
		if dir == dirV6 {
			return StateV6Trans
		}
		return StateV4Trans
	case StateV6Trans:
		if dir == dirV4 {
			return StateV4Trans
		}
		return StateV6Trans // retransmitted FIN
	case StateV4Trans:
		if dir == dirV6 {
			return StateV6Trans
		}
		return StateV4Trans
	}
	return state
}

// sessionDeadline computes the next expiry instant. V4_INIT holds the
// RFC 6146 6-second unsolicited-SYN timer; transitory TCP states use the
// 4-minute timer, established TCP uses 2 hours, UDP 5 minutes and ICMP 60s.
func (e *Engine) sessionDeadline(s *Session, now time.Time) time.Time {
	switch s.Proto {
	case ProtoICMP:
		return now.Add(e.cfg.ICMPTimeout)
	case ProtoUDP:
		return now.Add(e.cfg.UDPTimeout)
	case ProtoTCP:
		switch s.State {
		case StateV4Init:
			return now.Add(e.cfg.V4InitSynTimeout)
		case StateV6Trans, StateV4Trans, StateV6Init:
			return now.Add(e.cfg.TCPTransitoryTimeout)
		default:
			return now.Add(e.cfg.TCPEstablishedTimeout)
		}
	}
	return now.Add(e.cfg.UDPTimeout)
}
