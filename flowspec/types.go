package flowspec

import (
	"fmt"
	"net"
)

// AFI is the BGP Address Family Identifier used to interpret prefixes.
type AFI uint16

const (
	AFIPv4 AFI = 1 // RFC 8955
	AFIPv6 AFI = 2 // RFC 8956
)

// maxPrefixBits returns the prefix bit width for the address family.
func (a AFI) maxPrefixBits() int {
	switch a {
	case AFIPv6:
		return 128
	default:
		return 32
	}
}

// ComponentType identifies a FlowSpec NLRI component (RFC 8955 §4).
type ComponentType uint8

const (
	TypeDestPrefix   ComponentType = 1
	TypeSourcePrefix ComponentType = 2
	TypeProtocol     ComponentType = 3
	TypePort         ComponentType = 4
	TypeDestPort     ComponentType = 5
	TypeSourcePort   ComponentType = 6
	TypeICMPType     ComponentType = 7
	TypeICMPCode     ComponentType = 8
	TypeTCPFlags     ComponentType = 9
	TypePacketLength ComponentType = 10
	TypeDSCP         ComponentType = 11
	TypeFragment     ComponentType = 12
)

// String returns the canonical FlowSpec component name.
func (t ComponentType) String() string {
	switch t {
	case TypeDestPrefix:
		return "destination-prefix"
	case TypeSourcePrefix:
		return "source-prefix"
	case TypeProtocol:
		return "ip-protocol"
	case TypePort:
		return "port"
	case TypeDestPort:
		return "destination-port"
	case TypeSourcePort:
		return "source-port"
	case TypeICMPType:
		return "icmp-type"
	case TypeICMPCode:
		return "icmp-code"
	case TypeTCPFlags:
		return "tcp-flags"
	case TypePacketLength:
		return "packet-length"
	case TypeDSCP:
		return "dscp"
	case TypeFragment:
		return "fragment"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(t))
	}
}

// IsPrefix reports whether t is a variable-length prefix component (1 or 2).
func (t ComponentType) IsPrefix() bool { return t == TypeDestPrefix || t == TypeSourcePrefix }

// IsBitmask reports whether t carries bit-mask semantics (TCP flags/fragment).
func (t ComponentType) IsBitmask() bool { return t == TypeTCPFlags || t == TypeFragment }

// Operator byte bits (RFC 8955 §4.2). The low two bits encode value width as
// 1 << (op & LenMask), i.e. 0 -> 1, 1 -> 2, 2 -> 4 octets; code 3 is reserved.
const (
	OpEndOfList uint8 = 1 << 7 // E: final operator/value pair of the component
	OpAnd       uint8 = 1 << 6 // A: AND with the following term (otherwise OR)
	OpNot       uint8 = 1 << 5 // N: negate the comparison
	OpMatch     uint8 = 1 << 4 // m/=: equality (numeric) or mask-match (bitmask)
	OpGreater   uint8 = 1 << 3 // >: greater-than relation
	OpLess      uint8 = 1 << 2 // <: less-than relation
	OpLenMask   uint8 = 0x03   // low two bits: width code
)

// RelationKind is the decoded comparison relation of a term.
type RelationKind uint8

const (
	CmpNone RelationKind = iota
	CmpEqual
	CmpNotEqual
	CmpGT
	CmpGTE
	CmpLT
	CmpLTE
	CmpOther
)

// Term is one operator/value pair inside a type 3..12 component.
type Term struct {
	// Raw is the exact operator byte decoded from the wire. It is zero for
	// terms constructed in-process; OperatorByte recomputes the wire byte.
	Raw uint8

	// Width is the encoded value width in octets: 1, 2 or 4.
	Width int
	Value uint64

	End     bool
	And     bool
	Not     bool
	Match   bool
	Greater bool
	Less    bool
}

// WidthCode returns the two-bit width code for t.Width.
func (t Term) WidthCode() uint8 {
	switch t.Width {
	case 2:
		return 1
	case 4:
		return 2
	default:
		return 0
	}
}

// OperatorByte recomputes the wire operator byte. end forces the E bit because
// the encoder, not the caller, knows which term is last.
func (t Term) OperatorByte(end bool) uint8 {
	var b uint8 = t.WidthCode()
	if end {
		b |= OpEndOfList
	}
	if t.And {
		b |= OpAnd
	}
	if t.Not {
		b |= OpNot
	}
	if t.Match {
		b |= OpMatch
	}
	if t.Greater {
		b |= OpGreater
	}
	if t.Less {
		b |= OpLess
	}
	return b
}

// Relation decodes the common comparison relation represented by the bits.
func (t Term) Relation() RelationKind {
	switch {
	case t.Match && !t.Greater && !t.Less:
		if t.Not {
			return CmpNotEqual
		}
		return CmpEqual
	case t.Greater && t.Match && !t.Less && !t.Not:
		return CmpGTE
	case t.Greater && !t.Match && !t.Less && !t.Not:
		return CmpGT
	case t.Less && t.Match && !t.Greater && !t.Not:
		return CmpLTE
	case t.Less && !t.Match && !t.Greater && !t.Not:
		return CmpLT
	default:
		return CmpOther
	}
}

// MatchesNumeric reports whether an ordered/numeric field value satisfies t.
// The three relation bits are independent predicates combined by OR before the
// NOT bit is applied, matching the RFC operator semantics.
func (t Term) MatchesNumeric(v uint64) bool {
	matched := false
	if t.Match && v == t.Value {
		matched = true
	}
	if t.Less && v < t.Value {
		matched = true
	}
	if t.Greater && v > t.Value {
		matched = true
	}
	if t.Not {
		return !matched
	}
	return matched
}

// MatchesBitmask reports whether a bit-mask field satisfies t. With Match set
// every bit in t.Value must be present ((v & m) == m, "match all"); otherwise
// any masked bit suffices ((v & m) != 0). NOT inverts the result.
func (t Term) MatchesBitmask(v uint64) bool {
	mask := t.Value
	var matched bool
	if t.Match {
		matched = (v & mask) == mask
	} else {
		matched = (v & mask) != 0
	}
	if t.Not {
		return !matched
	}
	return matched
}

// Component is one decoded FlowSpec NLRI component. Prefix is populated for
// types 1/2; Terms for types 3..12. Raw holds the exact wire bytes (including
// the type octet) when the component came from DecodeComponents.
type Component struct {
	Type   ComponentType
	Prefix net.IPNet
	Terms  []Term
	Raw    []byte
}

// PrefixComponent builds a type 1/2 component.
func PrefixComponent(t ComponentType, prefix net.IPNet) Component {
	return Component{Type: t, Prefix: prefix}
}

// TermsComponent builds a type 3..12 component from operator terms.
func TermsComponent(t ComponentType, terms ...Term) Component {
	return Component{Type: t, Terms: terms}
}

// EqualTerm is a convenience equality term of minimal width for value.
func EqualTerm(value uint64) Term {
	return Term{Match: true, Value: value, Width: MinWidth(value)}
}

// MinWidth returns the smallest RFC value width (1, 2 or 4 octets) for v.
func MinWidth(v uint64) int {
	switch {
	case v <= 0xff:
		return 1
	case v <= 0xffff:
		return 2
	default:
		return 4
	}
}

// Verdict is the forwarding disposition implied by a rule's actions.
type Verdict uint8

const (
	VerdictPermit Verdict = iota
	VerdictDrop
	VerdictRateLimit
	VerdictRedirect
)

// String returns the verdict name.
func (v Verdict) String() string {
	switch v {
	case VerdictPermit:
		return "PERMIT"
	case VerdictDrop:
		return "DROP"
	case VerdictRateLimit:
		return "RATE-LIMIT"
	case VerdictRedirect:
		return "REDIRECT"
	default:
		return "UNKNOWN"
	}
}

// TrafficRate is the decoded traffic-rate extended community (0x8006).
// Rate is in bytes/second as an IEEE-754 single precision value; Rate == 0 is
// the canonical discard/blackhole action.
type TrafficRate struct {
	AS       uint16
	Rate     float32
	RateBits uint32
}

// IsDrop reports whether the rate is zero (discard/blackhole).
func (r *TrafficRate) IsDrop() bool {
	return r != nil && r.RateBits == 0
}

// TrafficAction is the decoded traffic-action extended community (0x8007).
type TrafficAction struct {
	Terminal bool
	Sample   bool
	Flags    byte
}

// Redirect is the VRF/route-target style redirect community (0x8008).
type Redirect struct {
	// Target holds the raw six-octet redirect value.
	Target [6]byte
}

// RedirectToIP is the redirect-to-IP action carried by an IPv4-address
// specific extended community.
type RedirectToIP struct {
	IP   net.IP
	Copy bool
	// Flags retains the raw trailing flag octet.
	Flags byte
}

// ActionSet groups the FlowSpec action extended communities attached to a rule.
type ActionSet struct {
	TrafficRate   *TrafficRate
	TrafficAction *TrafficAction
	Redirect      *Redirect
	RedirectToIP  *RedirectToIP

	// Permit marks an explicit host-level allow statement. Wire FlowSpec has
	// no permit community; absence of a discard/redirect action is a permit.
	Permit bool

	// Communities retains every decoded extended community in wire order.
	Communities []ExtendedCommunity
}

// Verdict resolves the actionable disposition. A zero traffic-rate (drop) and
// a redirect take precedence over rate limiting.
func (a *ActionSet) Verdict() Verdict {
	if a == nil {
		return VerdictPermit
	}
	switch {
	case a.TrafficRate.IsDrop():
		return VerdictDrop
	case a.RedirectToIP != nil || a.Redirect != nil:
		return VerdictRedirect
	case a.TrafficRate != nil:
		return VerdictRateLimit
	default:
		return VerdictPermit
	}
}

// Rule is a single FlowSpec route: an ordered component set plus actions and
// administrative identity used by ROA admission / tenant conflict analysis.
type Rule struct {
	// ID is an optional stable identifier (e.g. tenant rule UUID). When zero
	// the auditor assigns R[index].
	ID string

	AF         AFI
	Components []Component
	Actions    ActionSet

	// OriginAS is the AS that announced/owns the rule. It is administrative
	// context supplied by the host (BGP path AS_PATH origin), not NLRI bytes.
	OriginAS uint32
	// Tenant optionally identifies the owning tenant for hijack reporting.
	Tenant string
}

// Component returns the component of type t or nil.
func (r *Rule) Component(t ComponentType) *Component {
	for i := range r.Components {
		if r.Components[i].Type == t {
			return &r.Components[i]
		}
	}
	return nil
}

// DestPrefix returns the type-1 destination prefix when present.
func (r *Rule) DestPrefix() (net.IPNet, bool) {
	if c := r.Component(TypeDestPrefix); c != nil {
		return c.Prefix, true
	}
	return net.IPNet{}, false
}

// SourcePrefix returns the type-2 source prefix when present.
func (r *Rule) SourcePrefix() (net.IPNet, bool) {
	if c := r.Component(TypeSourcePrefix); c != nil {
		return c.Prefix, true
	}
	return net.IPNet{}, false
}

// label returns ID or a fallback built from index.
func (r *Rule) label(index int) string {
	if r != nil && r.ID != "" {
		return r.ID
	}
	return fmt.Sprintf("R%d", index)
}
