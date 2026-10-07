// Package infer infers the narrowest column type (bool, integer,
// rational, or text) that can losslessly represent every value seen
// in a column, and supports retracting records so the inferred type
// can narrow again.
package infer

// Kind is a column type. Kinds are ordered by narrowness: every bool
// is an integer, every integer is a rational, and every value is text.
type Kind int

const (
	Bool Kind = iota
	Int
	Rational
	Text
)

func (k Kind) String() string {
	switch k {
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Rational:
		return "rational"
	case Text:
		return "text"
	}
	return "unknown"
}
