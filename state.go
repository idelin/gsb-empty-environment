package netsec

import "net/netip"

// State is the leak-protection state of the VPN connection.
type State string

const (
	StateConnecting         State = "connecting"
	StateUp                 State = "up"
	StateReconnecting       State = "reconnecting"
	StateSwitchingNetwork   State = "switching_network"
	StateDisconnectedByUser State = "disconnected_by_user"
)

func (s State) valid() bool {
	switch s {
	case StateConnecting, StateUp, StateReconnecting,
		StateSwitchingNetwork, StateDisconnectedByUser:
		return true
	default:
		return false
	}
}

// DNSMode tells the filter layer how DNS traffic must be handled.
type DNSMode string

const (
	// DNSDeny discards DNS traffic and forbids falling back to the system resolver.
	DNSDeny DNSMode = "deny"
	// DNSTunnel sends DNS traffic through the tunnel interface.
	DNSTunnel DNSMode = "tunnel"
	// DNSDirect permits direct DNS only after an explicit user disconnect.
	DNSDirect DNSMode = "direct"
)

// Rule permits traffic from one interface to a destination prefix. An invalid
// zero Destination means that the interface may reach any destination.
type Rule struct {
	Interface   string
	Destination netip.Prefix
}

// Ruleset is a complete, replacement-style view of the filter rules. Rules not
// listed here are denied.
type Ruleset struct {
	State      State
	Generation uint64
	Rules      []Rule
	DNS        DNSMode
	// FailClosed is true when the last replacement failed and the machine
	// deliberately replaced the intended rules with a deny-all ruleset.
	FailClosed bool
}

func (r Ruleset) clone() Ruleset {
	out := r
	out.Rules = append([]Rule(nil), r.Rules...)
	return out
}
