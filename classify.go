package infer

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
)

// kind is the evidence class a single value contributes to its column.
type kind int

const (
	kindEmpty kind = iota
	kindBool
	kindInt
	kindRational
	kindText
)

var (
	// Canonical integers: optional sign, no leading zeros ("0" itself is
	// fine, "-0" and "007" are not). Leading zeros would be lost by
	// numeric parsing, so such values stay text (e.g. zip codes, IDs).
	intRe = regexp.MustCompile("^-?(0|[1-9][0-9]*)$")

	// Decimal or scientific notation, same leading-zero rule on the
	// integer part. Deliberately excludes hex floats, "NaN" and "Inf",
	// which strconv.ParseFloat would otherwise accept.
	rationalRe = regexp.MustCompile("^-?((0|[1-9][0-9]*)(\\.[0-9]*)?|\\.[0-9]+)([eE][+-]?[0-9]+)?$")
)

// classify reports the narrowest evidence class for a single value.
// The empty string is reported as kindEmpty; the column decides whether
// that counts as text evidence (default) or as a null.
func classify(s string) kind {
	switch s {
	case "":
		return kindEmpty
	case "true", "false":
		return kindBool
	case "-0":
		// The canonical integer form of zero is "0"; the signed
		// spelling would not survive a round trip.
		return kindText
	}

	if intRe.MatchString(s) {
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return kindInt
		}
		// Integer literal beyond int64: only lossless as a rational
		// when the float64 nearest to it is exactly its value.
		if exactFloat64(s) {
			return kindRational
		}
		return kindText
	}

	if rationalRe.MatchString(s) {
		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) {
			// Decimals are approximate by nature; float64 rounding
			// (e.g. 0.1) is inherent to the rational type.
			return kindRational
		}
	}

	return kindText
}

// exactFloat64 reports whether the integer literal s is exactly
// representable as a float64 (e.g. 2^63 or 1e20 are, 1e20-1 is not).
func exactFloat64(s string) bool {
	rat, ok := new(big.Rat).SetString(s)
	if !ok {
		return false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return false
	}
	fr := new(big.Rat).SetFloat64(f)
	return fr != nil && fr.Cmp(rat) == 0
}
