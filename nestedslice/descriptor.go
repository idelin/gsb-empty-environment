package nestedslice

import "sort"

// NodeKind enumerates the supported array types.
type NodeKind int

const (
	PrimitiveNode NodeKind = iota
	ListNode
	StructNode
	DenseUnionNode
	DictionaryNode
)

func (k NodeKind) String() string {
	switch k {
	case PrimitiveNode:
		return "primitive"
	case ListNode:
		return "list"
	case StructNode:
		return "struct"
	case DenseUnionNode:
		return "dense_union"
	case DictionaryNode:
		return "dictionary"
	default:
		return "unknown"
	}
}

// BufferKind identifies the role of a physical buffer.
type BufferKind int

const (
	ValidityBuffer BufferKind = iota
	ValuesBuffer
	OffsetsBuffer
	TypeIDBuffer
	UnionOffsetBuffer
	DictIndexBuffer
)

func (k BufferKind) width() int64 {
	switch k {
	case ValidityBuffer:
		return 0 // bit addressed
	case TypeIDBuffer:
		return 1
	case ValuesBuffer:
		return 0 // width comes from the node
	default:
		return 8 // int64 offsets / union offsets / dictionary indices
	}
}

// Buffer describes one contiguous physical byte buffer. Length is the number
// of addressed elements (bits for a validity buffer).
type Buffer struct {
	ID     string
	Kind   BufferKind
	Length int64
	// ValueWidth is used only for ValuesBuffer.
	ValueWidth int64
}

func (b *Buffer) elemWidth(nodeWidth int64) int64 {
	switch b.Kind {
	case ValidityBuffer:
		return 0
	case TypeIDBuffer:
		return 1
	case ValuesBuffer:
		if nodeWidth <= 0 {
			panic("nestedslice: values buffer needs positive node value width")
		}
		return nodeWidth
	default:
		return 8
	}
}

// ByteSize returns the full byte size of the buffer.
func (b *Buffer) ByteSize() int64 {
	if b.Kind == ValidityBuffer {
		return bitmapBytes(b.Length)
	}
	return mulChecked(b.Length, b.elemWidth(0))
}

// ByteRange converts the half-open element interval [lo, hi) into a byte
// range within the buffer.
func (b *Buffer) ByteRange(lo, hi int64, nodeWidth int64) ByteRange {
	if lo < 0 || hi < lo || hi > b.Length {
		panic("nestedslice: buffer element range out of bounds")
	}
	if b.Kind == ValidityBuffer {
		return ByteRange{Start: lo / 8, End: bitmapBytes(hi)}
	}
	width := b.elemWidth(nodeWidth)
	return ByteRange{Start: mulChecked(lo, width), End: mulChecked(hi, width)}
}

func bitmapBytes(n int64) int64 { return (n + 7) / 8 }

// ByteRange is a half-open byte interval.
type ByteRange struct {
	Start int64
	End   int64
}

func (r ByteRange) len() int64 { return r.End - r.Start }

func mergeRanges(ranges []ByteRange) []ByteRange {
	if len(ranges) == 0 {
		return nil
	}
	cp := append([]ByteRange(nil), ranges...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })
	out := cp[:0]
	for _, r := range cp {
		if len(out) == 0 || r.Start > out[len(out)-1].End {
			out = append(out, r)
			continue
		}
		if r.End > out[len(out)-1].End {
			out[len(out)-1].End = r.End
		}
	}
	return out
}

// Node describes one array in the nested schema. Buffers are referenced by ID
// and stored separately so the descriptor stays free of payload bytes.
type Node struct {
	ID     string
	Name   string
	Kind   NodeKind
	Length int64

	// ValueWidth is the fixed element width of a primitive values buffer.
	ValueWidth int64

	// NullRows lists null logical rows. A null row still owns its physical
	// child interval, which the planner shadows. An empty set means no nulls.
	NullRows PSet
	Validity *Buffer

	// Primitive values.
	Value *Buffer

	// List offsets: Length+1 monotonically non-decreasing int64 entries.
	Offsets *Buffer
	// FixedSize declares offsets equal to OffsetFirst + i*Stride for every
	// row, which keeps giant lists fully symbolic.
	FixedSize   bool
	OffsetFirst int64
	Stride      int64
	// OffsetData holds explicit offsets for small arrays. OffsetFn allows a
	// caller-supplied symbolic function; the planner only invokes it when the
	// selection is within the expansion limit.
	OffsetData []int64
	OffsetFn   func(row int64) int64

	// Struct children.
	Children []*Node

	// Dense union: one type id and one child offset per row. Children are
	// indexed by type id.
	TypeIDs         *Buffer
	UnionOffsets    *Buffer
	TypeIDData      []uint8
	TypeIDFn        func(row int64) int64
	UnionOffsetData []int64
	UnionOffsetFn   func(row int64) int64

	// Dictionary: indices into the shared Dictionary subtree.
	Index         *Buffer
	Dictionary    *Node
	DictIndexData []int64
	DictIndexFn   func(row int64) int64
	// IndexFirst/IndexStep give an affine symbolic index mapping for giant
	// dictionaries. Step may be negative.
	IndexAffine bool
	IndexFirst  int64
	IndexStep   int64
}

func (n *Node) offsetAt(row int64) int64 {
	if n.FixedSize {
		return addChecked(n.OffsetFirst, mulChecked(n.Stride, row))
	}
	if n.OffsetData != nil {
		return n.OffsetData[row]
	}
	if n.OffsetFn != nil {
		return n.OffsetFn(row)
	}
	panic("nestedslice: node has no resolvable offsets")
}

func (n *Node) typeIDAt(row int64) int64 {
	if n.TypeIDData != nil {
		return int64(n.TypeIDData[row])
	}
	if n.TypeIDFn != nil {
		return n.TypeIDFn(row)
	}
	panic("nestedslice: node has no resolvable type ids")
}

func (n *Node) unionOffsetAt(row int64) int64 {
	if n.UnionOffsetData != nil {
		return n.UnionOffsetData[row]
	}
	if n.UnionOffsetFn != nil {
		return n.UnionOffsetFn(row)
	}
	panic("nestedslice: node has no resolvable union offsets")
}

func (n *Node) dictIndexAt(row int64) int64 {
	if n.DictIndexData != nil {
		return n.DictIndexData[row]
	}
	if n.IndexAffine {
		return addChecked(n.IndexFirst, mulChecked(n.IndexStep, row))
	}
	if n.DictIndexFn != nil {
		return n.DictIndexFn(row)
	}
	panic("nestedslice: node has no resolvable dictionary indices")
}

// BufferRef pairs a buffer with the node width needed to address it.
type BufferRef struct {
	Buffer    *Buffer
	NodeWidth int64
}

func (n *Node) bufferRefs() []BufferRef {
	var refs []BufferRef
	add := func(b *Buffer) {
		if b != nil {
			refs = append(refs, BufferRef{Buffer: b, NodeWidth: n.ValueWidth})
		}
	}
	add(n.Validity)
	switch n.Kind {
	case PrimitiveNode:
		add(n.Value)
	case ListNode:
		add(n.Offsets)
	case DenseUnionNode:
		add(n.TypeIDs)
		add(n.UnionOffsets)
	case DictionaryNode:
		add(n.Index)
	}
	return refs
}
