package scim

import (
	"math"
	"strings"
	"unicode/utf8"
)

// AttrType is the expected type of an attribute's values.
type AttrType int

const (
	AttrString AttrType = iota
	AttrBool
	AttrNumber
)

// AttributeRule declares what a caller may do with one attribute.
type AttributeRule struct {
	// Name is the canonical attribute name used in projections.
	Name string
	// Type is the expected literal type for comparisons.
	Type AttrType
	// Ops lists the operators allowed on this attribute.
	Ops map[Op]bool
	// Sortable allows the attribute in sortBy.
	Sortable bool
	// Projectable allows the attribute in attributes/excludedAttributes
	// and in the default projection.
	Projectable bool
	// Sensitive marks attributes (userName, emails, ...) whose equality
	// matches must be executed strictly inside the tenant scope.
	Sensitive bool
	// MultiValued marks complex multi-valued attributes (emails, groups)
	// usable as valuePath roots.
	MultiValued bool
}

// Policy defines the authorization boundary for SCIM queries.
type Policy struct {
	// Attributes maps lower-cased attribute paths to their rules.
	Attributes map[string]AttributeRule
	// MaxPageSize caps Query.Count.
	MaxPageSize int
	// MaxOrBranches caps the total number of OR branches in a filter.
	MaxOrBranches int
	// MaxDepth caps filter nesting depth.
	MaxDepth int
	// MaxValueLength caps string literal length in comparisons.
	MaxValueLength int
	// AllowTopLevelNot permits a filter that is a bare NOT expression.
	// Even when allowed, evaluation never leaves the tenant scope.
	AllowTopLevelNot bool
}

// DefaultPolicy returns a restrictive policy suitable for a multi-tenant
// user directory: exact-match only on sensitive attributes, no tenant
// pivot attribute, bounded OR/NOT complexity.
func DefaultPolicy() Policy {
	str := func(ops ...Op) map[Op]bool {
		m := make(map[Op]bool, len(ops))
		for _, o := range ops {
			m[o] = true
		}
		return m
	}
	attrs := map[string]AttributeRule{
		"id": {
			Name: "id", Type: AttrString,
			Ops: str(OpEq), Projectable: true,
		},
		"username": {
			Name: "userName", Type: AttrString,
			Ops: str(OpEq), Sortable: true, Projectable: true, Sensitive: true,
		},
		"displayname": {
			Name: "displayName", Type: AttrString,
			Ops: str(OpEq, OpCo, OpSw), Sortable: true, Projectable: true,
		},
		"active": {
			Name: "active", Type: AttrBool,
			Ops: str(OpEq), Projectable: true,
		},
		"emails": {
			Name: "emails", Type: AttrString,
			Ops: str(OpEq), Projectable: true, Sensitive: true, MultiValued: true,
		},
		"emails.value": {
			Name: "emails.value", Type: AttrString,
			Ops: str(OpEq), Sensitive: true,
		},
		"groups": {
			Name: "groups", Type: AttrString,
			Ops: str(OpEq), Projectable: true, MultiValued: true,
		},
		"groups.value": {
			Name: "groups.value", Type: AttrString,
			Ops: str(OpEq),
		},
	}
	return Policy{
		Attributes:       attrs,
		MaxPageSize:      100,
		MaxOrBranches:    8,
		MaxDepth:         6,
		MaxValueLength:   256,
		AllowTopLevelNot: false,
	}
}

// normalizePath lower-cases a SCIM attribute path for case-insensitive
// lookup and strips any URN schema prefix.
func normalizePath(path string) string {
	p := strings.ToLower(strings.TrimSpace(path))
	if i := strings.LastIndex(p, ":user:"); i >= 0 {
		return p[i+len(":user:"):]
	}
	if i := strings.LastIndexByte(p, ':'); i >= 0 && strings.HasPrefix(p, "urn:") {
		return p[i+1:]
	}
	return p
}

// Validate authorizes a parsed filter tree against the policy, checking
// every node: attribute, operator and literal value.
func (p *Policy) Validate(e Expr) error {
	v := &validator{pol: p}
	return v.walk(e, walkCtx{top: true, depth: 1})
}

type walkCtx struct {
	top     bool // expression is the filter root
	underOr bool // direct parent is an OR
	prefix  string
	depth   int
}

type validator struct {
	pol        *Policy
	orBranches int
}

func (v *validator) walk(e Expr, ctx walkCtx) error {
	if ctx.depth > v.pol.MaxDepth {
		return newError(ErrCodeTooComplex, "filter nesting exceeds depth limit %d", v.pol.MaxDepth)
	}
	switch n := e.(type) {
	case *LogExpr:
		if n.Op == LogOr {
			v.orBranches += len(n.Args)
			if v.orBranches > v.pol.MaxOrBranches {
				return newError(ErrCodeTooComplex,
					"filter exceeds OR branch limit %d", v.pol.MaxOrBranches)
			}
		}
		for _, arg := range n.Args {
			child := walkCtx{prefix: ctx.prefix, depth: ctx.depth + 1}
			if n.Op == LogOr {
				child.underOr = true
			}
			if err := v.walk(arg, child); err != nil {
				return err
			}
		}
		return nil
	case *NotExpr:
		// NOT may only appear directly beneath an AND and never at the
		// filter root: anywhere else it would act as a set-difference
		// that widens rather than narrows the scoped result set.
		if ctx.top && !v.pol.AllowTopLevelNot {
			return newError(ErrCodeNegationNotAllowed,
				"top-level NOT is not allowed; combine it with AND")
		}
		if ctx.underOr {
			return newError(ErrCodeNegationNotAllowed,
				"NOT is not allowed inside OR branches")
		}
		switch n.Sub.(type) {
		case *AttrExpr, *ValuePath:
		default:
			return newError(ErrCodeNegationNotAllowed,
				"NOT may only wrap a single attribute comparison")
		}
		return v.walk(n.Sub, walkCtx{prefix: ctx.prefix, depth: ctx.depth + 1})
	case *AttrExpr:
		return v.checkAttr(n, ctx.prefix)
	case *ValuePath:
		root := normalizePath(n.Path)
		rule, ok := v.pol.Attributes[root]
		if !ok {
			return newError(ErrCodeUnauthorizedAttr, "attribute %q is not authorized", n.Path)
		}
		if !rule.MultiValued {
			return newError(ErrCodeOperatorNotAllowed,
				"attribute %q does not support value-path filters", n.Path)
		}
		return v.walk(n.Sub, walkCtx{prefix: root + ".", depth: ctx.depth + 1})
	default:
		return newError(ErrCodeParse, "unknown expression node %T", e)
	}
}

func (v *validator) checkAttr(a *AttrExpr, prefix string) error {
	full := normalizePath(prefix + a.Path)
	rule, ok := v.pol.Attributes[full]
	if !ok {
		return newError(ErrCodeUnauthorizedAttr, "attribute %q is not authorized", a.Path)
	}
	if !rule.Ops[a.Op] {
		return newError(ErrCodeOperatorNotAllowed,
			"operator %q is not allowed on attribute %q", a.Op, rule.Name)
	}
	if a.Op == OpPr {
		return nil
	}
	return v.checkValue(rule, a.Value)
}

func (v *validator) checkValue(rule AttributeRule, val Value) error {
	switch rule.Type {
	case AttrString:
		if val.Kind != ValString {
			return newError(ErrCodeInvalidValue,
				"attribute %q expects a string comparison value", rule.Name)
		}
	case AttrBool:
		if val.Kind != ValBool {
			return newError(ErrCodeInvalidValue,
				"attribute %q expects a boolean comparison value", rule.Name)
		}
	case AttrNumber:
		if val.Kind != ValNumber {
			return newError(ErrCodeInvalidValue,
				"attribute %q expects a numeric comparison value", rule.Name)
		}
	}
	if val.Kind == ValString {
		if len(val.Str) == 0 {
			return newError(ErrCodeInvalidValue,
				"empty comparison value for attribute %q", rule.Name)
		}
		if len(val.Str) > v.pol.MaxValueLength {
			return newError(ErrCodeInvalidValue,
				"comparison value for attribute %q exceeds %d bytes", rule.Name, v.pol.MaxValueLength)
		}
		if !utf8.ValidString(val.Str) || strings.ContainsAny(val.Str, "\x00") {
			return newError(ErrCodeInvalidValue,
				"comparison value for attribute %q contains invalid characters", rule.Name)
		}
		for _, r := range val.Str {
			if r < 0x20 && r != '\t' {
				return newError(ErrCodeInvalidValue,
					"comparison value for attribute %q contains control characters", rule.Name)
			}
		}
	}
	if val.Kind == ValNumber && (math.IsNaN(val.Num) || math.IsInf(val.Num, 0)) {
		return newError(ErrCodeInvalidValue, "invalid numeric comparison value")
	}
	return nil
}
