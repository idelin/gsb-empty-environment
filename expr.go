package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Expr interface{ isExpr() }

type Col struct{ Name string }
type Lit struct{ V Value }
type Cmp struct {
	Op   string
	L, R Expr
}
type AndE struct{ Args []Expr }
type OrE struct{ Args []Expr }
type NotE struct{ Arg Expr }
type IsNull struct{ Arg Expr }
type Coalesce struct{ Args []Expr }
type Exists struct{ Sub Plan }
type CountSubq struct {
	Arg string
	Sub Plan
}

func (Col) isExpr()       {}
func (Lit) isExpr()       {}
func (Cmp) isExpr()       {}
func (AndE) isExpr()      {}
func (OrE) isExpr()       {}
func (NotE) isExpr()      {}
func (IsNull) isExpr()    {}
func (Coalesce) isExpr()  {}
func (Exists) isExpr()    {}
func (CountSubq) isExpr() {}

type frame struct {
	schema []string
	row    Row
}

type Scope []frame

func (sc Scope) lookup(name string) (Value, error) {
	for _, f := range sc {
		for i, c := range f.schema {
			if c == name {
				return f.row[i], nil
			}
		}
	}
	return Value{}, fmt.Errorf("unknown column %q", name)
}

func push(outer Scope, schema []string, row Row) Scope {
	sc := make(Scope, 0, len(outer)+1)
	sc = append(sc, frame{schema, row})
	return append(sc, outer...)
}

func evalScalar(e Expr, sc Scope, env *Env) (Value, error) {
	switch n := e.(type) {
	case Col:
		return sc.lookup(n.Name)
	case Lit:
		return n.V, nil
	case Coalesce:
		for _, a := range n.Args {
			v, err := evalScalar(a, sc, env)
			if err != nil {
				return Value{}, err
			}
			if v.Kind != Null {
				return v, nil
			}
		}
		return NullV(), nil
	case CountSubq:
		schema, rows, err := evalPlan(n.Sub, env, sc)
		if err != nil {
			return Value{}, err
		}
		if n.Arg == "*" {
			return IntV(int64(len(rows))), nil
		}
		idx := indexOf(schema, n.Arg)
		if idx < 0 {
			return Value{}, fmt.Errorf("COUNT argument column %q not in subquery output %v", n.Arg, schema)
		}
		var cnt int64
		for _, row := range rows {
			if row[idx].Kind != Null {
				cnt++
			}
		}
		return IntV(cnt), nil
	}
	return Value{}, fmt.Errorf("expression %s is not a scalar expression", exprString(e))
}

func evalPred(e Expr, sc Scope, env *Env) (Tri, error) {
	switch n := e.(type) {
	case Cmp:
		l, err := evalScalar(n.L, sc, env)
		if err != nil {
			return False, err
		}
		r, err := evalScalar(n.R, sc, env)
		if err != nil {
			return False, err
		}
		if l.Kind == Null || r.Kind == Null {
			return Unknown, nil
		}
		if l.Kind != r.Kind {
			return False, fmt.Errorf("type mismatch in comparison: %s vs %s", l, r)
		}
		var c int
		if l.Kind == Int {
			switch {
			case l.I < r.I:
				c = -1
			case l.I > r.I:
				c = 1
			}
		} else {
			c = strings.Compare(l.S, r.S)
		}
		switch n.Op {
		case "eq":
			return tri(c == 0), nil
		case "ne":
			return tri(c != 0), nil
		case "lt":
			return tri(c < 0), nil
		case "le":
			return tri(c <= 0), nil
		case "gt":
			return tri(c > 0), nil
		case "ge":
			return tri(c >= 0), nil
		}
		return False, fmt.Errorf("unknown comparison operator %q", n.Op)
	case AndE:
		res := True
		for _, a := range n.Args {
			t, err := evalPred(a, sc, env)
			if err != nil {
				return False, err
			}
			res = And(res, t)
		}
		return res, nil
	case OrE:
		res := False
		for _, a := range n.Args {
			t, err := evalPred(a, sc, env)
			if err != nil {
				return False, err
			}
			res = Or(res, t)
		}
		return res, nil
	case NotE:
		t, err := evalPred(n.Arg, sc, env)
		if err != nil {
			return False, err
		}
		return Not(t), nil
	case IsNull:
		v, err := evalScalar(n.Arg, sc, env)
		if err != nil {
			return False, err
		}
		return tri(v.Kind == Null), nil
	case Exists:
		_, rows, err := evalPlan(n.Sub, env, sc)
		if err != nil {
			return False, err
		}
		return tri(len(rows) > 0), nil
	}
	return False, fmt.Errorf("expression %s is not a predicate", exprString(e))
}

func evalPredOrTrue(e Expr, sc Scope, env *Env) (Tri, error) {
	if e == nil {
		return True, nil
	}
	return evalPred(e, sc, env)
}

func exprString(e Expr) string {
	b, err := json.Marshal(exprJSON(e))
	if err != nil {
		return fmt.Sprintf("%T", e)
	}
	return string(b)
}

func splitAnd(e Expr) []Expr {
	if a, ok := e.(AndE); ok {
		var out []Expr
		for _, x := range a.Args {
			out = append(out, splitAnd(x)...)
		}
		return out
	}
	return []Expr{e}
}

func andOf(es []Expr) Expr {
	switch len(es) {
	case 0:
		return nil
	case 1:
		return es[0]
	}
	return AndE{Args: es}
}

func containsSubq(e Expr) bool {
	switch n := e.(type) {
	case Exists, CountSubq:
		return true
	case Cmp:
		return containsSubq(n.L) || containsSubq(n.R)
	case AndE:
		for _, a := range n.Args {
			if containsSubq(a) {
				return true
			}
		}
	case OrE:
		for _, a := range n.Args {
			if containsSubq(a) {
				return true
			}
		}
	case NotE:
		return containsSubq(n.Arg)
	case IsNull:
		return containsSubq(n.Arg)
	case Coalesce:
		for _, a := range n.Args {
			if containsSubq(a) {
				return true
			}
		}
	}
	return false
}

func colRefs(e Expr) map[string]bool {
	out := map[string]bool{}
	var walk func(e Expr)
	walk = func(e Expr) {
		switch n := e.(type) {
		case Col:
			out[n.Name] = true
		case Cmp:
			walk(n.L)
			walk(n.R)
		case AndE:
			for _, a := range n.Args {
				walk(a)
			}
		case OrE:
			for _, a := range n.Args {
				walk(a)
			}
		case NotE:
			walk(n.Arg)
		case IsNull:
			walk(n.Arg)
		case Coalesce:
			for _, a := range n.Args {
				walk(a)
			}
		}
	}
	walk(e)
	return out
}
