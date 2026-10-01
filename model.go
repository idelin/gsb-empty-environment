package main

import (
	"fmt"
	"net/netip"
)

// Action is the verdict a rule applies to a matching packet.
type Action int

const (
	Deny Action = iota
	Allow
)

func (a Action) String() string {
	if a == Allow {
		return "allow"
	}
	return "deny"
}

// ProtoAny matches any protocol number.
const ProtoAny = -1

// Rule matches on destination prefix, protocol and destination port range.
// Lower Priority values are evaluated first; ID breaks ties.
type Rule struct {
	ID       int
	Priority int
	Prefix   netip.Prefix
	Protocol int // ProtoAny or 0..255
	PortLo   int // destination port range, inclusive, 0..65535
	PortHi   int
	Action   Action
}

// Validate reports whether the rule fields are well formed.
func (r Rule) Validate() error {
	if !r.Prefix.IsValid() {
		return fmt.Errorf("rule #%d: invalid prefix", r.ID)
	}
	if r.Protocol != ProtoAny && (r.Protocol < 0 || r.Protocol > 255) {
		return fmt.Errorf("rule #%d: protocol %d out of range", r.ID, r.Protocol)
	}
	if r.PortLo < 0 || r.PortHi > 65535 || r.PortLo > r.PortHi {
		return fmt.Errorf("rule #%d: bad port range %d-%d", r.ID, r.PortLo, r.PortHi)
	}
	return nil
}

// Family returns "ipv4" or "ipv6". The two families are never mixed:
// a rule only ever matches packets of its own address family.
func (r Rule) Family() string {
	if r.Prefix.Addr().Is4() {
		return "ipv4"
	}
	return "ipv6"
}

func (r Rule) String() string {
	proto := "any"
	if r.Protocol != ProtoAny {
		proto = fmt.Sprintf("%d", r.Protocol)
	}
	return fmt.Sprintf("#%d prio=%d %s %s proto=%s dport=%d-%d => %s",
		r.ID, r.Priority, r.Family(), r.Prefix.Masked(), proto, r.PortLo, r.PortHi, r.Action)
}

// Matches reports whether the rule matches the packet header.
// IPv4 rules never match IPv6 packets and vice versa.
func (r Rule) Matches(p Packet) bool {
	if p.Dst.Is4() != r.Prefix.Addr().Is4() {
		return false
	}
	if !r.Prefix.Contains(p.Dst) {
		return false
	}
	if r.Protocol != ProtoAny && r.Protocol != p.Proto {
		return false
	}
	return p.DstPort >= r.PortLo && p.DstPort <= r.PortHi
}

// Packet is a synthetic packet header used inside the discrete-event
// model. It is never put on a real network.
type Packet struct {
	ID      int
	Dst     netip.Addr
	Proto   int
	DstPort int
}

func (p Packet) String() string {
	fam := "ipv4"
	if p.Dst.Is6() {
		fam = "ipv6"
	}
	return fmt.Sprintf("pkt#%d %s dst=%s proto=%d dport=%d", p.ID, fam, p.Dst, p.Proto, p.DstPort)
}

// Classifier evaluates rules in priority order; first match wins.
// The default verdict when nothing matches is Deny.
type Classifier struct {
	Rules []Rule
}

func NewClassifier(rules []Rule) Classifier {
	return Classifier{Rules: sortedRules(rules)}
}

// Decide returns the verdict and the matching rule, or Deny and nil.
func (c Classifier) Decide(p Packet) (Action, *Rule) {
	for i := range c.Rules {
		if c.Rules[i].Matches(p) {
			return c.Rules[i].Action, &c.Rules[i]
		}
	}
	return Deny, nil
}
