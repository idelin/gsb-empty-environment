package ra

import (
	"fmt"
	"sort"
	"strings"
)

// Node 是关系代数计划节点。Exec 以 bag 语义返回行（保留重复）。
// outer 非空时，本节点是相关子查询，outer 为外层查询当前行。
type Node interface {
	Exec(outer Row) ([]Row, error)
	Schema() []string
	ToJSON() any
}

// Scan 读取一个有限表。
type Scan struct {
	Name string
	Rows []Row
	Cols []string // 可选显式模式；为空时从行数据推断
}

func (n *Scan) Exec(outer Row) ([]Row, error) {
	out := make([]Row, len(n.Rows))
	copy(out, n.Rows)
	return out, nil
}

func (n *Scan) Schema() []string {
	if n.Cols != nil {
		return n.Cols
	}
	set := map[string]bool{}
	for _, r := range n.Rows {
		for k := range r {
			set[k] = true
		}
	}
	var cols []string
	for k := range set {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	return cols
}

func (n *Scan) ToJSON() any {
	return map[string]any{"op": "scan", "table": n.Name}
}

// NamedExpr 是 SELECT 列表中的一项。
type NamedExpr struct {
	Expr Expr
	As   string
}

// Project 计算命名表达式列表。
type Project struct {
	Input Node
	Exprs []NamedExpr
}

func (n *Project) Exec(outer Row) ([]Row, error) {
	in, err := n.Input.Exec(outer)
	if err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(in))
	for _, r := range in {
		nr := Row{}
		for _, ne := range n.Exprs {
			v, err := ne.Expr.Eval(r, outer)
			if err != nil {
				return nil, err
			}
			nr[ne.As] = v
		}
		out = append(out, nr)
	}
	return out, nil
}

func (n *Project) Schema() []string {
	cols := make([]string, len(n.Exprs))
	for i, ne := range n.Exprs {
		cols[i] = ne.As
	}
	return cols
}

func (n *Project) ToJSON() any {
	exprs := make([]any, len(n.Exprs))
	for i, ne := range n.Exprs {
		exprs[i] = map[string]any{"expr": ne.Expr.ToJSON(), "as": ne.As}
	}
	return map[string]any{"op": "project", "input": n.Input.ToJSON(), "exprs": exprs}
}

// Filter 只保留谓词为 TRUE 的行（Unknown/False 均丢弃）。
type Filter struct {
	Input Node
	Pred  Expr
}

func (n *Filter) Exec(outer Row) ([]Row, error) {
	in, err := n.Input.Exec(outer)
	if err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(in))
	for _, r := range in {
		t, err := boolOf(n.Pred, r, outer)
		if err != nil {
			return nil, err
		}
		if t == True {
			out = append(out, r)
		}
	}
	return out, nil
}

func (n *Filter) Schema() []string { return n.Input.Schema() }

func (n *Filter) ToJSON() any {
	return map[string]any{"op": "filter", "input": n.Input.ToJSON(), "pred": n.Pred.ToJSON()}
}

type JoinKind int

const (
	InnerJoin JoinKind = iota
	LeftJoin
)

// Join 是内连接或左连接。谓词在合并行上求值：
// 左列可用原名或 "l." 前缀，右列可用原名（无冲突时）或 "r." 前缀。
type Join struct {
	Kind        JoinKind
	Left, Right Node
	Pred        Expr
}

func mergeRow(l, r Row) Row {
	m := Row{}
	for k, v := range l {
		m[k] = v
		m["l."+k] = v
	}
	for k, v := range r {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
		m["r."+k] = v
	}
	return m
}

func nullRow(cols []string) Row {
	r := Row{}
	for _, c := range cols {
		r[c] = nil
	}
	return r
}

func (n *Join) Exec(outer Row) ([]Row, error) {
	ls, err := n.Left.Exec(outer)
	if err != nil {
		return nil, err
	}
	rs, err := n.Right.Exec(outer)
	if err != nil {
		return nil, err
	}
	var out []Row
	for _, l := range ls {
		matched := false
		for _, r := range rs {
			t, err := boolOf(n.Pred, mergeRow(l, r), outer)
			if err != nil {
				return nil, err
			}
			if t == True {
				matched = true
				out = append(out, mergeRow(l, r))
			}
		}
		if n.Kind == LeftJoin && !matched {
			out = append(out, mergeRow(l, nullRow(n.Right.Schema())))
		}
	}
	return out, nil
}

func (n *Join) Schema() []string {
	seen := map[string]bool{}
	var cols []string
	for _, c := range n.Left.Schema() {
		if !seen[c] {
			seen[c] = true
			cols = append(cols, c)
		}
	}
	for _, c := range n.Right.Schema() {
		if !seen[c] {
			seen[c] = true
			cols = append(cols, c)
		}
	}
	return cols
}

func (n *Join) ToJSON() any {
	how := "inner"
	if n.Kind == LeftJoin {
		how = "left"
	}
	return map[string]any{"op": "join", "how": how, "left": n.Left.ToJSON(), "right": n.Right.ToJSON(), "pred": n.Pred.ToJSON()}
}

// SemiJoin 保留在右侧存在匹配（谓词为 TRUE）的左侧行，每行至多输出一次，
// 因此保留左侧行的重复度且不引入右侧的乘法重复。仅由改写器生成。
type SemiJoin struct {
	Left, Right Node
	Pred        Expr
}

func (n *SemiJoin) Exec(outer Row) ([]Row, error) {
	ls, err := n.Left.Exec(outer)
	if err != nil {
		return nil, err
	}
	rs, err := n.Right.Exec(outer)
	if err != nil {
		return nil, err
	}
	var out []Row
	for _, l := range ls {
		for _, r := range rs {
			t, err := boolOf(n.Pred, mergeRow(l, r), outer)
			if err != nil {
				return nil, err
			}
			if t == True {
				out = append(out, l)
				break
			}
		}
	}
	return out, nil
}

func (n *SemiJoin) Schema() []string { return n.Left.Schema() }

func (n *SemiJoin) ToJSON() any {
	return map[string]any{"op": "semijoin", "left": n.Left.ToJSON(), "right": n.Right.ToJSON(), "pred": n.Pred.ToJSON()}
}

// AggSpec 是一个聚合：Func 目前仅支持 "count"；Col 为 "*" 或列名。
type AggSpec struct {
	Func string
	Col  string
	As   string
}

// GroupBy 按键分组并计算聚合；空输入产生零个分组。
// NULL 键按 SQL 习惯归入同一组。
type GroupBy struct {
	Input Node
	Keys  []string
	Aggs  []AggSpec
}

func (n *GroupBy) Exec(outer Row) ([]Row, error) {
	in, err := n.Input.Exec(outer)
	if err != nil {
		return nil, err
	}
	type group struct {
		keyVals []any
		rows    []Row
	}
	groups := map[string]*group{}
	var order []string
	for _, r := range in {
		var keyVals []any
		var parts []string
		for _, k := range n.Keys {
			v := r[k]
			keyVals = append(keyVals, v)
			parts = append(parts, Canonical(v))
		}
		gk := strings.Join(parts, "|")
		g, ok := groups[gk]
		if !ok {
			g = &group{keyVals: keyVals}
			groups[gk] = g
			order = append(order, gk)
		}
		g.rows = append(g.rows, r)
	}
	var out []Row
	for _, gk := range order {
		g := groups[gk]
		row := Row{}
		for i, k := range n.Keys {
			row[k] = g.keyVals[i]
		}
		for _, a := range n.Aggs {
			if a.Func != "count" {
				return nil, fmt.Errorf("不支持的聚合函数 %q", a.Func)
			}
			var cnt int64
			if a.Col == "*" {
				cnt = int64(len(g.rows))
			} else {
				for _, r := range g.rows {
					if r[a.Col] != nil {
						cnt++
					}
				}
			}
			row[a.As] = cnt
		}
		out = append(out, row)
	}
	return out, nil
}

func (n *GroupBy) Schema() []string {
	cols := append([]string{}, n.Keys...)
	for _, a := range n.Aggs {
		cols = append(cols, a.As)
	}
	return cols
}

func (n *GroupBy) ToJSON() any {
	aggs := make([]any, len(n.Aggs))
	for i, a := range n.Aggs {
		aggs[i] = map[string]any{"func": a.Func, "col": a.Col, "as": a.As}
	}
	return map[string]any{"op": "groupby", "input": n.Input.ToJSON(), "keys": n.Keys, "aggs": aggs}
}
