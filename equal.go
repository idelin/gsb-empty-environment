package graphser

// ValueEqual reports whether a and b are structurally equal within the
// supported value domain. Object identity is ignored: two distinct
// *Object pointers with equal fields are value-equal. This is the
// counterpart of identity comparison, which for *Object is plain Go
// pointer equality. Cycles are handled, so ValueEqual terminates on
// cyclic graphs.
func ValueEqual(a, b any) bool {
	return valueEqual(a, b, make(map[objectPair]bool))
}

type objectPair struct{ a, b *Object }

func valueEqual(a, b any, seen map[objectPair]bool) bool {
	switch av := a.(type) {
	case nil:
		return b == nil
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !valueEqual(av[i], bv[i], seen) {
				return false
			}
		}
		return true
	case *Object:
		bv, ok := b.(*Object)
		if !ok || av == nil || bv == nil {
			return false
		}
		if av == bv {
			return true
		}
		pair := objectPair{av, bv}
		if seen[pair] {
			return true
		}
		seen[pair] = true
		if len(av.Fields) != len(bv.Fields) {
			return false
		}
		for k, va := range av.Fields {
			vb, ok := bv.Fields[k]
			if !ok || !valueEqual(va, vb, seen) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
