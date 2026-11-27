package nat64

import "net/netip"

// synthV6 embeds an IPv4 address into the configured RFC 6052 prefix.
// The well-known /96 prefix yields 64:ff9b::<v4>; the other standard
// prefix lengths place the IPv4 bits before the suffix u bits and require
// the reserved octet between them.
func (e *Engine) synthV6(v4 netip.Addr) netip.Addr {
	p := e.cfg.Prefix6
	bits := p.Bits()
	prefixBytes := p.Addr().As16()
	v4b := v4.As4()

	var out [16]byte
	copy(out[:], prefixBytes[:])

	if bits == 96 {
		copy(out[12:], v4b[:])
		return netip.AddrFrom16(out).Unmap()
	}

	// For lengths 32..64 the IPv4 address starts at the prefix boundary,
	// followed by a one-octet suffix length indicator per RFC 6052.
	start := bits / 8
	copy(out[start:start+4], v4b[:])
	out[start+4] = 0
	return netip.AddrFrom16(out).Unmap()
}

// extractV4 reverses synthV6. It returns false when the address does not
// belong to the configured prefix.
func (e *Engine) extractV4(v6 netip.Addr) (netip.Addr, bool) {
	p := e.cfg.Prefix6
	bits := p.Bits()
	a16 := v6.As16()
	prefixBytes := p.Addr().As16()

	if bits == 96 {
		for i := 0; i < 12; i++ {
			if a16[i] != prefixBytes[i] {
				return netip.Addr{}, false
			}
		}
		return netip.AddrFrom4([4]byte{a16[12], a16[13], a16[14], a16[15]}), true
	}

	for i := 0; i < bits/8; i++ {
		if a16[i] != prefixBytes[i] {
			return netip.Addr{}, false
		}
	}
	start := bits / 8
	return netip.AddrFrom4([4]byte{a16[start], a16[start+1], a16[start+2], a16[start+3]}), true
}

func addr4(b [4]byte) netip.Addr   { return netip.AddrFrom4(b) }
func addr16(b [16]byte) netip.Addr { return netip.AddrFrom16(b).Unmap() }
