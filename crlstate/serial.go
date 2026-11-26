package crlstate

import (
	"fmt"
	"math/big"
)

// Serial is an arbitrary-precision certificate serial number.
//
// The zero value represents serial number zero. Serial values are immutable:
// methods never mutate the receiver's magnitude.
type Serial struct {
	n big.Int
}

// SerialFromUint64 returns a Serial for v.
func SerialFromUint64(v uint64) Serial {
	var s Serial
	s.n.SetUint64(v)
	return s
}

// SerialFromBigInt returns a Serial copy of v. Negative serials are rejected.
func SerialFromBigInt(v *big.Int) (Serial, error) {
	if v == nil {
		return Serial{}, fmt.Errorf("crlstate: nil serial")
	}
	if v.Sign() < 0 {
		return Serial{}, fmt.Errorf("crlstate: negative serial")
	}
	var s Serial
	s.n.Set(v)
	return s, nil
}

// ParseSerial parses a decimal serial. A 0x or 0X prefix selects hexadecimal.
func ParseSerial(text string) (Serial, error) {
	var s Serial
	base := 10
	if len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		base = 16
		text = text[2:]
	}
	if text == "" {
		return Serial{}, fmt.Errorf("crlstate: empty serial")
	}
	if _, ok := s.n.SetString(text, base); !ok {
		return Serial{}, fmt.Errorf("crlstate: invalid serial %q", text)
	}
	if s.n.Sign() < 0 {
		return Serial{}, fmt.Errorf("crlstate: negative serial %q", text)
	}
	return s, nil
}

// Big returns a copy of the serial magnitude.
func (s Serial) Big() *big.Int {
	return new(big.Int).Set(&s.n)
}

// Cmp compares s and other.
func (s Serial) Cmp(other Serial) int {
	return s.n.Cmp(&other.n)
}

// Add returns s+v.
func (s Serial) Add(v uint64) Serial {
	var out Serial
	out.n.Add(&s.n, new(big.Int).SetUint64(v))
	return out
}

// String returns the decimal representation.
func (s Serial) String() string {
	return s.n.String()
}

// GoString implements fmt.GoStringer.
func (s Serial) GoString() string {
	return "Serial(" + s.String() + ")"
}
