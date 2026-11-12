package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

const (
	// IdentifierTypeIP is the ACME identifier type for IP addresses (RFC 8738).
	IdentifierTypeIP = "ip"

	// ChallengeHTTP01 and ChallengeTLSALPN01 are the only challenge types that
	// may be used with an IP identifier.
	ChallengeHTTP01      = "http-01"
	ChallengeTLSALPN01   = "tls-alpn-01"
	ChallengeDNS01       = "dns-01"
	TLSALPN01Protocol    = "acme-tls/1"
	HTTP01ChallengePath  = "/.well-known/acme-challenge/"
	http01DefaultPort    = "80"
	tlsALPN01DefaultPort = "443"
)

var (
	// ErrIdentifierType is returned when an ACME identifier is not of type "ip".
	ErrIdentifierType = errors.New("netsec: ACME identifier type must be \"ip\"")
	// ErrInvalidIPIdentifier is returned when an identifier value is not an IP address.
	ErrInvalidIPIdentifier = errors.New("netsec: invalid IP identifier value")
	// ErrNonCanonicalIP is returned for a valid address written in a non-canonical textual form.
	ErrNonCanonicalIP = errors.New("netsec: IP identifier is not in canonical text form")
	// ErrWildcardIPIdentifier is returned for wildcard values, which do not apply to IP identifiers.
	ErrWildcardIPIdentifier = errors.New("netsec: wildcard identifiers do not apply to IP identifiers")
	// ErrDisallowedIPAddress is returned for non-public addresses, including loopback,
	// private, link-local, unique-local, multicast, IPv4-mapped, and reserved addresses.
	ErrDisallowedIPAddress = errors.New("netsec: IP address is not a public globally routable address")
	// ErrUnsupportedChallenge is returned when a challenge cannot validate an IP identifier.
	ErrUnsupportedChallenge = errors.New("netsec: challenge type cannot validate an IP identifier")
)

// Identifier is an ACME identifier as it appears in a newOrder request.
type Identifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// NewIPIdentifier constructs a validated ACME "ip" identifier.
func NewIPIdentifier(value string) (Identifier, error) {
	id := Identifier{Type: IdentifierTypeIP, Value: value}
	if err := id.Validate(); err != nil {
		return Identifier{}, err
	}
	return id, nil
}

// ParseIdentifierJSON decodes a single ACME identifier object and validates it.
func ParseIdentifierJSON(data []byte) (Identifier, error) {
	var id Identifier
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&id); err != nil {
		return Identifier{}, fmt.Errorf("%w: %v", ErrInvalidIPIdentifier, err)
	}
	if dec.More() {
		return Identifier{}, fmt.Errorf("%w: trailing JSON data", ErrInvalidIPIdentifier)
	}
	if err := id.Validate(); err != nil {
		return Identifier{}, err
	}
	return id, nil
}

// Validate checks that the identifier is an RFC 8738 IP identifier with a
// canonical, public, globally routable address.
func (id Identifier) Validate() error {
	if id.Type != IdentifierTypeIP {
		return fmt.Errorf("%w: got %q", ErrIdentifierType, id.Type)
	}
	_, err := ParseIPValue(id.Value)
	return err
}

// ValidateIdentifier is the package-level form of Identifier.Validate.
func ValidateIdentifier(id Identifier) error {
	return id.Validate()
}

// ValidateIdentifiers validates all identifiers in an ACME newOrder request.
func ValidateIdentifiers(ids []Identifier) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: no identifiers", ErrInvalidIPIdentifier)
	}
	for i, id := range ids {
		if err := id.Validate(); err != nil {
			return fmt.Errorf("identifier %d: %w", i, err)
		}
	}
	return nil
}

// ValidateChallenge checks that the identifier is valid and that method may be
// used to validate an IP identifier. dns-01 is never permitted.
func (id Identifier) ValidateChallenge(method string) error {
	if err := id.Validate(); err != nil {
		return err
	}
	switch method {
	case ChallengeHTTP01, ChallengeTLSALPN01:
		return nil
	case ChallengeDNS01:
		return fmt.Errorf("%w: %s cannot validate an IP address", ErrUnsupportedChallenge, method)
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedChallenge, method)
	}
}

// ValidateChallenge is the package-level form of Identifier.ValidateChallenge.
func ValidateChallenge(id Identifier, method string) error {
	return id.ValidateChallenge(method)
}

// SupportsChallenge reports whether method can validate an IP identifier.
func SupportsChallenge(method string) bool {
	return method == ChallengeHTTP01 || method == ChallengeTLSALPN01
}

// IPAddr returns the canonical, policy-validated netip.Addr represented by the
// identifier.
func (id Identifier) IPAddr() (netip.Addr, error) {
	if id.Type != IdentifierTypeIP {
		return netip.Addr{}, fmt.Errorf("%w: got %q", ErrIdentifierType, id.Type)
	}
	return ParseIPValue(id.Value)
}

// CanonicalIPAddr returns the identifier's address after only canonical-form
// checks. Unlike IPAddr, it does not enforce the public-address policy, so it
// is suitable for formatting helpers used with already-vetted identifiers.
func (id Identifier) CanonicalIPAddr() (netip.Addr, error) {
	if id.Type != IdentifierTypeIP {
		return netip.Addr{}, fmt.Errorf("%w: got %q", ErrIdentifierType, id.Type)
	}
	return parseCanonicalIP(id.Value)
}

// ParseIPValue parses and validates an RFC 8738 IP identifier value.
//
// The input must already be in canonical form: dotted decimal without leading
// zeros for IPv4, and the RFC 5952 compressed, lower-case form for IPv6. The
// address must be public and globally routable.
func ParseIPValue(value string) (netip.Addr, error) {
	addr, err := parseCanonicalIP(value)
	if err != nil {
		return netip.Addr{}, err
	}

	if addr.Is4() {
		if reason := disallowedIPv4Reason(addr); reason != "" {
			return netip.Addr{}, fmt.Errorf("%w: %s address %s", ErrDisallowedIPAddress, reason, addr)
		}
		return addr, nil
	}

	if reason := disallowedIPv6Reason(addr); reason != "" {
		return netip.Addr{}, fmt.Errorf("%w: %s address %s", ErrDisallowedIPAddress, reason, addr)
	}
	return addr, nil
}

func parseCanonicalIP(value string) (netip.Addr, error) {
	if value == "" {
		return netip.Addr{}, fmt.Errorf("%w: empty value", ErrInvalidIPIdentifier)
	}
	if value != strings.TrimSpace(value) {
		return netip.Addr{}, fmt.Errorf("%w: leading or trailing whitespace", ErrNonCanonicalIP)
	}
	if strings.Contains(value, "*") {
		return netip.Addr{}, fmt.Errorf("%w: %q", ErrWildcardIPIdentifier, value)
	}

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: %q: %v", ErrNonCanonicalIP, value, err)
	}
	if !addr.IsValid() {
		return netip.Addr{}, fmt.Errorf("%w: %q", ErrInvalidIPIdentifier, value)
	}
	if addr.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%w: IPv6 zone is not allowed", ErrNonCanonicalIP)
	}
	if addr.Is4In6() {
		return netip.Addr{}, fmt.Errorf("%w: IPv4-mapped address %s", ErrDisallowedIPAddress, addr)
	}

	// netip.String follows RFC 5952 for IPv6 and emits plain dotted decimal for
	// IPv4. Comparing the text catches uppercase hex, unnecessary zero groups,
	// long-form IPv6, and any other non-canonical spelling.
	if canonical := addr.String(); canonical != value {
		return netip.Addr{}, fmt.Errorf("%w: %q is not canonical (use %q)", ErrNonCanonicalIP, value, canonical)
	}
	return addr, nil
}

// ValidateIPValue is a convenience wrapper around ParseIPValue.
func ValidateIPValue(value string) error {
	_, err := ParseIPValue(value)
	return err
}

// ParseIPIdentifier is a convenience wrapper around ParseIPValue for callers
// that already know the identifier type is "ip".
func ParseIPIdentifier(value string) (netip.Addr, error) {
	return ParseIPValue(value)
}

// ReverseDNSName returns the IN-ADDR.ARPA or IP6.ARPA reverse mapping used as
// the SNI HostName for tls-alpn-01 IP validation.
func ReverseDNSName(addr netip.Addr) string {
	if addr.Is4() {
		a := addr.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", a[3], a[2], a[1], a[0])
	}
	b := addr.As16()
	labels := make([]string, 0, 32)
	for i := len(b) - 1; i >= 0; i-- {
		labels = append(labels, strconv.FormatUint(uint64(b[i]&0x0f), 16))
		labels = append(labels, strconv.FormatUint(uint64(b[i]>>4), 16))
	}
	return strings.Join(labels, ".") + ".ip6.arpa"
}

// ReverseDNSName returns the reverse mapping for the identifier.
func (id Identifier) ReverseDNSName() (string, error) {
	addr, err := id.CanonicalIPAddr()
	if err != nil {
		return "", err
	}
	return ReverseDNSName(addr), nil
}

// TLSALPN01ServerName returns the SNI value required for tls-alpn-01.
func (id Identifier) TLSALPN01ServerName() (string, error) {
	return id.ReverseDNSName()
}

type reservedRange struct {
	prefix netip.Prefix
	reason string
}

var disallowedIPv4Ranges = []reservedRange{
	{netip.MustParsePrefix("0.0.0.0/8"), "reserved"},
	{netip.MustParsePrefix("10.0.0.0/8"), "private"},
	{netip.MustParsePrefix("100.64.0.0/10"), "shared"},
	{netip.MustParsePrefix("127.0.0.0/8"), "loopback"},
	{netip.MustParsePrefix("169.254.0.0/16"), "link-local"},
	{netip.MustParsePrefix("172.16.0.0/12"), "private"},
	{netip.MustParsePrefix("192.0.0.0/24"), "reserved"},
	{netip.MustParsePrefix("192.0.2.0/24"), "documentation"},
	{netip.MustParsePrefix("192.88.99.0/24"), "reserved"},
	{netip.MustParsePrefix("192.168.0.0/16"), "private"},
	{netip.MustParsePrefix("198.18.0.0/15"), "benchmarking"},
	{netip.MustParsePrefix("198.51.100.0/24"), "documentation"},
	{netip.MustParsePrefix("203.0.113.0/24"), "documentation"},
	{netip.MustParsePrefix("224.0.0.0/4"), "multicast"},
	{netip.MustParsePrefix("240.0.0.0/4"), "reserved"},
}

var (
	ipv6GlobalUnicast = netip.MustParsePrefix("2000::/3")
	ipv6UniqueLocal   = netip.MustParsePrefix("fc00::/7")
	ipv6Mapped        = netip.MustParsePrefix("::ffff:0:0/96")

	disallowedIPv6Ranges = []reservedRange{
		{netip.MustParsePrefix("2001::/23"), "reserved"},
		{netip.MustParsePrefix("2001:db8::/32"), "documentation"},
		{netip.MustParsePrefix("2002::/16"), "6to4"},
		{netip.MustParsePrefix("3fff::/20"), "documentation"},
		{netip.MustParsePrefix("5f00::/16"), "reserved"},
	}
)

func disallowedIPv4Reason(addr netip.Addr) string {
	for _, r := range disallowedIPv4Ranges {
		if r.prefix.Contains(addr) {
			return r.reason
		}
	}
	return ""
}

func disallowedIPv6Reason(addr netip.Addr) string {
	switch {
	case addr.IsUnspecified():
		return "unspecified"
	case addr.IsLoopback():
		return "loopback"
	case addr.IsInterfaceLocalMulticast(), addr.IsLinkLocalMulticast(), addr.IsMulticast():
		return "multicast"
	case addr.IsLinkLocalUnicast():
		return "link-local"
	case ipv6UniqueLocal.Contains(addr):
		return "unique-local"
	case ipv6Mapped.Contains(addr):
		return "IPv4-mapped"
	}

	if !ipv6GlobalUnicast.Contains(addr) {
		return "reserved"
	}
	for _, r := range disallowedIPv6Ranges {
		if r.prefix.Contains(addr) {
			return r.reason
		}
	}
	return ""
}
