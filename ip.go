package netsec

import (
	"fmt"
	"net/netip"
	"strings"
)

var idnaProfile = newIDNAProfile()

var (
	prefixSiteLocalV6 = netip.MustParsePrefix("fec0::/10")
	prefixDocV6       = netip.MustParsePrefix("2001:db8::/32")
	prefixBenchV6     = netip.MustParsePrefix("2001:2::/48")
	prefixOrchid1     = netip.MustParsePrefix("2001:10::/28")
	prefixOrchid2     = netip.MustParsePrefix("2001:20::/28")
	prefixDocV6b      = netip.MustParsePrefix("3fff::/20")
	prefixNAT64Local  = netip.MustParsePrefix("64:ff9b:1::/48")
	prefixTeredo      = netip.MustParsePrefix("2001::/32")

	prefixZeroV4     = netip.MustParsePrefix("0.0.0.0/8")
	prefixCGNAT      = netip.MustParsePrefix("100.64.0.0/10")
	prefixIETFV4     = netip.MustParsePrefix("192.0.0.0/24")
	prefixDocV4      = netip.MustParsePrefix("192.0.2.0/24")
	prefix6to4Relay  = netip.MustParsePrefix("192.88.99.0/24")
	prefixBenchV4    = netip.MustParsePrefix("198.18.0.0/15")
	prefixDocV4b     = netip.MustParsePrefix("198.51.100.0/24")
	prefixDocV4c     = netip.MustParsePrefix("203.0.113.0/24")
	prefixReservedV4 = netip.MustParsePrefix("240.0.0.0/4")
)

// NormalizeHost converts a host name to its canonical ASCII representation.
// Unicode names are mapped with UTS #46/IDNA, and legacy numeric IPv4 forms
// (decimal, octal, hexadecimal, and shortened dotted forms) are converted to
// canonical dotted decimal addresses. IPv6 literals are returned without
// brackets.
func NormalizeHost(host string) (string, error) {
	normalized, _, _, err := normalizeHost(host)
	return normalized, err
}

func normalizeHost(host string) (string, netip.Addr, bool, error) {
	if host == "" {
		return "", netip.Addr{}, false, ErrHost
	}
	if strings.TrimSpace(host) != host {
		return "", netip.Addr{}, false, fmt.Errorf("%w: contains whitespace", ErrHost)
	}
	if strings.HasPrefix(host, "[") || strings.HasSuffix(host, "]") {
		if len(host) < 2 || !strings.HasPrefix(host, "[") || !strings.HasSuffix(host, "]") {
			return "", netip.Addr{}, false, fmt.Errorf("%w: unmatched bracket", ErrHost)
		}
		host = host[1 : len(host)-1]
	}
	if strings.ContainsAny(host, "\\/?#@") {
		return "", netip.Addr{}, false, fmt.Errorf("%w: contains URL delimiter", ErrHost)
	}
	if strings.Contains(host, "%") {
		return "", netip.Addr{}, false, fmt.Errorf("%w: percent-encoded host", ErrHost)
	}

	// A colon can only occur in an IPv6 literal here; net/url strips the
	// brackets before exposing Hostname.
	if strings.Contains(host, ":") {
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return "", netip.Addr{}, false, fmt.Errorf("%w: invalid IPv6 literal", ErrHost)
		}
		if addr.Zone() != "" {
			return "", netip.Addr{}, false, fmt.Errorf("%w: IPv6 zones are not allowed", ErrHost)
		}
		addr, err = normalizeAddr(addr)
		if err != nil {
			return "", netip.Addr{}, false, err
		}
		return addr.String(), addr, true, nil
	}

	host = strings.TrimSuffix(host, ".")
	if host == "" || strings.HasSuffix(host, ".") {
		return "", netip.Addr{}, false, fmt.Errorf("%w: empty label", ErrHost)
	}

	ascii, err := idnaProfile.ToASCII(host)
	if err != nil {
		return "", netip.Addr{}, false, fmt.Errorf("%w: IDNA: %v", ErrHost, err)
	}
	ascii = strings.ToLower(strings.TrimSuffix(ascii, "."))
	if ascii == "" || strings.HasSuffix(ascii, ".") {
		return "", netip.Addr{}, false, fmt.Errorf("%w: empty label", ErrHost)
	}

	if addr, ok, err := parseIPv4Host(ascii); err != nil {
		return "", netip.Addr{}, false, err
	} else if ok {
		return addr.String(), addr, true, nil
	}
	if err := validateASCIIDomain(ascii); err != nil {
		return "", netip.Addr{}, false, err
	}
	return ascii, netip.Addr{}, false, nil
}

func validateASCIIDomain(host string) error {
	if len(host) > 253 {
		return fmt.Errorf("%w: name is too long", ErrHost)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return fmt.Errorf("%w: empty label", ErrHost)
		}
		if len(label) > 63 {
			return fmt.Errorf("%w: label is too long", ErrHost)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%w: label starts or ends with hyphen", ErrHost)
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return fmt.Errorf("%w: invalid character %q", ErrHost, c)
		}
	}
	return nil
}

// parseIPv4Host implements the inet_aton numeric forms accepted by common
// URL implementations. If the host is not numeric, ok is false and no error
// is returned. Ambiguous, numeric-looking hosts that do not parse are
// rejected instead of being sent to the resolver.
func parseIPv4Host(host string) (netip.Addr, bool, error) {
	parts := strings.Split(host, ".")
	allNumeric := true
	for _, part := range parts {
		if !looksNumericPart(part) {
			allNumeric = false
			break
		}
	}
	if !allNumeric {
		return netip.Addr{}, false, nil
	}
	if len(parts) > 4 {
		return netip.Addr{}, false, fmt.Errorf("%w: invalid numeric IPv4 form", ErrHost)
	}

	values := make([]uint64, len(parts))
	for i, part := range parts {
		v, err := parseNumericPart(part)
		if err != nil {
			return netip.Addr{}, false, fmt.Errorf("%w: %v", ErrHost, err)
		}
		values[i] = v
	}

	var out [4]byte
	switch len(values) {
	case 1:
		if values[0] > 0xffffffff {
			return netip.Addr{}, false, fmt.Errorf("%w: numeric IPv4 value overflow", ErrHost)
		}
		out[0] = byte(values[0] >> 24)
		out[1] = byte(values[0] >> 16)
		out[2] = byte(values[0] >> 8)
		out[3] = byte(values[0])
	case 2:
		if values[0] > 0xff || values[1] > 0xffffff {
			return netip.Addr{}, false, fmt.Errorf("%w: numeric IPv4 value overflow", ErrHost)
		}
		out[0] = byte(values[0])
		out[1] = byte(values[1] >> 16)
		out[2] = byte(values[1] >> 8)
		out[3] = byte(values[1])
	case 3:
		if values[0] > 0xff || values[1] > 0xff || values[2] > 0xffff {
			return netip.Addr{}, false, fmt.Errorf("%w: numeric IPv4 value overflow", ErrHost)
		}
		out[0] = byte(values[0])
		out[1] = byte(values[1])
		out[2] = byte(values[2] >> 8)
		out[3] = byte(values[2])
	case 4:
		for i, v := range values {
			if v > 0xff {
				return netip.Addr{}, false, fmt.Errorf("%w: numeric IPv4 value overflow", ErrHost)
			}
			out[i] = byte(v)
		}
	}
	return netip.AddrFrom4(out), true, nil
}

func looksNumericPart(part string) bool {
	if part == "" {
		return false
	}
	if strings.HasPrefix(part, "0x") || strings.HasPrefix(part, "0X") {
		return true
	}
	for i := 0; i < len(part); i++ {
		if part[i] < '0' || part[i] > '9' {
			return false
		}
	}
	return true
}

func parseNumericPart(part string) (uint64, error) {
	switch {
	case strings.HasPrefix(part, "0x") || strings.HasPrefix(part, "0X"):
		if len(part) == 2 {
			return 0, fmt.Errorf("invalid hexadecimal IPv4 component %q", part)
		}
		var v uint64
		for i := 2; i < len(part); i++ {
			d, ok := hexDigit(part[i])
			if !ok {
				return 0, fmt.Errorf("invalid hexadecimal IPv4 component %q", part)
			}
			v = v*16 + uint64(d)
			if v > 0xffffffff {
				return 0, fmt.Errorf("numeric IPv4 value overflow")
			}
		}
		return v, nil
	case len(part) > 1 && part[0] == '0':
		var v uint64
		for i := 1; i < len(part); i++ {
			if part[i] < '0' || part[i] > '7' {
				return 0, fmt.Errorf("invalid octal IPv4 component %q", part)
			}
			v = v*8 + uint64(part[i]-'0')
			if v > 0xffffffff {
				return 0, fmt.Errorf("numeric IPv4 value overflow")
			}
		}
		return v, nil
	default:
		var v uint64
		for i := 0; i < len(part); i++ {
			if part[i] < '0' || part[i] > '9' {
				return 0, fmt.Errorf("invalid decimal IPv4 component %q", part)
			}
			v = v*10 + uint64(part[i]-'0')
			if v > 0xffffffff {
				return 0, fmt.Errorf("numeric IPv4 value overflow")
			}
		}
		return v, nil
	}
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// normalizeAddr removes IPv6 zones and IPv4-in-IPv6 encodings so that policy
// checks cannot be bypassed with ::ffff:127.0.0.1 or similar spellings.
func normalizeAddr(addr netip.Addr) (netip.Addr, error) {
	if !addr.IsValid() {
		return netip.Addr{}, fmt.Errorf("%w: invalid IP address", ErrHost)
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%w: IP zones are not allowed", ErrHost)
	}
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if addr.Is6() {
		raw := addr.As16()
		first12Zero := true
		for _, b := range raw[:12] {
			if b != 0 {
				first12Zero = false
				break
			}
		}
		if first12Zero {
			// Keep ::1 as IPv6 so it is reported as loopback rather than
			// collapsed into the IPv4 0.0.0.1 reserved range.
			if addr != netip.MustParseAddr("::1") {
				var b [4]byte
				copy(b[:], raw[12:])
				addr = netip.AddrFrom4(b)
			}
		}
	}
	return addr, nil
}

type addressClass struct {
	private  bool
	loopback bool
	linkLoc  bool
	blocked  bool
}

func classifyAddress(addr netip.Addr) addressClass {
	var class addressClass
	if !addr.IsValid() || addr.Zone() != "" {
		class.blocked = true
		return class
	}
	if addr.Is4() {
		class = classifyIPv4(addr)
		return class
	}

	class.loopback = addr.IsLoopback()
	class.linkLoc = addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast()
	class.private = addr.IsPrivate()
	if addr.IsUnspecified() || addr.IsMulticast() ||
		(!addr.IsGlobalUnicast() && !class.loopback && !class.linkLoc && !class.private) {
		class.blocked = true
	}

	switch {
	case prefixSiteLocalV6.Contains(addr): // deprecated IPv6 site-local
		class.blocked = true
	case prefixDocV6.Contains(addr): // documentation
		class.blocked = true
	case prefixBenchV6.Contains(addr): // benchmarking
		class.blocked = true
	case prefixOrchid1.Contains(addr), prefixOrchid2.Contains(addr): // ORCHID
		class.blocked = true
	case prefixDocV6b.Contains(addr): // documentation
		class.blocked = true
	case prefixNAT64Local.Contains(addr): // local-use NAT64 prefix
		class.blocked = true
	case prefixTeredo.Contains(addr): // Teredo transition space
		class.blocked = true
	}

	// IPv4 embedded in 6to4 and NAT64 addresses must not hide a private or
	// link-local destination.
	b := addr.As16()
	if b[0] == 0x20 && b[1] == 0x02 {
		embedded := netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
		emb := classifyIPv4(embedded)
		class.private = class.private || emb.private
		class.loopback = class.loopback || emb.loopback
		class.linkLoc = class.linkLoc || emb.linkLoc
		class.blocked = class.blocked || emb.blocked
	}
	if b[0] == 0x00 && b[1] == 0x64 && b[2] == 0xff && b[3] == 0x9b &&
		b[4] == 0x00 && b[5] == 0x00 {
		embedded := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		emb := classifyIPv4(embedded)
		class.private = class.private || emb.private
		class.loopback = class.loopback || emb.loopback
		class.linkLoc = class.linkLoc || emb.linkLoc
		class.blocked = class.blocked || emb.blocked
	}
	return class
}

func classifyIPv4(addr netip.Addr) addressClass {
	var class addressClass
	switch {
	case addr.IsLoopback():
		class.loopback = true
	case addr.IsLinkLocalUnicast():
		class.linkLoc = true
	case addr.IsPrivate():
		class.private = true
	case prefixCGNAT.Contains(addr): // CGNAT
		class.private = true
	}
	if addr.IsUnspecified() || addr.IsMulticast() ||
		(!addr.IsGlobalUnicast() && !class.loopback && !class.linkLoc && !class.private) {
		class.blocked = true
	}
	switch {
	case prefixZeroV4.Contains(addr):
		class.blocked = true
	case prefixIETFV4.Contains(addr):
		class.blocked = true
	case prefixDocV4.Contains(addr):
		class.blocked = true
	case prefix6to4Relay.Contains(addr):
		class.blocked = true
	case prefixBenchV4.Contains(addr):
		class.blocked = true
	case prefixDocV4b.Contains(addr):
		class.blocked = true
	case prefixDocV4c.Contains(addr):
		class.blocked = true
	case prefixReservedV4.Contains(addr):
		class.blocked = true
	}
	return class
}
