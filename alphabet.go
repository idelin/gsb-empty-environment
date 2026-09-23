// Package mtf implements a Move-To-Front transform over a configurable,
// versioned alphabet with an explicit unknown-symbol escape protocol,
// resettable logarithmic-time state, and block-boundary checkpoints.
package mtf

import "errors"

// ErrDuplicateSymbol is returned when an alphabet definition or extension
// contains a symbol that is already present.
var ErrDuplicateSymbol = errors.New("mtf: duplicate symbol in alphabet")

// Alphabet is an ordered, versioned set of distinct symbols (runes).
// It is immutable: extensions produce a new Alphabet with Version+1.
type Alphabet struct {
	version uint32
	symbols []rune
	index   map[rune]int
}

// NewAlphabet builds the version-0 alphabet from the given symbols.
// Duplicate symbols are illegal and rejected.
func NewAlphabet(symbols []rune) (*Alphabet, error) {
	a := &Alphabet{index: make(map[rune]int, len(symbols))}
	for i, s := range symbols {
		if _, dup := a.index[s]; dup {
			return nil, ErrDuplicateSymbol
		}
		a.index[s] = i
	}
	a.symbols = append([]rune(nil), symbols...)
	return a, nil
}

// Extend returns a new alphabet at Version+1 with the extra symbols appended.
// Extras duplicating existing symbols or each other are rejected.
func (a *Alphabet) Extend(extras ...rune) (*Alphabet, error) {
	next := &Alphabet{
		version: a.version + 1,
		symbols: append(append([]rune(nil), a.symbols...), extras...),
		index:   make(map[rune]int, len(a.symbols)+len(extras)),
	}
	for i, s := range next.symbols {
		if _, dup := next.index[s]; dup {
			return nil, ErrDuplicateSymbol
		}
		next.index[s] = i
	}
	return next, nil
}

// Version is the alphabet revision; the base alphabet is version 0 and each
// extension (explicit or via the escape-update protocol) increments it.
func (a *Alphabet) Version() uint32 { return a.version }

// Len returns the number of symbols in the alphabet.
func (a *Alphabet) Len() int { return len(a.symbols) }

// Symbol returns the symbol with the given id (its position in the alphabet
// definition, not its current MTF rank).
func (a *Alphabet) Symbol(id int) (rune, bool) {
	if id < 0 || id >= len(a.symbols) {
		return 0, false
	}
	return a.symbols[id], true
}

// ID returns the alphabet id of a symbol and whether it is known.
func (a *Alphabet) ID(sym rune) (int, bool) {
	id, ok := a.index[sym]
	return id, ok
}

// Symbols returns a copy of the alphabet definition in id order.
func (a *Alphabet) Symbols() []rune { return append([]rune(nil), a.symbols...) }
