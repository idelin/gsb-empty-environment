package main

import (
	"encoding/json"
	"fmt"
	"math"
)

func ParseInput(data []byte) (*Env, Plan, error) {
	var in struct {
		Tables map[string]json.RawMessage `json:"tables"`
		Plan   json.RawMessage            `json:"plan"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, nil, fmt.Errorf("invalid input JSON: %w", err)
	}
	if len(in.Plan) == 0 {
		return nil, nil, fmt.Errorf("input JSON missing \"plan\"")
	}
	env := &Env{Tables: map[string]Table{}}
	for name, raw := range in.Tables {
		var t struct {
			Cols []string        `json:"cols"`
			Rows [][]interface{} `json:"rows"`
		}
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, nil, fmt.Errorf("table %s: %w", name, err)
		}
		rows := make([]Row, len(t.Rows))
		for i, r := range t.Rows {
			if len(r) != len(t.Cols) {
				return nil, nil, fmt.Errorf("table %s row %d: got %d values, want %d", name, i, len(r), len(t.Cols))
			}
			row := make(Row, len(r))
			for j, x := range r {
				v, err := jsonValue(x)
				if err != nil {
					return nil, nil, fmt.Errorf("table %s row %d: %w", name, i, err)
				}
				row[j] = v
			}
			rows[i] = row
		}
		env.Tables[name] = Table{Cols: t.Cols, Rows: rows}
	}
	plan, err := ParsePlan(in.Plan)
	if err != nil {
		return nil, nil, err
	}
	return env, plan, nil
}

func jsonValue(x interface{}) (Value, error) {
	switch v := x.(type) {
	case nil:
		return NullV(), nil
	case float64:
		if v != math.Trunc(v) {
			return Value{}, fmt.Errorf("non-integer number %v", v)
		}
		return IntV(int64(v)), nil
	case string:
		return StrV(v), nil
	}
	return Value{}, fmt.Errorf("unsupported JSON value %v (only null, integer, string)", x)
}

func ParsePlan(raw json.RawMessage) (Plan, error) {
	var head struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	switch head.Op {
	case "scan":
		var n struct {
			Table string `json:"table"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		return Scan{Table: n.Table}, nil
	case "filter":
		var n struct {
			Input json.RawMessage `json:"input"`
			Pred  json.RawMessage `json:"pred"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		input, err := ParsePlan(n.Input)
		if err != nil {
			return nil, err
		}
		pred, err := ParseExpr(n.Pred)
		if err != nil {
			return nil, err
		}
		return Filter{Input: input, Pred: pred}, nil
	case "project":
		var n struct {
			Input json.RawMessage `json:"input"`
			Cols  []struct {
				Name string          `json:"name"`
				Expr json.RawMessage `json:"expr"`
			} `json:"cols"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		input, err := ParsePlan(n.Input)
		if err != nil {
			return nil, err
		}
		cols := make([]ProjCol, len(n.Cols))
		for i, c := range n.Cols {
			e, err := ParseExpr(c.Expr)
			if err != nil {
				return nil, err
			}
			cols[i] = ProjCol{Name: c.Name, Expr: e}
		}
		return Project{Input: input, Cols: cols}, nil
	case "join":
		var n struct {
			Kind  string          `json:"kind"`
			Left  json.RawMessage `json:"left"`
			Right json.RawMessage `json:"right"`
			On    json.RawMessage `json:"on"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		l, err := ParsePlan(n.Left)
		if err != nil {
			return nil, err
		}
		r, err := ParsePlan(n.Right)
		if err != nil {
			return nil, err
		}
		on, err := parseOptExpr(n.On)
		if err != nil {
			return nil, err
		}
		return Join{Kind: n.Kind, Left: l, Right: r, On: on}, nil
	case "semijoin":
		var n struct {
			Left  json.RawMessage `json:"left"`
			Right json.RawMessage `json:"right"`
			On    json.RawMessage `json:"on"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		l, err := ParsePlan(n.Left)
		if err != nil {
			return nil, err
		}
		r, err := ParsePlan(n.Right)
		if err != nil {
			return nil, err
		}
		on, err := parseOptExpr(n.On)
		if err != nil {
			return nil, err
		}
		return SemiJoin{Left: l, Right: r, On: on}, nil
	case "aggregate":
		var n struct {
			Alias   string          `json:"alias"`
			Input   json.RawMessage `json:"input"`
			GroupBy []string        `json:"groupBy"`
			Aggs    []struct {
				Name string `json:"name"`
				Fn   string `json:"fn"`
				Arg  string `json:"arg"`
			} `json:"aggs"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		input, err := ParsePlan(n.Input)
		if err != nil {
			return nil, err
		}
		aggs := make([]AggSpec, len(n.Aggs))
		for i, a := range n.Aggs {
			aggs[i] = AggSpec{Name: a.Name, Fn: a.Fn, Arg: a.Arg}
		}
		return Aggregate{Alias: n.Alias, Input: input, GroupBy: n.GroupBy, Aggs: aggs}, nil
	}
	return nil, fmt.Errorf("unknown plan op %q", head.Op)
}

func parseOptExpr(raw json.RawMessage) (Expr, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return ParseExpr(raw)
}

func ParseExpr(raw json.RawMessage) (Expr, error) {
	var head struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	switch head.Kind {
	case "col":
		var n struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		return Col{Name: n.Name}, nil
	case "lit":
		var n struct {
			Value interface{} `json:"value"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		v, err := jsonValue(n.Value)
		if err != nil {
			return nil, err
		}
		return Lit{V: v}, nil
	case "cmp":
		var n struct {
			Op string          `json:"op"`
			L  json.RawMessage `json:"l"`
			R  json.RawMessage `json:"r"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		l, err := ParseExpr(n.L)
		if err != nil {
			return nil, err
		}
		r, err := ParseExpr(n.R)
		if err != nil {
			return nil, err
		}
		return Cmp{Op: n.Op, L: l, R: r}, nil
	case "and", "or":
		var n struct {
			Args []json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		args := make([]Expr, len(n.Args))
		for i, a := range n.Args {
			e, err := ParseExpr(a)
			if err != nil {
				return nil, err
			}
			args[i] = e
		}
		if head.Kind == "and" {
			return AndE{Args: args}, nil
		}
		return OrE{Args: args}, nil
	case "not":
		var n struct {
			Arg json.RawMessage `json:"arg"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		e, err := ParseExpr(n.Arg)
		if err != nil {
			return nil, err
		}
		return NotE{Arg: e}, nil
	case "isnull":
		var n struct {
			Arg json.RawMessage `json:"arg"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		e, err := ParseExpr(n.Arg)
		if err != nil {
			return nil, err
		}
		return IsNull{Arg: e}, nil
	case "coalesce":
		var n struct {
			Args []json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		args := make([]Expr, len(n.Args))
		for i, a := range n.Args {
			e, err := ParseExpr(a)
			if err != nil {
				return nil, err
			}
			args[i] = e
		}
		return Coalesce{Args: args}, nil
	case "exists":
		var n struct {
			Sub json.RawMessage `json:"sub"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		sub, err := ParsePlan(n.Sub)
		if err != nil {
			return nil, err
		}
		return Exists{Sub: sub}, nil
	case "count":
		var n struct {
			Arg string          `json:"arg"`
			Sub json.RawMessage `json:"sub"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			return nil, err
		}
		sub, err := ParsePlan(n.Sub)
		if err != nil {
			return nil, err
		}
		return CountSubq{Arg: n.Arg, Sub: sub}, nil
	}
	return nil, fmt.Errorf("unknown expr kind %q", head.Kind)
}

func planJSON(p Plan) interface{} {
	switch n := p.(type) {
	case Scan:
		return map[string]interface{}{"op": "scan", "table": n.Table}
	case Filter:
		return map[string]interface{}{"op": "filter", "input": planJSON(n.Input), "pred": exprJSON(n.Pred)}
	case Project:
		cols := make([]interface{}, len(n.Cols))
		for i, c := range n.Cols {
			cols[i] = map[string]interface{}{"name": c.Name, "expr": exprJSON(c.Expr)}
		}
		return map[string]interface{}{"op": "project", "input": planJSON(n.Input), "cols": cols}
	case Join:
		return map[string]interface{}{"op": "join", "kind": n.Kind, "left": planJSON(n.Left), "right": planJSON(n.Right), "on": exprJSON(n.On)}
	case SemiJoin:
		return map[string]interface{}{"op": "semijoin", "left": planJSON(n.Left), "right": planJSON(n.Right), "on": exprJSON(n.On)}
	case Aggregate:
		aggs := make([]interface{}, len(n.Aggs))
		for i, a := range n.Aggs {
			aggs[i] = map[string]interface{}{"name": a.Name, "fn": a.Fn, "arg": a.Arg}
		}
		return map[string]interface{}{"op": "aggregate", "alias": n.Alias, "input": planJSON(n.Input), "groupBy": n.GroupBy, "aggs": aggs}
	}
	return map[string]interface{}{"op": fmt.Sprintf("unknown:%T", p)}
}

func exprJSON(e Expr) interface{} {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case Col:
		return map[string]interface{}{"kind": "col", "name": n.Name}
	case Lit:
		return map[string]interface{}{"kind": "lit", "value": n.V}
	case Cmp:
		return map[string]interface{}{"kind": "cmp", "op": n.Op, "l": exprJSON(n.L), "r": exprJSON(n.R)}
	case AndE:
		return map[string]interface{}{"kind": "and", "args": exprListJSON(n.Args)}
	case OrE:
		return map[string]interface{}{"kind": "or", "args": exprListJSON(n.Args)}
	case NotE:
		return map[string]interface{}{"kind": "not", "arg": exprJSON(n.Arg)}
	case IsNull:
		return map[string]interface{}{"kind": "isnull", "arg": exprJSON(n.Arg)}
	case Coalesce:
		return map[string]interface{}{"kind": "coalesce", "args": exprListJSON(n.Args)}
	case Exists:
		return map[string]interface{}{"kind": "exists", "sub": planJSON(n.Sub)}
	case CountSubq:
		return map[string]interface{}{"kind": "count", "arg": n.Arg, "sub": planJSON(n.Sub)}
	}
	return map[string]interface{}{"kind": fmt.Sprintf("unknown:%T", e)}
}

func exprListJSON(es []Expr) []interface{} {
	out := make([]interface{}, len(es))
	for i, e := range es {
		out[i] = exprJSON(e)
	}
	return out
}
