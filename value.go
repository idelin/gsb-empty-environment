package netsec

// Kind enumerates the node kinds of a Value graph.
type Kind uint8

const (
	KindInvalid Kind = iota
	KindString
	KindNumber
	KindArray
	KindDict
	KindRef
)

// Value is a node in a directed object graph. Graphs may contain
// sharing (multiple parents) and cycles. A node tagged Secret taints
// every node reachable from it, regardless of other incoming paths.
type Value struct {
	Kind   Kind
	Secret bool
	Str    string
	Num    float64
	Elems  []*Value
	Dict   map[string]*Value
	Ref    *Value
}

func Str(s string) *Value { return &Value{Kind: KindString, Str: s} }

func Num(f float64) *Value { return &Value{Kind: KindNumber, Num: f} }

func Arr(elems ...*Value) *Value { return &Value{Kind: KindArray, Elems: elems} }

func Dict(entries map[string]*Value) *Value { return &Value{Kind: KindDict, Dict: entries} }

// RefTo creates an alias node that transparently forwards to target.
func RefTo(target *Value) *Value { return &Value{Kind: KindRef, Ref: target} }

// Sec marks v as a trusted secret entry tag. The tag cannot be
// stripped by whoever controls the graph shape or aliases.
func Sec(v *Value) *Value {
	if v != nil {
		v.Secret = true
	}
	return v
}

// resolve follows a chain of reference nodes to the concrete target.
// It returns nil for nil input and for reference-only cycles, which
// carry no concrete value.
func resolve(v *Value) *Value {
	if v == nil || v.Kind != KindRef {
		return v
	}
	seen := make(map[*Value]struct{})
	for v != nil && v.Kind == KindRef {
		if _, ok := seen[v]; ok {
			return nil
		}
		seen[v] = struct{}{}
		v = v.Ref
	}
	return v
}

func children(v *Value) []*Value {
	switch v.Kind {
	case KindArray:
		return v.Elems
	case KindDict:
		out := make([]*Value, 0, len(v.Dict))
		for _, child := range v.Dict {
			out = append(out, child)
		}
		return out
	case KindRef:
		if v.Ref != nil {
			return []*Value{v.Ref}
		}
	}
	return nil
}
