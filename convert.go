package migrate

import (
	"fmt"
	"math/big"
	"strconv"
)

// Convert converts v to the target type. The conversion is permitted only if
// it round-trips: converting the result back to the source type must
// reproduce v exactly. Null is handled by the caller (field nullability),
// not here.
func Convert(v Value, to Type) (Value, error) {
	if v.IsNull() {
		return Value{}, fmt.Errorf("cannot convert null to %s", to)
	}
	fwd, err := convertRaw(v, to)
	if err != nil {
		return Value{}, err
	}
	back, err := convertRaw(fwd, typeOfKind(v.Kind()))
	if err != nil {
		return Value{}, fmt.Errorf("round-trip check failed: %v", err)
	}
	if !back.Equal(v) {
		return Value{}, fmt.Errorf(
			"conversion %s -> %s is not round-trip safe for %s (returns %s)",
			v.Kind(), to, v, back)
	}
	return fwd, nil
}

// convertRaw performs the forward conversion without the round-trip check.
func convertRaw(v Value, to Type) (Value, error) {
	switch v.Kind() {
	case Int:
		switch to {
		case TInt:
			return v, nil
		case TRat:
			return RatVal(big.NewRat(v.Int(), 1)), nil
		case TText:
			return TextVal(strconv.FormatInt(v.Int(), 10)), nil
		}
	case Rat:
		r := v.Rat()
		switch to {
		case TRat:
			return v, nil
		case TInt:
			if !r.IsInt() {
				return Value{}, fmt.Errorf("rational %s is not an integer", ratText(r))
			}
			n := r.Num()
			if !n.IsInt64() {
				return Value{}, fmt.Errorf("integer %s overflows int64", n.String())
			}
			return IntVal(n.Int64()), nil
		case TText:
			return TextVal(ratText(r)), nil
		}
	case Text:
		s := v.Text()
		switch to {
		case TText:
			return v, nil
		case TInt:
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return Value{}, fmt.Errorf("%q is not an int64", s)
			}
			return IntVal(n), nil
		case TRat:
			r, ok := new(big.Rat).SetString(s)
			if !ok {
				return Value{}, fmt.Errorf("%q is not a rational", s)
			}
			return RatVal(r), nil
		}
	}
	return Value{}, fmt.Errorf("unsupported conversion %s -> %s", v.Kind(), to)
}
