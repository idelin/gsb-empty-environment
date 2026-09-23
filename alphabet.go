package mtf

import (
	"errors"
	"fmt"
)

var (
	ErrUnknownSymbol   = errors.New("mtf: unknown symbol")
	ErrIndexOutOfRange = errors.New("mtf: index out of range")
	ErrDuplicateSymbol = errors.New("mtf: duplicate symbol")
	ErrEmptyAlphabet   = errors.New("mtf: empty alphabet")
	ErrCorruptStream   = errors.New("mtf: corrupt stream")
	ErrOutputLimit     = errors.New("mtf: decode output limit exceeded")
	ErrBadCheckpoint   = errors.New("mtf: invalid checkpoint")
)

// Alphabet is a configurable, versioned symbol set. The base alphabet has
// version 1; every extension via Extend bumps the version by one, so a
// checkpoint can pin the exact alphabet version it was taken against.
type Alphabet struct {
	order []byte // canonical order: base symbols, then extensions in order
	set   map[byte]struct{}
	ext   int // number of extensions applied
}

// NewAlphabet builds an alphabet from the given symbols. Duplicate symbols
// are illegal and rejected.
func NewAlphabet(symbols []byte) (*Alphabet, error) {
	if len(symbols) == 0 {
		return nil, ErrEmptyAlphabet
	}
	set := make(map[byte]struct{}, len(symbols))
	for _, s := range symbols {
		if _, ok := set[s]; ok {
			return nil, fmt.Errorf("%w: 0x%02x", ErrDuplicateSymbol, s)
		}
		set[s] = struct{}{}
	}
	return &Alphabet{order: append([]byte(nil), symbols...), set: set}, nil
}

// Size returns the current number of symbols.
func (a *Alphabet) Size() int { return len(a.order) }

// Version returns the alphabet version: 1 for the base alphabet, +1 per
// extension.
func (a *Alphabet) Version() uint32 { return uint32(1 + a.ext) }

// Contains reports whether s is in the alphabet.
func (a *Alphabet) Contains(s byte) bool {
	_, ok := a.set[s]
	return ok
}

// Symbols returns a copy of the canonical symbol order.
func (a *Alphabet) Symbols() []byte { return append([]byte(nil), a.order...) }

// Extend appends a new symbol, bumping the version. Extending with an
// already-known symbol is illegal.
func (a *Alphabet) Extend(s byte) error {
	if a.Contains(s) {
		return fmt.Errorf("%w: 0x%02x", ErrDuplicateSymbol, s)
	}
	a.order = append(a.order, s)
	a.set[s] = struct{}{}
	a.ext++
	return nil
}
