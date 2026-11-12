package policy

import (
	"bytes"
	"fmt"
	"math"
)

// compareValues applies SQL semantics: NULL on either side yields NULL,
// numbers compare numerically, strings and byte slices lexicographically,
// and booleans among themselves. Mismatched types are an error rather than a
// silent coercion.
func compareValues(op string, left, right any) (any, error) {
	if left == nil || right == nil {
		return nil, nil
	}

	ln, lnum := toFloat(left)
	rn, rnum := toFloat(right)
	switch {
	case lnum && rnum:
		return compareOrdered(op, ln, rn)
	}

	ls, lsOK := left.(string)
	rs, rsOK := right.(string)
	if lsOK && rsOK {
		return compareOrdered(op, float64(stringsCompare(ls, rs)), 0)
	}

	lb, lbOK := left.([]byte)
	rb, rbOK := right.([]byte)
	if lbOK && rbOK {
		return compareOrdered(op, float64(bytes.Compare(lb, rb)), 0)
	}
	if lsOK && rbOK {
		return compareOrdered(op, float64(bytes.Compare([]byte(ls), rb)), 0)
	}
	if lbOK && rsOK {
		return compareOrdered(op, float64(bytes.Compare(lb, []byte(rs))), 0)
	}

	lBool, lbOK := left.(bool)
	rBool, rbOK := right.(bool)
	if lbOK && rbOK {
		li, ri := 0, 0
		if lBool {
			li = 1
		}
		if rBool {
			ri = 1
		}
		return compareOrdered(op, float64(li), float64(ri))
	}

	return nil, fmt.Errorf("%w: cannot compare %T with %T", ErrBadStatement, left, right)
}

func compareOrdered[T int64 | float64](op string, left, right T) (bool, error) {
	switch op {
	case "=":
		return left == right, nil
	case "<>":
		return left != right, nil
	case "<":
		return left < right, nil
	case "<=":
		return left <= right, nil
	case ">":
		return left > right, nil
	case ">=":
		return left >= right, nil
	default:
		return false, fmt.Errorf("%w: unknown comparison %q", ErrBadStatement, op)
	}
}

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	default:
		return 0, false
	}
}

// stringsCompare mirrors bytes.Compare for strings without allocating.
func stringsCompare(a, b string) int {
	if a == b {
		return 0
	}
	if a < b {
		return -1
	}
	return 1
}

func arithNeg(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch v := value.(type) {
	case int64:
		return -v, nil
	case int:
		return -v, nil
	case float64:
		return -v, nil
	default:
		return nil, fmt.Errorf("%w: cannot negate %T", ErrBadStatement, value)
	}
}

// arithOp keeps int64 arithmetic integral and promotes to float64 only when
// an operand is fractional or division needs it.
func arithOp(op string, left, right any) (any, error) {
	if left == nil || right == nil {
		return nil, nil
	}
	li, lInt := left.(int64)
	ri, rInt := right.(int64)
	if lInt && rInt {
		return arithInt(op, li, ri)
	}

	ln, lOK := toFloat(left)
	rn, rOK := toFloat(right)
	if !lOK || !rOK {
		return nil, fmt.Errorf("%w: cannot apply %q to %T and %T",
			ErrBadStatement, op, left, right)
	}
	switch op {
	case "+":
		return ln + rn, nil
	case "-":
		return ln - rn, nil
	case "*":
		return ln * rn, nil
	case "/":
		if rn == 0 {
			return nil, fmt.Errorf("%w: division by zero", ErrBadStatement)
		}
		return ln / rn, nil
	case "%":
		if rn == 0 {
			return nil, fmt.Errorf("%w: modulo by zero", ErrBadStatement)
		}
		return math.Mod(ln, rn), nil
	default:
		return nil, fmt.Errorf("%w: unknown arithmetic operator %q", ErrBadStatement, op)
	}
}

func arithInt(op string, left, right int64) (any, error) {
	switch op {
	case "+":
		return left + right, nil
	case "-":
		return left - right, nil
	case "*":
		return left * right, nil
	case "/":
		if right == 0 {
			return nil, fmt.Errorf("%w: division by zero", ErrBadStatement)
		}
		return left / right, nil
	case "%":
		if right == 0 {
			return nil, fmt.Errorf("%w: modulo by zero", ErrBadStatement)
		}
		return left % right, nil
	default:
		return nil, fmt.Errorf("%w: unknown arithmetic operator %q", ErrBadStatement, op)
	}
}
