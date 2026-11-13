package netsec

import (
	"encoding/binary"
	"fmt"
)

// WrapSEIPD wraps a v2 SEIPD body in a new-format OpenPGP packet with tag 18.
func WrapSEIPD(body []byte) ([]byte, error) {
	if len(body) > int(^uint32(0)) {
		return nil, fmt.Errorf("netsec: SEIPD body too large")
	}
	out := []byte{0xC0 | PacketTagSEIPD}
	switch {
	case len(body) < 192:
		out = append(out, byte(len(body)))
	case len(body) <= 8383:
		length := len(body) - 192
		out = append(out, byte(length>>8)+192, byte(length))
	default:
		out = append(out, 0xFF)
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(body)))
		out = append(out, length[:]...)
	}
	out = append(out, body...)
	return out, nil
}

// UnwrapSEIPD extracts the body of a tag-18 OpenPGP packet. Both old- and
// new-format definite-length headers are accepted.
func UnwrapSEIPD(packet []byte) ([]byte, error) {
	if len(packet) < 2 {
		return nil, fmt.Errorf("%w: short packet header", ErrMalformedPacket)
	}
	var tag byte
	var length uint32
	var headerLength int

	if packet[0]&0xC0 == 0xC0 {
		tag = packet[0] & 0x3F
		first := packet[1]
		switch {
		case first < 192:
			length = uint32(first)
			headerLength = 2
		case first <= 223:
			if len(packet) < 3 {
				return nil, fmt.Errorf("%w: short packet length", ErrMalformedPacket)
			}
			length = (uint32(first-192) << 8) + uint32(packet[2]) + 192
			headerLength = 3
		case first == 255:
			if len(packet) < 6 {
				return nil, fmt.Errorf("%w: short packet length", ErrMalformedPacket)
			}
			length = binary.BigEndian.Uint32(packet[2:6])
			headerLength = 6
		default:
			return nil, fmt.Errorf("%w: partial body lengths are not supported", ErrMalformedPacket)
		}
	} else {
		tag = (packet[0] >> 2) & 0x0F
		switch packet[0] & 0x03 {
		case 0:
			length = uint32(packet[1])
			headerLength = 2
		case 1:
			if len(packet) < 3 {
				return nil, fmt.Errorf("%w: short packet length", ErrMalformedPacket)
			}
			length = uint32(binary.BigEndian.Uint16(packet[1:3]))
			headerLength = 3
		case 2:
			if len(packet) < 5 {
				return nil, fmt.Errorf("%w: short packet length", ErrMalformedPacket)
			}
			length = binary.BigEndian.Uint32(packet[1:5])
			headerLength = 5
		case 3:
			length = uint32(len(packet) - 1)
			headerLength = 1
		}
	}

	if tag != PacketTagSEIPD {
		return nil, fmt.Errorf("%w: packet tag %d", ErrMalformedPacket, tag)
	}
	if uint64(headerLength)+uint64(length) > uint64(len(packet)) {
		return nil, fmt.Errorf("%w: truncated packet body", ErrMalformedPacket)
	}
	if uint64(headerLength)+uint64(length) != uint64(len(packet)) {
		return nil, fmt.Errorf("%w: trailing packet data", ErrMalformedPacket)
	}
	return packet[headerLength : headerLength+int(length)], nil
}

// EncryptPacket seals plaintext and wraps the result as a tag-18 packet.
func EncryptPacket(sessionKey, plaintext []byte, configs ...Config) ([]byte, error) {
	body, err := Encrypt(sessionKey, plaintext, configs...)
	if err != nil {
		return nil, err
	}
	return WrapSEIPD(body)
}

// DecryptPacket unwraps a tag-18 packet and decrypts its v2 SEIPD body.
func DecryptPacket(sessionKey, packet []byte, configs ...Config) ([]byte, error) {
	body, err := UnwrapSEIPD(packet)
	if err != nil {
		return nil, err
	}
	return Decrypt(sessionKey, body, configs...)
}

// ParsePacket unwraps and parses a tag-18 packet.
func ParsePacket(packet []byte) (*Message, error) {
	body, err := UnwrapSEIPD(packet)
	if err != nil {
		return nil, err
	}
	return ParseMessage(body)
}
