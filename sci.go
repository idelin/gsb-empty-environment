package netsec

import (
	"encoding/binary"
	"errors"
)

// PortNumber identifies a connectivity-association member port within an SCI.
// It occupies the lower 16 bits of the 8-byte SCI, per IEEE 802.1AE.
type PortNumber uint16

// macLen is the length in bytes of an EUI-48 MAC address.
const macLen = 6

// sciLen is the length in bytes of an encoded SCI (6-byte MAC + 2-byte port).
const sciLen = 8

// SCI is the 64-bit Secure Channel Identifier carried in a SecTAG. It is the
// concatenation of a peer's EUI-48 MAC address and a 16-bit port number.
//
// SCI is a value type with no slice or pointer fields, so it is comparable and
// safe to use directly as a map key when selecting a receive channel.
type SCI struct {
	MAC  [macLen]byte
	Port PortNumber
}

// ErrInvalidMAC is returned when an address cannot be decoded into an SCI.
var ErrInvalidMAC = errors.New("netsec: invalid MAC address length")

// NewSCI builds an SCI from a peer MAC address and port number.
func NewSCI(mac []byte, port PortNumber) (SCI, error) {
	var sci SCI
	if len(mac) != macLen {
		return SCI{}, ErrInvalidMAC
	}
	copy(sci.MAC[:], mac)
	sci.Port = port
	return sci, nil
}

// MustSCI is like NewSCI but panics when mac is not six bytes long. It is
// intended for compile-time constants and tests.
func MustSCI(mac []byte, port PortNumber) SCI {
	sci, err := NewSCI(mac, port)
	if err != nil {
		panic(err)
	}
	return sci
}

// Encode writes the SCI into out using the 802.1AE big-endian wire layout and
// returns the number of bytes written.
func (s SCI) Encode(out []byte) (int, error) {
	if len(out) < sciLen {
		return 0, ErrBufferTooShort
	}
	copy(out, s.MAC[:])
	binary.BigEndian.PutUint16(out[macLen:], uint16(s.Port))
	return sciLen, nil
}

// DecodeSCI reads an SCI from the leading eight bytes of an encoded buffer.
func DecodeSCI(b []byte) (SCI, error) {
	if len(b) < sciLen {
		return SCI{}, ErrInvalidSCI
	}
	var sci SCI
	copy(sci.MAC[:], b[:macLen])
	sci.Port = PortNumber(binary.BigEndian.Uint16(b[macLen:]))
	return sci, nil
}

// String renders the SCI as the conventional MAC#port representation.
func (s SCI) String() string {
	const hex = "0123456789abcdef"
	buf := make([]byte, 0, sciLen*3)
	for i, b := range s.MAC {
		if i != 0 {
			buf = append(buf, ':')
		}
		buf = append(buf, hex[b>>4], hex[b&0x0f])
	}
	buf = append(buf, '#')
	port := [2]byte{}
	binary.BigEndian.PutUint16(port[:], uint16(s.Port))
	for _, b := range port {
		buf = append(buf, hex[b>>4], hex[b&0x0f])
	}
	return string(buf)
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (s SCI) MarshalBinary() ([]byte, error) {
	out := make([]byte, sciLen)
	if _, err := s.Encode(out); err != nil {
		return nil, err
	}
	return out, nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (s *SCI) UnmarshalBinary(b []byte) error {
	decoded, err := DecodeSCI(b)
	if err != nil {
		return err
	}
	*s = decoded
	return nil
}
