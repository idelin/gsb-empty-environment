package quicguard

import (
	"errors"
	"fmt"
)

// QUIC v1 constants from RFC 9000.
const (
	Version1 uint32 = 0x00000001

	maxConnectionIDLen = 20

	formBit   byte = 0x80
	fixedBit  byte = 0x40
	typeMask  byte = 0x30
	typeShift      = 4

	packetTypeInitial   = 0
	packetType0RTT      = 1
	packetTypeHandshake = 2
	packetTypeRetry     = 3
)

// PacketType enumerates the five QUIC v1 packet types the guard can parse.
type PacketType uint8

const (
	TypeInitial PacketType = iota
	Type0RTT
	TypeHandshake
	TypeRetry
	Type1RTT
)

// String returns the RFC name of the packet type.
func (t PacketType) String() string {
	switch t {
	case TypeInitial:
		return "Initial"
	case Type0RTT:
		return "0-RTT"
	case TypeHandshake:
		return "Handshake"
	case TypeRetry:
		return "Retry"
	case Type1RTT:
		return "1-RTT"
	default:
		return "unknown"
	}
}

// Header is the parsed, unencrypted header view of a QUIC packet. All byte
// slices reference the original datagram and must not be retained after the
// datagram memory is reused.
type Header struct {
	Type      PacketType
	Version   uint32
	DCID      []byte
	SCID      []byte
	Token     []byte // Initial token field or Retry token field
	Raw       []byte // full wire bytes of this single packet
	Offset    int    // offset of this packet inside the source datagram
	HeaderEnd int    // offset immediately after the parsed header fields
	IsLong    bool
}

// IsInitial reports whether the packet is a long-header Initial.
func (h *Header) IsInitial() bool { return h.Type == TypeInitial }

func readVarInt(b []byte, pos int) (value uint64, next int, err error) {
	if pos >= len(b) {
		return 0, pos, ErrMalformed
	}
	first := b[pos]
	length := 1 << (first >> 6) // prefix 0..3 => 1,2,4,8 bytes
	if pos+length > len(b) {
		return 0, pos, ErrMalformed
	}
	value = uint64(first & 0x3f)
	for i := 1; i < length; i++ {
		value = (value << 8) | uint64(b[pos+i])
	}
	return value, pos + length, nil
}

// EncodeVarInt appends a QUIC variable-length integer (RFC 9000 section 16).
func EncodeVarInt(buf []byte, value uint64) []byte {
	switch {
	case value <= 63:
		return append(buf, byte(value))
	case value <= 16383:
		return append(buf, byte(value>>8)|0x40, byte(value))
	case value <= 1073741823:
		return append(buf, byte(value>>24)|0x80, byte(value>>16), byte(value>>8), byte(value))
	default:
		return append(buf,
			byte(value>>56)|0xc0, byte(value>>48), byte(value>>40), byte(value>>32),
			byte(value>>24), byte(value>>16), byte(value>>8), byte(value))
	}
}

func readCID(b []byte, pos int) (cid []byte, next int, err error) {
	if pos >= len(b) {
		return nil, pos, ErrMalformed
	}
	length := int(b[pos])
	pos++
	if length > maxConnectionIDLen {
		return nil, pos, ErrConnectionIDTooLong
	}
	if pos+length > len(b) {
		return nil, pos, ErrMalformed
	}
	return b[pos : pos+length], pos + length, nil
}

func readBytes(b []byte, pos int) (out []byte, next int, err error) {
	length, pos, err := readVarInt(b, pos)
	if err != nil {
		return nil, pos, err
	}
	if length > uint64(len(b)-pos) {
		return nil, pos, ErrMalformed
	}
	return b[pos : pos+int(length)], pos + int(length), nil
}

func parseLongHeader(dgram []byte, offset int) (*Header, error) {
	if len(dgram)-offset < 5 {
		return nil, ErrMalformed
	}
	first := dgram[offset]
	if first&fixedBit == 0 {
		return nil, ErrFixedBit
	}

	pos := offset + 1
	version := uint32(dgram[pos])<<24 | uint32(dgram[pos+1])<<16 |
		uint32(dgram[pos+2])<<8 | uint32(dgram[pos+3])
	pos += 4
	if version != Version1 {
		return nil, fmt.Errorf("%w: 0x%08x", ErrUnsupportedVersion, version)
	}

	dcid, pos, err := readCID(dgram, pos)
	if err != nil {
		return nil, err
	}
	scid, pos, err := readCID(dgram, pos)
	if err != nil {
		return nil, err
	}

	h := &Header{
		IsLong:  true,
		Version: version,
		DCID:    dcid,
		SCID:    scid,
		Offset:  offset,
	}

	packetType := (first & typeMask) >> typeShift
	switch packetType {
	case packetTypeInitial:
		h.Type = TypeInitial
		token, newPos, err := readBytes(dgram, pos)
		if err != nil {
			return nil, err
		}
		pos = newPos
		length, newPos, err := readVarInt(dgram, pos)
		if err != nil {
			return nil, err
		}
		pos = newPos
		// Length counts bytes after the Length field, not from packet start.
		packetEnd := pos + int(length)
		if packetEnd > len(dgram) {
			return nil, ErrMalformed
		}
		h.Token = token
		h.HeaderEnd = pos
		h.Raw = dgram[offset:packetEnd]
		return h, nil

	case packetType0RTT:
		h.Type = Type0RTT
		length, newPos, err := readVarInt(dgram, pos)
		if err != nil {
			return nil, err
		}
		pos = newPos
		packetEnd := pos + int(length)
		if packetEnd > len(dgram) {
			return nil, ErrMalformed
		}
		h.HeaderEnd = pos
		h.Raw = dgram[offset:packetEnd]
		return h, nil

	case packetTypeHandshake:
		h.Type = TypeHandshake
		length, newPos, err := readVarInt(dgram, pos)
		if err != nil {
			return nil, err
		}
		pos = newPos
		packetEnd := pos + int(length)
		if packetEnd > len(dgram) {
			return nil, ErrMalformed
		}
		h.HeaderEnd = pos
		h.Raw = dgram[offset:packetEnd]
		return h, nil

	case packetTypeRetry:
		h.Type = TypeRetry
		// A Retry packet has no Length field; everything after the SCID up to
		// the final 16 integrity-tag bytes is the Retry token.
		if len(dgram)-pos < 16 {
			return nil, ErrMalformed
		}
		h.Token = dgram[pos : len(dgram)-16]
		h.HeaderEnd = len(dgram)
		h.Raw = dgram[offset:]
		return h, nil

	default:
		return nil, fmt.Errorf("%w: unknown long packet type %d", ErrMalformed, packetType)
	}
}

func parseShortHeader(dgram []byte, offset int) (*Header, error) {
	if len(dgram) <= offset {
		return nil, ErrMalformed
	}
	first := dgram[offset]
	if first&fixedBit == 0 {
		return nil, ErrFixedBit
	}
	dcid, next, err := readCID(dgram, offset+1)
	if err != nil {
		return nil, err
	}
	h := &Header{
		Type:      Type1RTT,
		Version:   Version1,
		DCID:      dcid,
		IsLong:    false,
		Offset:    offset,
		HeaderEnd: next,
		Raw:       dgram[offset:],
	}
	return h, nil
}

// ParsePacket parses one QUIC packet beginning at offset. Long-header packets
// report their declared end so callers can walk coalesced packets; short-header
// packets always consume the remainder of the datagram.
func ParsePacket(dgram []byte, offset int) (*Header, error) {
	if offset < 0 || offset >= len(dgram) {
		return nil, ErrMalformed
	}
	if dgram[offset]&formBit != 0 {
		return parseLongHeader(dgram, offset)
	}
	return parseShortHeader(dgram, offset)
}

// SplitDatagram splits a possibly coalesced UDP datagram into its constituent
// QUIC packets (RFC 9000 section 12.2). Coalesced long-header packets share a
// single UDP datagram, whose full wire length is used for amplification
// accounting; a short-header packet is always the final packet.
func SplitDatagram(dgram []byte) ([]*Header, error) {
	if len(dgram) == 0 {
		return nil, ErrMalformed
	}
	var packets []*Header
	offset := 0
	for offset < len(dgram) {
		h, err := ParsePacket(dgram, offset)
		if err != nil {
			return nil, err
		}
		packets = append(packets, h)
		if !h.IsLong || h.Type == TypeRetry {
			break
		}
		next := offset + len(h.Raw)
		if next <= offset {
			return nil, errors.New("quicguard: coalesced packet made no progress")
		}
		offset = next
	}
	return packets, nil
}
