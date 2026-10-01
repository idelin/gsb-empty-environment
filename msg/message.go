// Package msg is a hand-rolled, reflection-free codec for a demo schema
// that exercises every supported wire feature: varint, zigzag, fixed32/64,
// strings, nested messages, packed repeated fields and repeated messages.
//
//	message Inner {
//	  int32  id   = 1;             // varint
//	  string name = 2;             // length-delimited
//	}
//	message Outer {
//	  int64          big    = 1;   // varint
//	  sint32         delta  = 2;   // zigzag varint
//	  fixed64        f64    = 3;
//	  fixed32        f32    = 4;
//	  string         label  = 5;
//	  Inner          inner  = 6;   // nested message
//	  repeated int32 nums   = 7;   // packed
//	  repeated Inner items  = 8;   // repeated nested
//	}
//
// All message values come from object pools; decoding a warm pool performs
// zero heap allocations. String/bytes fields are zero-copy []byte views
// into the input buffer and must not be retained after the input is freed.
package msg

import (
	"errors"
	"sync"
	"sync/atomic"

	"protowire/wire"
)

// Field numbers and wire types, matching the schema above.
const (
	fieldInnerID   = 1
	fieldInnerName = 2

	fieldOuterBig   = 1
	fieldOuterDelta = 2
	fieldOuterF64   = 3
	fieldOuterF32   = 4
	fieldOuterLabel = 5
	fieldOuterInner = 6
	fieldOuterNums  = 7
	fieldOuterItems = 8
)

// ErrNegativeLength guards against corrupt packed-field lengths.
var ErrNegativeLength = errors.New("msg: invalid packed field length")

var (
	innerPool = sync.Pool{New: func() any { return &Inner{} }}
	outerPool = sync.Pool{New: func() any { return &Outer{} }}

	innerAcquired atomic.Int64
	innerReleased atomic.Int64
	outerAcquired atomic.Int64
	outerReleased atomic.Int64
)

// PoolStats reports acquire/release counters for leak assertions.
type PoolStats struct {
	InnerAcquired, InnerReleased int64
	OuterAcquired, OuterReleased int64
}

// Stats returns the current pool counters. At quiesce, acquired must equal
// released for both message types.
func Stats() PoolStats {
	return PoolStats{
		InnerAcquired: innerAcquired.Load(),
		InnerReleased: innerReleased.Load(),
		OuterAcquired: outerAcquired.Load(),
		OuterReleased: outerReleased.Load(),
	}
}

// Inner is a nested message. Name is a zero-copy view into the input.
type Inner struct {
	ID   int32
	Name []byte
}

// Reset zeroes the message for reuse, retaining slice capacity.
func (m *Inner) Reset() {
	m.ID = 0
	m.Name = m.Name[:0]
}

// AcquireInner leases a reset Inner from the pool.
func AcquireInner() *Inner {
	innerAcquired.Add(1)
	m := innerPool.Get().(*Inner)
	m.Reset()
	return m
}

// Release returns m to the pool.
func (m *Inner) Release() {
	if m == nil {
		return
	}
	m.Reset()
	innerReleased.Add(1)
	innerPool.Put(m)
}

// Outer is the top-level demo message.
type Outer struct {
	Big   int64
	Delta int32
	F64   uint64
	F32   uint32
	Label []byte
	Inner *Inner
	Nums  []int32
	Items []*Inner
}

// Reset zeroes the message for reuse. Nested messages are released back to
// their pool; slice capacity is retained to keep decoding allocation-free.
func (m *Outer) Reset() {
	m.Big = 0
	m.Delta = 0
	m.F64 = 0
	m.F32 = 0
	m.Label = m.Label[:0]
	if m.Inner != nil {
		m.Inner.Release()
		m.Inner = nil
	}
	m.Nums = m.Nums[:0]
	for _, it := range m.Items {
		it.Release()
	}
	m.Items = m.Items[:0]
}

// AcquireOuter leases a reset Outer from the pool.
func AcquireOuter() *Outer {
	outerAcquired.Add(1)
	m := outerPool.Get().(*Outer)
	m.Reset()
	return m
}

// Release returns m (and its nested values) to the pool.
func (m *Outer) Release() {
	if m == nil {
		return
	}
	m.Reset()
	outerReleased.Add(1)
	outerPool.Put(m)
}

// DecodeInner parses one Inner message from r until the stream is
// exhausted. Unknown fields are skipped with full bounds checks.
func DecodeInner(r *wire.Reader, m *Inner) error {
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		switch num {
		case fieldInnerID:
			if wt != wire.WireVarint {
				return wire.ErrInvalidWireType
			}
			v, err := r.ReadVarint()
			if err != nil {
				return err
			}
			m.ID = int32(v)
		case fieldInnerName:
			if wt != wire.WireBytes {
				return wire.ErrInvalidWireType
			}
			b, err := r.ReadLengthDelimited()
			if err != nil {
				return err
			}
			m.Name = append(m.Name[:0], b...)
		default:
			if err := r.Skip(wt); err != nil {
				return err
			}
		}
	}
	return nil
}

// DecodeOuter parses one Outer message from r until the stream is
// exhausted. Packed repeated fields accept both packed (length-delimited)
// and unpacked (one varint per element) encodings, per the protobuf spec.
func DecodeOuter(r *wire.Reader, m *Outer) error {
	for !r.Done() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return err
		}
		switch num {
		case fieldOuterBig:
			if wt != wire.WireVarint {
				return wire.ErrInvalidWireType
			}
			v, err := r.ReadVarint()
			if err != nil {
				return err
			}
			m.Big = int64(v)
		case fieldOuterDelta:
			if wt != wire.WireVarint {
				return wire.ErrInvalidWireType
			}
			v, err := r.ReadZigZag32()
			if err != nil {
				return err
			}
			m.Delta = v
		case fieldOuterF64:
			if wt != wire.WireFixed64 {
				return wire.ErrInvalidWireType
			}
			v, err := r.ReadFixed64()
			if err != nil {
				return err
			}
			m.F64 = v
		case fieldOuterF32:
			if wt != wire.WireFixed32 {
				return wire.ErrInvalidWireType
			}
			v, err := r.ReadFixed32()
			if err != nil {
				return err
			}
			m.F32 = v
		case fieldOuterLabel:
			if wt != wire.WireBytes {
				return wire.ErrInvalidWireType
			}
			b, err := r.ReadLengthDelimited()
			if err != nil {
				return err
			}
			m.Label = append(m.Label[:0], b...)
		case fieldOuterInner:
			if wt != wire.WireBytes {
				return wire.ErrInvalidWireType
			}
			sub, err := r.Sub()
			if err != nil {
				return err
			}
			if m.Inner == nil {
				m.Inner = AcquireInner()
			}
			err = DecodeInner(sub, m.Inner)
			sub.Release()
			if err != nil {
				return err
			}
		case fieldOuterNums:
			switch wt {
			case wire.WireBytes: // packed
				b, err := r.ReadLengthDelimited()
				if err != nil {
					return err
				}
				sub := wire.AcquireReader(b)
				for !sub.Done() {
					v, err := sub.ReadVarint()
					if err != nil {
						sub.Release()
						return err
					}
					m.Nums = append(m.Nums, int32(v))
				}
				sub.Release()
			case wire.WireVarint: // unpacked form, still legal
				v, err := r.ReadVarint()
				if err != nil {
					return err
				}
				m.Nums = append(m.Nums, int32(v))
			default:
				return wire.ErrInvalidWireType
			}
		case fieldOuterItems:
			if wt != wire.WireBytes {
				return wire.ErrInvalidWireType
			}
			sub, err := r.Sub()
			if err != nil {
				return err
			}
			it := AcquireInner()
			err = DecodeInner(sub, it)
			sub.Release()
			if err != nil {
				it.Release()
				return err
			}
			m.Items = append(m.Items, it)
		default:
			if err := r.Skip(wt); err != nil {
				return err
			}
		}
	}
	return nil
}

// EncodeInner appends the wire encoding of m to w.
func EncodeInner(w *wire.Writer, m *Inner) {
	w.WriteTag(fieldInnerID, wire.WireVarint)
	w.WriteVarint(uint64(uint32(m.ID)))
	w.WriteTag(fieldInnerName, wire.WireBytes)
	w.WriteLengthDelimited(m.Name)
}

// EncodeOuter appends the wire encoding of m to w. Nums is emitted in
// packed form.
func EncodeOuter(w *wire.Writer, m *Outer) {
	w.WriteTag(fieldOuterBig, wire.WireVarint)
	w.WriteVarint(uint64(m.Big))
	w.WriteTag(fieldOuterDelta, wire.WireVarint)
	w.WriteZigZag32(m.Delta)
	w.WriteTag(fieldOuterF64, wire.WireFixed64)
	w.WriteFixed64(m.F64)
	w.WriteTag(fieldOuterF32, wire.WireFixed32)
	w.WriteFixed32(m.F32)
	w.WriteTag(fieldOuterLabel, wire.WireBytes)
	w.WriteLengthDelimited(m.Label)
	if m.Inner != nil {
		w.WriteTag(fieldOuterInner, wire.WireBytes)
		sub := wire.AcquireWriter()
		EncodeInner(sub, m.Inner)
		w.WriteLengthDelimited(sub.Bytes())
		sub.Release()
	}
	if len(m.Nums) > 0 {
		w.WriteTag(fieldOuterNums, wire.WireBytes)
		sub := wire.AcquireWriter()
		for _, n := range m.Nums {
			sub.WriteVarint(uint64(uint32(n)))
		}
		w.WriteLengthDelimited(sub.Bytes())
		sub.Release()
	}
	for _, it := range m.Items {
		w.WriteTag(fieldOuterItems, wire.WireBytes)
		sub := wire.AcquireWriter()
		EncodeInner(sub, it)
		w.WriteLengthDelimited(sub.Bytes())
		sub.Release()
	}
}
