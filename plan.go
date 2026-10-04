package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Plan interface{ isPlan() }

type Scan struct{ Table string }

type Filter struct {
	Input Plan
	Pred  Expr
}

type ProjCol struct {
	Name string
	Expr Expr
}

type Project struct {
	Input Plan
	Cols  []ProjCol
}

const (
	InnerJoin = "inner"
	LeftJoin  = "left"
)

type Join struct {
	Kind        string
	Left, Right Plan
	On          Expr
}

type SemiJoin struct {
	Left, Right Plan
	On          Expr
}

type AggSpec struct {
	Name string
	Fn   string
	Arg  string
}

type Aggregate struct {
	Alias   string
	Input   Plan
	GroupBy []string
	Aggs    []AggSpec
}

func (Scan) isPlan()      {}
func (Filter) isPlan()    {}
func (Project) isPlan()   {}
func (Join) isPlan()      {}
func (SemiJoin) isPlan()  {}
func (Aggregate) isPlan() {}

func shortName(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

func containsStr(xs []string, s string) bool { return indexOf(xs, s) >= 0 }

func evalSchema(p Plan, env *Env) ([]string, error) {
	switch n := p.(type) {
	case Scan:
		return env.schema(n.Table)
	case Filter:
		return evalSchema(n.Input, env)
	case Project:
		cols := make([]string, len(n.Cols))
		for i, c := range n.Cols {
			cols[i] = c.Name
		}
		return cols, nil
	case Join:
		l, err := evalSchema(n.Left, env)
		if err != nil {
			return nil, err
		}
		r, err := evalSchema(n.Right, env)
		if err != nil {
			return nil, err
		}
		return append(l, r...), nil
	case SemiJoin:
		return evalSchema(n.Left, env)
	case Aggregate:
		var cols []string
		for _, g := range n.GroupBy {
			cols = append(cols, n.Alias+"."+shortName(g))
		}
		for _, a := range n.Aggs {
			cols = append(cols, n.Alias+"."+a.Name)
		}
		return cols, nil
	}
	return nil, fmt.Errorf("unknown plan node %T", p)
}

func planString(p Plan) string {
	b, err := json.Marshal(planJSON(p))
	if err != nil {
		return fmt.Sprintf("%T", p)
	}
	return string(b)
}
