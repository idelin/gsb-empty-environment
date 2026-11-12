package netsec

import (
	"errors"
	"fmt"
	"math"
)

// XDR (RFC 4506) primitives plus a small shape language used to skip over
// transaction-body fields that the verifier does not need to interpret.

var errXDR = errors.New("netsec: malformed xdr")

type xdrReader struct {
	buf []byte
	pos int
}

func (r *xdrReader) remaining() []byte { return r.buf[r.pos:] }

func (r *xdrReader) read(n int) ([]byte, error) {
	if n < 0 || r.pos > len(r.buf)-n {
		return nil, errXDR
	}
	out := r.buf[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

func (r *xdrReader) u32() (uint32, error) {
	b, err := r.read(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]), nil
}

func (r *xdrReader) i32() (int32, error) {
	v, err := r.u32()
	return int32(v), err
}

func (r *xdrReader) u64() (uint64, error) {
	b, err := r.read(8)
	if err != nil {
		return 0, err
	}
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v, nil
}

func (r *xdrReader) i64() (int64, error) {
	v, err := r.u64()
	return int64(v), err
}

func (r *xdrReader) boolean() (bool, error) {
	v, err := r.u32()
	if err != nil {
		return false, err
	}
	if v != 0 && v != 1 {
		return false, fmt.Errorf("%w: non-canonical boolean %d", errXDR, v)
	}
	return v == 1, nil
}

func (r *xdrReader) fixedOpaque(n int) ([]byte, error) {
	return r.read(n)
}

func (r *xdrReader) varOpaque(max int) ([]byte, error) {
	n, err := r.u32()
	if err != nil {
		return nil, err
	}
	if int(n) > max || n > math.MaxInt32 {
		return nil, fmt.Errorf("%w: opaque length %d exceeds max %d", errXDR, n, max)
	}
	out, err := r.read(int(n))
	if err != nil {
		return nil, err
	}
	// XDR variable-length data is padded to a 4-byte boundary.
	if pad := (4 - int(n)%4) % 4; pad > 0 {
		if _, err := r.read(pad); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *xdrReader) xdrString(max int) (string, error) {
	b, err := r.varOpaque(max)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// shape knows how to skip one XDR value.
type shape interface {
	skip(r *xdrReader) error
}

type primKind int

const (
	primU32 primKind = iota
	primI32
	primI64
	primU64
	primBool
	primString
	primVarOpaque
)

type primShape struct {
	kind primKind
	max  int // string / variable opaque bound
}

func (s primShape) skip(r *xdrReader) error {
	switch s.kind {
	case primU32:
		_, err := r.u32()
		return err
	case primI32:
		_, err := r.i32()
		return err
	case primI64:
		_, err := r.i64()
		return err
	case primU64:
		_, err := r.u64()
		return err
	case primBool:
		_, err := r.boolean()
		return err
	case primString:
		_, err := r.xdrString(s.max)
		return err
	case primVarOpaque:
		_, err := r.varOpaque(s.max)
		return err
	default:
		return errors.New("netsec: unknown primitive shape")
	}
}

type fixedOpaqueShape struct{ n int }

func (s fixedOpaqueShape) skip(r *xdrReader) error {
	_, err := r.fixedOpaque(s.n)
	return err
}

type structShape struct{ fields []shape }

func (s structShape) skip(r *xdrReader) error {
	for _, f := range s.fields {
		if err := f.skip(r); err != nil {
			return err
		}
	}
	return nil
}

type refShape struct{ name string }

func (s refShape) skip(r *xdrReader) error {
	t, ok := shapeRegistry[s.name]
	if !ok {
		return fmt.Errorf("netsec: unknown shape %q", s.name)
	}
	return t.skip(r)
}

type optionalShape struct{ inner shape }

func (s optionalShape) skip(r *xdrReader) error {
	present, err := r.boolean()
	if err != nil {
		return err
	}
	if present {
		return s.inner.skip(r)
	}
	return nil
}

type fixedArrayShape struct {
	n     int
	inner shape
}

func (s fixedArrayShape) skip(r *xdrReader) error {
	for i := 0; i < s.n; i++ {
		if err := s.inner.skip(r); err != nil {
			return err
		}
	}
	return nil
}

type varArrayShape struct {
	max   int
	inner shape
}

func (s varArrayShape) skip(r *xdrReader) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	if int(n) > s.max || n > math.MaxInt32 {
		return fmt.Errorf("%w: array length %d exceeds max %d", errXDR, n, s.max)
	}
	for i := uint32(0); i < n; i++ {
		if err := s.inner.skip(r); err != nil {
			return err
		}
	}
	return nil
}

// unionShape dispatches on a uint32 discriminant. A nil arm means the void
// arm carries no further bytes. An unknown discriminant is rejected.
type unionShape struct {
	arms map[uint32]shape
}

func (s unionShape) skip(r *xdrReader) error {
	disc, err := r.u32()
	if err != nil {
		return err
	}
	arm, ok := s.arms[disc]
	if !ok {
		return fmt.Errorf("%w: unknown union discriminant %d", errXDR, disc)
	}
	if arm == nil {
		return nil
	}
	return arm.skip(r)
}

var shapeRegistry = map[string]shape{}

func registerShape(name string, s shape) { shapeRegistry[name] = s }

// Shapes reused across definitions.
var (
	shU32       = primShape{kind: primU32}
	shI32       = primShape{kind: primI32}
	shI64       = primShape{kind: primI64}
	shU64       = primShape{kind: primU64}
	shBool      = primShape{kind: primBool}
	shOpaque32  = fixedOpaqueShape{n: 32}
	shOpaque4   = fixedOpaqueShape{n: 4}
	shString28  = primShape{kind: primString, max: 28}
	shString32  = primShape{kind: primString, max: 32}
	shString64  = primShape{kind: primString, max: 64}
	shDataValue = primShape{kind: primVarOpaque, max: 64}
	shVoidUnion = unionShape{arms: map[uint32]shape{0: nil}}
)
