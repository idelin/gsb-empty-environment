package vlan

import (
	"errors"
	"sync/atomic"
)

// Datapath drop reasons. These are intentionally distinct from parse errors:
// a parse failure means the wire bytes are bad, while these mean the policy
// refuses admission for a well-formed frame.
var (
	ErrUnknownPort    = errors.New("vlan: unknown port")
	ErrIngressDenied  = errors.New("vlan: ingress denied by port policy")
	ErrEgressDenied   = errors.New("vlan: egress denied by port policy")
	ErrTenantMismatch = errors.New("vlan: frame tenant does not match its tag stack")
)

// Switch runs the bounded VLAN label-stack rewrite on the receive path. It is
// safe for concurrent use: policy swaps install an immutable snapshot
// atomically, and every rewrite works on freshly allocated byte slices, so a
// swap can never expose another tenant's half-rewritten frame.
type Switch struct {
	policy atomic.Pointer[Policy]
}

// NewSwitch creates a switch with the given initial policy.
func NewSwitch(initial *Policy) *Switch {
	if initial == nil {
		panic("vlan: NewSwitch requires a non-nil policy")
	}
	s := &Switch{}
	s.policy.Store(initial)
	return s
}

// Swap atomically installs a new policy snapshot. Frames already being
// processed finish under the old snapshot; new frames see the new one. No
// frame is ever processed with a mix of the two.
func (s *Switch) Swap(p *Policy) {
	if p == nil {
		panic("vlan: Swap requires a non-nil policy")
	}
	s.policy.Store(p)
}

// Current returns the policy snapshot in effect.
func (s *Switch) Current() *Policy { return s.policy.Load() }

// Ingress parses a raw frame received on port and returns the canonical
// internal frame (full tag stack attached) together with its tenant. A
// non-nil error means the frame must be dropped.
func (s *Switch) Ingress(portName string, raw []byte) ([]byte, Tenant, error) {
	port, ok := s.Current().port(portName)
	if !ok {
		return nil, Tenant{}, ErrUnknownPort
	}
	frame, err := ParseFrame(raw)
	if err != nil {
		return nil, Tenant{}, err
	}

	var stack []Tag
	switch port.Mode {
	case ModeAccess:
		stack, err = s.ingressAccess(port, frame)
	case ModeTrunk:
		stack, err = s.ingressTrunk(port, frame)
	case ModeTunnel:
		stack, err = s.ingressTunnel(port, frame)
	default:
		return nil, Tenant{}, ErrIngressDenied
	}
	if err != nil {
		return nil, Tenant{}, err
	}
	frame.Tags = stack
	canonical := frame.Marshal()
	tenant := tenantOf(stack)
	return canonical, tenant, nil
}

func (s *Switch) ingressAccess(port *PortPolicy, f *Frame) ([]Tag, error) {
	switch f.Class() {
	case StackUntagged:
		return []Tag{{TPID: EtherTypeVLAN, VID: port.AccessVID}}, nil
	case StackPriority:
		// Priority tag: keep PCP/DEI, bind the frame to the access VLAN.
		t := f.Tags[0]
		t.VID = port.AccessVID
		return []Tag{t}, nil
	default:
		return nil, ErrIngressDenied
	}
}

func (s *Switch) ingressTrunk(port *PortPolicy, f *Frame) ([]Tag, error) {
	switch f.Class() {
	case StackUntagged:
		if port.NativeVID == 0 {
			return nil, ErrIngressDenied
		}
		return []Tag{{TPID: EtherTypeVLAN, VID: port.NativeVID}}, nil
	case StackPriority:
		if port.NativeVID == 0 {
			return nil, ErrIngressDenied
		}
		t := f.Tags[0]
		t.VID = port.NativeVID
		return []Tag{t}, nil
	case StackSingle:
		vid := f.Tags[0].VID
		if !inList(port.Allowed, vid) {
			return nil, ErrIngressDenied
		}
		return f.Tags, nil
	case StackDouble:
		outer := f.Tags[0]
		if !inList(port.Allowed, outer.VID) {
			return nil, ErrIngressDenied
		}
		return f.Tags, nil
	default:
		return nil, ErrIngressDenied
	}
}

func (s *Switch) ingressTunnel(port *PortPolicy, f *Frame) ([]Tag, error) {
	// Adding the service tag must keep the stack bounded at two.
	if len(f.Tags) == MaxTags {
		return nil, ErrIngressDenied
	}
	service := Tag{TPID: EtherTypeQinQ, VID: port.AccessVID}
	stack := make([]Tag, 0, MaxTags)
	stack = append(stack, service)
	stack = append(stack, f.Tags...)
	return stack, nil
}

// Egress maps a canonical frame back onto the wire shape expected by port.
// The tenant is checked against the frame's own stack before any rewrite, so
// misrouted frames are dropped rather than leaking onto another tenant's port.
func (s *Switch) Egress(portName string, tenant Tenant, canonical []byte) ([]byte, error) {
	port, ok := s.Current().port(portName)
	if !ok {
		return nil, ErrUnknownPort
	}
	frame, err := ParseFrame(canonical)
	if err != nil {
		return nil, err
	}
	if got := tenantOf(frame.Tags); got != tenant {
		return nil, ErrTenantMismatch
	}

	switch port.Mode {
	case ModeAccess:
		return egressAccess(port, frame)
	case ModeTrunk:
		return egressTrunk(port, frame)
	case ModeTunnel:
		return egressTunnel(port, frame)
	default:
		return nil, ErrEgressDenied
	}
}

func egressAccess(port *PortPolicy, f *Frame) ([]byte, error) {
	if len(f.Tags) != 1 || f.Tags[0].VID != port.AccessVID {
		return nil, ErrEgressDenied
	}
	t := f.Tags[0]
	f.Tags = nil
	out := f.Marshal()
	if t.PCP != 0 || t.DEI {
		// Preserve QoS markings without leaking the VID.
		out = insertTag(out, Tag{TPID: EtherTypeVLAN, PCP: t.PCP, DEI: t.DEI, VID: priorityVID})
	}
	return out, nil
}

func egressTrunk(port *PortPolicy, f *Frame) ([]byte, error) {
	switch len(f.Tags) {
	case 1:
		vid := f.Tags[0].VID
		if vid == port.NativeVID {
			t := f.Tags[0]
			f.Tags = nil
			out := f.Marshal()
			if t.PCP != 0 || t.DEI {
				out = insertTag(out, Tag{TPID: EtherTypeVLAN, PCP: t.PCP, DEI: t.DEI, VID: priorityVID})
			}
			return out, nil
		}
		if !inList(port.Allowed, vid) {
			return nil, ErrEgressDenied
		}
		return f.Marshal(), nil
	case 2:
		outer := f.Tags[0].VID
		if !inList(port.Allowed, outer) {
			return nil, ErrEgressDenied
		}
		return f.Marshal(), nil
	default:
		return nil, ErrEgressDenied
	}
}

func egressTunnel(port *PortPolicy, f *Frame) ([]byte, error) {
	if len(f.Tags) == 0 || f.Tags[0].TPID != EtherTypeQinQ || f.Tags[0].VID != port.AccessVID {
		return nil, ErrEgressDenied
	}
	f.Tags = f.Tags[1:]
	return f.Marshal(), nil
}

func tenantOf(stack []Tag) Tenant {
	switch len(stack) {
	case 1:
		return Tenant{InnerVID: stack[0].VID}
	case 2:
		return Tenant{OuterVID: stack[0].VID, InnerVID: stack[1].VID}
	default:
		return Tenant{}
	}
}

func inList(list []uint16, vid uint16) bool {
	for _, v := range list {
		if v == vid {
			return true
		}
	}
	return false
}
