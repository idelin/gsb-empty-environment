package objectgraph

import (
	"encoding/binary"
	"math"
)

// KV is one object field supplied to Builder.SetObject/AddObject.
type KV struct {
	Key string
	Ref int
}

// Builder incrementally constructs an encoded graph. Nodes are first declared
// (reserving an index) and later filled exactly once, which permits cycles:
// declare an index, point other nodes at it, then fill it with edges pointing
// back. The convenience Add* methods declare and fill in one step.
type Builder struct {
	nodes []encNode
}

type encKind uint8

const (
	encUnset encKind = iota
	encNull
	encBool
	encInt
	encFloat
	encString
	encArray
	encObject
)

type encNode struct {
	kind   encKind
	boolV  bool
	intV   int64
	floatV float64
	strV   string
	edges  []int
	keys   []string
}

// NewBuilder creates an empty builder.
func NewBuilder() *Builder { return &Builder{} }

// NewBuilderSized preallocates node tables; the hint is a capacity only.
func NewBuilderSized(hint int) *Builder {
	if hint < 0 {
		hint = 0
	}
	return &Builder{nodes: make([]encNode, 0, hint)}
}

// Declare reserves a node index and returns it. The node must be filled
// exactly once before Encode.
func (b *Builder) Declare() int {
	id := len(b.nodes)
	b.nodes = append(b.nodes, encNode{kind: encUnset})
	return id
}

func (b *Builder) require(id int) (*encNode, error) {
	if id < 0 || id >= len(b.nodes) {
		return nil, ErrEncoderInvalid
	}
	return &b.nodes[id], nil
}

func (b *Builder) SetNull(id int) error {
	n, err := b.require(id)
	if err != nil {
		return err
	}
	if n.kind != encUnset {
		return ErrEncoderInvalid
	}
	n.kind = encNull
	return nil
}

func (b *Builder) SetBool(id int, v bool) error {
	n, err := b.require(id)
	if err != nil {
		return err
	}
	if n.kind != encUnset {
		return ErrEncoderInvalid
	}
	n.kind, n.boolV = encBool, v
	return nil
}

func (b *Builder) SetInt(id int, v int64) error {
	n, err := b.require(id)
	if err != nil {
		return err
	}
	if n.kind != encUnset {
		return ErrEncoderInvalid
	}
	n.kind, n.intV = encInt, v
	return nil
}

func (b *Builder) SetFloat(id int, v float64) error {
	n, err := b.require(id)
	if err != nil {
		return err
	}
	if n.kind != encUnset {
		return ErrEncoderInvalid
	}
	n.kind, n.floatV = encFloat, v
	return nil
}

func (b *Builder) SetString(id int, s string) error {
	n, err := b.require(id)
	if err != nil {
		return err
	}
	if n.kind != encUnset {
		return ErrEncoderInvalid
	}
	n.kind, n.strV = encString, s
	return nil
}

func (b *Builder) SetArray(id int, refs []int) error {
	n, err := b.require(id)
	if err != nil {
		return err
	}
	if n.kind != encUnset {
		return ErrEncoderInvalid
	}
	n.kind = encArray
	n.edges = append(n.edges, refs...)
	return nil
}

func (b *Builder) SetObject(id int, fields []KV) error {
	n, err := b.require(id)
	if err != nil {
		return err
	}
	if n.kind != encUnset {
		return ErrEncoderInvalid
	}
	n.kind = encObject
	n.keys = make([]string, len(fields))
	n.edges = make([]int, len(fields))
	for i, fv := range fields {
		n.keys[i] = fv.Key
		n.edges[i] = fv.Ref
	}
	return nil
}

// Add* convenience methods declare and fill a node at once.

func (b *Builder) AddNull() int {
	id := b.Declare()
	_ = b.SetNull(id)
	return id
}

func (b *Builder) AddBool(v bool) int {
	id := b.Declare()
	_ = b.SetBool(id, v)
	return id
}

func (b *Builder) AddInt(v int64) int {
	id := b.Declare()
	_ = b.SetInt(id, v)
	return id
}

func (b *Builder) AddFloat(v float64) int {
	id := b.Declare()
	_ = b.SetFloat(id, v)
	return id
}

func (b *Builder) AddString(v string) int {
	id := b.Declare()
	_ = b.SetString(id, v)
	return id
}

func (b *Builder) AddArray(refs []int) int {
	id := b.Declare()
	_ = b.SetArray(id, refs)
	return id
}

func (b *Builder) AddObject(fields []KV) int {
	id := b.Declare()
	_ = b.SetObject(id, fields)
	return id
}

// Encode validates the whole graph and serializes it with root as the entry
// node. Validation happens before any output is returned; on error the
// returned byte slice is always nil.
func (b *Builder) Encode(root int) ([]byte, error) {
	n := len(b.nodes)
	if root < 0 || root >= n {
		return nil, wrapf(ErrEncoderInvalid, "root index %d out of range", root)
	}
	for id := range b.nodes {
		if b.nodes[id].kind == encUnset {
			return nil, wrapf(ErrEncoderInvalid, "node %d declared but never set", id)
		}
		for _, r := range b.nodes[id].edges {
			if r < 0 || r >= n {
				return nil, wrapf(ErrEncoderInvalid, "node %d references %d out of range", id, r)
			}
		}
		if b.nodes[id].kind == encObject && len(b.nodes[id].keys) != len(b.nodes[id].edges) {
			return nil, wrapf(ErrEncoderInvalid, "node %d malformed object", id)
		}
	}
	if n == 0 || n > math.MaxInt32 {
		return nil, wrapf(ErrEncoderInvalid, "node count %d unsupported", n)
	}

	out := make([]byte, 0, 64)
	out = append(out, magic0, magic1, magic2, version1)
	out = putUvarint(out, uint64(n))
	out = putUvarint(out, uint64(root))
	for id := range b.nodes {
		nd := &b.nodes[id]
		switch nd.kind {
		case encNull:
			out = append(out, tagNull)
		case encBool:
			out = append(out, tagBool, boolByte(nd.boolV))
		case encInt:
			out = append(out, tagInt)
			out = putVarint(out, nd.intV)
		case encFloat:
			out = append(out, tagFloat)
			var fb [8]byte
			binary.BigEndian.PutUint64(fb[:], math.Float64bits(nd.floatV))
			out = append(out, fb[:]...)
		case encString:
			out = append(out, tagString)
			out = putUvarint(out, uint64(len(nd.strV)))
			out = append(out, nd.strV...)
		case encArray:
			out = append(out, tagArray)
			out = putUvarint(out, uint64(len(nd.edges)))
			for _, r := range nd.edges {
				out = putUvarint(out, uint64(r))
			}
		case encObject:
			out = append(out, tagObject)
			out = putUvarint(out, uint64(len(nd.edges)))
			for i := range nd.edges {
				out = putUvarint(out, uint64(len(nd.keys[i])))
				out = append(out, nd.keys[i]...)
				out = putUvarint(out, uint64(nd.edges[i]))
			}
		default:
			return nil, wrapf(ErrEncoderInvalid, "node %d has unsupported kind", id)
		}
	}
	return out, nil
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func putUvarint(dst []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	k := binary.PutUvarint(buf[:], v)
	return append(dst, buf[:k]...)
}

func putVarint(dst []byte, v int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	k := binary.PutVarint(buf[:], v)
	return append(dst, buf[:k]...)
}
