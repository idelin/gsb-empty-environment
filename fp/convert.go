package fp

import "math/big"

// ConvertString parses a decimal literal and rounds it directly into dst.
// Parsing never uses a host floating-point type, so the value is not first
// rounded to binary64.
func ConvertString(dst *Format, s string, mode Round) (Result, error) {
	if err := dst.validate(); err != nil {
		return Result{}, err
	}
	x, err := ParseDecimal(s)
	if err != nil {
		return Result{}, err
	}
	return ConvertValue(dst, x, mode), nil
}

// MustParseDecimal panics on malformed input. It is intended for literals.
func MustParseDecimal(s string) Exact {
	x, err := ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return x
}

// RationalExact constructs the exact value n*2^k.
func RationalExact(neg bool, n *big.Int, k int) Exact {
	nn := new(big.Int).Set(n)
	return scaledExact(neg, nn, k)
}
