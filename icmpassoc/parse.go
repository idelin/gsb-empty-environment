package icmpassoc

import (
	"encoding/binary"
	"net/netip"
)

// Reason gives the precise cause behind an ErrCannotAssociate result.
type Reason string

const (
	// ReasonNone means the error was delivered to an active connection.
	ReasonNone Reason = ""
	// ReasonTruncated means the quoted bytes did not contain a full header
	// or the transport bytes required by the protocol.
	ReasonTruncated Reason = "quoted datagram truncated"
	// ReasonBadHeader means the quoted IP header was malformed.
	ReasonBadHeader Reason = "quoted IP header malformed"
	// ReasonUnsupportedIP means the IP version was not 4 or 6.
	ReasonUnsupportedIP Reason = "unsupported IP version"
	// ReasonExtHeader means an IPv6 extension header was present. Only the
	// fixed 40-byte IPv6 header is accepted.
	ReasonExtHeader Reason = "IPv6 extension headers not supported"
	// ReasonUnsupportedProto means the transport protocol cannot be
	// demultiplexed (not TCP/UDP).
	ReasonUnsupportedProto Reason = "unsupported transport protocol"
	// ReasonNoMapper means no fixed-version mapper exists for the family.
	ReasonNoMapper Reason = "no mapper for IP version"
	// ReasonVersionMismatch means the fixed-version mapper rejected the
	// datagram's address family.
	ReasonVersionMismatch Reason = "address mapper version mismatch"
	// ReasonNoConn means no active connection matched the tuple.
	ReasonNoConn Reason = "no matching active connection"
	// ReasonGeneration means the tuple existed but not for the current
	// active generation (stale reuse or closed connection).
	ReasonGeneration Reason = "generation mismatch"
	// ReasonNoWindow means the active TCP connection has no sequence window.
	ReasonNoWindow Reason = "TCP connection has no sequence window"
	// ReasonSeqOutside means the quoted TCP sequence number is not in the
	// active generation's window.
	ReasonSeqOutside Reason = "sequence number outside window"
)

// parsedDatagram is the demultiplexing information recovered from the bytes
// quoted by an ICMP error message.
type parsedDatagram struct {
	version      IPVersion
	protocol     Protocol
	local        Endpoint
	remote       Endpoint
	transportLen int
	tcpSeq       uint32
	hasTCPSeq    bool
}

// parseQuotedDatagram parses the original IP header embedded in an ICMP
// error together with whatever transport-layer bytes survived. It never
// guesses: any missing byte yields ReasonTruncated so the caller can treat
// the packet as unassociable rather than tearing down an unrelated flow.
func parseQuotedDatagram(b []byte) (parsedDatagram, Reason) {
	if len(b) < 1 {
		return parsedDatagram{}, ReasonTruncated
	}
	switch b[0] >> 4 {
	case 4:
		if len(b) < 20 {
			return parsedDatagram{}, ReasonTruncated
		}
		return parseIPv4(b)
	case 6:
		if len(b) < 40 {
			return parsedDatagram{}, ReasonTruncated
		}
		return parseIPv6(b)
	default:
		// An unrecognized version nibble on a quote too short for even the
		// smallest fixed IP header cannot establish any tuple, so call it
		// truncation. A full-size packet with a bogus nibble is a bad
		// version rather than a truncated one.
		if len(b) < 20 {
			return parsedDatagram{}, ReasonTruncated
		}
		return parsedDatagram{}, ReasonUnsupportedIP
	}
}

func parseIPv4(b []byte) (parsedDatagram, Reason) {
	// Minimum IPv4 header is 20 bytes.
	if len(b) < 20 {
		return parsedDatagram{}, ReasonTruncated
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || ihl > len(b) {
		return parsedDatagram{}, ReasonBadHeader
	}
	totalLen := int(binary.BigEndian.Uint16(b[2:4]))
	if totalLen != 0 && totalLen < ihl {
		return parsedDatagram{}, ReasonBadHeader
	}

	protocol := Protocol(b[9])
	src, ok1 := netip.AddrFromSlice(b[12:16])
	dst, ok2 := netip.AddrFromSlice(b[16:20])
	if !ok1 || !ok2 {
		return parsedDatagram{}, ReasonBadHeader
	}

	available := len(b) - ihl
	if totalLen != 0 && totalLen-ihl < available {
		// Respect the declared length when the link layer padded the frame.
		available = totalLen - ihl
	}

	d := parsedDatagram{
		version:      Version4,
		protocol:     protocol,
		local:        Endpoint{Version: Version4, Addr: src},
		remote:       Endpoint{Version: Version4, Addr: dst},
		transportLen: available,
	}
	if err := parseTransport(b[ihl:], available, &d); err != ReasonNone {
		return parsedDatagram{}, err
	}
	return d, ReasonNone
}

func parseIPv6(b []byte) (parsedDatagram, Reason) {
	// Fixed IPv6 header is 40 bytes.
	if len(b) < 40 {
		return parsedDatagram{}, ReasonTruncated
	}
	nextHeader := Protocol(b[6])
	switch nextHeader {
	case ProtoTCP, ProtoUDP:
	default:
		// Any other value denotes an extension header or unsupported
		// protocol; extension chains are deliberately not walked.
		return parsedDatagram{}, ReasonExtHeader
	}

	payloadLen := int(binary.BigEndian.Uint16(b[4:6]))
	src, ok1 := netip.AddrFromSlice(b[8:24])
	dst, ok2 := netip.AddrFromSlice(b[24:40])
	if !ok1 || !ok2 {
		return parsedDatagram{}, ReasonBadHeader
	}

	available := len(b) - 40
	if payloadLen < available {
		available = payloadLen
	}

	d := parsedDatagram{
		version:      Version6,
		protocol:     nextHeader,
		local:        Endpoint{Version: Version6, Addr: src},
		remote:       Endpoint{Version: Version6, Addr: dst},
		transportLen: available,
	}
	if err := parseTransport(b[40:], available, &d); err != ReasonNone {
		return parsedDatagram{}, err
	}
	return d, ReasonNone
}

// parseTransport extracts ports (and, for TCP, the sequence number) from
// the quoted transport bytes. RFC 792/4443 quoting may truncate the
// datagram, so every read is bounds-checked.
func parseTransport(b []byte, available int, d *parsedDatagram) Reason {
	switch d.protocol {
	case ProtoTCP:
		// Ports (4) + sequence number (4) are the minimum useful quote.
		if available < 8 {
			return ReasonTruncated
		}
		d.local.Port = binary.BigEndian.Uint16(b[0:2])
		d.remote.Port = binary.BigEndian.Uint16(b[2:4])
		d.tcpSeq = binary.BigEndian.Uint32(b[4:8])
		d.hasTCPSeq = true
		return ReasonNone
	case ProtoUDP:
		// Only the port pair is needed for UDP demultiplexing.
		if available < 4 {
			return ReasonTruncated
		}
		d.local.Port = binary.BigEndian.Uint16(b[0:2])
		d.remote.Port = binary.BigEndian.Uint16(b[2:4])
		return ReasonNone
	default:
		return ReasonUnsupportedProto
	}
}
