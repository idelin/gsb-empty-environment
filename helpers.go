package jsonpathstream

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

func indexSuffix(i int) string { return fmt.Sprintf("[%d]", i) }

func memberSuffix(key string) string {
	if isIdentifier(key) {
		return "." + key
	}
	return "[" + quoteString(key) + "]"
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !(unicode.IsLetter(r) || r == 0x5F) {
			return false
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == 0x5F) {
			return false
		}
	}
	return true
}

func quoteString(s string) string {
	return fmt.Sprintf("%q", s)
}

var _ = utf8.RuneError

// resolveRef follows an @-relative reference from node. The boolean reports
// whether the reference resolved to an existing node (a missing field is
// undefined, distinct from an explicit null).
func resolveRef(node any, ref refPath) (any, bool) {
	cur := node
	for _, step := range ref.steps {
		if step.isIndex {
			arr, ok := cur.([]any)
			if !ok {
				return nil, false
			}
			idx := step.index
			if idx < 0 {
				idx += len(arr)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
			continue
		}
		obj, ok := cur.(Object)
		if !ok {
			return nil, false
		}
		v, ok := obj.get(step.name)
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// evalFilter evaluates one comparison against the candidate node.
// Missing reference values make the comparison false (including !=).
func evalFilter(f *filterExpr, node any) bool {
	lhs, ok := resolveRef(node, f.lhs)
	if !ok {
		return false
	}
	var rhs any
	if f.rhsIsRef {
		v, ok := resolveRef(node, f.rhs)
		if !ok {
			return false
		}
		rhs = v
	} else {
		rhs = f.literal
	}
	return compare(f.op, lhs, rhs)
}

func compare(op string, lhs, rhs any) bool {
	// Equality works across any JSON types (type-sensitive).
	if op == "==" {
		return valuesEqual(lhs, rhs)
	}
	if op == "!=" {
		return !valuesEqual(lhs, rhs)
	}
	// Ordering only applies to two numbers or two strings.
	ln, lnum := lhs.(float64)
	rn, rnum := rhs.(float64)
	if lnum && rnum {
		switch op {
		case "<":
			return ln < rn
		case "<=":
			return ln <= rn
		case ">":
			return ln > rn
		case ">=":
			return ln >= rn
		}
	}
	ls, lstr := lhs.(string)
	rs, rstr := rhs.(string)
	if lstr && rstr {
		switch op {
		case "<":
			return ls < rs
		case "<=":
			return ls <= rs
		case ">":
			return ls > rs
		case ">=":
			return ls >= rs
		}
	}
	return false
}

func valuesEqual(a, b any) bool {
	// Object and []any are compared by identity here, which is sufficient for
	// scalar filter literals and references pointing at scalars.
	return a == b
}

// joinPath builds a child canonical path. The root has the empty base, which is
// rendered as $.
func joinPath(base, suffix string) string {
	if base == "" {
		return "$" + suffix
	}
	return base + suffix
}
