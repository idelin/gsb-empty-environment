package gsbdiff

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
)

// Wire format (all integers little-endian):
//
//	"GDF1"                          magic
//	u64 baseLen, 32B baseHash       baseline summary
//	u32 opCount, ops...             operations
//
// op:      u8 kind, u32 pathLen, pathSegs..., payload
// pathSeg: u8 tag (0=key,1=index); key: u32 len + bytes; index: u32
// payload: OpSet -> value; OpDel -> nothing; OpArrayEdit -> u32 n, edits...
// edit:    u8 kind; keep/delete: u32 n; insert: u32 count, values...
// value:   u8 tag (0 null,1 false,2 true,3 f64,4 string,5 array,6 object)
//
// The decoder validates every declared count/length against the remaining
// input bytes before allocating, and never executes host code: the stream
// carries plain data only.

var (
	ErrTruncated = errors.New("gsbdiff: truncated or invalid encoding")
	ErrBadLength = errors.New("gsbdiff: declared length exceeds remaining input")
	ErrTrailing  = errors.New("gsbdiff: trailing bytes after diff")
	ErrBadMagic  = errors.New("gsbdiff: bad magic")
)

var magic = []byte{'G', 'D', 'F', '1'}

type writer struct {
	buf []byte
	err error
}

func (w *writer) u8(v byte)      { w.buf = append(w.buf, v) }
func (w *writer) u32(v uint32)   { w.buf = binary.LittleEndian.AppendUint32(w.buf, v) }
func (w *writer) u64(v uint64)   { w.buf = binary.LittleEndian.AppendUint64(w.buf, v) }
func (w *writer) bytes(b []byte) { w.buf = append(w.buf, b...) }

// Encode serializes a diff into the binary wire format.
func Encode(d *Diff) ([]byte, error) {
	if d == nil {
		return nil, ErrNilDiff
	}
	w := &writer{}
	w.bytes(magic)
	w.u64(d.BaseLen)
	w.bytes(d.BaseHash[:])
	w.u32(uint32(len(d.Ops)))
	for _, op := range d.Ops {
		w.u8(byte(op.Kind))
		w.u32(uint32(len(op.Path)))
		for _, seg := range op.Path {
			if seg.IsIndex {
				if seg.Index < 0 {
					return nil, fmt.Errorf("%w: negative path index", ErrTruncated)
				}
				w.u8(1)
				w.u32(uint32(seg.Index))
			} else {
				w.u8(0)
				w.u32(uint32(len(seg.Key)))
				w.bytes([]byte(seg.Key))
			}
		}
		switch op.Kind {
		case OpSet:
			if err := encodeValue(w, op.Value, 0); err != nil {
				return nil, err
			}
		case OpDel:
		case OpArrayEdit:
			w.u32(uint32(len(op.Edits)))
			for _, e := range op.Edits {
				w.u8(byte(e.Kind))
				switch e.Kind {
				case EditKeep, EditDelete:
					if e.N < 0 {
						return nil, fmt.Errorf("%w: negative edit count", ErrTruncated)
					}
					w.u32(uint32(e.N))
				case EditInsert:
					w.u32(uint32(len(e.Values)))
					for _, v := range e.Values {
						if err := encodeValue(w, v, 0); err != nil {
							return nil, err
						}
					}
				default:
					return nil, fmt.Errorf("%w: unknown edit kind %d", ErrTruncated, e.Kind)
				}
			}
		default:
			return nil, fmt.Errorf("%w: unknown op kind %d", ErrTruncated, op.Kind)
		}
	}
	return w.buf, nil
}

func encodeValue(w *writer, v any, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("%w: nesting too deep", ErrUnsupportedValue)
	}
	switch t := v.(type) {
	case nil:
		w.u8(0)
	case bool:
		if t {
			w.u8(2)
		} else {
			w.u8(1)
		}
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return fmt.Errorf("%w: NaN/Inf number", ErrUnsupportedValue)
		}
		w.u8(3)
		w.u64(math.Float64bits(t))
	case string:
		w.u8(4)
		w.u32(uint32(len(t)))
		w.bytes([]byte(t))
	case []any:
		w.u8(5)
		w.u32(uint32(len(t)))
		for _, e := range t {
			if err := encodeValue(w, e, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		w.u8(6)
		w.u32(uint32(len(t)))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w.u32(uint32(len(k)))
			w.bytes([]byte(k))
			if err := encodeValue(w, t[k], depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedValue, v)
	}
	return nil
}

type reader struct {
	buf []byte
	off int
}

func (r *reader) remaining() int { return len(r.buf) - r.off }

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || r.remaining() < n {
		return nil, ErrTruncated
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b, nil
}

func (r *reader) u8() (byte, error) {
	b, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *reader) u32() (uint32, error) {
	b, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (r *reader) u64() (uint64, error) {
	b, err := r.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

// count reads a u32 count and validates it against the remaining input
// BEFORE the caller allocates: every encoded element costs at least one
// byte, so count > remaining is always invalid.
func (r *reader) count() (int, error) {
	n, err := r.u32()
	if err != nil {
		return 0, err
	}
	if uint64(n) > uint64(r.remaining()) {
		return 0, ErrBadLength
	}
	return int(n), nil
}

// Decode parses a diff from the wire format. All lengths are validated
// before allocation; trailing garbage is rejected.
func Decode(b []byte) (*Diff, error) {
	r := &reader{buf: b}
	m, err := r.take(4)
	if err != nil {
		return nil, err
	}
	if string(m) != string(magic) {
		return nil, ErrBadMagic
	}
	baseLen, err := r.u64()
	if err != nil {
		return nil, err
	}
	hashBytes, err := r.take(32)
	if err != nil {
		return nil, err
	}
	d := &Diff{BaseLen: baseLen}
	copy(d.BaseHash[:], hashBytes)
	opCount, err := r.count()
	if err != nil {
		return nil, err
	}
	d.Ops = make([]Op, 0, opCount)
	for i := 0; i < opCount; i++ {
		op, err := decodeOp(r)
		if err != nil {
			return nil, err
		}
		d.Ops = append(d.Ops, op)
	}
	if r.remaining() != 0 {
		return nil, ErrTrailing
	}
	return d, nil
}

func decodeOp(r *reader) (Op, error) {
	var op Op
	kind, err := r.u8()
	if err != nil {
		return op, err
	}
	op.Kind = OpKind(kind)
	pathLen, err := r.count()
	if err != nil {
		return op, err
	}
	op.Path = make([]Seg, 0, pathLen)
	for i := 0; i < pathLen; i++ {
		tag, err := r.u8()
		if err != nil {
			return op, err
		}
		switch tag {
		case 0:
			n, err := r.count()
			if err != nil {
				return op, err
			}
			b, err := r.take(n)
			if err != nil {
				return op, err
			}
			op.Path = append(op.Path, Seg{Key: string(b)})
		case 1:
			idx, err := r.u32()
			if err != nil {
				return op, err
			}
			op.Path = append(op.Path, Seg{Index: int(idx), IsIndex: true})
		default:
			return op, fmt.Errorf("%w: bad path segment tag %d", ErrTruncated, tag)
		}
	}
	switch op.Kind {
	case OpSet:
		v, err := decodeValue(r, 0)
		if err != nil {
			return op, err
		}
		op.Value = v
	case OpDel:
	case OpArrayEdit:
		n, err := r.count()
		if err != nil {
			return op, err
		}
		op.Edits = make([]Edit, 0, n)
		for i := 0; i < n; i++ {
			e, err := decodeEdit(r)
			if err != nil {
				return op, err
			}
			op.Edits = append(op.Edits, e)
		}
	default:
		return op, fmt.Errorf("%w: unknown op kind %d", ErrTruncated, op.Kind)
	}
	return op, nil
}

func decodeEdit(r *reader) (Edit, error) {
	var e Edit
	kind, err := r.u8()
	if err != nil {
		return e, err
	}
	e.Kind = EditKind(kind)
	switch e.Kind {
	case EditKeep, EditDelete:
		n, err := r.u32()
		if err != nil {
			return e, err
		}
		e.N = int(n)
	case EditInsert:
		n, err := r.count()
		if err != nil {
			return e, err
		}
		e.Values = make([]any, 0, n)
		for i := 0; i < n; i++ {
			v, err := decodeValue(r, 0)
			if err != nil {
				return e, err
			}
			e.Values = append(e.Values, v)
		}
	default:
		return e, fmt.Errorf("%w: unknown edit kind %d", ErrTruncated, e.Kind)
	}
	return e, nil
}

func decodeValue(r *reader, depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w: nesting too deep", ErrUnsupportedValue)
	}
	tag, err := r.u8()
	if err != nil {
		return nil, err
	}
	switch tag {
	case 0:
		return nil, nil
	case 1:
		return false, nil
	case 2:
		return true, nil
	case 3:
		bits, err := r.u64()
		if err != nil {
			return nil, err
		}
		f := math.Float64frombits(bits)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%w: NaN/Inf number", ErrUnsupportedValue)
		}
		return f, nil
	case 4:
		n, err := r.count()
		if err != nil {
			return nil, err
		}
		b, err := r.take(n)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case 5:
		n, err := r.count()
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			v, err := decodeValue(r, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case 6:
		n, err := r.count()
		if err != nil {
			return nil, err
		}
		out := make(map[string]any, n)
		for i := 0; i < n; i++ {
			kl, err := r.count()
			if err != nil {
				return nil, err
			}
			kb, err := r.take(kl)
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(r, depth+1)
			if err != nil {
				return nil, err
			}
			out[string(kb)] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: unknown value tag %d", ErrTruncated, tag)
}
