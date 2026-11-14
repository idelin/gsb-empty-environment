package netsec

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ParseAddress parses an address literal and classifies its effective
// destination. It accepts bare addresses, bracketed IPv6 addresses, zone
// suffixes, host:port endpoints, and URLs.
func ParseAddress(raw string) (Result, error) {
	original := strings.TrimSpace(raw)
	literal, err := literalFromInput(original)
	if err != nil {
		return Result{
			Input:    original,
			Category: CategoryReserved,
			Scope:    CategoryReserved,
			Internal: true,
			Reason:   err.Error(),
		}, err
	}

	result := ClassifyAddr(literal)
	result.Input = original
	return result, nil
}

// Classify is an alias for ParseAddress.
func Classify(raw string) (Result, error) {
	return ParseAddress(raw)
}

// Parse is an alias for ParseAddress.
func Parse(raw string) (Result, error) {
	return ParseAddress(raw)
}

// ClassifyAddress is an alias for ParseAddress.
func ClassifyAddress(raw string) (Result, error) {
	return ParseAddress(raw)
}

// Normalize returns the effective destination address after unwrapping
// transition forms. The returned address is always unzoned and un-mapped, so
// it can be compared with netip prefixes directly.
func Normalize(raw string) (netip.Addr, error) {
	result, err := ParseAddress(raw)
	if err != nil {
		return netip.Addr{}, err
	}
	return result.Address, nil
}

// ClassifyIP classifies a net.IP value. Invalid or nil values are treated as
// reserved/internal.
func ClassifyIP(ip net.IP) Result {
	if len(ip) == 0 {
		return Result{
			Category: CategoryReserved,
			Scope:    CategoryReserved,
			Internal: true,
			Reason:   "invalid IP",
		}
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return Result{
			Category: CategoryReserved,
			Scope:    CategoryReserved,
			Internal: true,
			Reason:   "invalid IP",
		}
	}
	return ClassifyAddr(addr.Unmap())
}

// IsPublic reports whether raw resolves to a globally reachable public
// unicast address. Parse errors fail closed and return false.
func IsPublic(raw string) bool {
	result, err := ParseAddress(raw)
	return err == nil && result.Public
}

// IsInternal reports whether raw is non-public or cannot be parsed. It is
// the fail-closed inverse of IsPublic.
func IsInternal(raw string) bool {
	result, err := ParseAddress(raw)
	return err != nil || result.Internal
}

// IsPrivate reports whether raw is an IPv4 private-use, IPv6 unique-local,
// or deprecated site-local address. Parse errors fail closed and return true.
func IsPrivate(raw string) bool {
	result, err := ParseAddress(raw)
	return err != nil || result.IsPrivate()
}

// IsLoopback reports whether raw is a loopback destination. Parse errors fail
// closed and return true.
func IsLoopback(raw string) bool {
	result, err := ParseAddress(raw)
	return err != nil || result.IsLoopback()
}

// IsLinkLocal reports whether raw is a link-local destination. Parse errors
// fail closed and return true.
func IsLinkLocal(raw string) bool {
	result, err := ParseAddress(raw)
	return err != nil || result.IsLinkLocal()
}

// IsMulticast reports whether raw is a multicast destination. Parse errors
// fail closed and return true.
func IsMulticast(raw string) bool {
	result, err := ParseAddress(raw)
	return err != nil || result.IsMulticast()
}

// IsReserved reports whether raw is in a reserved destination range. Parse
// errors fail closed and return true.
func IsReserved(raw string) bool {
	result, err := ParseAddress(raw)
	return err != nil || result.IsReserved()
}

// IsPublicIP reports whether ip is a globally reachable public address.
func IsPublicIP(ip net.IP) bool {
	return ClassifyIP(ip).Public
}

// IsInternalIP reports whether ip is non-public or invalid.
func IsInternalIP(ip net.IP) bool {
	return ClassifyIP(ip).Internal
}

// IsPrivateIP reports whether ip is in a private or unique-local range.
func IsPrivateIP(ip net.IP) bool {
	return ClassifyIP(ip).IsPrivate()
}

// IsLoopbackIP reports whether ip is a loopback address.
func IsLoopbackIP(ip net.IP) bool {
	return ClassifyIP(ip).IsLoopback()
}

// IsLinkLocalIP reports whether ip is a link-local address.
func IsLinkLocalIP(ip net.IP) bool {
	return ClassifyIP(ip).IsLinkLocal()
}

// IsMulticastIP reports whether ip is a multicast address.
func IsMulticastIP(ip net.IP) bool {
	return ClassifyIP(ip).IsMulticast()
}

// IsReservedIP reports whether ip is in a reserved range.
func IsReservedIP(ip net.IP) bool {
	return ClassifyIP(ip).IsReserved()
}

// Check returns a BlockedError when raw is not a public destination or cannot
// be parsed.
func Check(raw string) error {
	result, err := ParseAddress(raw)
	if err != nil {
		return err
	}
	return result.Err()
}

// CheckAddress is an alias for Check.
func CheckAddress(raw string) error {
	return Check(raw)
}

// ValidateAddress is an alias for Check.
func ValidateAddress(raw string) error {
	return Check(raw)
}

// ParseEndpoint splits a host:port endpoint. The host is returned without
// surrounding brackets, but an IPv6 zone suffix is preserved. A bare address
// is accepted and returned with an empty port.
func ParseEndpoint(raw string) (host, port string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("%w: empty endpoint", ErrInvalidAddress)
	}

	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(preprocessURL(raw))
		if err != nil {
			return "", "", fmt.Errorf("%w: %v", ErrInvalidAddress, err)
		}
		host, port = parsed.Hostname(), parsed.Port()
		if host == "" {
			return "", "", fmt.Errorf("%w: URL has no host", ErrInvalidAddress)
		}
		if port != "" {
			if err := validatePort(port); err != nil {
				return "", "", err
			}
		}
		return host, port, nil
	}

	if strings.HasPrefix(raw, "[") {
		end := strings.IndexByte(raw, ']')
		if end < 0 {
			return "", "", fmt.Errorf("%w: missing closing bracket", ErrInvalidAddress)
		}
		host = raw[1:end]
		rest := raw[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return "", "", fmt.Errorf("%w: unexpected text after bracket", ErrInvalidAddress)
			}
			port = rest[1:]
		}
		if host == "" {
			return "", "", fmt.Errorf("%w: empty host", ErrInvalidAddress)
		}
		if port != "" {
			if err := validatePort(port); err != nil {
				return "", "", err
			}
		}
		return host, port, nil
	}

	// A single colon can only be a host/port separator. Multiple colons
	// belong to an IPv6 literal; a port on an IPv6 literal must be bracketed.
	if strings.Count(raw, ":") == 1 {
		host, port, _ = strings.Cut(raw, ":")
		if host == "" {
			return "", "", fmt.Errorf("%w: empty host", ErrInvalidAddress)
		}
		if port != "" {
			if err := validatePort(port); err != nil {
				return "", "", err
			}
		}
		return host, port, nil
	}

	return raw, "", nil
}

// preprocessURL escapes a raw percent sign inside an IPv6 bracket host.
// RFC 6874 requires "%25", but accepting the unescaped form is useful when
// classification is applied to operator-supplied URLs.
func preprocessURL(raw string) string {
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return raw
	}
	hostStart := schemeEnd + 3
	hostEnd := len(raw)
	for i := hostStart; i < len(raw); i++ {
		switch raw[i] {
		case '/', '?', '#':
			hostEnd = i
		}
		if hostEnd != len(raw) {
			break
		}
	}

	authority := raw[hostStart:hostEnd]
	searchStart := 0
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		searchStart = at + 1
	}
	openOffset := strings.IndexByte(authority[searchStart:], '[')
	if openOffset < 0 {
		return raw
	}
	open := searchStart + openOffset
	closeOffset := strings.IndexByte(authority[open:], ']')
	if closeOffset < 0 {
		return raw
	}
	close := open + closeOffset
	host := authority[open+1 : close]
	if !strings.Contains(host, "%") {
		return raw
	}

	escapedHost := escapeRawZone(host)
	if escapedHost == host {
		return raw
	}
	return raw[:hostStart+open+1] + escapedHost + raw[hostStart+close:]
}

func escapeRawZone(host string) string {
	var builder strings.Builder
	builder.Grow(len(host) + 8)
	for i := 0; i < len(host); i++ {
		if host[i] != '%' {
			builder.WriteByte(host[i])
			continue
		}
		if i+2 < len(host) && isHexByte(host[i+1]) && isHexByte(host[i+2]) {
			builder.WriteByte('%')
			continue
		}
		builder.WriteString("%25")
	}
	return builder.String()
}

func isHexByte(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func literalFromInput(raw string) (netip.Addr, error) {
	host, _, err := ParseEndpoint(raw)
	if err != nil {
		return netip.Addr{}, err
	}
	return parseLiteral(host)
}

func parseLiteral(raw string) (netip.Addr, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, fmt.Errorf("%w: empty address", ErrInvalidAddress)
	}
	if strings.ContainsAny(raw, " \t\r\n/[]\\") || containsControl(raw) {
		return netip.Addr{}, fmt.Errorf("%w: %q contains invalid characters", ErrInvalidAddress, raw)
	}

	// In URLs, a zone separator is percent-encoded as %25.
	raw = strings.ReplaceAll(raw, "%25", "%")

	zone := ""
	if index := strings.LastIndexByte(raw, '%'); index >= 0 {
		zone = raw[index+1:]
		raw = raw[:index]
		if zone == "" {
			return netip.Addr{}, fmt.Errorf("%w: empty zone", ErrInvalidAddress)
		}
		if strings.Contains(zone, "%") {
			return netip.Addr{}, fmt.Errorf("%w: invalid zone", ErrInvalidAddress)
		}
	}
	if raw == "" {
		return netip.Addr{}, fmt.Errorf("%w: empty address", ErrInvalidAddress)
	}

	// The standard forms, including IPv6 with an embedded dotted IPv4
	// address, are handled by netip directly.
	if addr, err := netip.ParseAddr(raw); err == nil {
		if zone != "" {
			if addr.Is4() {
				return netip.Addr{}, fmt.Errorf("%w: IPv4 address cannot have a zone", ErrInvalidAddress)
			}
			addr = addr.WithZone(zone)
			if !addr.IsValid() {
				return netip.Addr{}, fmt.Errorf("%w: invalid zone", ErrInvalidAddress)
			}
		}
		return addr, nil
	}

	if !strings.Contains(raw, ":") {
		addr, err := parseIPv4Loose(raw)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
		}
		if zone != "" {
			return netip.Addr{}, fmt.Errorf("%w: IPv4 address cannot have a zone", ErrInvalidAddress)
		}
		return addr, nil
	}

	normalized, err := normalizeIPv6Embedded(raw)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	addr, err := netip.ParseAddr(normalized)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	if zone != "" {
		addr = addr.WithZone(zone)
		if !addr.IsValid() {
			return netip.Addr{}, fmt.Errorf("%w: invalid zone", ErrInvalidAddress)
		}
	}
	return addr, nil
}

// normalizeIPv6Embedded rewrites an IPv4 tail that uses a non-standard IPv4
// spelling (leading-zero octal, 0x hex, or a bare 32-bit integer) into two
// hexadecimal IPv6 groups.
func normalizeIPv6Embedded(text string) (string, error) {
	index := strings.LastIndexByte(text, ':')
	if index < 0 {
		return "", fmt.Errorf("not an IPv6 literal")
	}
	tail := text[index+1:]
	if tail == "" {
		return text, nil
	}
	if !strings.Contains(tail, ".") && !isBareIPv4Integer(tail) && !isBareIPv4Hex(tail) {
		return text, nil
	}

	addr, err := parseIPv4Loose(tail)
	if err != nil {
		return "", err
	}
	octets := addr.As4()
	replacement := fmt.Sprintf("%02x%02x:%02x%02x", octets[0], octets[1], octets[2], octets[3])
	return text[:index+1] + replacement, nil
}

func isBareIPv4Integer(text string) bool {
	if len(text) <= 4 {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isBareIPv4Hex(text string) bool {
	if len(text) < 3 || (text[0:2] != "0x" && text[0:2] != "0X") {
		return false
	}
	digits := text[2:]
	if len(digits) > 8 {
		return false
	}
	for _, r := range digits {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// parseIPv4Loose implements the traditional inet_aton forms: one to four
// dot-separated fields, decimal by default, octal with a leading zero, and
// hexadecimal with a 0x prefix.
func parseIPv4Loose(text string) (netip.Addr, error) {
	text = strings.TrimSpace(text)
	text = strings.TrimSuffix(text, ".")
	if text == "" {
		return netip.Addr{}, fmt.Errorf("empty IPv4 address")
	}

	parts := strings.Split(text, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return netip.Addr{}, fmt.Errorf("IPv4 address has %d fields", len(parts))
	}

	values := make([]uint32, len(parts))
	for i, part := range parts {
		value, err := parseIPv4Field(part)
		if err != nil {
			return netip.Addr{}, err
		}
		values[i] = value
	}

	var address uint32
	switch len(values) {
	case 1:
		address = values[0]
	case 2:
		if values[0] > 0xff || values[1] > 0xffffff {
			return netip.Addr{}, fmt.Errorf("IPv4 field out of range")
		}
		address = values[0]<<24 | values[1]
	case 3:
		if values[0] > 0xff || values[1] > 0xff || values[2] > 0xffff {
			return netip.Addr{}, fmt.Errorf("IPv4 field out of range")
		}
		address = values[0]<<24 | values[1]<<16 | values[2]
	case 4:
		for _, value := range values {
			if value > 0xff {
				return netip.Addr{}, fmt.Errorf("IPv4 octet out of range")
			}
		}
		address = values[0]<<24 | values[1]<<16 | values[2]<<8 | values[3]
	}

	return netip.AddrFrom4([4]byte{
		byte(address >> 24),
		byte(address >> 16),
		byte(address >> 8),
		byte(address),
	}), nil
}

func parseIPv4Field(field string) (uint32, error) {
	if field == "" {
		return 0, fmt.Errorf("empty IPv4 field")
	}

	base := 10
	switch {
	case len(field) >= 2 && (field[0:2] == "0x" || field[0:2] == "0X"):
		base = 16
		field = field[2:]
		if field == "" {
			return 0, fmt.Errorf("empty hexadecimal IPv4 field")
		}
	case len(field) > 1 && field[0] == '0':
		base = 8
		field = field[1:]
		if field == "" {
			return 0, nil
		}
	}

	value, err := strconv.ParseUint(field, base, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid IPv4 field %q: %v", field, err)
	}
	return uint32(value), nil
}

func validatePort(port string) error {
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil || value == 0 {
		return fmt.Errorf("%w: invalid port %q", ErrInvalidAddress, port)
	}
	return nil
}

func looksLikeIPLiteral(host string) bool {
	if strings.Contains(host, ":") {
		return true
	}
	if host == "" {
		return false
	}
	allNumericDots := true
	allHexDots := true
	for _, r := range host {
		if r != '.' && (r < '0' || r > '9') {
			allNumericDots = false
		}
		if r != '.' && r != 'x' && r != 'X' &&
			(r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			allHexDots = false
		}
	}
	hasHexPrefix := strings.HasPrefix(host, "0x") || strings.HasPrefix(host, "0X")
	return allNumericDots || (hasHexPrefix && allHexDots)
}

func containsControl(text string) bool {
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
