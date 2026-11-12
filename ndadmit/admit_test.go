package ndadmit

import (
	"encoding/binary"
	"net/netip"
)

// Test addresses.
var (
	addrLL     = netip.MustParseAddr("fe80::1")
	addrLL2    = netip.MustParseAddr("fe80::2")
	addrGlobal = netip.MustParseAddr("2001:db8::1")
	addrAll    = netip.MustParseAddr("ff02::1")
	addrUnspec = netip.MustParseAddr("::")
	addrTarget = netip.MustParseAddr("fe80::abcd")
	addrPrefix = netip.MustParseAddr("2001:db8:1::")
	macA       = []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	macB       = []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
)

// baseHeader builds the type/code/checksum prefix. checksum is left zero and
// filled by withChecksum when checksum verification is under test.
func baseHeader(icmpType uint8) []byte {
	return []byte{icmpType, 0, 0, 0}
}

func raBody() []byte {
	b := make([]byte, 12)
	b[0] = 64   // Cur Hop Limit
	b[1] = 0x80 // Managed address configuration flag
	binary.BigEndian.PutUint16(b[2:4], 1800)
	binary.BigEndian.PutUint32(b[4:8], 30000)
	binary.BigEndian.PutUint32(b[8:12], 1000)
	return b
}

func nsBody(target netip.Addr) []byte {
	b := make([]byte, 20)
	copy(b[4:20], target.AsSlice())
	return b
}

func naBody(flags byte, target netip.Addr) []byte {
	b := make([]byte, 20)
	b[0] = flags
	copy(b[4:20], target.AsSlice())
	return b
}

func rsBody() []byte {
	return make([]byte, 0) // RS fixed part after the 4-byte header is empty
}

func redirectBody(target, dest netip.Addr) []byte {
	b := make([]byte, 32)
	copy(b[0:16], target.AsSlice())
	copy(b[16:32], dest.AsSlice())
	return b
}

// llOption builds an Ethernet-sized link-layer option (length unit 1 -> 8
// bytes total).
func llOption(kind byte, mac []byte) []byte {
	opt := []byte{kind, 1, 0, 0, 0, 0, 0, 0}
	copy(opt[2:], mac)
	return opt
}

// rawOption builds an arbitrary option for malformed-packet tests.
func rawOption(kind, units byte, payload []byte) []byte {
	opt := make([]byte, int(units)*8)
	opt[0] = kind
	opt[1] = units
	copy(opt[2:], payload)
	return opt
}

func mtuOption(mtu uint32) []byte {
	opt := []byte{OptMTU, 1, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(opt[4:8], mtu)
	return opt
}

func prefixOption() []byte {
	opt := make([]byte, 32)
	opt[0] = OptPrefixInformation
	opt[1] = 4
	opt[2] = 64   // prefix length
	opt[3] = 0xc0 // L + A flags
	binary.BigEndian.PutUint32(opt[4:8], 86400)
	binary.BigEndian.PutUint32(opt[8:12], 14400)
	copy(opt[16:32], addrPrefix.AsSlice())
	return opt
}

// withChecksum computes and inserts the ICMPv6 checksum for packet.
func withChecksum(src, dst netip.Addr, packet []byte) []byte {
	out := append([]byte(nil), packet...)
	pseudo := make([]byte, 40)
	copy(pseudo[0:16], src.AsSlice())
	copy(pseudo[16:32], dst.AsSlice())
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(out)))
	pseudo[39] = 58

	sum := uint32(0)
	add := func(b []byte) {
		for i := 0; i < len(b); i += 2 {
			word := uint32(b[i]) << 8
			if i+1 < len(b) {
				word |= uint32(b[i+1])
			}
			sum += word
		}
	}
	add(pseudo)
	add(out)
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	csum := ^uint16(sum)
	binary.BigEndian.PutUint16(out[2:4], csum)
	return out
}

func concat(parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
