package netsec

import "fmt"

// Decode validates and decodes input against typ. It never tolerates trailing
// bytes: the whole input must be exactly one value of typ.
//
// All offset tables are checked before any sub-value is decoded, so malformed
// offsets cannot cause out-of-range reads or inconsistent interpretations.
func Decode(typ Type, input []byte) (*Value, error) {
	if typ == nil {
		return nil, fmt.Errorf("ssz: cannot decode against nil type")
	}
	if err := typ.valid(); err != nil {
		return nil, err
	}
	if uint64(len(input)) > typ.MaxSize() {
		return nil, fmt.Errorf("%w: %d > %d bytes", ErrMaxSize, len(input), typ.MaxSize())
	}
	r := &reader{data: input, base: 0, end: len(input), logicalEnd: len(input)}
	v, err := r.decode(typ)
	if err != nil {
		return nil, err
	}
	if len(v.raw) != len(input) {
		return nil, fmt.Errorf("%w: value consumed %d of %d bytes",
			ErrTrailingBytes, len(v.raw), len(input))
	}
	return v, nil
}

// reader walks one contiguous region of a shared wire buffer. base is the
// absolute start of this region and end its exclusive boundary. SSZ offsets
// inside a composite are all relative to that region's base.
type reader struct {
	data []byte
	base int
	end  int
	// logicalEnd is the mandatory exclusive end of this value, or -1 when the
	// value may run to end. It is set when a parent knows a final child's exact
	// boundary so surplus bytes are detected instead of being absorbed.
	logicalEnd int
}

// span reports the value's logical byte length. When a parent knows the exact
// end of a final child it sets logicalEnd; otherwise the value fills its whole
// readable region. This lets a nested fixed-size collection detect surplus
// bytes placed beyond its logical end rather than silently absorbing them.
func (r *reader) span() int {
	if r.logicalEnd >= 0 {
		return r.logicalEnd - r.base
	}
	return r.end - r.base
}

// sub returns a reader for the absolute range [start, end).
func (r *reader) sub(start, end int) *reader {
	return &reader{data: r.data, base: start, end: end, logicalEnd: end}
}

// length is the value's logical size (the bytes it must fully account for).
func (r *reader) length() int { return r.span() }

func (r *reader) raw() []byte { return clone(r.data[r.base : r.base+r.span()]) }

// readRange decodes typ from the absolute range [start, end) and requires it to
// fill the range exactly, so a nested value cannot read past a parent boundary.
func (r *reader) readRange(typ Type, start, end int) (*Value, error) {
	if start < r.base || end < start || end > r.end {
		return nil, fmt.Errorf("%w: range [%d:%d) outside region [%d:%d)",
			ErrOffsetRange, start, end, r.base, r.end)
	}
	child := &reader{data: r.data, base: start, end: end, logicalEnd: end}
	v, err := child.decode(typ)
	if err != nil {
		return nil, err
	}
	if len(v.raw) != end-start {
		return nil, fmt.Errorf("%w: value consumed %d of %d bytes",
			ErrTrailingBytes, len(v.raw), end-start)
	}
	return v, nil
}

// readOpenEnded decodes a self-delimiting value beginning at start whose exact
// logical end is not given by an offset. The hard readable boundary is limit;
// the returned value's raw length reflects its true content so callers can
// verify it lands exactly on the expected boundary.
func (r *reader) readOpenEnded(typ Type, start, limit int) (*Value, error) {
	if start < r.base || limit < start || limit > r.end {
		return nil, fmt.Errorf("%w: open range [%d:%d) outside region",
			ErrOffsetRange, start, limit)
	}
	child := &reader{data: r.data, base: start, end: limit, logicalEnd: -1}
	return child.decode(typ)
}

func (r *reader) decode(typ Type) (*Value, error) {
	switch t := typ.(type) {
	case *BasicType:
		return r.decodeBasic(t)
	case *VectorType:
		return r.decodeVector(t)
	case *ListType:
		return r.decodeList(t)
	case *BitvectorType:
		return r.decodeBitvector(t)
	case *BitlistType:
		return r.decodeBitlist(t)
	case *ContainerType:
		return r.decodeContainer(t)
	case *UnionType:
		return r.decodeUnion(t)
	default:
		return nil, fmt.Errorf("ssz: unsupported type %T", typ)
	}
}

func (r *reader) decodeBasic(t *BasicType) (*Value, error) {
	if r.length() != int(t.size) {
		if r.length() < int(t.size) {
			return nil, fmt.Errorf("%w: basic needs %d bytes, got %d",
				ErrTruncated, t.size, r.length())
		}
		return nil, fmt.Errorf("%w: basic value has %d extra byte(s)",
			ErrTrailingBytes, r.length()-int(t.size))
	}
	raw := r.raw()
	if t.isBool && raw[0] > 1 {
		return nil, fmt.Errorf("%w: 0x%02x", ErrBoolValue, raw[0])
	}
	return &Value{typ: t, raw: raw}, nil
}

func (r *reader) decodeVector(t *VectorType) (*Value, error) {
	n := t.Length
	if t.elemSize != 0 {
		need := t.elemSize * n
		if r.length() != int(need) {
			if r.length() < int(need) {
				return nil, fmt.Errorf("%w: vector needs %d bytes, got %d",
					ErrTruncated, need, r.length())
			}
			return nil, fmt.Errorf("%w: vector has %d extra byte(s)",
				ErrTrailingBytes, r.length()-int(need))
		}
		children, err := r.decodeFixedElements(t.Elem, n, t.elemSize)
		if err != nil {
			return nil, err
		}
		return &Value{typ: t, raw: r.raw(), children: children}, nil
	}

	offsets, err := r.checkOffsetTable(0, int(4*n), n)
	if err != nil {
		return nil, err
	}
	children, err := r.decodeOffsetElements(t.Elem, offsets, false)
	if err != nil {
		return nil, err
	}
	return &Value{typ: t, raw: r.raw(), children: children}, nil
}

func (r *reader) decodeList(t *ListType) (*Value, error) {
	if t.elemSize != 0 {
		span := r.span()
		if span%int(t.elemSize) != 0 {
			return nil, fmt.Errorf("%w: list of %d-byte elements has %d bytes",
				ErrTruncated, t.elemSize, span)
		}
		n := uint64(span) / t.elemSize
		if n > t.MaxLength {
			return nil, fmt.Errorf("%w: %d > %d elements",
				ErrLengthExceeded, n, t.MaxLength)
		}
		children, err := r.decodeFixedElements(t.Elem, n, t.elemSize)
		if err != nil {
			return nil, err
		}
		return &Value{typ: t, raw: clone(r.data[r.base : r.base+span]), children: children}, nil
	}

	if r.length() == 0 {
		return &Value{typ: t, raw: []byte{}, children: []*Value{}}, nil
	}
	if r.length() < 4 {
		return nil, fmt.Errorf("%w: variable list cannot fit first offset", ErrTruncated)
	}
	first := readUint32(r.data[r.base : r.base+4])
	if first%4 != 0 || first < 4 {
		return nil, fmt.Errorf("%w: first offset %d is not a valid offset-table size",
			ErrOffsetFirst, first)
	}
	n := uint64(first) / 4
	if n > t.MaxLength {
		return nil, fmt.Errorf("%w: %d > %d elements", ErrLengthExceeded, n, t.MaxLength)
	}
	offsets, err := r.checkOffsetTable(0, int(first), n)
	if err != nil {
		return nil, err
	}
	children, err := r.decodeOffsetElements(t.Elem, offsets, r.logicalEnd < 0)
	if err != nil {
		return nil, err
	}
	end := r.base + r.length()
	if r.logicalEnd < 0 {
		last := n - 1
		end = r.base + int(offsets[last]) + len(children[last].raw)
	}
	return &Value{typ: t, raw: clone(r.data[r.base:end]), children: children}, nil
}

func (r *reader) decodeBitvector(t *BitvectorType) (*Value, error) {
	need := t.FixedSize()
	if r.length() != int(need) {
		if r.length() < int(need) {
			return nil, fmt.Errorf("%w: bitvector needs %d bytes, got %d",
				ErrTruncated, need, r.length())
		}
		return nil, fmt.Errorf("%w: bitvector has extra byte(s)", ErrTrailingBytes)
	}
	bits := make([]bool, t.Length)
	for i := uint64(0); i < t.Length; i++ {
		bits[i] = r.data[r.base+int(i/8)]&(1<<(i%8)) != 0
	}
	if padding := t.Length % 8; padding != 0 {
		mask := byte(0xff) << padding
		if r.data[r.end-1]&mask != 0 {
			return nil, fmt.Errorf("%w: bitvector padding bits must be zero", ErrTrailingBytes)
		}
	}
	return &Value{typ: t, raw: r.raw(), bits: bits}, nil
}

func (r *reader) decodeBitlist(t *BitlistType) (*Value, error) {
	if r.length() == 0 {
		return nil, fmt.Errorf("%w: bitlist requires a delimiter bit", ErrBitlistDelimiter)
	}
	lastIdx := r.length() - 1
	if r.logicalEnd < 0 {
		// Open-ended: locate the delimiter byte instead of assuming the hard
		// boundary is the end. Bytes after it are left for the parent's strict
		// boundary check.
		found := -1
		for i := 0; i < r.length(); i++ {
			if r.data[r.base+i] != 0 {
				found = i // first non-zero byte carries the terminator bit
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("%w: no delimiter bit in region", ErrBitlistDelimiter)
		}
		lastIdx = found
	}
	last := r.data[r.base+lastIdx]
	if last == 0 {
		return nil, fmt.Errorf("%w: final byte has no set delimiter bit", ErrBitlistDelimiter)
	}
	highest := 7
	for last&(1<<highest) == 0 {
		highest--
	}
	bitCount := uint64(lastIdx)*8 + uint64(highest)
	if bitCount > t.MaxLength {
		return nil, fmt.Errorf("%w: %d > %d bits", ErrLengthExceeded, bitCount, t.MaxLength)
	}
	bits := make([]bool, bitCount)
	for i := uint64(0); i < bitCount; i++ {
		bits[i] = r.data[r.base+int(i/8)]&(1<<(i%8)) != 0
	}
	rawLen := lastIdx + 1
	return &Value{typ: t, raw: clone(r.data[r.base : r.base+rawLen]), bits: bits}, nil
}

func (r *reader) decodeContainer(t *ContainerType) (*Value, error) {
	type varSlot struct {
		fieldIdx int
	}
	var varSlots []varSlot
	fixedPartLen := 0
	cursor := 0
	offsetPos := -1
	for i, f := range t.Fields {
		if f.Type.FixedSize() == 0 {
			if offsetPos < 0 {
				offsetPos = cursor
			}
			varSlots = append(varSlots, varSlot{fieldIdx: i})
			fixedPartLen += 4
			cursor += 4
		} else {
			size := int(f.Type.FixedSize())
			fixedPartLen += size
			cursor += size
		}
	}

	if len(varSlots) == 0 {
		if r.length() != fixedPartLen {
			if r.length() < fixedPartLen {
				return nil, fmt.Errorf("%w: container needs %d bytes, got %d",
					ErrTruncated, fixedPartLen, r.length())
			}
			return nil, fmt.Errorf("%w: container has %d extra byte(s)",
				ErrTrailingBytes, r.length()-fixedPartLen)
		}
	}
	if r.length() < fixedPartLen {
		return nil, fmt.Errorf("%w: container fixed part needs %d bytes, got %d",
			ErrTruncated, fixedPartLen, r.length())
	}

	var offsets []uint32
	if len(varSlots) > 0 {
		var err error
		offsets, err = r.checkOffsetTable(offsetPos, fixedPartLen, uint64(len(varSlots)))
		if err != nil {
			return nil, err
		}
	}

	children := make([]*Value, len(t.Fields))
	fixedCursor := r.base
	varCursor := 0
	for i, f := range t.Fields {
		if f.Type.FixedSize() != 0 {
			size := int(f.Type.FixedSize())
			child, err := r.readRange(f.Type, fixedCursor, fixedCursor+size)
			if err != nil {
				return nil, fmt.Errorf("container field %d: %w", i, err)
			}
			children[i] = child
			fixedCursor += size
			continue
		}

		start := r.base + int(offsets[varCursor])
		end := r.base + r.length()
		if varCursor+1 < len(offsets) {
			end = r.base + int(offsets[varCursor+1])
		}
		var (
			child *Value
			err   error
		)
		if varCursor+1 < len(offsets) {
			child, err = r.readRange(f.Type, start, end)
		} else {
			// The final variable field determines its own length. Decode it up
			// to the hard boundary, then verify it lands exactly on it.
			child, err = r.readOpenEnded(f.Type, start, end)
		}
		if err != nil {
			return nil, fmt.Errorf("container field %d: %w", i, err)
		}
		children[i] = child
		varCursor++
	}
	if len(varSlots) > 0 {
		lastField := varSlots[len(varSlots)-1].fieldIdx
		lastStart := r.base + int(offsets[len(offsets)-1])
		if lastStart+len(children[lastField].raw) != r.base+r.length() {
			return nil, fmt.Errorf("%w: unreachable bytes after last container field",
				ErrTrailingBytes)
		}
	}
	return &Value{typ: t, raw: r.raw(), children: children}, nil
}

func (r *reader) decodeUnion(t *UnionType) (*Value, error) {
	if r.length() == 0 {
		return nil, fmt.Errorf("%w: union missing selector", ErrTruncated)
	}
	selector := int(r.data[r.base])
	if selector >= len(t.Options) {
		return nil, fmt.Errorf("%w: selector %d, union has %d options",
			ErrUnionSelector, selector, len(t.Options))
	}
	memberType := t.Options[selector]

	if memberType.FixedSize() != 0 {
		child, err := r.readRange(memberType, r.base+1, r.end)
		if err != nil {
			return nil, fmt.Errorf("union option %d: %w", selector, err)
		}
		return &Value{typ: t, raw: r.raw(), children: []*Value{child}, selector: selector}, nil
	}

	if r.length() < 5 {
		return nil, fmt.Errorf("%w: union variable option missing offset", ErrTruncated)
	}
	body := r.sub(r.base+1, r.end)
	offsets, err := body.checkOffsetTable(0, 4, 1)
	if err != nil {
		return nil, err
	}
	if offsets[0] != 4 {
		return nil, fmt.Errorf("%w: union offset got %d, want 4", ErrOffsetFirst, offsets[0])
	}
	child, err := r.readRange(memberType, r.base+1+4, r.end)
	if err != nil {
		return nil, fmt.Errorf("union option %d: %w", selector, err)
	}
	return &Value{typ: t, raw: r.raw(), children: []*Value{child}, selector: selector}, nil
}

// checkOffsetTable validates count offsets located at relative offsetPos.
// fixedPartLen is the total fixed-part size; the first offset must equal it,
// offsets must be non-decreasing, and every offset must stay within the region.
func (r *reader) checkOffsetTable(offsetPos, fixedPartLen int, count uint64) ([]uint32, error) {
	if count == 0 {
		return nil, nil
	}
	tableLen := int(4 * count)
	if offsetPos < 0 || r.length() < offsetPos+tableLen || fixedPartLen < offsetPos+tableLen {
		return nil, fmt.Errorf("%w: cannot fit %d offsets in %d bytes",
			ErrTruncated, count, r.length())
	}
	offsets := make([]uint32, count)
	for i := uint64(0); i < count; i++ {
		pos := r.base + offsetPos + int(4*i)
		offsets[i] = readUint32(r.data[pos : pos+4])
	}
	if int(offsets[0]) != fixedPartLen {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrOffsetFirst, offsets[0], fixedPartLen)
	}
	for i := 1; i < len(offsets); i++ {
		if offsets[i] < offsets[i-1] {
			return nil, fmt.Errorf("%w: offset %d (%d) < offset %d (%d)",
				ErrOffsetOrder, i, offsets[i], i-1, offsets[i-1])
		}
	}
	for i, off := range offsets {
		if int(off) > r.length() {
			return nil, fmt.Errorf("%w: offset %d (%d) > region length %d",
				ErrOffsetRange, i, off, r.length())
		}
	}
	return offsets, nil
}

func (r *reader) decodeFixedElements(elem Type, n, elemSize uint64) ([]*Value, error) {
	children := make([]*Value, n)
	for i := uint64(0); i < n; i++ {
		start := r.base + int(i*elemSize)
		child, err := r.readRange(elem, start, start+int(elemSize))
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		children[i] = child
	}
	return children, nil
}

func (r *reader) decodeOffsetElements(elem Type, offsets []uint32, openEnded bool) ([]*Value, error) {
	children := make([]*Value, len(offsets))
	for i, rel := range offsets {
		start := r.base + int(rel)
		if i+1 < len(offsets) {
			end := r.base + int(offsets[i+1])
			child, err := r.readRange(elem, start, end)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			children[i] = child
		} else {
			var (
				child *Value
				err   error
			)
			if openEnded {
				child, err = r.readOpenEnded(elem, start, r.end)
			} else {
				// The last element runs to the region end; readRange requires it
				// to account for every remaining byte.
				child, err = r.readRange(elem, start, r.end)
			}
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			children[i] = child
		}
	}
	return children, nil
}

func clone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
