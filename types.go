package netsec

import (
	"errors"
	"fmt"
	"net/netip"
)

var (
	// ErrBlocked is returned when a destination is not a globally reachable
	// public unicast address.
	ErrBlocked = errors.New("netsec: destination is not public")

	// ErrInvalidAddress is returned when an address or endpoint cannot be
	// parsed.
	ErrInvalidAddress = errors.New("netsec: invalid address")

	// ErrUnsupportedScheme is returned for URLs that use a scheme other than
	// http or https.
	ErrUnsupportedScheme = errors.New("netsec: unsupported URL scheme")
)

// Family is the effective address family of the destination that will
// actually be reached. A transition address, such as a 6to4 address, has the
// family of its embedded target.
type Family string

const (
	FamilyUnknown Family = ""
	FamilyIPv4    Family = "ipv4"
	FamilyIPv6    Family = "ipv6"
)

// Category describes the range that contains the effective destination.
type Category string

const (
	CategoryUnknown       Category = ""
	CategoryPublic        Category = "public"
	CategoryUnspecified   Category = "unspecified"
	CategoryLoopback      Category = "loopback"
	CategoryLinkLocal     Category = "link-local"
	CategoryPrivate       Category = "private"
	CategoryUniqueLocal   Category = "unique-local"
	CategorySiteLocal     Category = "site-local"
	CategoryShared        Category = "shared"
	CategoryMulticast     Category = "multicast"
	CategoryDocumentation Category = "documentation"
	CategoryBenchmark     Category = "benchmark"
	CategoryDiscard       Category = "discard"
	CategoryReserved      Category = "reserved"
)

// Short aliases for callers that prefer the range name without the Category
// prefix.
const (
	Public        = CategoryPublic
	Unspecified   = CategoryUnspecified
	Loopback      = CategoryLoopback
	LinkLocal     = CategoryLinkLocal
	Private       = CategoryPrivate
	UniqueLocal   = CategoryUniqueLocal
	SiteLocal     = CategorySiteLocal
	Shared        = CategoryShared
	Multicast     = CategoryMulticast
	Documentation = CategoryDocumentation
	Benchmark     = CategoryBenchmark
	Discard       = CategoryDiscard
	Reserved      = CategoryReserved
)

// Scope is an alias for Category.
type Scope = Category

const (
	ScopePublic        = CategoryPublic
	ScopeUnspecified   = CategoryUnspecified
	ScopeLoopback      = CategoryLoopback
	ScopeLinkLocal     = CategoryLinkLocal
	ScopePrivate       = CategoryPrivate
	ScopeUniqueLocal   = CategoryUniqueLocal
	ScopeSiteLocal     = CategorySiteLocal
	ScopeShared        = CategoryShared
	ScopeMulticast     = CategoryMulticast
	ScopeDocumentation = CategoryDocumentation
	ScopeBenchmark     = CategoryBenchmark
	ScopeDiscard       = CategoryDiscard
	ScopeReserved      = CategoryReserved
)

// Mechanism identifies an address translation or transition form that was
// unwrapped before classification.
type Mechanism string

const (
	MechanismNone           Mechanism = ""
	MechanismIPv4Mapped     Mechanism = "ipv4-mapped"
	MechanismIPv4Compatible Mechanism = "ipv4-compatible"
	MechanismIPv4Translated Mechanism = "ipv4-translated"
	Mechanism6to4           Mechanism = "6to4"
	MechanismTeredo         Mechanism = "teredo"
	MechanismNAT64          Mechanism = "nat64"
	MechanismISATAP         Mechanism = "isatap"
)

// Result is the normalized classification of one address.
type Result struct {
	// Input is the original text supplied by the caller.
	Input string

	// Source is the parsed address before transition addresses are unwrapped.
	// Its zone is removed; Zone contains the original zone.
	Source netip.Addr

	// Address is the primary effective destination after IPv4-mapped,
	// IPv4-compatible, 6to4, Teredo, NAT64, and ISATAP forms are reduced to
	// the address family that will actually be reached.
	Address netip.Addr

	// Normalized is the same value as Address. It is provided under the
	// terminology used by outbound filters that compare the effective
	// destination against ranges.
	Normalized netip.Addr

	// DialAddr is the address that should be passed to the network stack.
	// It differs from Address for transition forms that are actually dialed
	// as IPv6 (6to4, Teredo, NAT64, IPv4-translated, and ISATAP), while
	// Address is the effective target used for range classification.
	DialAddr netip.Addr

	Family       Family
	SourceFamily Family
	Category     Category
	Scope        Category
	Mechanism    Mechanism

	// Targets contains every effective endpoint represented by the input.
	// Teredo addresses expose both the client and the server.
	Targets []netip.Addr

	Zone   string
	Public bool

	// Internal is the inverse of Public. Invalid addresses are treated as
	// internal (fail closed).
	Internal bool

	Reason string
}

// Classification is an alias for Result.
type Classification = Result

// Allowed reports whether the destination is globally reachable public
// unicast.
func (r Result) Allowed() bool {
	return r.Public
}

// Err returns a BlockedError when the result is not public.
func (r Result) Err() error {
	if r.Public {
		return nil
	}
	return &BlockedError{Input: r.Input, Result: r}
}

// IsPrivate reports whether the effective destination is an IPv4 private-use
// address, IPv6 unique-local address, or deprecated site-local address.
func (r Result) IsPrivate() bool {
	return r.Category == CategoryPrivate || r.Category == CategoryUniqueLocal || r.Category == CategorySiteLocal
}

func (r Result) IsUnspecified() bool { return r.Category == CategoryUnspecified }
func (r Result) IsLoopback() bool    { return r.Category == CategoryLoopback }
func (r Result) IsLinkLocal() bool   { return r.Category == CategoryLinkLocal }
func (r Result) IsMulticast() bool   { return r.Category == CategoryMulticast }
func (r Result) IsReserved() bool    { return r.Category == CategoryReserved }
func (r Result) IsShared() bool      { return r.Category == CategoryShared }
func (r Result) IsDocumentation() bool {
	return r.Category == CategoryDocumentation
}

func (r Result) String() string {
	if r.Address.IsValid() {
		return fmt.Sprintf("%s (%s, %s)", r.Address, r.Family, r.Category)
	}
	return fmt.Sprintf("%s (%s)", r.Input, r.Category)
}

// BlockedError describes a destination that was rejected. It wraps
// ErrBlocked, so errors.Is(err, ErrBlocked) can be used.
type BlockedError struct {
	Input   string
	Result  Result
	Results []Result
}

func (e *BlockedError) Error() string {
	if e == nil {
		return ErrBlocked.Error()
	}

	name := e.Input
	if name == "" {
		name = e.Result.Input
	}
	if e.Result.Address.IsValid() {
		if e.Result.Reason != "" {
			return fmt.Sprintf("%s: %s resolves to %s (%s): %s",
				ErrBlocked, name, e.Result.Address, e.Result.Category, e.Result.Reason)
		}
		return fmt.Sprintf("%s: %s resolves to %s (%s)",
			ErrBlocked, name, e.Result.Address, e.Result.Category)
	}
	if e.Result.Reason != "" {
		return fmt.Sprintf("%s: %s: %s", ErrBlocked, name, e.Result.Reason)
	}
	return fmt.Sprintf("%s: %s", ErrBlocked, name)
}

func (e *BlockedError) Unwrap() error {
	return ErrBlocked
}

func (c Category) IsPublic() bool {
	return c == CategoryPublic
}

func (c Category) IsInternal() bool {
	return c != CategoryPublic
}

func (c Category) String() string {
	if c == CategoryUnknown {
		return "unknown"
	}
	return string(c)
}

func familyOf(addr netip.Addr) Family {
	switch {
	case !addr.IsValid():
		return FamilyUnknown
	case addr.Is4():
		return FamilyIPv4
	case addr.Is6():
		return FamilyIPv6
	default:
		return FamilyUnknown
	}
}

func severity(c Category) int {
	switch c {
	case CategoryPublic:
		return 0
	case CategoryDocumentation:
		return 1
	case CategoryBenchmark:
		return 2
	case CategoryReserved:
		return 3
	case CategoryShared:
		return 4
	case CategoryPrivate, CategoryUniqueLocal, CategorySiteLocal:
		return 5
	case CategoryLinkLocal:
		return 6
	case CategoryMulticast:
		return 7
	case CategoryLoopback:
		return 8
	case CategoryUnspecified:
		return 9
	default:
		return 10
	}
}

func worseCategory(a, b Category) Category {
	if a == CategoryUnknown {
		return b
	}
	if b == CategoryUnknown {
		return a
	}
	if severity(b) > severity(a) {
		return b
	}
	return a
}

// effectiveTarget is an address reached through the parsed address, before
// range classification. The label is used in human-readable reasons.
type effectiveTarget struct {
	addr  netip.Addr
	label string
}
