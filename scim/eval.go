package scim

import (
	"strings"
)

// User is a directory entry. TenantID is the isolation boundary and is
// never exposed as a filterable, sortable or projectable attribute.
type User struct {
	ID          string
	TenantID    string
	UserName    string
	DisplayName string
	Active      bool
	Emails      []string
	Groups      []string
}

// userValues resolves an authorized attribute path to its values.
// Only attributes present in the policy ever reach this function.
func userValues(u *User, path string) []Value {
	switch normalizePath(path) {
	case "id":
		return []Value{{Kind: ValString, Str: u.ID}}
	case "username":
		return []Value{{Kind: ValString, Str: u.UserName}}
	case "displayname":
		return []Value{{Kind: ValString, Str: u.DisplayName}}
	case "active":
		return []Value{{Kind: ValBool, Bool: u.Active}}
	case "emails", "emails.value":
		return stringValues(u.Emails)
	case "groups", "groups.value":
		return stringValues(u.Groups)
	}
	return nil
}

func stringValues(ss []string) []Value {
	out := make([]Value, 0, len(ss))
	for _, s := range ss {
		out = append(out, Value{Kind: ValString, Str: s})
	}
	return out
}

// resolver resolves an attribute path to candidate values.
type resolver func(path string) []Value

// eval evaluates an already-validated expression against a resolver.
func eval(e Expr, resolve resolver) bool {
	switch n := e.(type) {
	case *LogExpr:
		if n.Op == LogOr {
			for _, a := range n.Args {
				if eval(a, resolve) {
					return true
				}
			}
			return false
		}
		for _, a := range n.Args {
			if !eval(a, resolve) {
				return false
			}
		}
		return true
	case *NotExpr:
		return !eval(n.Sub, resolve)
	case *AttrExpr:
		return evalAttr(n, resolve(n.Path))
	case *ValuePath:
		for _, elem := range resolve(n.Path) {
			sub := func(path string) []Value {
				if normalizePath(path) == "value" {
					return []Value{elem}
				}
				return nil
			}
			if eval(n.Sub, sub) {
				return true
			}
		}
		return false
	}
	return false
}

func evalAttr(a *AttrExpr, candidates []Value) bool {
	if a.Op == OpPr {
		for _, c := range candidates {
			if c.Kind != ValString || c.Str != "" {
				return true
			}
		}
		return false
	}
	for _, c := range candidates {
		if compare(a.Op, c, a.Value) {
			return true
		}
	}
	return false
}

func compare(op Op, candidate, literal Value) bool {
	if candidate.Kind != literal.Kind {
		return false
	}
	switch candidate.Kind {
	case ValBool:
		switch op {
		case OpEq:
			return candidate.Bool == literal.Bool
		case OpNe:
			return candidate.Bool != literal.Bool
		}
	case ValNumber:
		switch op {
		case OpEq:
			return candidate.Num == literal.Num
		case OpNe:
			return candidate.Num != literal.Num
		case OpGt:
			return candidate.Num > literal.Num
		case OpGe:
			return candidate.Num >= literal.Num
		case OpLt:
			return candidate.Num < literal.Num
		case OpLe:
			return candidate.Num <= literal.Num
		}
	case ValString:
		c := strings.ToLower(candidate.Str)
		l := strings.ToLower(literal.Str)
		switch op {
		case OpEq:
			return c == l
		case OpNe:
			return c != l
		case OpCo:
			return strings.Contains(c, l)
		case OpSw:
			return strings.HasPrefix(c, l)
		case OpEw:
			return strings.HasSuffix(c, l)
		case OpGt:
			return c > l
		case OpGe:
			return c >= l
		case OpLt:
			return c < l
		case OpLe:
			return c <= l
		}
	}
	return false
}
