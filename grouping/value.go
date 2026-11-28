package grouping

import (
	"fmt"
	"strconv"
	"strings"
)

// valueKey encodes a cell value with a type tag so that grouping and
// DISTINCT comparisons never confuse NULL, numbers, strings and bools.
func valueKey(v any) string {
	switch t := v.(type) {
	case nil:
		return "n"
	case int:
		return "i" + strconv.Itoa(t)
	case int64:
		return "i" + strconv.FormatInt(t, 10)
	case float64:
		return "f" + strconv.FormatFloat(t, 'g', -1, 64)
	case string:
		return "s" + t
	case bool:
		return "b" + strconv.FormatBool(t)
	default:
		return fmt.Sprintf("x%#v", v)
	}
}

// toNumber interprets int, int64 and float64 cells as numbers.
func toNumber(v any) (f float64, isInt bool, ok bool) {
	switch t := v.(type) {
	case int:
		return float64(t), true, true
	case int64:
		return float64(t), true, true
	case float64:
		return t, false, true
	}
	return 0, false, false
}

// typeRank orders values across types for MIN/MAX: numbers < strings <
// bools < anything else.
func typeRank(v any) int {
	switch v.(type) {
	case int, int64, float64:
		return 0
	case string:
		return 1
	case bool:
		return 2
	}
	return 3
}

// compareValues orders two non-NULL values for MIN/MAX.
func compareValues(a, b any) int {
	ra, rb := typeRank(a), typeRank(b)
	if ra != rb {
		if ra < rb {
			return -1
		}
		return 1
	}
	switch ra {
	case 0:
		fa, _, _ := toNumber(a)
		fb, _, _ := toNumber(b)
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	case 1:
		return strings.Compare(a.(string), b.(string))
	case 2:
		ba, bb := a.(bool), b.(bool)
		switch {
		case ba == bb:
			return 0
		case !ba:
			return -1
		}
		return 1
	}
	return 0
}
