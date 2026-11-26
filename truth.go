package gin

import "fmt"

// Truth is a three-valued truth value. The zero value is False.
type Truth uint8

// TriState and Ternary are compatibility aliases for Truth.
type TriState = Truth
type Ternary = Truth

const (
	// False means that a predicate is definitely false.
	False Truth = iota
	// Maybe means that the available index information cannot prove the
	// predicate true or false. A heap recheck is required.
	Maybe
	// True means that a predicate is definitely true.
	True
)

// Short aliases used by callers that prefer explicit names.
const (
	TriFalse = False
	TriMaybe = Maybe
	TriTrue  = True
)

// Uppercase aliases are convenient in predicate specifications.
const (
	FALSE = False
	MAYBE = Maybe
	TRUE  = True
)

// KeyLookup returns the tri-state value of a key for one candidate.
type KeyLookup func(key string) Truth

// ConsistencyFunc combines key membership states into a tri-state result.
// Implementations should use Kleene logic, for which the helpers in this
// package are suitable.
type ConsistencyFunc func(KeyLookup) Truth

// MapConsistencyFunc is a convenience form of ConsistencyFunc that receives a
// materialized, read-only state map.
type MapConsistencyFunc func(map[string]Truth) Truth

// TriAnd returns the Kleene conjunction of values.
func TriAnd(values ...Truth) Truth {
	result := True
	for _, value := range values {
		switch value {
		case False:
			return False
		case Maybe:
			result = Maybe
		}
	}
	return result
}

// TriOr returns the Kleene disjunction of values.
func TriOr(values ...Truth) Truth {
	result := False
	for _, value := range values {
		switch value {
		case True:
			return True
		case Maybe:
			result = Maybe
		}
	}
	return result
}

// TriNot returns the Kleene negation of value.
func TriNot(value Truth) Truth {
	switch value {
	case True:
		return False
	case False:
		return True
	default:
		return Maybe
	}
}

// TAnd is an alias for TriAnd.
func TAnd(values ...Truth) Truth { return TriAnd(values...) }

// TOr is an alias for TriOr.
func TOr(values ...Truth) Truth { return TriOr(values...) }

// TNot is an alias for TriNot.
func TNot(value Truth) Truth { return TriNot(value) }

// And returns the Kleene conjunction of t and all values.
func (t Truth) And(values ...Truth) Truth {
	return TriAnd(append([]Truth{t}, values...)...)
}

// Or returns the Kleene disjunction of t and all values.
func (t Truth) Or(values ...Truth) Truth {
	return TriOr(append([]Truth{t}, values...)...)
}

// Not returns the Kleene negation of t.
func (t Truth) Not() Truth {
	return TriNot(t)
}

// AndFuncs returns a consistency function that ANDs the results of funcs.
func AndFuncs(funcs ...ConsistencyFunc) ConsistencyFunc {
	return func(lookup KeyLookup) Truth {
		values := make([]Truth, 0, len(funcs))
		for _, fn := range funcs {
			values = append(values, fn(lookup))
		}
		return TriAnd(values...)
	}
}

// OrFuncs returns a consistency function that ORs the results of funcs.
func OrFuncs(funcs ...ConsistencyFunc) ConsistencyFunc {
	return func(lookup KeyLookup) Truth {
		values := make([]Truth, 0, len(funcs))
		for _, fn := range funcs {
			values = append(values, fn(lookup))
		}
		return TriOr(values...)
	}
}

// NotFunc returns a consistency function that negates fn.
func NotFunc(fn ConsistencyFunc) ConsistencyFunc {
	return func(lookup KeyLookup) Truth {
		return TriNot(fn(lookup))
	}
}

// KeyFunc returns a consistency function that looks up one key.
func KeyFunc(key string) ConsistencyFunc {
	return func(lookup KeyLookup) Truth {
		return lookup(key)
	}
}

// Const returns a consistency function with a fixed result.
func Const(value Truth) ConsistencyFunc {
	return func(KeyLookup) Truth {
		return value
	}
}

// TriContainsAll returns a consistency function that is true when all required
// keys are present. It is the set-containment form commonly used for array and
// JSONB queries.
func TriContainsAll(required ...string) ConsistencyFunc {
	return func(lookup KeyLookup) Truth {
		values := make([]Truth, 0, len(required))
		for _, key := range required {
			values = append(values, lookup(key))
		}
		return TriAnd(values...)
	}
}

// String implements fmt.Stringer.
func (t Truth) String() string {
	switch t {
	case False:
		return "FALSE"
	case Maybe:
		return "MAYBE"
	case True:
		return "TRUE"
	default:
		return fmt.Sprintf("Truth(%d)", uint8(t))
	}
}

// Bool converts a definite truth value to bool. Maybe is reported as false;
// callers should inspect the value explicitly when a recheck is required.
func (t Truth) Bool() bool {
	return t == True
}

// IsTrue reports whether t is definitely true.
func (t Truth) IsTrue() bool {
	return t == True
}

// IsFalse reports whether t is definitely false.
func (t Truth) IsFalse() bool {
	return t == False
}

// IsMaybe reports whether t requires verification.
func (t Truth) IsMaybe() bool {
	return t == Maybe
}

func normalizeTruth(value Truth) Truth {
	switch value {
	case False, Maybe, True:
		return value
	default:
		return Maybe
	}
}
