package main

import (
	"fmt"
	"strings"
)

type Row []Value

type Table struct {
	Cols []string
	Rows []Row
}

type Env struct {
	Tables map[string]Table
}

func (e *Env) schema(table string) ([]string, error) {
	t, ok := e.Tables[table]
	if !ok {
		return nil, fmt.Errorf("unknown table %q", table)
	}
	cols := make([]string, len(t.Cols))
	for i, c := range t.Cols {
		cols[i] = table + "." + c
	}
	return cols, nil
}

func evalPlan(p Plan, env *Env, outer Scope) ([]string, []Row, error) {
	switch n := p.(type) {
	case Scan:
		schema, err := env.schema(n.Table)
		if err != nil {
			return nil, nil, err
		}
		return schema, env.Tables[n.Table].Rows, nil

	case Filter:
		if n.Pred == nil {
			return nil, nil, fmt.Errorf("FILTER requires a predicate")
		}
		schema, rows, err := evalPlan(n.Input, env, outer)
		if err != nil {
			return nil, nil, err
		}
		var out []Row
		for _, row := range rows {
			t, err := evalPred(n.Pred, push(outer, schema, row), env)
			if err != nil {
				return nil, nil, err
			}
			if t == True {
				out = append(out, row)
			}
		}
		return schema, out, nil

	case Project:
		_, rows, err := evalPlan(n.Input, env, outer)
		if err != nil {
			return nil, nil, err
		}
		inSchema, err := evalSchema(n.Input, env)
		if err != nil {
			return nil, nil, err
		}
		outSchema := make([]string, len(n.Cols))
		for i, c := range n.Cols {
			outSchema[i] = c.Name
		}
		var out []Row
		for _, row := range rows {
			sc := push(outer, inSchema, row)
			nr := make(Row, len(n.Cols))
			for i, c := range n.Cols {
				v, err := evalScalar(c.Expr, sc, env)
				if err != nil {
					return nil, nil, err
				}
				nr[i] = v
			}
			out = append(out, nr)
		}
		return outSchema, out, nil

	case Join:
		if n.Kind != InnerJoin && n.Kind != LeftJoin {
			return nil, nil, fmt.Errorf("unknown join kind %q", n.Kind)
		}
		ls, lr, err := evalPlan(n.Left, env, outer)
		if err != nil {
			return nil, nil, err
		}
		rs, rr, err := evalPlan(n.Right, env, outer)
		if err != nil {
			return nil, nil, err
		}
		schema := append(append([]string{}, ls...), rs...)
		var out []Row
		for _, l := range lr {
			matched := false
			for _, r := range rr {
				row := concatRow(l, r)
				t, err := evalPredOrTrue(n.On, push(outer, schema, row), env)
				if err != nil {
					return nil, nil, err
				}
				if t == True {
					out = append(out, row)
					matched = true
				}
			}
			if n.Kind == LeftJoin && !matched {
				out = append(out, concatRow(l, nullRow(len(rs))))
			}
		}
		return schema, out, nil

	case SemiJoin:
		ls, lr, err := evalPlan(n.Left, env, outer)
		if err != nil {
			return nil, nil, err
		}
		rs, rr, err := evalPlan(n.Right, env, outer)
		if err != nil {
			return nil, nil, err
		}
		schema := append(append([]string{}, ls...), rs...)
		var out []Row
		for _, l := range lr {
			for _, r := range rr {
				t, err := evalPredOrTrue(n.On, push(outer, schema, concatRow(l, r)), env)
				if err != nil {
					return nil, nil, err
				}
				if t == True {
					out = append(out, l)
					break
				}
			}
		}
		return ls, out, nil

	case Aggregate:
		schema, rows, err := evalPlan(n.Input, env, outer)
		if err != nil {
			return nil, nil, err
		}
		keyIdx := make([]int, len(n.GroupBy))
		for i, g := range n.GroupBy {
			keyIdx[i] = indexOf(schema, g)
			if keyIdx[i] < 0 {
				return nil, nil, fmt.Errorf("group-by column %q not in input schema %v", g, schema)
			}
		}
		argIdx := make([]int, len(n.Aggs))
		for i, a := range n.Aggs {
			if a.Fn != "count" {
				return nil, nil, fmt.Errorf("unsupported aggregate function %q", a.Fn)
			}
			if a.Arg != "*" {
				argIdx[i] = indexOf(schema, a.Arg)
				if argIdx[i] < 0 {
					return nil, nil, fmt.Errorf("aggregate argument column %q not in input schema %v", a.Arg, schema)
				}
			}
		}
		type group struct {
			key     []Value
			rows    int64
			nonNull []int64
		}
		groups := map[string]*group{}
		var order []string
		if len(n.GroupBy) == 0 {
			groups[""] = &group{nonNull: make([]int64, len(n.Aggs))}
			order = append(order, "")
		}
		for _, row := range rows {
			keyVals := make([]Value, len(keyIdx))
			parts := make([]string, len(keyIdx))
			for i, idx := range keyIdx {
				keyVals[i] = row[idx]
				parts[i] = row[idx].key()
			}
			k := strings.Join(parts, "\x00")
			g, ok := groups[k]
			if !ok {
				g = &group{key: keyVals, nonNull: make([]int64, len(n.Aggs))}
				groups[k] = g
				order = append(order, k)
			}
			g.rows++
			for ai, a := range n.Aggs {
				if a.Arg != "*" && row[argIdx[ai]].Kind != Null {
					g.nonNull[ai]++
				}
			}
		}
		var outSchema []string
		for _, gcol := range n.GroupBy {
			outSchema = append(outSchema, n.Alias+"."+shortName(gcol))
		}
		for _, a := range n.Aggs {
			outSchema = append(outSchema, n.Alias+"."+a.Name)
		}
		var out []Row
		for _, k := range order {
			g := groups[k]
			row := append(Row{}, g.key...)
			for ai, a := range n.Aggs {
				if a.Arg == "*" {
					row = append(row, IntV(g.rows))
				} else {
					row = append(row, IntV(g.nonNull[ai]))
				}
			}
			out = append(out, row)
		}
		return outSchema, out, nil
	}
	return nil, nil, fmt.Errorf("unknown plan node %T", p)
}

func concatRow(a, b Row) Row {
	out := make(Row, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

func nullRow(n int) Row {
	out := make(Row, n)
	for i := range out {
		out[i] = NullV()
	}
	return out
}
