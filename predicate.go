package gin

import (
	"fmt"
	"sort"
)

// Predicate describes both how to evaluate a candidate and which keys can
// safely drive candidate generation. A predicate containing negation must
// usually report ScanAll because a complement cannot be generated from only
// the postings of its positive keys.
type Predicate interface {
	ScanKeys() []string
	ScanAll() bool
	Eval(KeyLookup) Truth
}

type exprKind uint8

const (
	exprKey exprKind = iota
	exprAnd
	exprOr
	exprNot
	exprCustom
)

// Expr is a composable tri-state predicate expression.
type Expr struct {
	kind     exprKind
	key      string
	children []Predicate
	keys     []string
	all      bool
	fn       ConsistencyFunc
	err      error
}

// ExprOption customizes a custom predicate expression.
type ExprOption func(*Expr)

// WithFullScan makes a custom predicate scan every live TID. Use this when the
// custom function can refer to keys outside the declared key set or implements
// negation.
func WithFullScan(enabled bool) ExprOption {
	return func(expr *Expr) {
		expr.all = enabled
	}
}

// Key builds a predicate testing one key.
func Key(key string) *Expr {
	return &Expr{kind: exprKey, key: key, keys: []string{key}}
}

// And builds a conjunction. Children may be strings, []string, Predicate
// values, ConsistencyFunc values, or nested expressions.
func And(children ...any) *Expr {
	return newCompound(exprAnd, children)
}

// Or builds a disjunction. Children may be strings, []string, Predicate
// values, ConsistencyFunc values, or nested expressions.
func Or(children ...any) *Expr {
	return newCompound(exprOr, children)
}

// Not builds a negated predicate. Candidate generation uses a full scan so
// that tuples with no matching positive posting are not lost.
func Not(child any) *Expr {
	predicate := normalizePredicate(child, nil)
	expr := &Expr{kind: exprNot, children: []Predicate{predicate}, all: true}
	expr.keys = append([]string(nil), predicate.ScanKeys()...)
	return expr
}

// Subset builds a conjunction requiring every supplied key. It expresses the
// common "document key set contains the query set" relation.
func Subset(keys ...string) *Expr {
	children := make([]any, 0, len(keys))
	for _, key := range keys {
		children = append(children, key)
	}
	return And(children...)
}

// ContainsAll is an alias for Subset.
func ContainsAll(keys ...string) *Expr {
	return Subset(keys...)
}

// AnyKey builds a disjunction over keys.
func AnyKey(keys ...string) *Expr {
	children := make([]any, 0, len(keys))
	for _, key := range keys {
		children = append(children, key)
	}
	return Or(children...)
}

// MatchAll matches every live TID.
func MatchAll() *Expr {
	return &Expr{
		kind: exprCustom,
		all:  true,
		fn:   Const(True),
	}
}

// Custom builds a predicate with an explicit scan key set and a custom
// consistency function. fn may be a ConsistencyFunc, func(KeyLookup) Truth,
// MapConsistencyFunc, or func(map[string]Truth) Truth.
func Custom(keys []string, fn any, options ...ExprOption) *Expr {
	expr := &Expr{
		kind: exprCustom,
		keys: uniqueSortedStrings(keys),
		all:  len(keys) == 0,
	}
	for _, option := range options {
		if option != nil {
			option(expr)
		}
	}
	expr.fn, expr.err = adaptConsistencyFunc(expr.keys, fn)
	if expr.err != nil {
		expr.fn = Const(False)
	}
	return expr
}

// CustomAll builds a custom predicate that scans all live TIDs.
func CustomAll(fn any) *Expr {
	return Custom(nil, fn, WithFullScan(true))
}

// CustomE is Custom with construction-error reporting.
func CustomE(keys []string, fn any, options ...ExprOption) (*Expr, error) {
	expr := Custom(keys, fn, options...)
	return expr, expr.err
}

// NewPredicate is an alias for Custom.
func NewPredicate(keys []string, fn any, options ...ExprOption) *Expr {
	return Custom(keys, fn, options...)
}

// Err returns a predicate construction error, if any.
func (e *Expr) Err() error {
	if e == nil {
		return nil
	}
	return e.err
}

// ScanKeys returns the positive keys that can be used for candidate
// generation.
func (e *Expr) ScanKeys() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.keys...)
}

// ScanAll reports whether candidate generation requires every live TID.
func (e *Expr) ScanAll() bool {
	return e != nil && e.all
}

// Eval evaluates the predicate using lookup.
func (e *Expr) Eval(lookup KeyLookup) Truth {
	if e == nil {
		return False
	}
	switch e.kind {
	case exprKey:
		return lookup(e.key)
	case exprAnd:
		values := make([]Truth, 0, len(e.children))
		for _, child := range e.children {
			value := child.Eval(lookup)
			if value == False {
				return False
			}
			values = append(values, value)
		}
		return TriAnd(values...)
	case exprOr:
		values := make([]Truth, 0, len(e.children))
		for _, child := range e.children {
			value := child.Eval(lookup)
			if value == True {
				return True
			}
			values = append(values, value)
		}
		return TriOr(values...)
	case exprNot:
		if len(e.children) == 0 {
			return Maybe
		}
		return TriNot(e.children[0].Eval(lookup))
	case exprCustom:
		if e.fn == nil {
			return False
		}
		return normalizeTruth(e.fn(lookup))
	default:
		return False
	}
}

func newCompound(kind exprKind, children []any) *Expr {
	expr := &Expr{kind: kind}
	keys := make(map[string]struct{})
	for _, child := range children {
		switch value := child.(type) {
		case nil:
			continue
		case string:
			expr.children = append(expr.children, Key(value))
			keys[value] = struct{}{}
		case []string:
			for _, key := range value {
				expr.children = append(expr.children, Key(key))
				keys[key] = struct{}{}
			}
		case []any:
			for _, item := range value {
				nested := normalizePredicate(item, nil)
				expr.children = append(expr.children, nested)
				expr.all = expr.all || nested.ScanAll()
				for _, key := range nested.ScanKeys() {
					keys[key] = struct{}{}
				}
			}
		case Predicate:
			expr.children = append(expr.children, value)
			expr.all = expr.all || value.ScanAll()
			for _, key := range value.ScanKeys() {
				keys[key] = struct{}{}
			}
		default:
			nested := normalizePredicate(value, nil)
			expr.children = append(expr.children, nested)
			expr.all = expr.all || nested.ScanAll()
			for _, key := range nested.ScanKeys() {
				keys[key] = struct{}{}
			}
		}
	}
	expr.keys = sortedMapKeys(keys)
	return expr
}

func normalizePredicate(value any, keys []string) Predicate {
	switch predicate := value.(type) {
	case nil:
		return MatchAll()
	case Predicate:
		return predicate
	case *Expr:
		return predicate
	case string:
		return Key(predicate)
	case []string:
		children := make([]any, 0, len(predicate))
		for _, key := range predicate {
			children = append(children, key)
		}
		return And(children...)
	case ConsistencyFunc:
		return Custom(keys, predicate, WithFullScan(len(keys) == 0))
	case MapConsistencyFunc:
		return Custom(keys, predicate, WithFullScan(len(keys) == 0))
	case func(KeyLookup) Truth:
		return Custom(keys, ConsistencyFunc(predicate), WithFullScan(len(keys) == 0))
	case func(map[string]Truth) Truth:
		return Custom(keys, MapConsistencyFunc(predicate), WithFullScan(len(keys) == 0))
	default:
		return &Expr{kind: exprCustom, err: fmt.Errorf("unsupported predicate type %T", value), fn: Const(False)}
	}
}

func adaptConsistencyFunc(keys []string, fn any) (ConsistencyFunc, error) {
	switch value := fn.(type) {
	case nil:
		return nil, fmt.Errorf("nil consistency function")
	case ConsistencyFunc:
		return value, nil
	case func(KeyLookup) Truth:
		return ConsistencyFunc(value), nil
	case MapConsistencyFunc:
		return adaptMapFunc(keys, value), nil
	case func(map[string]Truth) Truth:
		return adaptMapFunc(keys, MapConsistencyFunc(value)), nil
	default:
		return nil, fmt.Errorf("unsupported consistency function type %T", fn)
	}
}

func adaptMapFunc(keys []string, fn MapConsistencyFunc) ConsistencyFunc {
	keys = append([]string(nil), keys...)
	return func(lookup KeyLookup) Truth {
		states := make(map[string]Truth, len(keys))
		for _, key := range keys {
			states[key] = lookup(key)
		}
		return fn(states)
	}
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return sortedMapKeys(set)
}

func sortedMapKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
