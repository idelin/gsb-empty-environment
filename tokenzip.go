// Package tokenzip implements a dictionary-based token compression scheme.
//
// A dictionary is a list of entries. Each entry is a phrase: a sequence of
// elements, where an element is either a literal token or a reference to
// another entry. Entries may reference earlier (or any other) entries, so
// expansions can be exponentially large. The package never materializes a
// full expansion: it builds a length index with big-integer arithmetic and
// answers interval queries by descending the reference tree, mapping any
// expanded interval back to the original token positions.
package tokenzip

import (
	"fmt"
	"math/big"
	"strings"
)

// ErrKind classifies the errors returned by this package.
type ErrKind int

const (
	// ErrEmptyPhrase indicates a dictionary entry with no elements.
	ErrEmptyPhrase ErrKind = iota + 1
	// ErrBadReference indicates a reference to a non-existent entry.
	ErrBadReference
	// ErrCycle indicates a cyclic chain of references.
	ErrCycle
	// ErrBudgetExceeded indicates an expansion length above the budget.
	ErrBudgetExceeded
	// ErrBadInterval indicates a query interval outside the expansion.
	ErrBadInterval
)

// Error is the error type returned by this package.
type Error struct {
	Kind   ErrKind
	Entry  int
	Detail string
}

func (e *Error) Error() string {
	var what string
	switch e.Kind {
	case ErrEmptyPhrase:
		what = "empty phrase"
	case ErrBadReference:
		what = "bad reference"
	case ErrCycle:
		what = "cyclic reference"
	case ErrBudgetExceeded:
		what = "expansion budget exceeded"
	case ErrBadInterval:
		what = "interval out of range"
	default:
		what = "unknown error"
	}
	if e.Detail != "" {
		return fmt.Sprintf("tokenzip: %s: entry %d: %s", what, e.Entry, e.Detail)
	}
	return fmt.Sprintf("tokenzip: %s: entry %d", what, e.Entry)
}

// Elem is one element of a phrase: either a literal token or a reference
// to another dictionary entry.
type Elem struct {
	IsRef bool   // true if this element is a reference
	Ref   int    // referenced entry index (valid when IsRef)
	Token string // literal token (valid when !IsRef)
}

// Lit returns a literal-token element.
func Lit(token string) Elem { return Elem{Token: token} }

// Ref returns a reference element pointing at the given entry index.
func Ref(entry int) Elem { return Elem{IsRef: true, Ref: entry} }

// Dictionary is a compiled set of phrases with a big-integer length index.
type Dictionary struct {
	entries [][]Elem
	lengths []*big.Int
	budget  *big.Int
}

// Compile validates the entries and builds the length index without
// expanding anything. budget caps the expanded length of every entry;
// a nil budget means unlimited. It returns *Error with kind
// ErrEmptyPhrase, ErrBadReference, ErrCycle or ErrBudgetExceeded.
func Compile(entries [][]Elem, budget *big.Int) (*Dictionary, error) {
	d := &Dictionary{
		entries: entries,
		lengths: make([]*big.Int, len(entries)),
		budget:  budget,
	}
	for i := range entries {
		if len(entries[i]) == 0 {
			return nil, &Error{Kind: ErrEmptyPhrase, Entry: i}
		}
	}
	state := make([]int8, len(entries)) // 0=unvisited 1=visiting 2=done
	for i := range entries {
		if _, err := d.lengthOf(i, state); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *Dictionary) lengthOf(i int, state []int8) (*big.Int, error) {
	if i < 0 || i >= len(d.entries) {
		return nil, &Error{Kind: ErrBadReference, Entry: i}
	}
	switch state[i] {
	case 1:
		return nil, &Error{Kind: ErrCycle, Entry: i}
	case 2:
		return d.lengths[i], nil
	}
	state[i] = 1
	total := new(big.Int)
	for _, e := range d.entries[i] {
		var l *big.Int
		if e.IsRef {
			var err error
			l, err = d.lengthOf(e.Ref, state)
			if err != nil {
				return nil, err
			}
		} else {
			l = big.NewInt(1)
		}
		total.Add(total, l)
		if d.budget != nil && total.Cmp(d.budget) > 0 {
			return nil, &Error{
				Kind:   ErrBudgetExceeded,
				Entry:  i,
				Detail: fmt.Sprintf("length exceeds budget %s", d.budget),
			}
		}
	}
	state[i] = 2
	d.lengths[i] = total
	return total, nil
}

// NumEntries returns the number of dictionary entries.
func (d *Dictionary) NumEntries() int { return len(d.entries) }

// Len returns the expanded length of the given entry. The result is a
// copy and may be mutated by the caller.
func (d *Dictionary) Len(entry int) (*big.Int, error) {
	if entry < 0 || entry >= len(d.entries) {
		return nil, &Error{Kind: ErrBadReference, Entry: entry}
	}
	return new(big.Int).Set(d.lengths[entry]), nil
}

// Span maps one original token to its position in the expanded stream.
type Span struct {
	Path   []int    // entry indices from the query root to the defining entry
	Elem   int      // element index inside the innermost entry
	Token  string   // the literal token
	Offset *big.Int // absolute offset of this token in the expanded stream
}

func (s Span) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q @%s via", s.Token, s.Offset)
	for _, e := range s.Path {
		fmt.Fprintf(&b, " %d", e)
	}
	fmt.Fprintf(&b, ".%d", s.Elem)
	return b.String()
}

// Locate maps the half-open interval [start, end) of the expanded stream
// of entry back to the original tokens covering it, without expanding
// anything. Offsets in the returned spans are absolute positions in the
// expansion of entry.
func (d *Dictionary) Locate(entry int, start, end *big.Int) ([]Span, error) {
	if entry < 0 || entry >= len(d.entries) {
		return nil, &Error{Kind: ErrBadReference, Entry: entry}
	}
	if start.Sign() < 0 || end.Cmp(start) < 0 || end.Cmp(d.lengths[entry]) > 0 {
		return nil, &Error{
			Kind:  ErrBadInterval,
			Entry: entry,
			Detail: fmt.Sprintf("interval [%s, %s) outside length %s",
				start, end, d.lengths[entry]),
		}
	}
	var spans []Span
	d.locate(entry, start, end, new(big.Int), nil, &spans)
	return spans, nil
}

func (d *Dictionary) locate(entry int, start, end, base *big.Int, path []int, out *[]Span) {
	path = append(append([]int(nil), path...), entry)
	pos := new(big.Int) // offset local to this entry
	for idx, e := range d.entries[entry] {
		var l *big.Int
		if e.IsRef {
			l = d.lengths[e.Ref]
		} else {
			l = big.NewInt(1)
		}
		next := new(big.Int).Add(pos, l)
		if next.Cmp(start) > 0 && pos.Cmp(end) < 0 {
			if e.IsRef {
				s := new(big.Int).Sub(maxBig(start, pos), pos)
				en := new(big.Int).Sub(minBig(end, next), pos)
				abs := new(big.Int).Add(base, pos)
				d.locate(e.Ref, s, en, abs, path, out)
			} else {
				*out = append(*out, Span{
					Path:   path,
					Elem:   idx,
					Token:  e.Token,
					Offset: new(big.Int).Add(base, pos),
				})
			}
		}
		pos = next
		if pos.Cmp(end) >= 0 {
			return
		}
	}
}

func maxBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) >= 0 {
		return a
	}
	return b
}

func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}
