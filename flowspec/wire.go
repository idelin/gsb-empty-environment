package flowspec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// Length-of-length selectors occupy the two most significant bits of the
// component header octet (RFC 8955 section 4.2.1); the remaining six bits
// carry the component type.
const (
	lenSel16Bit  = 0b00000000 // 16-bit length follows the header
	lenSel8Bit   = 0b01000000 // 8-bit length follows the header
	lenSelSmall  = 0b10000000 // 8-bit length follows the header
	lenSelInline = 0b11000000 // length in the low six bits, no type octet
)

// DecodeNLRI parses a FlowSpec NLRI body for the given address family. It
// decodes every component but does not enforce ordering; callers use
// CheckComponentOrder (and Validate) for the section 6 ordering requirement.
func DecodeNLRI(afi uint16, wire []byte) (NLRI, error) {
	if afi != AFIIPv4 && afi != AFIIPv6 {
		return NLRI{}, fmt.Errorf("flowspec: unsupported AFI %d", afi)
	}
	nlri := NLRI{AFI: afi}
	rest := wire
	for len(rest) > 0 {
		comp, next, err := decodeComponent(rest)
		if err != nil {
			return NLRI{}, err
		}
		nlri.Components = append(nlri.Components, comp)
		rest = next
	}
	return nlri, nil
}

func decodeComponent(b []byte) (Component, []byte, error) {
	if len(b) < 1 {
		return Component{}, nil, errors.New("flowspec: truncated NLRI component header")
	}
	selector := b[0] & 0xc0
	typ := ComponentType(b[0] & 0x3f)

	var length int
	var valueOffset int
	switch selector {
	case lenSel16Bit:
		if len(b) < 3 {
			return Component{}, nil, errors.New("flowspec: truncated 16-bit component length")
		}
		length = int(binary.BigEndian.Uint16(b[1:3]))
		valueOffset = 3
	case lenSel8Bit, lenSelSmall:
		if len(b) < 2 {
			return Component{}, nil, errors.New("flowspec: truncated 8-bit component length")
		}
		length = int(b[1])
		valueOffset = 2
	default:
		// The inline form (0b11) encodes only a 0..63 byte value and carries
		// no component type octet, so it cannot express a typed FlowSpec
		// component. RFC 8955 section 4.2.2 requires typed components, so a
		// conformant sender never emits this form inside a FlowSpec NLRI.
		return Component{}, nil, errors.New("flowspec: inline (len-of-length 0b11) component cannot carry a FlowSpec type")
	}

	if length < 0 || len(b) < valueOffset+length {
		return Component{}, nil, errors.New("flowspec: component value exceeds NLRI bounds")
	}
	value := make([]byte, length)
	copy(value, b[valueOffset:valueOffset+length])
	return Component{Type: typ, Value: value}, b[valueOffset+length:], nil
}

// EncodeNLRI serializes an NLRI into its on-wire component sequence. It does
// not reorder components; Validate enforces ascending type order.
func EncodeNLRI(nlri NLRI) ([]byte, error) {
	if nlri.AFI != AFIIPv4 && nlri.AFI != AFIIPv6 {
		return nil, fmt.Errorf("flowspec: unsupported AFI %d", nlri.AFI)
	}
	out := make([]byte, 0)
	for _, comp := range nlri.Components {
		encoded, err := encodeComponent(comp)
		if err != nil {
			return nil, err
		}
		out = append(out, encoded...)
	}
	return out, nil
}

func encodeComponent(comp Component) ([]byte, error) {
	n := len(comp.Value)
	switch {
	case n <= 0xff:
		// Selector 0b10 + type octet, then one length octet.
		return append([]byte{lenSelSmall | byte(comp.Type), byte(n)}, comp.Value...), nil
	case n <= 0xffff:
		out := []byte{lenSel16Bit | byte(comp.Type), 0, 0}
		binary.BigEndian.PutUint16(out[1:3], uint16(n))
		return append(out, comp.Value...), nil
	default:
		return nil, fmt.Errorf("flowspec: component type %d value too large (%d bytes)", comp.Type, n)
	}
}

// PrefixComponent builds a destination- or source-prefix NLRI component from
// an IP prefix using the RFC 8955 section 4.2.2 prefix encoding.
func PrefixComponent(typ ComponentType, p netip.Prefix) (Component, error) {
	if typ != TypeDestinationPrefix && typ != TypeSourcePrefix {
		return Component{}, errors.New("flowspec: not a prefix component type")
	}
	value, err := encodePrefix(p)
	if err != nil {
		return Component{}, err
	}
	return Component{Type: typ, Value: value}, nil
}

func encodePrefix(p netip.Prefix) ([]byte, error) {
	addrBytes := p.Addr().AsSlice()
	bits := p.Bits()
	if bits < 0 || bits > len(addrBytes)*8 {
		return nil, fmt.Errorf("flowspec: invalid prefix length %d", bits)
	}
	// RFC 8955 section 4.2.2: first octet is the prefix length in bits,
	// followed by the prefix bits padded to an octet boundary.
	wholeBytes := bits / 8
	value := make([]byte, 0, wholeBytes+1)
	value = append(value, byte(bits))
	value = append(value, addrBytes[:wholeBytes]...)
	if rem := bits % 8; rem != 0 {
		value = append(value, addrBytes[wholeBytes]&(0xff<<(8-rem)))
	}
	return value, nil
}

func decodePrefix(value []byte, afi uint16) (netip.Prefix, error) {
	var addrLen int
	switch afi {
	case AFIIPv4:
		addrLen = 4
	case AFIIPv6:
		addrLen = 16
	default:
		return netip.Prefix{}, fmt.Errorf("flowspec: unsupported AFI %d", afi)
	}
	if len(value) < 1 {
		return netip.Prefix{}, errors.New("flowspec: empty prefix component value")
	}
	bits := int(value[0])
	if bits > addrLen*8 {
		return netip.Prefix{}, fmt.Errorf("flowspec: prefix length %d exceeds address width", bits)
	}
	expected := 1 + (bits+7)/8
	if len(value) != expected {
		return netip.Prefix{}, fmt.Errorf("flowspec: prefix value length %d does not match prefix length %d", len(value), bits)
	}

	addr := make([]byte, addrLen)
	copy(addr, value[1:])
	if rem := bits % 8; rem != 0 {
		last := value[len(value)-1]
		if last&(0xff>>rem) != 0 {
			return netip.Prefix{}, errors.New("flowspec: non-zero padding bits in prefix component")
		}
	}

	ip, ok := netip.AddrFromSlice(addr)
	if !ok {
		return netip.Prefix{}, errors.New("flowspec: invalid prefix address")
	}
	prefix := netip.PrefixFrom(ip.Unmap(), bits)
	if !prefix.IsValid() {
		return netip.Prefix{}, errors.New("flowspec: invalid decoded prefix")
	}
	return prefix.Masked(), nil
}
