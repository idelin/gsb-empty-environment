// Package netsec provides MPLS label-stack admission for the forwarding
// plane's ingress classification stage.
//
// The admission rule is intentionally fail-closed for customer-facing
// interfaces: a labeled packet is accepted only when every label in its stack
// is an operator backbone label explicitly permitted by the caller. VPN
// (service) labels are never resolved from an untrusted interface. They may
// only be resolved to a VRF after arriving on an interface marked as trusted
// backbone.
package netsec

import (
	"fmt"
	"strings"
)

const (
	// MaxLabelValue is the largest value representable by the 20-bit MPLS
	// label field.
	MaxLabelValue uint32 = 1<<20 - 1

	// ReservedLabelMin and ReservedLabelMax delimit the IANA reserved label
	// range. Reserved labels are denied unless the interface explicitly
	// permits them.
	ReservedLabelMin uint32 = 0
	ReservedLabelMax uint32 = 15

	// ExplicitNullLabel is the IPv4 explicit-null label. It is reserved and
	// therefore denied by default.
	ExplicitNullLabel uint32 = 0

	// DefaultMaxStackDepth bounds an otherwise unconfigured label stack. Real
	// MPLS deployments use only a handful of labels; a small default keeps a
	// policy that forgot to set a limit from becoming unbounded.
	DefaultMaxStackDepth = 8
)

// Reason explains why an ingress label stack was admitted or dropped.
type Reason string

const (
	ReasonNone                  Reason = ""
	ReasonMalformedLabel        Reason = "label value is outside the 20-bit MPLS range"
	ReasonReservedLabelDenied   Reason = "reserved label is not permitted on this interface"
	ReasonExplicitNullNotBottom Reason = "explicit-null label is not at the bottom of the stack"
	ReasonVPNLabelUntrusted     Reason = "VPN label is not usable on an untrusted interface"
	ReasonUntrustedLabel        Reason = "label is not a permitted backbone label on an untrusted interface"
	ReasonBottomOfStackMismatch Reason = "bottom-of-stack bit does not match stack depth"
	ReasonStackTooDeep          Reason = "label stack exceeds maximum depth"
	ReasonTTLZero               Reason = "label TTL is zero"
	ReasonConflictingVPNLabels  Reason = "label stack resolves to conflicting VRFs"
	ReasonInvalidConfiguration  Reason = "admitter configuration is invalid"
)

// Label is one entry in an MPLS label stack, ordered from the outermost label
// towards the payload. TTL is the per-label TTL octet.
type Label struct {
	Value         uint32
	BottomOfStack bool
	TTL           uint8
}

// Interface describes the ingress interface supplied by the ingress
// classifier.
//
// Trusted marks a provider backbone (core) interface. BackboneLabels are the
// operator labels the caller explicitly permits on this interface; they are
// required for every non-reserved label received on an untrusted interface.
// AllowedReserved lists reserved labels (0 through 15) that this particular
// interface may accept.
type Interface struct {
	Name            string
	Trusted         bool
	BackboneLabels  []uint32
	AllowedReserved []uint32
}

// NewUntrustedInterface builds a customer-facing interface that permits only
// the supplied provider backbone labels on labeled packets.
func NewUntrustedInterface(name string, backboneLabels ...uint32) Interface {
	return Interface{
		Name:           name,
		BackboneLabels: append([]uint32(nil), backboneLabels...),
	}
}

// NewTrustedInterface builds a provider backbone interface. VPN labels
// arriving on it may be resolved to a VRF.
func NewTrustedInterface(name string) Interface {
	return Interface{Name: name, Trusted: true}
}

// AllowReserved returns a copy of the interface with the supplied reserved
// labels explicitly permitted. Labels outside 0 through 15 are ignored, since
// such values are not reserved labels and are handled by the normal admission
// rules.
func (i Interface) AllowReserved(labels ...uint32) Interface {
	out := i
	out.AllowedReserved = append([]uint32(nil), i.AllowedReserved...)
	for _, label := range labels {
		if label >= ReservedLabelMin && label <= ReservedLabelMax {
			out.AllowedReserved = append(out.AllowedReserved, label)
		}
	}
	return out
}

// Policy holds the forwarding-plane admission configuration.
//
// MaxDepth is the maximum number of labels in an accepted stack. A value less
// than or equal to zero selects DefaultMaxStackDepth. VPNLabels maps service
// labels to the VRF they select. ExplicitNullInterfaces names interfaces on
// which label 0 is explicitly allowed, in addition to the per-interface
// AllowedReserved list.
type Policy struct {
	MaxDepth               int
	VPNLabels              map[uint32]string
	ExplicitNullInterfaces []string
}

// Verdict is the ingress classifier's admission decision. VRF is set only
// when a VPN label was resolved on a trusted backbone interface.
type Verdict struct {
	Allowed bool
	Reason  Reason
	VRF     string
}

// Dropped reports whether the packet must be discarded.
func (v Verdict) Dropped() bool {
	return !v.Allowed
}

func (v Verdict) String() string {
	switch {
	case v.Allowed && v.VRF != "":
		return "allow vrf=" + v.VRF
	case v.Allowed:
		return "allow"
	case v.Reason == ReasonNone:
		return "drop"
	default:
		return "drop: " + string(v.Reason)
	}
}

// Admitter is an immutable, concurrency-safe snapshot of a Policy. Ingress
// classification should create one Admitter per configuration update and then
// call Admit for each packet.
type Admitter struct {
	maxDepth     int
	vpnLabels    map[uint32]string
	explicitNull map[string]struct{}
	configured   bool
}

// NewAdmitter snapshots the supplied policy. The policy's maps and interface
// name list are copied so later caller mutations cannot weaken admission rules
// while packets are in flight.
func NewAdmitter(policy Policy) *Admitter {
	a := &Admitter{
		maxDepth:     policy.MaxDepth,
		vpnLabels:    make(map[uint32]string, len(policy.VPNLabels)),
		explicitNull: make(map[string]struct{}, len(policy.ExplicitNullInterfaces)),
		configured:   true,
	}
	if a.maxDepth <= 0 {
		a.maxDepth = DefaultMaxStackDepth
	}
	for label, vrf := range policy.VPNLabels {
		a.vpnLabels[label] = vrf
	}
	for _, name := range policy.ExplicitNullInterfaces {
		if name != "" {
			a.explicitNull[name] = struct{}{}
		}
	}
	return a
}

// Admit applies label-stack admission to one packet. stack is ordered from the
// outermost label to the innermost label and is not modified.
//
// An empty stack is an unlabeled packet and is admitted without a VRF.
// Otherwise a packet is admitted only if:
//   - the stack is within the configured depth limit;
//   - every label has a valid 20-bit value and a non-zero TTL;
//   - exactly the final label has the bottom-of-stack bit set;
//   - reserved labels are explicitly permitted on the interface (label 0 must
//     additionally be the bottom label);
//   - on an untrusted interface, every non-reserved label is a caller-listed
//     backbone label and no label is a configured VPN label;
//   - on a trusted interface, any configured VPN labels in the stack resolve
//     consistently to one VRF.
func (a *Admitter) Admit(iface Interface, stack []Label) Verdict {
	if a == nil || !a.configured {
		return Verdict{Reason: ReasonInvalidConfiguration}
	}
	if len(stack) == 0 {
		return Verdict{Allowed: true}
	}
	if len(stack) > a.maxDepth {
		return Verdict{Reason: ReasonStackTooDeep}
	}

	last := len(stack) - 1
	for index, label := range stack {
		if label.Value > MaxLabelValue {
			return Verdict{Reason: ReasonMalformedLabel}
		}
		if label.TTL == 0 {
			return Verdict{Reason: ReasonTTLZero}
		}
		if label.BottomOfStack != (index == last) {
			return Verdict{Reason: ReasonBottomOfStackMismatch}
		}
		if isReservedLabel(label.Value) && !a.reservedAllowed(iface, label.Value) {
			return Verdict{Reason: ReasonReservedLabelDenied}
		}
		if label.Value == ExplicitNullLabel && index != last {
			return Verdict{Reason: ReasonExplicitNullNotBottom}
		}
	}

	if !iface.Trusted {
		return a.admitUntrusted(iface, stack)
	}
	return a.admitTrusted(stack)
}

func (a *Admitter) admitUntrusted(iface Interface, stack []Label) Verdict {
	backbone := make(map[uint32]struct{}, len(iface.BackboneLabels))
	for _, label := range iface.BackboneLabels {
		if label <= MaxLabelValue {
			backbone[label] = struct{}{}
		}
	}

	// VPN labels take precedence over any caller-supplied backbone allow-list:
	// a service label must never be accepted from a customer-facing interface,
	// even if it was accidentally listed as a backbone label.
	for _, label := range stack {
		if isReservedLabel(label.Value) {
			continue
		}
		if _, ok := a.vpnLabels[label.Value]; ok {
			return Verdict{Reason: ReasonVPNLabelUntrusted}
		}
	}
	for _, label := range stack {
		if isReservedLabel(label.Value) {
			continue
		}
		if _, ok := backbone[label.Value]; !ok {
			return Verdict{Reason: ReasonUntrustedLabel}
		}
	}
	return Verdict{Allowed: true}
}

func (a *Admitter) admitTrusted(stack []Label) Verdict {
	vrf := ""
	resolved := false
	for _, label := range stack {
		if isReservedLabel(label.Value) {
			continue
		}
		candidate, ok := a.vpnLabels[label.Value]
		if !ok {
			continue
		}
		if resolved && candidate != vrf {
			return Verdict{Reason: ReasonConflictingVPNLabels}
		}
		vrf = candidate
		resolved = true
	}
	return Verdict{Allowed: true, VRF: vrf}
}

func (a *Admitter) reservedAllowed(iface Interface, label uint32) bool {
	if !isReservedLabel(label) {
		return false
	}
	for _, allowed := range iface.AllowedReserved {
		if allowed == label {
			return true
		}
	}
	if label == ExplicitNullLabel {
		_, ok := a.explicitNull[iface.Name]
		return ok
	}
	return false
}

func isReservedLabel(label uint32) bool {
	return label >= ReservedLabelMin && label <= ReservedLabelMax
}

// Admit is a convenience wrapper for callers that do not cache an Admitter.
// The policy is snapshotted for the duration of this call.
func Admit(iface Interface, stack []Label, policy Policy) Verdict {
	return NewAdmitter(policy).Admit(iface, stack)
}

// CheckIngress is the entry point used by ingress classification.
func CheckIngress(iface Interface, stack []Label, policy Policy) Verdict {
	return Admit(iface, stack, policy)
}

// ValidateLabelStack is an alias for callers that validate a stack before
// handing it to the forwarding path.
func ValidateLabelStack(iface Interface, stack []Label, policy Policy) Verdict {
	return Admit(iface, stack, policy)
}

// ParseBackboneLabels parses a comma-separated caller configuration string
// such as "100,200,0x1234" into backbone label values. It is a small
// convenience for control-plane adapters; admission itself always uses the
// numeric values in Interface.BackboneLabels.
func ParseBackboneLabels(value string) ([]uint32, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	labels := make([]uint32, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty backbone label")
		}
		var label uint64
		if _, err := fmt.Sscan(part, &label); err != nil {
			return nil, fmt.Errorf("invalid backbone label %q: %w", part, err)
		}
		if label > uint64(MaxLabelValue) {
			return nil, fmt.Errorf("backbone label %d exceeds 20-bit range", label)
		}
		labels = append(labels, uint32(label))
	}
	return labels, nil
}
