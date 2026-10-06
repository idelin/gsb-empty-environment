package migrate

import (
	"math/big"
	"testing"
)

func rat(num, den int64) Value { return RatVal(big.NewRat(num, den)) }

// TestRoundTripMatrix verifies the core contract: whenever Convert succeeds,
// converting the result back to the source type reproduces the original
// value exactly, for every pair of types.
func TestRoundTripMatrix(t *testing.T) {
	values := []Value{
		IntVal(0), IntVal(7), IntVal(-42), IntVal(9223372036854775807),
		IntVal(-9223372036854775808),
		rat(1, 2), rat(-3, 4), rat(4, 2), rat(0, 5), rat(22, 7),
		TextVal(""), TextVal("hello"), TextVal("42"), TextVal("-7"),
		TextVal("007"), TextVal("3/2"), TextVal("1.50"), TextVal("1e3"),
		TextVal(" 1"), TextVal("+42"),
	}
	types := []Type{TInt, TRat, TText}
	for _, v := range values {
		for _, to := range types {
			fwd, err := Convert(v, to)
			if err != nil {
				continue // rejection is fine; only success must round-trip
			}
			back, err := Convert(fwd, typeOfKind(v.Kind()))
			if err != nil {
				t.Errorf("Convert(%v, %s) = %v, but back-conversion failed: %v",
					v, to, fwd, err)
				continue
			}
			if !back.Equal(v) {
				t.Errorf("round trip broke %v via %s: got %v", v, to, back)
			}
		}
	}
}

// TestTypeNarrowing checks that narrowing conversions only accept values
// that survive the round trip.
func TestTypeNarrowing(t *testing.T) {
	ok := []struct {
		v    Value
		to   Type
		want Value
	}{
		{rat(4, 2), TInt, IntVal(2)},      // integral rational narrows
		{rat(-9, 3), TInt, IntVal(-3)},    // normalizes before narrowing
		{TextVal("42"), TInt, IntVal(42)}, // canonical integer text
		{TextVal("-7"), TInt, IntVal(-7)},
		{TextVal("3/2"), TRat, rat(3, 2)}, // canonical rational text
		{TextVal("42"), TRat, rat(42, 1)},
		{IntVal(7), TRat, rat(7, 1)}, // widening is always safe
		{IntVal(-13), TText, TextVal("-13")},
		{rat(1, 2), TText, TextVal("1/2")},
		{rat(6, 3), TText, TextVal("2")},
	}
	for _, tc := range ok {
		got, err := Convert(tc.v, tc.to)
		if err != nil {
			t.Errorf("Convert(%v, %s) rejected: %v", tc.v, tc.to, err)
			continue
		}
		if !got.Equal(tc.want) {
			t.Errorf("Convert(%v, %s) = %v, want %v", tc.v, tc.to, got, tc.want)
		}
	}

	rejected := []struct {
		v  Value
		to Type
	}{
		{rat(1, 2), TInt},                      // fractional rational would lose data
		{rat(22, 7), TInt},                     // non-integral
		{TextVal("007"), TInt},                 // leading zeros would not round-trip
		{TextVal("+42"), TInt},                 // non-canonical sign
		{TextVal(" 1"), TInt},                  // whitespace
		{TextVal("1.50"), TRat},                // decimal form is not canonical
		{TextVal("6/4"), TRat},                 // unreduced fraction
		{TextVal("1e3"), TRat},                 // exponent form
		{TextVal("abc"), TInt},                 // unparseable
		{TextVal(""), TRat},                    // empty
		{TextVal("9223372036854775808"), TInt}, // overflows int64
	}
	for _, tc := range rejected {
		if got, err := Convert(tc.v, tc.to); err == nil {
			t.Errorf("Convert(%v, %s) = %v, want rejection", tc.v, tc.to, got)
		}
	}
}

// TestNullConversion: null is never converted by Convert itself.
func TestNullConversion(t *testing.T) {
	for _, to := range []Type{TInt, TRat, TText} {
		if _, err := Convert(NullVal(), to); err == nil {
			t.Errorf("Convert(null, %s) succeeded, want rejection", to)
		}
	}
}
