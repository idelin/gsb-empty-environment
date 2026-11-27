package rpz

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// RPZ reverse-CIDR owner encoding, as documented in the BIND ARM:
//
//   - The first (leftmost) label is the prefix length in bits.
//   - IPv4 addresses are encoded as reversed decimal octets, e.g. the owner
//     "24.0.1.168.192.rpz-ip" decodes to 192.168.1.0/24. Host bits are masked
//     off; owners may use the full four-octet form or BIND's minimal form,
//     where only the octets covered by the prefix are emitted (e.g.
//     "8.10.rpz-client-ip" -> 10.0.0.0/8).
//   - IPv6 addresses are encoded as reversed hex nibbles, e.g.
//     "32.0.8.b.d.0.1.0.0.2.rpz-ip" for 2001:db8::/32. A single "zz" label
//     compresses one run of zero nibbles: "48.zz.db8.2001.rpz-ip" decodes to
//     2001:db8::/48. At most one "zz" label is permitted per owner.

var (
	errMalformedIPOwner = errors.New("rpz: malformed reverse-CIDR owner")
	errBadPrefixLength  = errors.New("rpz: invalid prefix length label")
)

const zzSentinel = 0xFF

// DecodeIPOwner decodes the labels that precede an RPZ IP suffix label
// (rpz-ip, rpz-client-ip or rpz-nsip). labels must not contain the suffix
// itself. The returned prefix is always masked to its prefix length.
func DecodeIPOwner(labels []string) (netip.Prefix, error) {
	if len(labels) == 0 {
		return netip.Prefix{}, errMalformedIPOwner
	}

	var bits int
	if _, err := fmt.Sscanf(labels[0], "%d", &bits); err != nil {
		return netip.Prefix{}, fmt.Errorf("%w: %q is not a prefix length", errMalformedIPOwner, labels[0])
	}

	if len(labels) == 1 || looksLikeIPv4Owner(labels[1:]) {
		return decodeIPv4(labels, bits)
	}
	return decodeIPv6(labels, bits)
}

func decodeIPv4(labels []string, bits int) (netip.Prefix, error) {
	octets := labels[1:]
	if bits < 0 || bits > 32 || len(octets) > 4 {
		return netip.Prefix{}, errBadPrefixLength
	}

	var addr [4]byte
	for i, label := range octets {
		var octet int
		if _, err := fmt.Sscanf(label, "%d", &octet); err != nil || octet < 0 || octet > 255 {
			return netip.Prefix{}, fmt.Errorf("%w: %q is not an octet", errMalformedIPOwner, label)
		}
		// Labels are least-significant octet first, so the rightmost label
		// fills the most significant address byte. With fewer than four
		// labels (minimal form) the leading high-order octets stay zero.
		addr[len(octets)-1-i] = byte(octet)
	}
	return netip.PrefixFrom(netip.AddrFrom4(addr), bits).Masked(), nil
}

func decodeIPv6(labels []string, bits int) (netip.Prefix, error) {
	if bits < 0 || bits > 128 {
		return netip.Prefix{}, errBadPrefixLength
	}

	// Rebuild the most-significant-first nibble sequence. Owner labels are
	// stored in reverse order, so scan right-to-left. Within a hex group the
	// nibble characters keep their natural address order; labels shorter than
	// four nibbles are zero-padded on the left because the encoder drops
	// leading zero nibbles (e.g. "db8" is short for 0,d,b,8).
	var nibbles []byte
	zzUsed := false
	for i := len(labels) - 1; i >= 1; i-- {
		label := labels[i]
		switch {
		case label == "zz":
			if zzUsed {
				return netip.Prefix{}, fmt.Errorf("%w: multiple zz labels", errMalformedIPOwner)
			}
			zzUsed = true
			nibbles = append(nibbles, zzSentinel)
		case isHexLabel(label):
			for pad := len(label); pad < 4 && len(label) > 1; pad++ {
				nibbles = append(nibbles, 0)
			}
			for j := 0; j < len(label); j++ {
				nibbles = append(nibbles, hexVal(label[j]))
			}
		default:
			return netip.Prefix{}, fmt.Errorf("%w: %q is not a hex nibble group", errMalformedIPOwner, label)
		}
	}

	needed := (bits + 3) / 4
	explicit := 0
	for _, nibble := range nibbles {
		if nibble == zzSentinel {
			continue
		}
		explicit++
	}
	if explicit > needed {
		return netip.Prefix{}, fmt.Errorf("%w: %d nibbles for /%d prefix", errMalformedIPOwner, explicit, bits)
	}

	// "zz" expands to every zero nibble not supplied explicitly, so the gap
	// size is simply the prefix nibble count minus the explicit count.
	gap := needed - explicit
	expanded := make([]byte, 0, needed)
	for _, nibble := range nibbles {
		if nibble == zzSentinel {
			for j := 0; j < gap; j++ {
				expanded = append(expanded, 0)
			}
			continue
		}
		expanded = append(expanded, nibble)
	}
	if len(expanded) != needed {
		return netip.Prefix{}, fmt.Errorf("%w: %d nibbles for /%d prefix", errMalformedIPOwner, len(expanded), bits)
	}

	var addr [16]byte
	for i, nibble := range expanded {
		if i%2 == 0 {
			addr[i/2] = nibble << 4
		} else {
			addr[i/2] |= nibble
		}
	}
	return netip.PrefixFrom(netip.AddrFrom16(addr), bits).Masked(), nil
}

// EncodeIPOwner renders an address prefix as the owner labels that precede an
// RPZ IP suffix label. IPv4 always emits four reversed octets matching the
// documented examples (24.0.1.168.192...); IPv6 emits reversed hex groups and
// compresses the longest run of zero nibbles with one "zz" label.
func EncodeIPOwner(prefix netip.Prefix) ([]string, error) {
	prefix = prefix.Masked()
	bits := prefix.Bits()
	addr := prefix.Addr()

	if addr.Is4() {
		octets := addr.As4()
		return []string{
			fmt.Sprintf("%d", bits),
			fmt.Sprintf("%d", octets[3]),
			fmt.Sprintf("%d", octets[2]),
			fmt.Sprintf("%d", octets[1]),
			fmt.Sprintf("%d", octets[0]),
		}, nil
	}

	raw := addr.As16()
	totalNibbles := (bits + 3) / 4
	nibbles := make([]byte, totalNibbles)
	for i := 0; i < totalNibbles; i++ {
		if i%2 == 0 {
			nibbles[i] = raw[i/2] >> 4
		} else {
			nibbles[i] = raw[i/2] & 0x0F
		}
	}

	bestStart, bestLen := longestZeroRun(nibbles)
	labels := []string{fmt.Sprintf("%d", bits)}

	if bestLen >= 2 {
		// Single-nibble labels in reverse address order; zz marks the run.
		labels = append(labels, encodeNibbles(nibbles[bestStart+bestLen:])...)
		labels = append(labels, "zz")
		labels = append(labels, encodeNibbles(nibbles[:bestStart])...)
	} else {
		labels = append(labels, encodeNibbles(nibbles)...)
	}
	return labels, nil
}

// encodeNibbles renders MSB-first nibbles as single-nibble owner labels in
// reverse address order, matching the canonical BIND ARM spelling (e.g.
// "32.0.8.b.d.0.1.0.0.2"). Multi-nibble grouped labels such as "db8" remain
// accepted on input by DecodeIPOwner.
func encodeNibbles(nibbles []byte) []string {
	out := make([]string, 0, len(nibbles))
	for i := len(nibbles) - 1; i >= 0; i-- {
		out = append(out, fmt.Sprintf("%x", nibbles[i]))
	}
	return out
}

func longestZeroRun(nibbles []byte) (start, length int) {
	for i := 0; i < len(nibbles); {
		if nibbles[i] != 0 {
			i++
			continue
		}
		j := i
		for j < len(nibbles) && nibbles[j] == 0 {
			j++
		}
		if j-i > length {
			start, length = i, j-i
		}
		i = j
	}
	return start, length
}

// looksLikeIPv4Owner reports whether all remaining labels are decimal octets.
func looksLikeIPv4Owner(labels []string) bool {
	if len(labels) == 0 || len(labels) > 4 {
		return false
	}
	for _, label := range labels {
		var octet int
		if _, err := fmt.Sscanf(label, "%d", &octet); err != nil || octet < 0 || octet > 255 {
			return false
		}
	}
	return true
}

func isHexLabel(label string) bool {
	if label == "" {
		return false
	}
	for i := 0; i < len(label); i++ {
		if !isHex(label[i]) {
			return false
		}
	}
	return true
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// ParsePrefix is a convenience wrapper around netip.ParsePrefix that also
// accepts bare IP literals as host routes (/32 or /128).
func ParsePrefix(text string) (netip.Prefix, error) {
	text = strings.TrimSpace(text)
	if prefix, err := netip.ParsePrefix(text); err == nil {
		return prefix.Masked(), nil
	}
	addr, err := netip.ParseAddr(text)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}
