package netsec

import "errors"

// scalarFunc describes a registered scalar function.
type scalarFunc struct {
	arity      int
	sideEffect bool
	// mayError reports whether the function can fail for a given argument set
	// (for example sqrt of a negative number).
	mayError func(args []int64) bool
	fn       func(args []int64) (int64, error)
}

var scalarFuncs = map[string]scalarFunc{
	"abs": {
		arity: 1,
		fn: func(a []int64) (int64, error) {
			if a[0] < 0 {
				return -a[0], nil
			}
			return a[0], nil
		},
	},
	"sqrt": {
		arity: 1,
		mayError: func(a []int64) bool {
			return a[0] < 0
		},
		fn: func(a []int64) (int64, error) {
			if a[0] < 0 {
				return 0, errors.New("sqrt of negative")
			}
			r := int64(1)
			for r*r < a[0] {
				r++
			}
			return r, nil
		},
	},
	// audit is a deliberately impure function: it performs an external write,
	// which makes any containing information-flow proof invalid.
	"audit": {
		arity:      1,
		sideEffect: true,
		fn: func(a []int64) (int64, error) {
			return a[0], nil
		},
	},
}
