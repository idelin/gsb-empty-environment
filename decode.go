package netsec

import "errors"

var (
	ErrMissingLeaseIssuer = errors.New("netsec: missing lease issuer")
	ErrMalformedLeaseSet  = errors.New("netsec: lease issuer returned an incomplete lease set")
)

// Decoder restores attacker-controlled object graphs while enforcing tenant
// and generation policy at the capability boundary.
type Decoder struct {
	issuer LeaseIssuer
}

// NewDecoder constructs a decoder backed by a trusted LeaseIssuer. The issuer
// may be nil only when decoding graphs that contain no reachable capabilities.
func NewDecoder(issuer LeaseIssuer) *Decoder {
	return &Decoder{issuer: issuer}
}

// Decode validates the complete graph, atomically acquires exactly one lease
// per distinct reachable capability token, and only then materializes the
// object graph. On any error it returns no partial value or lease set.
//
// Object nodes retain pointer identity across aliases and cycles. Capability
// references contain no token in the output and repeated references share the
// same Capability pointer.
func Decode(graph Graph, principal Principal, issuer LeaseIssuer) (*Value, *LeaseSet, error) {
	return NewDecoder(issuer).Decode(graph, principal)
}

// Decode is the method form of the package-level Decode function.
func (d *Decoder) Decode(graph Graph, principal Principal) (*Value, *LeaseSet, error) {
	if principal.Tenant == "" {
		return nil, nil, ErrInvalidPrincipal
	}
	if err := graph.validate(); err != nil {
		return nil, nil, err
	}

	tokens := graph.reachableTokens()
	leases, err := d.acquire(principal, tokens)
	if err != nil {
		return nil, nil, err
	}

	value, err := newValueBuilder(graph, leases).build(graph.Root)
	if err != nil {
		leases.Release()
		return nil, nil, err
	}
	return value, leases, nil
}

func (d *Decoder) acquire(principal Principal, tokens []string) (*LeaseSet, error) {
	if len(tokens) == 0 {
		empty := &LeaseSet{
			tokens:    nil,
			tokenCaps: make(map[string]*Capability),
		}
		empty.active.Store(true)
		return empty, nil
	}
	if d == nil || d.issuer == nil {
		return nil, ErrMissingLeaseIssuer
	}

	leases, err := d.issuer.AcquireAll(principal, append([]string(nil), tokens...))
	if err != nil {
		return nil, err
	}
	if leases == nil {
		return nil, ErrMalformedLeaseSet
	}
	for _, token := range tokens {
		if _, ok := leases.capabilityFor(token); !ok {
			leases.Release()
			return nil, ErrMalformedLeaseSet
		}
	}
	return leases, nil
}

type valueBuilder struct {
	graph      Graph
	leases     *LeaseSet
	values     map[NodeID]*Value
	capability map[string]*Value
}

func newValueBuilder(graph Graph, leases *LeaseSet) *valueBuilder {
	return &valueBuilder{
		graph:      graph,
		leases:     leases,
		values:     make(map[NodeID]*Value, len(graph.Nodes)),
		capability: make(map[string]*Value),
	}
}

func (b *valueBuilder) build(id NodeID) (*Value, error) {
	if value, ok := b.values[id]; ok {
		return value, nil
	}

	node := b.graph.Nodes[id]
	switch node.Kind {
	case KindString:
		value := &Value{Kind: KindString, String: node.String}
		b.values[id] = value
		return value, nil

	case KindArray:
		value := &Value{Kind: KindArray, Array: make([]*Value, len(node.Array))}
		b.values[id] = value
		for i, child := range node.Array {
			decoded, err := b.build(child)
			if err != nil {
				return nil, err
			}
			value.Array[i] = decoded
		}
		return value, nil

	case KindDict:
		value := &Value{Kind: KindDict, Dict: make(map[string]*Value, len(node.Dict))}
		b.values[id] = value
		for key, child := range node.Dict {
			decoded, err := b.build(child)
			if err != nil {
				return nil, err
			}
			value.Dict[key] = decoded
		}
		return value, nil

	case KindObjectRef:
		value, err := b.build(node.Target)
		if err != nil {
			return nil, err
		}
		b.values[id] = value
		return value, nil

	case KindCapabilityRef:
		if value, ok := b.capability[node.Token]; ok {
			b.values[id] = value
			return value, nil
		}
		capability, ok := b.leases.capabilityFor(node.Token)
		if !ok {
			return nil, ErrMalformedLeaseSet
		}
		value := &Value{Kind: KindCapabilityRef, Capability: capability}
		b.capability[node.Token] = value
		b.values[id] = value
		return value, nil

	default:
		return nil, ErrInvalidNodeKind
	}
}
