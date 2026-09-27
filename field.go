package wireint

import (
	"errors"
	"fmt"
	"io"
	"math/big"
)

// ErrRange reports a value outside its field's bit-width constraint.
var ErrRange = errors.New("wireint: value outside field range")

// Field is a bit-width constraint for one serialized integer field.
type Field struct {
	Kind Kind
	// MaxBits applies only to KindBig: the magnitude must satisfy
	// |v| < 2^MaxBits, with 0 < MaxBits <= MaxBigIntBytes*8.
	MaxBits int
}

// Predefined fixed-width field constraints.
var (
	Uint8  = Field{Kind: KindUint8}
	Uint16 = Field{Kind: KindUint16}
	Uint32 = Field{Kind: KindUint32}
	Uint64 = Field{Kind: KindUint64}
	Int8   = Field{Kind: KindInt8}
	Int16  = Field{Kind: KindInt16}
	Int32  = Field{Kind: KindInt32}
	Int64  = Field{Kind: KindInt64}
)

// Big returns an arbitrary-precision field constraint: |v| < 2^maxBits.
func Big(maxBits int) Field { return Field{Kind: KindBig, MaxBits: maxBits} }

// MaxBigBits is the largest supported arbitrary-precision width.
const MaxBigBits = MaxBigIntBytes * 8

var (
	uint8Max  = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 8), big.NewInt(1))
	uint16Max = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 16), big.NewInt(1))
	uint32Max = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 32), big.NewInt(1))
	uint64Max = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1))
	int8Max   = big.NewInt(1<<7 - 1)
	int16Max  = big.NewInt(1<<15 - 1)
	int32Max  = big.NewInt(1<<31 - 1)
	int64Max  = big.NewInt(1<<63 - 1)
)

// Contains reports whether v satisfies the field's bit-width constraint.
// Negative values never satisfy unsigned fields.
func (f Field) Contains(v *big.Int) bool {
	switch f.Kind {
	case KindUint8:
		return v.Sign() >= 0 && v.Cmp(uint8Max) <= 0
	case KindUint16:
		return v.Sign() >= 0 && v.Cmp(uint16Max) <= 0
	case KindUint32:
		return v.Sign() >= 0 && v.Cmp(uint32Max) <= 0
	case KindUint64:
		return v.Sign() >= 0 && v.Cmp(uint64Max) <= 0
	case KindInt8:
		return v.Cmp(int8Min) >= 0 && v.Cmp(int8Max) <= 0
	case KindInt16:
		return v.Cmp(int16Min) >= 0 && v.Cmp(int16Max) <= 0
	case KindInt32:
		return v.Cmp(int32Min) >= 0 && v.Cmp(int32Max) <= 0
	case KindInt64:
		return v.Cmp(int64Min) >= 0 && v.Cmp(int64Max) <= 0
	case KindBig:
		if f.MaxBits <= 0 || f.MaxBits > MaxBigBits {
			return false
		}
		return new(big.Int).Abs(v).BitLen() <= f.MaxBits
	}
	return false
}

// maxBits returns the field's magnitude quota in bits, used to bound lexeme
// parsing before any big.Int conversion happens.
func (f Field) maxBits() int {
	if f.Kind == KindBig {
		return f.MaxBits
	}
	n, _ := kindWidth(f.Kind)
	return n * 8
}

// Value is a decoded integer field. The three facets are stored separately:
// the raw lexeme exactly as received, the exact mathematical value, and the
// bit-width constraint it was decoded against.
type Value struct {
	Raw   string   // raw lexeme (JSON number text or canonical decimal)
	Int   *big.Int // exact value, arbitrary precision
	Field Field    // bit-width constraint
}

// Tag returns the canonical JSON bridge form "iw:<kind>:<decimal>".
func (v Value) Tag() string {
	return "iw:" + v.Field.Kind.String() + ":" + v.Int.String()
}

// EncodeField validates v against f and writes it in canonical wire form.
func EncodeField(w io.Writer, f Field, v *big.Int, version int) error {
	if !f.Contains(v) {
		return fmt.Errorf("%w: %s not in %s", ErrRange, v, fieldName(f))
	}
	return Encode(w, v, version)
}

// DecodeField reads one wire integer and enforces the field constraint.
func DecodeField(r io.ByteReader, f Field, maxVersion int) (Value, error) {
	v, err := Decode(r, maxVersion)
	if err != nil {
		return Value{}, err
	}
	if !f.Contains(v) {
		return Value{}, fmt.Errorf("%w: %s not in %s", ErrRange, v, fieldName(f))
	}
	return Value{Raw: v.String(), Int: v, Field: f}, nil
}

func fieldName(f Field) string {
	if f.Kind == KindBig {
		return fmt.Sprintf("big(%d)", f.MaxBits)
	}
	return f.Kind.String()
}
