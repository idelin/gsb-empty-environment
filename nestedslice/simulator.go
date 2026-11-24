package nestedslice

import (
	"encoding/binary"
	"fmt"
	"reflect"
)

// Store holds independent payload copies for a descriptor tree. The simulator
// never shares pointers with production code paths.
type Store struct {
	bytes map[string][]byte
}

// NewStore copies the provided payloads.
func NewStore(payloads map[string][]byte) *Store {
	cp := make(map[string][]byte, len(payloads))
	for id, data := range payloads {
		cp[id] = append([]byte(nil), data...)
	}
	return &Store{bytes: cp}
}

func (s *Store) byteAt(id string, offset int64) byte {
	return s.bytes[id][offset]
}

func (s *Store) int64At(id string, index int64) int64 {
	offset := index * 8
	return int64(binary.LittleEndian.Uint64(s.bytes[id][offset : offset+8]))
}

func (s *Store) typeIDAt(id string, index int64) uint8 {
	return s.bytes[id][index]
}

func (s *Store) valueAt(id string, index, width int64) []byte {
	start := index * width
	return append([]byte(nil), s.bytes[id][start:start+width]...)
}

func (s *Store) validAt(n *Node, row int64) bool {
	if n.Validity == nil {
		return true
	}
	data := s.bytes[n.Validity.ID]
	return data[row/8]&(1<<uint(row%8)) != 0
}

// DValue is the language-level decoded value of one row.
type DValue struct {
	Null bool
	// List uses ListItems; empty lists use a non-nil zero-length slice while
	// null lists leave ListItems nil.
	ListItems []DValue
	// Struct uses Fields keyed by child name.
	Fields map[string]DValue
	// Union identifies the selected type and its value.
	UnionType int64
	UnionName string
	// Primitive values are copied into Raw.
	Raw []byte
}

// DecodeOracle fully decodes the selected rows directly from the payloads.
// Illegal type id bytes under null union parents are never inspected.
func DecodeOracle(root *Node, store *Store, rows PSet) ([]DValue, error) {
	values, ok := rows.Expand(MaxExpand)
	if !ok {
		return nil, ErrIrregularTooLarge
	}
	out := make([]DValue, 0, len(values))
	for _, row := range values {
		v, err := decodeOne(root, store, row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func decodeOne(n *Node, store *Store, row int64) (DValue, error) {
	if !store.validAt(n, row) {
		return DValue{Null: true}, nil
	}
	switch n.Kind {
	case PrimitiveNode:
		return DValue{Raw: store.valueAt(n.Value.ID, row, n.ValueWidth)}, nil
	case ListNode:
		start := n.offsetAt(row)
		end := n.offsetAt(row + 1)
		items := make([]DValue, 0, end-start)
		for childRow := start; childRow < end; childRow++ {
			v, err := decodeOne(n.Children[0], store, childRow)
			if err != nil {
				return DValue{}, err
			}
			items = append(items, v)
		}
		return DValue{ListItems: items}, nil
	case StructNode:
		fields := map[string]DValue{}
		for _, child := range n.Children {
			v, err := decodeOne(child, store, row)
			if err != nil {
				return DValue{}, err
			}
			fields[child.Name] = v
		}
		return DValue{Fields: fields}, nil
	case DenseUnionNode:
		typeID := int64(store.typeIDAt(n.TypeIDs.ID, row))
		if typeID < 0 || int(typeID) >= len(n.Children) {
			return DValue{}, fmt.Errorf("nestedslice: illegal union type id %d at row %d", typeID, row)
		}
		childRow := store.int64At(n.UnionOffsets.ID, row)
		v, err := decodeOne(n.Children[typeID], store, childRow)
		if err != nil {
			return DValue{}, err
		}
		v.UnionType = typeID
		v.UnionName = n.Children[typeID].Name
		return v, nil
	case DictionaryNode:
		idx := n.dictIndexAt(row)
		return decodeOne(n.Dictionary, store, idx)
	default:
		panic("nestedslice: unknown node kind")
	}
}

// SegmentReader enforces that a plan only touches bytes covered by its read
// segments and never touches forbidden ranges.
type SegmentReader struct {
	store     *Store
	segments  map[string][]ByteRange
	forbidden map[string][]ByteRange
	reads     []ReadSegment
}

// NewSegmentReader builds a reader from an executed plan.
func NewSegmentReader(plan *Plan, store *Store) *SegmentReader {
	segments := map[string][]ByteRange{}
	for _, seg := range plan.Segments {
		segments[seg.BufferID] = append(segments[seg.BufferID], seg.ByteRange)
	}
	return &SegmentReader{
		store:     store,
		segments:  segments,
		forbidden: plan.ForbiddenRanges,
		reads:     plan.Segments,
	}
}

func (r *SegmentReader) check(id string, br ByteRange, width int64) error {
	for _, f := range r.forbidden[id] {
		if br.Start < f.End && f.Start < br.End {
			return fmt.Errorf("nestedslice: read %s[%d:%d] intersects forbidden range [%d:%d]",
				id, br.Start, br.End, f.Start, f.End)
		}
	}
	for _, seg := range r.segments[id] {
		if br.Start >= seg.Start && br.End <= seg.End {
			return nil
		}
	}
	return fmt.Errorf("nestedslice: read %s[%d:%d] is not covered by any planned segment (width %d)",
		id, br.Start, br.End, width)
}

func (r *SegmentReader) readBytes(id string, index, width int64) ([]byte, error) {
	br := ByteRange{Start: index * width, End: (index + 1) * width}
	if err := r.check(id, br, width); err != nil {
		return nil, err
	}
	return append([]byte(nil), r.store.bytes[id][br.Start:br.End]...), nil
}

func (r *SegmentReader) readInt64(id string, index int64) (int64, error) {
	raw, err := r.readBytes(id, index, 8)
	if err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(raw)), nil
}

func (r *SegmentReader) readTypeID(id string, index int64) (uint8, error) {
	raw, err := r.readBytes(id, index, 1)
	if err != nil {
		return 0, err
	}
	return raw[0], nil
}

func (r *SegmentReader) readValid(n *Node, row int64) (bool, error) {
	if n.Validity == nil {
		return true, nil
	}
	br := ByteRange{Start: row / 8, End: row/8 + 1}
	if err := r.check(n.Validity.ID, br, 0); err != nil {
		return false, err
	}
	return r.store.bytes[n.Validity.ID][row/8]&(1<<uint(row%8)) != 0, nil
}

// DecodePlan reconstructs logical rows using only planned read segments.
func DecodePlan(plan *Plan, root *Node, store *Store) ([]DValue, error) {
	reader := NewSegmentReader(plan, store)
	values, ok := plan.Root.Rows.Expand(MaxExpand)
	if !ok {
		return nil, ErrIrregularTooLarge
	}
	out := make([]DValue, 0, len(values))
	for _, row := range values {
		v, err := decodeLayer(plan.Root, root, reader, store, row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func decodeLayer(spec *LayerSpec, n *Node, r *SegmentReader, store *Store, row int64) (DValue, error) {
	valid, err := r.readValid(n, row)
	if err != nil {
		return DValue{}, err
	}
	if !valid {
		return DValue{Null: true}, nil
	}
	switch n.Kind {
	case PrimitiveNode:
		raw, err := r.readBytes(n.Value.ID, row, n.ValueWidth)
		if err != nil {
			return DValue{}, err
		}
		return DValue{Raw: raw}, nil
	case ListNode:
		return decodeListLayer(spec, n, r, store, row)
	case StructNode:
		fields := map[string]DValue{}
		for _, childBinding := range spec.Children {
			child := findChild(n, childBinding.Name)
			v, err := decodeLayer(childBinding.Layer, child, r, store, row)
			if err != nil {
				return DValue{}, err
			}
			fields[childBinding.Name] = v
		}
		return DValue{Fields: fields}, nil
	case DenseUnionNode:
		typeID, err := r.readTypeID(n.TypeIDs.ID, row)
		if err != nil {
			return DValue{}, err
		}
		childRow, err := r.readInt64(n.UnionOffsets.ID, row)
		if err != nil {
			return DValue{}, err
		}
		child := n.Children[typeID]
		binding := findBinding(spec, child.Name)
		v, err := decodeLayer(binding.Layer, child, r, store, childRow)
		if err != nil {
			return DValue{}, err
		}
		v.UnionType = int64(typeID)
		v.UnionName = child.Name
		return v, nil
	case DictionaryNode:
		idx, err := r.readInt64(n.Index.ID, row)
		if err != nil {
			return DValue{}, err
		}
		return decodeLayer(spec.Dict.DictionaryLayer, n.Dictionary, r, store, idx)
	default:
		panic("nestedslice: unknown node kind")
	}
}

func decodeListLayer(spec *LayerSpec, n *Node, r *SegmentReader, store *Store, row int64) (DValue, error) {
	var start, end int64
	if spec.Offsets.FixedSize {
		start = addChecked(spec.Offsets.OffsetFirst, mulChecked(spec.Offsets.Stride, row))
		end = addChecked(spec.Offsets.OffsetFirst, mulChecked(spec.Offsets.Stride, row+1))
	} else {
		var err error
		start, err = r.readInt64(n.Offsets.ID, row)
		if err != nil {
			return DValue{}, err
		}
		end, err = r.readInt64(n.Offsets.ID, row+1)
		if err != nil {
			return DValue{}, err
		}
	}
	child := n.Children[0]
	binding := spec.Children[0]
	items := make([]DValue, 0, end-start)
	for childRow := start; childRow < end; childRow++ {
		v, err := decodeLayer(binding.Layer, child, r, store, childRow)
		if err != nil {
			return DValue{}, err
		}
		items = append(items, v)
	}
	return DValue{ListItems: items}, nil
}

func findChild(n *Node, name string) *Node {
	for _, child := range n.Children {
		if child.Name == name {
			return child
		}
	}
	panic("nestedslice: missing child " + name)
}

func findBinding(spec *LayerSpec, name string) ChildLayer {
	for _, binding := range spec.Children {
		if binding.Name == name {
			return binding
		}
	}
	panic("nestedslice: missing binding " + name)
}

// AssertEquivalent compares two decoded row sequences, distinguishing null
// lists from empty lists.
func AssertEquivalent(a, b []DValue) bool {
	return reflect.DeepEqual(a, b)
}
