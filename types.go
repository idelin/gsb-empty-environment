package netsec

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
)

// Action is the decision attached to a rule or an emitted shard.
// The zero value is not a valid action.
type Action uint8

const (
	// ActionDeny rejects matching traffic.
	ActionDeny Action = 1
	// ActionAllow permits matching traffic.
	ActionAllow Action = 2
)

// String renders an action for logs and source explanations.
func (a Action) String() string {
	switch a {
	case ActionDeny:
		return "deny"
	case ActionAllow:
		return "allow"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(a))
	}
}

// IPRange is an inclusive IPv4 interval. From and To are the 32-bit numeric
// form obtained from ParseIPv4; From must be less than or equal to To.
type IPRange struct {
	From uint32
	To   uint32
}

// Contains reports whether v falls inside the inclusive interval.
func (r IPRange) Contains(v uint32) bool {
	return r.From <= v && v <= r.To
}

// PortRange is an inclusive 16-bit port interval.
// From must be less than or equal to To.
type PortRange struct {
	From uint16
	To   uint16
}

// Contains reports whether v falls inside the inclusive interval.
func (r PortRange) Contains(v uint16) bool {
	return r.From <= v && v <= r.To
}

// Rule is one ordered first-match firewall rule.
// A packet matches when every dimension is covered: both IPv4 intervals,
// both port intervals and the protocol set.
type Rule struct {
	// ID is an optional caller-supplied identifier copied onto shards.
	ID string
	// SrcIP and DstIP are inclusive source and destination IPv4 intervals.
	SrcIP IPRange
	DstIP IPRange
	// SrcPort and DstPort are inclusive source and destination port intervals.
	SrcPort PortRange
	DstPort PortRange
	// Protocols is the set of matched IP protocol numbers; it must be non-empty.
	Protocols []uint8
	// Action is ActionAllow or ActionDeny.
	Action Action
	// Comment is optional provenance text copied onto emitted shards.
	Comment string
}

// Shard is one disjoint published hardware entry produced by Compress.
// All shards in a Plan are pairwise disjoint, so evaluating them in order
// reproduces the original ordered first-match semantics.
type Shard struct {
	SrcIP     IPRange
	DstIP     IPRange
	SrcPort   PortRange
	DstPort   PortRange
	Protocols []uint8
	Action    Action
	// SourceIndex is the index of the originating Rule, or -1 when the shard
	// covers traffic that no input rule matched (the implicit default deny).
	SourceIndex int
	// SourceID copies Rule.ID; it is "default-deny" for implicit deny shards.
	SourceID string
	// Explanation is a human-readable provenance string.
	Explanation string
}

// Options controls compilation.
type Options struct {
	// MaxEntries is the hardware entry budget. Compress fails with
	// ErrBudgetExceeded when more shards than this would be published.
	// A zero value means an unlimited budget.
	MaxEntries int
	// IncludeDefaultDeny emits explicit deny shards for the regions that no
	// input rule covers. They are always taken into account by Verify and
	// keep first-match semantics whether or not they are materialized.
	IncludeDefaultDeny bool
}

// Plan is the compiled, decision-equivalent shard set.
type Plan struct {
	Shards []Shard
	// RuleCount is the number of input rules.
	RuleCount int
}

// Errors returned by the package. Callers can use errors.Is directly.
var (
	// ErrInvalidRule marks a malformed input rule.
	ErrInvalidRule = errors.New("netsec: invalid rule")
	// ErrBudgetExceeded means the shard count exceeds the entry budget.
	ErrBudgetExceeded = errors.New("netsec: entry budget exceeded")
	// ErrVerificationFailed means lattice verification found a mismatch.
	ErrVerificationFailed = errors.New("netsec: decision verification failed")
)

// ParseIPv4 converts a dotted-quad IPv4 address to its 32-bit numeric form.
func ParseIPv4(s string) (uint32, error) {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return 0, fmt.Errorf("netsec: parse IPv4 %q: %w", s, err)
	}
	if !addr.Is4() {
		return 0, fmt.Errorf("netsec: parse IPv4 %q: not an IPv4 address", s)
	}
	return ipv4ToUint32(addr), nil
}

// IPv4String renders the 32-bit numeric form as a dotted-quad address.
func IPv4String(v uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func ipv4ToUint32(addr netip.Addr) uint32 {
	b := addr.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// validateRule checks structural constraints and normalizes the protocol set.
func validateRule(index int, rule Rule) (Rule, error) {
	switch rule.Action {
	case ActionAllow, ActionDeny:
	default:
		return rule, fmt.Errorf("%w: rule %d: unknown action %d", ErrInvalidRule, index, rule.Action)
	}
	if rule.SrcIP.From > rule.SrcIP.To {
		return rule, fmt.Errorf("%w: rule %d: inverted source IP interval", ErrInvalidRule, index)
	}
	if rule.DstIP.From > rule.DstIP.To {
		return rule, fmt.Errorf("%w: rule %d: inverted destination IP interval", ErrInvalidRule, index)
	}
	if rule.SrcPort.From > rule.SrcPort.To {
		return rule, fmt.Errorf("%w: rule %d: inverted source port interval", ErrInvalidRule, index)
	}
	if rule.DstPort.From > rule.DstPort.To {
		return rule, fmt.Errorf("%w: rule %d: inverted destination port interval", ErrInvalidRule, index)
	}
	if len(rule.Protocols) == 0 {
		return rule, fmt.Errorf("%w: rule %d: empty protocol set", ErrInvalidRule, index)
	}

	seen := make(map[uint8]struct{}, len(rule.Protocols))
	protos := make([]uint8, 0, len(rule.Protocols))
	for _, p := range rule.Protocols {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		protos = append(protos, p)
	}
	sort.Slice(protos, func(i, j int) bool { return protos[i] < protos[j] })
	rule.Protocols = protos
	return rule, nil
}

// ruleMatches reports whether a concrete packet lies inside the rule condition.
func ruleMatches(rule Rule, srcIP, dstIP uint32, srcPort, dstPort uint16, proto uint8) bool {
	if !rule.SrcIP.Contains(srcIP) || !rule.DstIP.Contains(dstIP) {
		return false
	}
	if !rule.SrcPort.Contains(srcPort) || !rule.DstPort.Contains(dstPort) {
		return false
	}
	for _, p := range rule.Protocols {
		if p == proto {
			return true
		}
	}
	return false
}

// Decide evaluates the ordered rules against one concrete packet using
// first-match semantics. Traffic that no rule matches is denied by default.
func Decide(rules []Rule, srcIP, dstIP uint32, srcPort, dstPort uint16, proto uint8) Action {
	for _, rule := range rules {
		if ruleMatches(rule, srcIP, dstIP, srcPort, dstPort, proto) {
			return rule.Action
		}
	}
	return ActionDeny
}
