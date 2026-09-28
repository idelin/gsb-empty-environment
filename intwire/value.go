// Package intwire defines a canonical wire format for signed, unsigned and
// arbitrary-precision integers, plus a streaming JSON bridge that never
// routes values through host floats.
//
// A Value keeps three things separately:
//   - Raw:   the original lexical form exactly as received (may be empty)
//   - exact value: U, I or B depending on Kind
//   - Width: the bit-width constraint (8/16/32/64, or 0 for unbounded big)
package intwire

import (
	"errors"
	"fmt"
	"math/big"
)

// Kind classifies the integer domain of a Value.
type Kind uint8

const (
	KindUnsigned Kind = iota // fixed-width unsigned, exact value in U
	KindSigned               // fixed-width signed, exact value in I
	KindBig                  // arbitrary precision, exact value in B (Width == 0)
)

func (k Kind) String() string {
	switch k {
	case KindUnsigned:
		return "u"
	case KindSigned:
		return "s"
	case KindBig:
		return "big"
	}
	return fmt.Sprintf("kind(%d)", uint8(k))
}

// SupportedWidths lists the fixed bit-width constraints this library
// serializes. Arbitrary-precision values use Width == 0.
var SupportedWidths = []int{8, 16, 32, 64}

func validWidth(w int) bool {
	switch w {
	case 8, 16, 32, 64:
		return true
	}
	return false
}

var (
	ErrWidth   = errors.New("intwire: unsupported or mismatched bit width")
	ErrRange   = errors.New("intwire: value out of range for declared bit width")
	ErrBadKind = errors.New("intwire: invalid kind")
)

// Value is one integer with its kind, bit-width constraint, exact value and
// (optionally) the raw lexeme it was decoded from.
type Value struct {
	Kind  Kind
	Width int      // 8/16/32/64 for fixed kinds; 0 for KindBig
	U     uint64   // exact value when Kind == KindUnsigned
	I     int64    // exact value when Kind == KindSigned
	B     *big.Int // exact value when Kind == KindBig
	Raw   string   // original lexeme, preserved verbatim; "" if constructed
}

// NewUnsigned builds a fixed-width unsigned value. Negatives are
// unrepresentable by construction; overflow past width is rejected.
func NewUnsigned(width int, u uint64) (Value, error) {
	if !validWidth(width) {
		return Value{}, ErrWidth
	}
	if width < 64 && u>>(uint(width)) != 0 {
		return Value{}, fmt.Errorf("%w: %d does not fit u%d", ErrRange, u, width)
	}
	return Value{Kind: KindUnsigned, Width: width, U: u}, nil
}

// NewSigned builds a fixed-width signed value with range checking.
func NewSigned(width int, i int64) (Value, error) {
	if !validWidth(width) {
		return Value{}, ErrWidth
	}
	if width < 64 {
		min := -(int64(1) << (uint(width) - 1))
		max := (int64(1) << (uint(width) - 1)) - 1
		if i < min || i > max {
			return Value{}, fmt.Errorf("%w: %d does not fit i%d", ErrRange, i, width)
		}
	}
	return Value{Kind: KindSigned, Width: width, I: i}, nil
}

// NewBig builds an arbitrary-precision value (Width == 0). The integer is
// copied so later mutation of the argument cannot alias the Value.
func NewBig(b *big.Int) Value {
	return Value{Kind: KindBig, B: new(big.Int).Set(b)}
}

// Big returns the exact value as a *big.Int regardless of kind.
func (v Value) Big() *big.Int {
	switch v.Kind {
	case KindUnsigned:
		return new(big.Int).SetUint64(v.U)
	case KindSigned:
		return big.NewInt(v.I)
	case KindBig:
		return new(big.Int).Set(v.B)
	}
	return nil
}

// Decimal renders the exact value in canonical decimal form.
func (v Value) Decimal() string { return v.Big().String() }

// Equal reports exact equality of kind, width and value (Raw is ignored:
// the same value may arrive through different lexemes).
func (v Value) Equal(o Value) bool {
	if v.Kind != o.Kind || v.Width != o.Width {
		return false
	}
	switch v.Kind {
	case KindUnsigned:
		return v.U == o.U
	case KindSigned:
		return v.I == o.I
	case KindBig:
		return v.B.Cmp(o.B) == 0
	}
	return false
}

// validate re-checks internal consistency before encoding.
func (v Value) validate() error {
	switch v.Kind {
	case KindUnsigned:
		_, err := NewUnsigned(v.Width, v.U)
		return err
	case KindSigned:
		_, err := NewSigned(v.Width, v.I)
		return err
	case KindBig:
		if v.Width != 0 {
			return ErrWidth
		}
		if v.B == nil {
			return ErrBadKind
		}
		return nil
	}
	return ErrBadKind
}
