package flowspec

import (
	"encoding/binary"
	"fmt"
	"net"
)

// ParsePrefix parses a CIDR string into a canonical, network-masked IPNet.
func ParsePrefix(cidr string) (net.IPNet, error) {
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return net.IPNet{}, err
	}
	network.IP = network.IP.Mask(network.Mask)
	if v4 := ip.To4(); v4 != nil {
		if network.Mask != nil {
			network.IP = network.IP.To4()
		}
	}
	return *network, nil
}

// MustParsePrefix is ParsePrefix that panics on error; it is intended for
// package examples and deterministic test fixtures.
func MustParsePrefix(cidr string) net.IPNet {
	p, err := ParsePrefix(cidr)
	if err != nil {
		panic(err)
	}
	return p
}

// EncodeComponents canonical-encodes a component slice to bare NLRI bytes.
func EncodeComponents(comps []Component, af AFI) ([]byte, error) {
	var out []byte
	for i := range comps {
		b, err := encodeComponent(&comps[i], af)
		if err != nil {
			return nil, fmt.Errorf("component %d (%s): %w", i, comps[i].Type, err)
		}
		out = append(out, b...)
	}
	return out, nil
}

func encodeComponent(c *Component, af AFI) ([]byte, error) {
	t := c.Type
	if t < TypeDestPrefix || t > TypeFragment {
		return nil, fmt.Errorf("%w: type %d", ErrUnknownComponent, uint8(t))
	}
	if t.IsPrefix() {
		return encodePrefix(c, af)
	}
	return encodeTermsComponent(c, t)
}

func encodePrefix(c *Component, af AFI) ([]byte, error) {
	bits := af.maxPrefixBits()
	ones, maskBits := c.Prefix.Mask.Size()
	if maskBits == 0 {
		maskBits = bits
	}
	if maskBits != bits {
		return nil, fmt.Errorf("%w: %s mask width %d != address family %d", ErrInvalidPrefix, c.Type, maskBits, bits)
	}
	if ones < 0 || ones > bits {
		return nil, fmt.Errorf("%w: %s length /%d", ErrInvalidPrefix, c.Type, ones)
	}
	ip := c.Prefix.IP
	if af == AFIPv6 {
		if v4 := ip.To4(); v4 != nil {
			// A v4-mapped address is not a valid IPv6 prefix body here.
			ip = v4
			if len(ip) == 4 {
				return nil, fmt.Errorf("%w: %s got IPv4 address for AF IPv6", ErrInvalidPrefix, c.Type)
			}
		}
		if len(ip) != net.IPv6len {
			ip = ip.To16()
		}
	} else {
		v4 := ip.To4()
		if v4 == nil {
			return nil, fmt.Errorf("%w: %s requires an IPv4 address", ErrInvalidPrefix, c.Type)
		}
		ip = v4
	}
	masked := ip.Mask(net.CIDRMask(ones, bits))
	nbytes := (ones + 7) / 8
	buf := make([]byte, 0, 2+nbytes)
	buf = append(buf, byte(tByte(c.Type)))
	buf = append(buf, byte(ones))
	buf = append(buf, masked[:nbytes]...)
	return buf, nil
}

func tByte(t ComponentType) byte { return byte(t) }

func encodeTermsComponent(c *Component, t ComponentType) ([]byte, error) {
	if len(c.Terms) == 0 {
		return nil, fmt.Errorf("%w: %s has no operator terms", ErrIllegalOperator, t)
	}
	buf := make([]byte, 0, 2+len(c.Terms)*2)
	buf = append(buf, byte(t))
	last := len(c.Terms) - 1
	for i := range c.Terms {
		term := c.Terms[i]
		if term.Width != 1 && term.Width != 2 && term.Width != 4 {
			term.Width = MinWidth(term.Value)
		}
		maxVal := uint64(1)<<(8*term.Width) - 1
		if term.Value > maxVal {
			return nil, fmt.Errorf("term %d value %d overflows %d octet(s)", i, term.Value, term.Width)
		}
		op := term.OperatorByte(i == last)
		buf = append(buf, op)
		val := make([]byte, 4)
		binary.BigEndian.PutUint32(val, uint32(term.Value))
		buf = append(buf, val[4-term.Width:]...)
	}
	return buf, nil
}

// EncodeBare returns the canonical bare component stream for the rule.
func (r *Rule) EncodeBare() ([]byte, error) {
	if r.AF == 0 {
		r.AF = r.effectiveAF()
	}
	return EncodeComponents(r.Components, r.AF)
}

// EncodeNLRI returns the length-prefixed FlowSpec NLRI bytes.
func (r *Rule) EncodeNLRI() ([]byte, error) {
	body, err := r.EncodeBare()
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, ErrEmptyNLRI
	}
	if len(body) > 0xffff {
		return nil, fmt.Errorf("flowspec: NLRI body too large: %d bytes", len(body))
	}
	out := []byte{0, 0}
	binary.BigEndian.PutUint16(out, uint16(len(body)))
	return append(out, body...), nil
}
