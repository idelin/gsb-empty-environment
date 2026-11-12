package netsec

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

// Fixed on-wire sizes from AUTOSAR FO PRS SOME/IP / SOME/IP Service Discovery.
const (
	someIPHeaderLen = 16
	sdHeaderLen     = 12
	entryLen        = 16

	sdMessageID   uint32 = 0xFFFF8100
	sdProtocolV8         = 0x01
	sdInterfaceV1        = 0x01
	sdMessageType        = 0x02

	flagReboot  = 0x80
	flagUnicast = 0x40
)

var (
	errShortPacket      = errors.New("netsec: packet shorter than SOME/IP header")
	errNotSD            = errors.New("netsec: packet is not a SOME/IP-SD message")
	errLengthMismatch   = errors.New("netsec: SOME/IP length field does not match packet size")
	errShortSDHeader    = errors.New("netsec: truncated SOME/IP-SD header")
	errBadSDReserved    = errors.New("netsec: non-zero reserved bytes in SOME/IP-SD header")
	errBadEntriesLength = errors.New("netsec: entries array length is not a multiple of 16")
	errTruncatedEntries = errors.New("netsec: entries array overruns packet")
	errTruncatedOptions = errors.New("netsec: options array overruns packet")
	errTruncatedOption  = errors.New("netsec: truncated option")
	errBadOptionLength  = errors.New("netsec: option length smaller than its type/reserved bytes")
	errBadIPv4Option    = errors.New("netsec: IPv4 endpoint option shorter than 9 bytes")
	errBadIPv6Option    = errors.New("netsec: IPv6 endpoint option shorter than 21 bytes")
)

// ParseMessage decodes a complete SOME/IP-SD datagram (SOME/IP header plus SD
// payload). The IP source address is not part of the datagram: callers obtain
// it from the socket and pass it to Admit separately.
func ParseMessage(packet []byte) (*Message, error) {
	if len(packet) < someIPHeaderLen {
		return nil, errShortPacket
	}
	if binary.BigEndian.Uint32(packet[0:4]) != sdMessageID {
		return nil, errNotSD
	}
	if packet[12] != sdProtocolV8 || packet[13] != sdInterfaceV1 || packet[14] != sdMessageType {
		return nil, errNotSD
	}
	if int(binary.BigEndian.Uint32(packet[4:8]))+8 != len(packet) {
		return nil, errLengthMismatch
	}

	payload := packet[someIPHeaderLen:]
	if len(payload) < sdHeaderLen {
		return nil, errShortSDHeader
	}
	if payload[0] != 0 || payload[1] != 0 || payload[2] != 0 {
		return nil, errBadSDReserved
	}

	msg := &Message{
		ClientID:   binary.BigEndian.Uint16(packet[8:10]),
		SessionID:  binary.BigEndian.Uint16(packet[10:12]),
		ReturnCode: packet[15],
		Reboot:     payload[3]&flagReboot != 0,
		Unicast:    payload[3]&flagUnicast != 0,
	}

	entriesLen := int(binary.BigEndian.Uint32(payload[4:8]))
	if entriesLen%entryLen != 0 {
		return nil, errBadEntriesLength
	}
	optionsLen := int(binary.BigEndian.Uint32(payload[8:12]))

	entriesEnd := sdHeaderLen + entriesLen
	optionsEnd := entriesEnd + optionsLen
	if entriesEnd > len(payload) || optionsEnd > len(payload) {
		return nil, errTruncatedEntries
	}

	for offset := sdHeaderLen; offset < entriesEnd; offset += entryLen {
		e := Entry{
			Type:         payload[offset],
			Index1:       payload[offset+1],
			Index2:       payload[offset+2],
			Num1:         payload[offset+3] >> 4,
			Num2:         payload[offset+3] & 0x0F,
			ServiceID:    binary.BigEndian.Uint16(payload[offset+4 : offset+6]),
			InstanceID:   binary.BigEndian.Uint16(payload[offset+6 : offset+8]),
			MajorVersion: payload[offset+8],
			TTL:          uint24(payload[offset+9 : offset+12]),
			MinorVersion: binary.BigEndian.Uint32(payload[offset+12 : offset+16]),
		}
		msg.Entries = append(msg.Entries, e)
	}

	var err error
	msg.Options, err = parseOptions(payload[entriesEnd:optionsEnd])
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func parseOptions(buf []byte) ([]Option, error) {
	var options []Option
	for pos := 0; pos < len(buf); {
		if pos+3 > len(buf) {
			return nil, errTruncatedOption
		}
		// AUTOSAR Length counts every byte following the 2-byte Length field,
		// including Type and Reserved; the whole option therefore occupies
		// Length+2 bytes (e.g. IPv4 endpoint: Length 0x0009 -> 11 bytes).
		length := int(binary.BigEndian.Uint16(buf[pos : pos+2]))
		if length < 2 || pos+2+length > len(buf) {
			if length < 2 {
				return nil, errBadOptionLength
			}
			return nil, errTruncatedOption
		}
		optType := buf[pos+2]
		body := buf[pos+2 : pos+2+length]
		opt, err := decodeOption(optType, body)
		if err != nil {
			return nil, err
		}
		options = append(options, opt)
		pos += 2 + length
	}
	return options, nil
}

func decodeOption(optType uint8, body []byte) (Option, error) {
	opt := Option{Type: optType, Raw: body}
	switch optType {
	case OptionIPv4Endpoint, OptionIPv4SDEndpoint, OptionIPv4Multicast:
		// Type | Reserved | IPv4(4) | L4-Proto | Port(2), body is 9 bytes.
		if len(body) < 9 {
			return Option{}, errBadIPv4Option
		}
		addr, ok := netip.AddrFromSlice(body[2:6])
		if !ok {
			return Option{}, errBadIPv4Option
		}
		opt.Address = addr.Unmap()
		opt.L4Proto = body[6]
		opt.Port = binary.BigEndian.Uint16(body[7:9])
	case OptionIPv6Endpoint, OptionIPv6SDEndpoint, OptionIPv6Multicast:
		// Type | Reserved | IPv6(16) | L4-Proto | Port(2), body is 21 bytes.
		if len(body) < 21 {
			return Option{}, errBadIPv6Option
		}
		addr, ok := netip.AddrFromSlice(body[2:18])
		if !ok {
			return Option{}, errBadIPv6Option
		}
		opt.Address = addr.Unmap()
		opt.L4Proto = body[18]
		opt.Port = binary.BigEndian.Uint16(body[19:21])
	}
	return opt, nil
}

func uint24(b []byte) uint32 {
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
}
