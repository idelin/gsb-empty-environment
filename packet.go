package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ipv4HeaderMinLen = 20
	tcpHeaderMinLen  = 20
	tcpHeaderMaxLen  = 60

	protoTCP = 6

	tcpOptionEOL = 0
	tcpOptionNOP = 1
)

var (
	// ErrShortPacket means the packet does not contain the bytes a header
	// claims to cover.
	ErrShortPacket = errors.New("netsec: packet shorter than required")
	// ErrNotIPv4 means the packet does not start with an IPv4 version field.
	ErrNotIPv4 = errors.New("netsec: packet is not IPv4")
	// ErrNotTCP means the IPv4 protocol field is not TCP.
	ErrNotTCP = errors.New("netsec: IPv4 packet does not carry TCP")
	// ErrBadHeader means a fixed header field is inconsistent.
	ErrBadHeader = errors.New("netsec: malformed IP or TCP header")
	// ErrBadOption means a TCP option cannot be parsed safely.
	ErrBadOption = errors.New("netsec: malformed TCP option")
)

// OptionSet is the allowlist of TCP option kinds that the gateway is
// permitted to keep. Kind 0 (EOL) and kind 1 (NOP) are formatting controls
// and are handled regardless of the set.
type OptionSet map[uint8]bool

// NewOptionSet builds an OptionSet from the given option kinds.
func NewOptionSet(kinds ...uint8) OptionSet {
	set := make(OptionSet, len(kinds))
	for _, kind := range kinds {
		set[kind] = true
	}
	return set
}

// RewriteIPv4TCPPacket removes TCP options whose kind is not present in
// allowed from an IPv4 TCP packet.
//
// The packet is rewritten in place. Options are parsed before any byte is
// modified: if the packet is malformed the function returns an error and the
// input buffer is left byte-for-byte unchanged. On success the rewritten
// datagram occupies the prefix of packet and its length is returned; removing
// options always shrinks or preserves the datagram. Bytes beyond the IPv4
// total length (for example link-layer trailers) are never touched.
//
// NOP options are preserved, EOL terminates option parsing, and the rebuilt
// option area is padded to a 4-byte boundary with EOL. The TCP data offset,
// IPv4 total length, IPv4 header checksum and TCP checksum are recomputed.
// The TCP payload is preserved verbatim.
func RewriteIPv4TCPPacket(packet []byte, allowed OptionSet) (int, error) {
	rewritten, err := buildRewrittenPacket(packet, allowed)
	if err != nil {
		return 0, err
	}
	copy(packet, rewritten)
	return len(rewritten), nil
}

func buildRewrittenPacket(packet []byte, allowed OptionSet) ([]byte, error) {
	if len(packet) < ipv4HeaderMinLen {
		return nil, fmt.Errorf("%w: got %d bytes", ErrShortPacket, len(packet))
	}
	if packet[0]>>4 != 4 {
		return nil, fmt.Errorf("%w: IP version is %d", ErrNotIPv4, packet[0]>>4)
	}

	ihl := int(packet[0] & 0x0f)
	if ihl < 5 {
		return nil, fmt.Errorf("%w: IHL=%d", ErrBadHeader, ihl)
	}
	ipHeaderLen := ihl * 4
	if ipHeaderLen > len(packet) {
		return nil, fmt.Errorf("%w: IPv4 header (%d bytes) exceeds packet", ErrBadHeader, ipHeaderLen)
	}

	totalLen := int(binary.BigEndian.Uint16(packet[2:4]))
	if totalLen < ipHeaderLen+tcpHeaderMinLen {
		return nil, fmt.Errorf("%w: total length %d too small", ErrBadHeader, totalLen)
	}
	if totalLen > len(packet) {
		return nil, fmt.Errorf("%w: total length %d exceeds packet of %d", ErrBadHeader, totalLen, len(packet))
	}

	if packet[9] != protoTCP {
		return nil, fmt.Errorf("%w: protocol %d", ErrNotTCP, packet[9])
	}

	tcpStart := ipHeaderLen

	dataOffset := int(packet[tcpStart+12] >> 4)
	if dataOffset < 5 {
		return nil, fmt.Errorf("%w: TCP data offset %d", ErrBadHeader, dataOffset)
	}
	tcpHeaderLen := dataOffset * 4
	if tcpHeaderLen > tcpHeaderMaxLen || tcpStart+tcpHeaderLen > totalLen {
		return nil, fmt.Errorf("%w: TCP header (%d bytes) exceeds datagram", ErrBadHeader, tcpHeaderLen)
	}

	kept, err := filterOptions(packet[tcpStart:tcpStart+tcpHeaderLen], allowed)
	if err != nil {
		return nil, err
	}

	newTCPHeaderLen := tcpHeaderMinLen + roundUp4(len(kept))
	newTotalLen := totalLen - (tcpHeaderLen - newTCPHeaderLen)

	out := make([]byte, newTotalLen)
	copy(out, packet[:ipHeaderLen])
	copy(out[tcpStart:], packet[tcpStart:tcpStart+tcpHeaderMinLen])
	copy(out[tcpStart+tcpHeaderMinLen:], kept)
	// Remaining option bytes stay zero: the first zero is EOL and the rest is
	// RFC 793 end-of-options padding.
	copy(out[tcpStart+newTCPHeaderLen:], packet[tcpStart+tcpHeaderLen:totalLen])

	binary.BigEndian.PutUint16(out[2:4], uint16(newTotalLen))
	out[10] = 0
	out[11] = 0
	binary.BigEndian.PutUint16(out[10:12], ipv4HeaderChecksum(out[:ipHeaderLen]))

	out[tcpStart+12] = (uint8(newTCPHeaderLen/4) << 4) | (out[tcpStart+12] & 0x0f)
	out[tcpStart+16] = 0
	out[tcpStart+17] = 0
	binary.BigEndian.PutUint16(
		out[tcpStart+16:tcpStart+18],
		tcpv4Checksum(out[tcpStart:newTotalLen], out[12:16], out[16:20]),
	)

	return out, nil
}

// filterOptions walks the TCP option area (the full TCP header including its
// 20-byte fixed part) and returns the bytes of every allowed option. All
// validation happens here, before the caller builds the rewritten packet.
func filterOptions(tcpHeader []byte, allowed OptionSet) ([]byte, error) {
	optionEnd := len(tcpHeader)
	kept := make([]byte, 0, optionEnd-tcpHeaderMinLen)

	for i := tcpHeaderMinLen; i < optionEnd; {
		kind := tcpHeader[i]
		switch kind {
		case tcpOptionEOL:
			return kept, nil
		case tcpOptionNOP:
			kept = append(kept, tcpOptionNOP)
			i++
			continue
		}

		if i+1 >= optionEnd {
			return nil, fmt.Errorf("%w: kind %d has no length byte", ErrBadOption, kind)
		}
		optLen := int(tcpHeader[i+1])
		if optLen < 2 {
			return nil, fmt.Errorf("%w: kind %d length %d", ErrBadOption, kind, optLen)
		}
		if i+optLen > optionEnd {
			return nil, fmt.Errorf("%w: kind %d length %d overruns option area", ErrBadOption, kind, optLen)
		}
		if allowed[kind] {
			kept = append(kept, tcpHeader[i:i+optLen]...)
		}
		i += optLen
	}
	return kept, nil
}

func roundUp4(n int) int {
	return (n + 3) &^ 3
}
