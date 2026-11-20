package caproute

import "context"

// ResourceType identifies the kind of backing resource a capability points
// at. It is part of the capability binding and cannot be reinterpreted by a
// plug-in.
type ResourceType string

const (
	// TypeFile covers filesystem-backed resources.
	TypeFile ResourceType = "file"
	// TypeNetwork covers network-backed resources.
	TypeNetwork ResourceType = "network"
)

// Operation is an action that may be permitted on a resource.
type Operation string

const (
	OpFileRead   Operation = "file.read"
	OpFileWrite  Operation = "file.write"
	OpFileStat   Operation = "file.stat"
	OpNetDial    Operation = "net.dial"
	OpNetListen  Operation = "net.listen"
	OpNetSend    Operation = "net.send"
	OpNetReceive Operation = "net.receive"
)

// opType records which resource type an operation belongs to. It is the
// basis of the cross-type reinterpretation check.
var opType = map[Operation]ResourceType{
	OpFileRead:   TypeFile,
	OpFileWrite:  TypeFile,
	OpFileStat:   TypeFile,
	OpNetDial:    TypeNetwork,
	OpNetListen:  TypeNetwork,
	OpNetSend:    TypeNetwork,
	OpNetReceive: TypeNetwork,
}

// IsKnown reports whether op is a registered operation.
func (op Operation) IsKnown() bool {
	_, ok := opType[op]
	return ok
}

// Resource is a host object reachable through capabilities. The host invokes
// Serve while holding an operation lease, meaning Close on the owning gate is
// blocked until Serve returns.
type Resource interface {
	Serve(ctx context.Context, op Operation, payload []byte, chain CallChain) ([]byte, error)
}

// ResourceFunc adapts a plain function to the Resource interface.
type ResourceFunc func(ctx context.Context, op Operation, payload []byte, chain CallChain) ([]byte, error)

func (f ResourceFunc) Serve(ctx context.Context, op Operation, payload []byte, chain CallChain) ([]byte, error) {
	return f(ctx, op, payload, chain)
}

// InstanceID identifies a plug-in instance.
type InstanceID string

// Generation is a monotonic, non-rewindable instance generation. Each reload
// advances it, invalidating handles minted under earlier generations.
type Generation uint64

// Handle is the opaque token handed to plug-ins. Its bytes are random and
// carry no information about the resource or the tenant that owns it.
type Handle []byte

// opSet is the internal set representation used for permission intersection.
type opSet map[Operation]struct{}

func makeOpSet(ops []Operation) opSet {
	s := make(opSet, len(ops))
	for _, op := range ops {
		s[op] = struct{}{}
	}
	return s
}

func (s opSet) contains(op Operation) bool {
	_, ok := s[op]
	return ok
}

// intersect returns a new set holding only the operations present in both
// sets. It never grows, which is the property that keeps delegations and
// proxy chains strictly narrowing.
func (s opSet) intersect(other opSet) opSet {
	out := make(opSet, len(s))
	for op := range s {
		if other.contains(op) {
			out[op] = struct{}{}
		}
	}
	return out
}

// toList returns the operations as a slice. Order is not guaranteed.
func (s opSet) toList() []Operation {
	out := make([]Operation, 0, len(s))
	for op := range s {
		out = append(out, op)
	}
	return out
}

// ChainFrame is one hop of a host-mediated call chain: the instance making
// the hop together with the permission it is restricted to.
type ChainFrame struct {
	Instance   InstanceID
	Allowed    []Operation
	allowed    opSet
	Generation Generation
}

// CallChain is the ordered sequence of proxy frames for an invocation. The
// first frame is the entry instance; each nested proxy appends one frame.
type CallChain []ChainFrame

// Effective returns the operations permitted by the whole chain, i.e. the
// intersection of every frame. An empty chain permits nothing.
func (c CallChain) Effective() []Operation {
	if len(c) == 0 {
		return nil
	}
	eff := c[0].allowed
	for i := 1; i < len(c); i++ {
		eff = eff.intersect(c[i].allowed)
	}
	if eff == nil {
		return nil
	}
	return eff.toList()
}

// containsInstance reports whether id already appears in the chain, which
// would make a further hop cyclic.
func (c CallChain) containsInstance(id InstanceID) bool {
	for _, f := range c {
		if f.Instance == id {
			return true
		}
	}
	return false
}

// MaxChainDepth bounds the number of frames in a call chain, including the
// entry frame. The 33rd hop is rejected.
const MaxChainDepth = 32
