package sandbox

import (
	"fmt"
	"math"
)

// DefaultFunctions returns a safe starter whitelist of pure math functions.
// Hosts may pick any subset (or their own functions) per tenant.
func DefaultFunctions() map[string]Func {
	nums := func(fname string, args []any, min int) ([]float64, error) {
		if len(args) < min {
			return nil, fmt.Errorf("%s expects at least %d argument(s), got %d", fname, min, len(args))
		}
		out := make([]float64, len(args))
		for i, a := range args {
			f, ok := a.(float64)
			if !ok {
				return nil, fmt.Errorf("%s: argument %d is %T, expected number", fname, i+1, a)
			}
			out[i] = f
		}
		return out, nil
	}
	unary := func(fname string, args []any, f func(float64) float64) (any, error) {
		ns, err := nums(fname, args, 1)
		if err != nil {
			return nil, err
		}
		if len(ns) != 1 {
			return nil, fmt.Errorf("%s expects exactly 1 argument", fname)
		}
		return f(ns[0]), nil
	}
	return map[string]Func{
		"min": func(args []any) (any, error) {
			ns, err := nums("min", args, 1)
			if err != nil {
				return nil, err
			}
			m := ns[0]
			for _, v := range ns[1:] {
				m = math.Min(m, v)
			}
			return m, nil
		},
		"max": func(args []any) (any, error) {
			ns, err := nums("max", args, 1)
			if err != nil {
				return nil, err
			}
			m := ns[0]
			for _, v := range ns[1:] {
				m = math.Max(m, v)
			}
			return m, nil
		},
		"abs":   func(args []any) (any, error) { return unary("abs", args, math.Abs) },
		"floor": func(args []any) (any, error) { return unary("floor", args, math.Floor) },
		"ceil":  func(args []any) (any, error) { return unary("ceil", args, math.Ceil) },
		"round": func(args []any) (any, error) { return unary("round", args, math.Round) },
	}
}
