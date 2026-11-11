package vlan

import (
	"errors"
	"fmt"
)

// PortMode decides which ingress wire shapes are accepted and how the
// canonical tenant stack is mapped back onto the egress wire.
type PortMode int

const (
	// ModeAccess: edge port. Accepts untagged and priority-tagged frames,
	// assigns AccessVID on ingress; emits untagged (or priority-tagged when
	// the frame carried PCP) on egress.
	ModeAccess PortMode = iota
	// ModeTrunk: inter-switch port. Accepts single/double-tagged frames
	// whose outer VID is allowed (or the native VID). Native-VLAN frames are
	// accepted untagged and emitted untagged.
	ModeTrunk
	// ModeTunnel: QinQ edge. Accepts customer frames (untagged, priority,
	// single C-TAG) and wraps them in the service tag ServiceVID; double
	// tags are rejected to keep the stack bounded at two.
	ModeTunnel
)

func (m PortMode) String() string {
	switch m {
	case ModeAccess:
		return "access"
	case ModeTrunk:
		return "trunk"
	case ModeTunnel:
		return "tunnel"
	default:
		return "unknown"
	}
}

const (
	minVID = 1
	maxVID = 4095
)

// PortPolicy is the declarative configuration of one switch port.
type PortPolicy struct {
	Name      string
	Mode      PortMode
	AccessVID uint16   // access: assigned VID; tunnel: outer service VID
	NativeVID uint16   // trunk: VID carried untagged (0 = none)
	Allowed   []uint16 // trunk: permitted outer VIDs (native always allowed)
}

// Policy is an immutable switch-wide configuration snapshot. Swaps install a
// whole snapshot atomically, so every frame is processed under one policy.
type Policy struct {
	ports map[string]*PortPolicy
}

// NewPolicy validates and freezes the given port configurations. Validation is
// strict: an invalid policy never reaches the datapath.
func NewPolicy(ports []PortPolicy) (*Policy, error) {
	p := &Policy{ports: make(map[string]*PortPolicy, len(ports))}
	seen := make(map[string]bool, len(ports))
	for _, port := range ports {
		if port.Name == "" {
			return nil, errors.New("vlan: port with empty name")
		}
		if seen[port.Name] {
			return nil, fmt.Errorf("vlan: duplicate port %q", port.Name)
		}
		seen[port.Name] = true
		if err := validatePort(&port); err != nil {
			return nil, fmt.Errorf("vlan: port %q: %w", port.Name, err)
		}
		cp := port
		cp.Allowed = append([]uint16(nil), port.Allowed...)
		p.ports[port.Name] = &cp
	}
	return p, nil
}

func validatePort(port *PortPolicy) error {
	switch port.Mode {
	case ModeAccess:
		if !validVID(port.AccessVID) {
			return fmt.Errorf("access VID %d out of range 1..4095", port.AccessVID)
		}
	case ModeTunnel:
		if !validVID(port.AccessVID) {
			return fmt.Errorf("service VID %d out of range 1..4095", port.AccessVID)
		}
	case ModeTrunk:
		if port.NativeVID != 0 && !validVID(port.NativeVID) {
			return fmt.Errorf("native VID %d out of range 1..4095", port.NativeVID)
		}
		allowed := make(map[uint16]bool, len(port.Allowed)+1)
		for _, vid := range port.Allowed {
			if !validVID(vid) {
				return fmt.Errorf("allowed VID %d out of range 1..4095", vid)
			}
			if allowed[vid] {
				return fmt.Errorf("duplicate allowed VID %d", vid)
			}
			allowed[vid] = true
		}
		if port.NativeVID != 0 && allowed[port.NativeVID] {
			return fmt.Errorf("native VID %d must not also be an allowed tagged VID", port.NativeVID)
		}
	default:
		return fmt.Errorf("unknown port mode %d", port.Mode)
	}
	return nil
}

func validVID(vid uint16) bool { return vid >= minVID && vid <= maxVID }

func (p *Policy) port(name string) (*PortPolicy, bool) {
	port, ok := p.ports[name]
	return port, ok
}

// Tenant identifies the membership derived from a frame's tag stack.
type Tenant struct {
	OuterVID uint16 // 0 when the frame is single-tagged/untagged
	InnerVID uint16 // 0 only when the frame carries no VID at all
}

func (t Tenant) String() string {
	switch {
	case t.OuterVID != 0:
		return fmt.Sprintf("tenant(s=%d,c=%d)", t.OuterVID, t.InnerVID)
	case t.InnerVID != 0:
		return fmt.Sprintf("tenant(c=%d)", t.InnerVID)
	default:
		return "tenant(none)"
	}
}
