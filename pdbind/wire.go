package pdbind

import (
	"encoding/binary"
	"net/netip"
)

// DHCPv6 option codes relevant to prefix delegation (RFC 8415, RFC 8415).
const (
	OptClientID uint16 = 1
	OptIAPD     uint16 = 25
	OptIAPrefix uint16 = 26
	OptRelayMsg uint16 = 9
)

// walkOptions iterates over the TLV options in buf. It returns
// ErrMalformedMessage if the options do not fill the buffer exactly.
func walkOptions(buf []byte, fn func(code uint16, data []byte) error) error {
	for len(buf) >= 4 {
		code := binary.BigEndian.Uint16(buf[0:2])
		length := int(binary.BigEndian.Uint16(buf[2:4]))
		buf = buf[4:]
		if length > len(buf) {
			return ErrMalformedMessage
		}
		if err := fn(code, buf[:length]); err != nil {
			return err
		}
		buf = buf[length:]
	}
	if len(buf) != 0 {
		return ErrMalformedMessage
	}
	return nil
}

// ParseRelayChain unwraps nested relay-forward messages. It returns the hops
// ordered from the relay closest to the client to the outermost relay,
// followed by the innermost client message bytes. A chain deeper than depthCap
// (e.g. DefaultMaxRelayDepth) is rejected with ErrRelayDepth before parsing
// further, so a crafted packet cannot exhaust memory.
func ParseRelayChain(packet []byte, depthCap int) ([]RelayHop, []byte, error) {
	if depthCap <= 0 {
		depthCap = DefaultMaxRelayDepth
	}
	var hops []RelayHop
	cur := packet
	for {
		if len(cur) < 34 || MsgType(cur[0]) != MsgRelayForw {
			break
		}
		if len(hops) >= depthCap {
			return nil, nil, ErrRelayDepth
		}
		hop := RelayHop{HopCount: cur[1]}
		hop.LinkAddr = netip.AddrFrom16([16]byte(cur[2:18]))
		hop.PeerAddr = netip.AddrFrom16([16]byte(cur[18:34]))

		var inner []byte
		err := walkOptions(cur[34:], func(code uint16, data []byte) error {
			if code == OptRelayMsg {
				inner = data
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
		if inner == nil {
			return nil, nil, ErrMalformedMessage
		}
		// Packets nest the innermost relay closest to the client; unwrapping
		// walks from the outermost relay. Prepend so the returned slice is
		// ordered client-side first.
		hops = append([]RelayHop{hop}, hops...)
		cur = inner
	}
	if len(hops) == 0 {
		return nil, nil, ErrRelayMessage
	}
	if len(cur) < 1 || MsgType(cur[0]) == MsgRelayForw || MsgType(cur[0]) == MsgRelayRepl {
		return nil, nil, ErrMalformedMessage
	}
	return hops, cur, nil
}

// ParseClientMessage parses a non-relay DHCPv6 client message and extracts
// the fields relevant to prefix delegation.
func ParseClientMessage(packet []byte) (ClientMessage, error) {
	if len(packet) < 4 {
		return ClientMessage{}, ErrMalformedMessage
	}
	msgType := MsgType(packet[0])
	if msgType == MsgRelayForw || msgType == MsgRelayRepl {
		return ClientMessage{}, ErrRelayMessage
	}

	var msg ClientMessage
	msg.Type = msgType
	err := walkOptions(packet[4:], func(code uint16, data []byte) error {
		switch code {
		case OptClientID:
			if len(data) < 2 {
				return ErrMalformedMessage
			}
			msg.DUID = append([]byte(nil), data...)
		case OptIAPD:
			ia, err := parseIAPD(data)
			if err != nil {
				return err
			}
			msg.IAPDs = append(msg.IAPDs, ia)
		}
		return nil
	})
	if err != nil {
		return ClientMessage{}, err
	}
	return msg, nil
}

// parseIAPD parses an IA_PD option body: IAID (4), T1 (4), T2 (4), options.
func parseIAPD(data []byte) (IAPD, error) {
	if len(data) < 12 {
		return IAPD{}, ErrMalformedMessage
	}
	ia := IAPD{IAID: binary.BigEndian.Uint32(data[0:4])}
	err := walkOptions(data[12:], func(code uint16, opt []byte) error {
		if code != OptIAPrefix {
			return nil
		}
		if ia.Hint != nil {
			return nil
		}
		lease, err := parseIAPrefix(opt)
		if err != nil {
			return err
		}
		ia.Hint = lease
		return nil
	})
	if err != nil {
		return IAPD{}, err
	}
	return ia, nil
}

// parseIAPrefix parses an IAPREFIX option body: preferred (4), valid (4),
// prefix length (1), prefix (16).
func parseIAPrefix(data []byte) (*PrefixLease, error) {
	if len(data) != 25 {
		return nil, ErrMalformedMessage
	}
	prefixLen := int(data[8])
	if prefixLen > 128 {
		return nil, ErrMalformedMessage
	}
	addr := netip.AddrFrom16([16]byte(data[9:25]))
	return &PrefixLease{
		PreferredLft: binary.BigEndian.Uint32(data[0:4]),
		ValidLft:     binary.BigEndian.Uint32(data[4:8]),
		Prefix:       netip.PrefixFrom(addr, prefixLen),
	}, nil
}

// ParseMessage is a convenience entry point for the packet handler. It parses
// either a direct client message or a relayed message. peerAddr is the source
// address of the UDP packet and is used as the next hop for direct clients.
func ParseMessage(packet []byte, peerAddr netip.Addr, depthCap int) (ClientMessage, []RelayHop, error) {
	if len(packet) == 0 {
		return ClientMessage{}, nil, ErrMalformedMessage
	}
	if MsgType(packet[0]) == MsgRelayForw {
		hops, inner, err := ParseRelayChain(packet, depthCap)
		if err != nil {
			return ClientMessage{}, nil, err
		}
		msg, err := ParseClientMessage(inner)
		if err != nil {
			return ClientMessage{}, nil, err
		}
		return msg, hops, nil
	}
	msg, err := ParseClientMessage(packet)
	if err != nil {
		return ClientMessage{}, nil, err
	}
	msg.PeerAddr = peerAddr
	return msg, nil, nil
}
