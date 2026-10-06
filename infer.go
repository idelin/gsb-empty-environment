package csvq

import (
	"regexp"
	"strconv"
)

// Type is a column type inferred from record values.
type Type int

const (
	TInt Type = iota
	TFloat
	TText
)

func (t Type) String() string {
	switch t {
	case TInt:
		return "int"
	case TFloat:
		return "float"
	default:
		return "text"
	}
}

// canonicalInt and canonicalFloat match numbers in canonical form only:
// no leading zeros ("0" itself is allowed). Values like "007" classify
// as text so their leading zeros are never lost.
var canonicalInt = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
var canonicalFloat = regexp.MustCompile(`^-?(0|[1-9][0-9]*)\.[0-9]+([eE][+-]?[0-9]+)?$|^-?(0|[1-9][0-9]*)[eE][+-]?[0-9]+$`)

// classify infers the narrowest type of a single non-empty value.
func classify(s string) Type {
	if canonicalInt.MatchString(s) {
		return TInt
	}
	if canonicalFloat.MatchString(s) {
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return TFloat
		}
	}
	return TText
}

// widen returns the wider of two types (TInt < TFloat < TText).
func widen(a, b Type) Type {
	if a > b {
		return a
	}
	return b
}

// inferColumnTypes infers one type per column from a batch of records.
// Missing and empty fields carry no evidence and are ignored; a column
// with no evidence defaults to TInt.
func inferColumnTypes(recs []Record, width int) []Type {
	types := make([]Type, width)
	seen := make([]bool, width)
	for _, r := range recs {
		for c := 0; c < width && c < len(r.Fields); c++ {
			f := r.Fields[c]
			if f.Missing || f.Value == "" {
				continue
			}
			ct := classify(f.Value)
			if !seen[c] {
				types[c] = ct
				seen[c] = true
			} else {
				types[c] = widen(types[c], ct)
			}
		}
	}
	return types
}
