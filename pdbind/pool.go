package pdbind

import (
	"encoding/binary"
	"net/netip"
)

// PrefixPool describes an aggregate IPv6 prefix that is sliced into
// equal-sized delegated prefixes.
type PrefixPool struct {
	// CIDR is the aggregate pool, for example 2001:db8::/32.
	CIDR netip.Prefix
	// DelegatedLen is the prefix length of each delegation, for example 56.
	DelegatedLen int
}

// Validate checks that the pool is an IPv6 aggregate with a delegated length
// no shorter than the aggregate length and at most /128.
func (p PrefixPool) Validate() error {
	if !p.CIDR.IsValid() || !p.CIDR.Addr().Is6() {
		return ErrMalformedMessage
	}
	if p.DelegatedLen < p.CIDR.Bits() || p.DelegatedLen > 128 {
		return ErrMalformedMessage
	}
	return nil
}

// Size returns the number of distinct delegated prefixes in the pool.
func (p PrefixPool) Size() uint64 {
	if err := p.Validate(); err != nil {
		return 0
	}
	return uint64(1) << uint(p.DelegatedLen-p.CIDR.Bits())
}

// nthPrefix returns the i-th aligned delegated prefix within the aggregate
// pool, or false when i is outside the pool.
func (p PrefixPool) nthPrefix(i uint64) (netip.Prefix, bool) {
	if i >= p.Size() {
		return netip.Prefix{}, false
	}
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[8:], i)
	offset := shiftLeft128(raw, uint(128-p.DelegatedLen))

	addr := p.CIDR.Masked().Addr().As16()
	var carry uint16
	for i := uint(15); ; i-- {
		sum := uint16(addr[i]) + uint16(offset[i]) + carry
		addr[i] = byte(sum)
		carry = sum >> 8
		if i == 0 {
			break
		}
	}
	if carry != 0 {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(netip.AddrFrom16(addr), p.DelegatedLen), true
}

// indexOf returns the pool slot index of an aligned delegated prefix.
func (p PrefixPool) indexOf(prefix netip.Prefix) uint64 {
	poolBase := p.CIDR.Masked().Addr().As16()
	cand := prefix.Masked().Addr().As16()
	var diff [16]byte
	borrow := 0
	for i := 15; i >= 0; i-- {
		d := int(cand[i]) - int(poolBase[i]) - borrow
		if d < 0 {
			d += 256
			borrow = 1
		} else {
			borrow = 0
		}
		diff[i] = byte(d)
	}
	shifted := shiftRight128(diff, uint(128-p.DelegatedLen))
	return binary.BigEndian.Uint64(shifted[8:])
}

// contains reports whether prefix is an aligned delegated-sized prefix fully
// inside the aggregate pool.
func (p PrefixPool) contains(prefix netip.Prefix) bool {
	if !prefix.IsValid() || !prefix.Addr().Is6() || prefix.Bits() != p.DelegatedLen {
		return false
	}
	if !p.CIDR.Contains(prefix.Addr()) {
		return false
	}
	return prefix.Masked() == prefix
}

// alloc returns the first delegated prefix whose index is not in used.
func (p PrefixPool) alloc(used map[uint64]bool) (netip.Prefix, bool) {
	size := p.Size()
	for i := uint64(0); i < size; i++ {
		if used[i] {
			continue
		}
		return p.nthPrefix(i)
	}
	return netip.Prefix{}, false
}

// shiftLeft128 left-shifts a 128-bit big-endian value by bits (0..127).
func shiftLeft128(in [16]byte, bits uint) [16]byte {
	var out [16]byte
	if bits >= 128 {
		return out
	}
	byteShift := bits / 8
	bitShift := bits % 8
	for i := 15; i >= 0; i-- {
		dst := i - int(byteShift)
		if dst < 0 {
			break
		}
		out[dst] |= in[i] << bitShift
		if bitShift != 0 && dst-1 >= 0 {
			out[dst-1] |= in[i] >> (8 - bitShift)
		}
	}
	return out
}

// shiftRight128 right-shifts a 128-bit big-endian value by bits (0..127).
func shiftRight128(in [16]byte, bits uint) [16]byte {
	var out [16]byte
	if bits >= 128 {
		return out
	}
	byteShift := bits / 8
	bitShift := bits % 8
	for src := 0; src < 16; src++ {
		dst := src + int(byteShift)
		if dst >= 16 {
			break
		}
		out[dst] |= in[src] >> bitShift
		if bitShift != 0 && src > 0 {
			out[dst] |= in[src-1] << (8 - bitShift)
		}
	}
	return out
}
