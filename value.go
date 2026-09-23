package graphdec

// Kind identifies the payload type of a graph node.
type Kind byte

const (
	KindNull   Kind = 0
	KindBool   Kind = 1
	KindInt    Kind = 2
	KindString Kind = 3
	KindBytes  Kind = 4
	KindList   Kind = 5
	KindMap    Kind = 6
)

func (k Kind) valid() bool { return k <= KindMap }

func (k Kind) String() string {
	switch k {
	case KindNull:
		return "null"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindString:
		return "string"
	case KindBytes:
		return "bytes"
	case KindList:
		return "list"
	case KindMap:
		return "map"
	}
	return "unknown"
}

// MapEntry is one key/value pair of a KindMap node.
type MapEntry struct {
	Key   *Value
	Value *Value
}

// Value is a decoded object-graph node. Cyclic and shared references are
// represented directly by pointer identity, so a decoded graph may contain
// cycles; traversal code must track visited nodes.
type Value struct {
	Kind  Kind
	Bool  bool
	Int   int64
	Str   string
	Bytes []byte
	List  []*Value
	Map   []MapEntry
}

// children returns the direct successors of v in the graph.
func (v *Value) children() []*Value {
	switch v.Kind {
	case KindList:
		return v.List
	case KindMap:
		out := make([]*Value, 0, 2*len(v.Map))
		for _, e := range v.Map {
			out = append(out, e.Key, e.Value)
		}
		return out
	}
	return nil
}
