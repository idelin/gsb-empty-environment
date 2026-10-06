// Package infer infers the narrowest column type able to losslessly
// represent every non-empty value seen in a column of string records.
//
// The type lattice, from narrowest to widest, is:
//
//	TypeBool < TypeInt < TypeRational < TypeText
//
// Values are classified by their narrowest lossless type and counted as
// evidence. A column's type is the widest evidence class observed, so
// removing records (undo) can narrow the inferred type again.
package infer

// Type is a column type in the inference lattice. The declared order is
// the widening order: a larger value can represent every value of a
// smaller one without loss.
type Type int

const (
	// TypeBool matches exactly true and false.
	TypeBool Type = iota
	// TypeInt matches canonical base-10 integers that fit in an int64.
	TypeInt
	// TypeRational matches decimal or scientific-notation numbers
	// representable as a finite float64, plus integer literals beyond
	// int64 that are exactly representable as a float64.
	TypeRational
	// TypeText matches anything, including empty strings by default.
	TypeText
)

// String returns a human-readable name for the type.
func (t Type) String() string {
	switch t {
	case TypeBool:
		return "bool"
	case TypeInt:
		return "int"
	case TypeRational:
		return "rational"
	case TypeText:
		return "text"
	default:
		return "unknown"
	}
}
