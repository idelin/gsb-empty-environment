package ra

import "fmt"

// Expr 是标量表达式。row 为当前行的列，outer 为相关子查询中外层查询的行。
type Expr interface {
	Eval(row, outer Row) (any, error)
	// RefsOuter 报告表达式是否引用外层查询的列。
	RefsOuter() bool
	// ToJSON 序列化为计划 JSON。
	ToJSON() any
}

type Lit struct{ V any }

func (e *Lit) Eval(row, outer Row) (any, error) { return e.V, nil }
func (e *Lit) RefsOuter() bool                  { return false }
func (e *Lit) ToJSON() any                      { return map[string]any{"kind": "lit", "value": e.V} }

// Col 引用当前（最内层）行的列；列缺失时按 NULL 处理。
type Col struct{ Name string }

func (e *Col) Eval(row, outer Row) (any, error) { return row[e.Name], nil }
func (e *Col) RefsOuter() bool                  { return false }
func (e *Col) ToJSON() any                      { return map[string]any{"kind": "col", "name": e.Name} }

// Outer 引用外层查询行的列（相关列）。
type Outer struct{ Name string }

func (e *Outer) Eval(row, outer Row) (any, error) {
	if outer == nil {
		return nil, fmt.Errorf("外层列 %q 出现在非子查询上下文中", e.Name)
	}
	return outer[e.Name], nil
}
func (e *Outer) RefsOuter() bool { return true }
func (e *Outer) ToJSON() any     { return map[string]any{"kind": "outer", "name": e.Name} }

type And struct{ Args []Expr }

func (e *And) Eval(row, outer Row) (any, error) {
	r := True
	for _, a := range e.Args {
		v, err := boolOf(a, row, outer)
		if err != nil {
			return nil, err
		}
		r = And3(r, v)
	}
	return r == True, nil
}
func (e *And) RefsOuter() bool { return anyRefsOuter(e.Args) }
func (e *And) ToJSON() any     { return map[string]any{"kind": "and", "args": exprsJSON(e.Args)} }

type Or struct{ Args []Expr }

func (e *Or) Eval(row, outer Row) (any, error) {
	r := False
	for _, a := range e.Args {
		v, err := boolOf(a, row, outer)
		if err != nil {
			return nil, err
		}
		r = Or3(r, v)
	}
	return r == True, nil
}
func (e *Or) RefsOuter() bool { return anyRefsOuter(e.Args) }
func (e *Or) ToJSON() any     { return map[string]any{"kind": "or", "args": exprsJSON(e.Args)} }

type Not struct{ E Expr }

func (e *Not) Eval(row, outer Row) (any, error) {
	v, err := boolOf(e.E, row, outer)
	if err != nil {
		return nil, err
	}
	return Not3(v) == True, nil
}
func (e *Not) RefsOuter() bool { return e.E.RefsOuter() }
func (e *Not) ToJSON() any     { return map[string]any{"kind": "not", "arg": e.E.ToJSON()} }

// Cmp 是比较运算：eq/ne/lt/le/gt/ge，三值逻辑。
type Cmp struct {
	Op   string
	L, R Expr
}

func (e *Cmp) Eval(row, outer Row) (any, error) {
	l, err := e.L.Eval(row, outer)
	if err != nil {
		return nil, err
	}
	r, err := e.R.Eval(row, outer)
	if err != nil {
		return nil, err
	}
	var t Trilean
	switch e.Op {
	case "eq":
		t = Eq3(l, r)
	case "ne":
		t = Not3(Eq3(l, r))
	case "lt":
		t, err = Less3(l, r)
	case "le":
		var lt Trilean
		lt, err = Less3(l, r)
		if err == nil {
			t = Or3(lt, Eq3(l, r))
		}
	case "gt":
		t, err = Less3(r, l)
	case "ge":
		var lt Trilean
		lt, err = Less3(r, l)
		if err == nil {
			t = Or3(lt, Eq3(l, r))
		}
	default:
		return nil, fmt.Errorf("未知比较运算 %q", e.Op)
	}
	if err != nil {
		return nil, err
	}
	return t == True, nil
}
func (e *Cmp) RefsOuter() bool { return e.L.RefsOuter() || e.R.RefsOuter() }
func (e *Cmp) ToJSON() any {
	return map[string]any{"kind": "cmp", "op": e.Op, "l": e.L.ToJSON(), "r": e.R.ToJSON()}
}

type IsNull struct{ E Expr }

func (e *IsNull) Eval(row, outer Row) (any, error) {
	v, err := e.E.Eval(row, outer)
	if err != nil {
		return nil, err
	}
	return v == nil, nil
}
func (e *IsNull) RefsOuter() bool { return e.E.RefsOuter() }
func (e *IsNull) ToJSON() any     { return map[string]any{"kind": "isnull", "arg": e.E.ToJSON()} }

type Coalesce struct{ Args []Expr }

func (e *Coalesce) Eval(row, outer Row) (any, error) {
	for _, a := range e.Args {
		v, err := a.Eval(row, outer)
		if err != nil {
			return nil, err
		}
		if v != nil {
			return v, nil
		}
	}
	return nil, nil
}
func (e *Coalesce) RefsOuter() bool { return anyRefsOuter(e.Args) }
func (e *Coalesce) ToJSON() any {
	return map[string]any{"kind": "coalesce", "args": exprsJSON(e.Args)}
}

// Exists 是相关/非相关 EXISTS 子查询。
type Exists struct{ Sub Node }

func (e *Exists) Eval(row, outer Row) (any, error) {
	rows, err := e.Sub.Exec(row)
	if err != nil {
		return nil, err
	}
	return len(rows) > 0, nil
}
func (e *Exists) RefsOuter() bool { return false }
func (e *Exists) ToJSON() any     { return map[string]any{"kind": "exists", "plan": e.Sub.ToJSON()} }

// CountSub 是标量 COUNT 子查询。Col 为 "*" 表示 COUNT(*)，否则为 COUNT(column)（不计 NULL）。
type CountSub struct {
	Col string
	Sub Node
}

func (e *CountSub) Eval(row, outer Row) (any, error) {
	rows, err := e.Sub.Exec(row)
	if err != nil {
		return nil, err
	}
	if e.Col == "*" {
		return int64(len(rows)), nil
	}
	var n int64
	for _, r := range rows {
		if r[e.Col] != nil {
			n++
		}
	}
	return n, nil
}
func (e *CountSub) RefsOuter() bool { return false }
func (e *CountSub) ToJSON() any {
	return map[string]any{"kind": "count", "col": e.Col, "plan": e.Sub.ToJSON()}
}

// boolOf 求值并按 SQL 三值逻辑解释：true/false 之外（含 NULL）视为 Unknown。
func boolOf(e Expr, row, outer Row) (Trilean, error) {
	v, err := e.Eval(row, outer)
	if err != nil {
		return Unknown, err
	}
	switch b := v.(type) {
	case bool:
		if b {
			return True, nil
		}
		return False, nil
	case nil:
		return Unknown, nil
	default:
		return Unknown, fmt.Errorf("期望布尔表达式，得到 %v", v)
	}
}

func anyRefsOuter(es []Expr) bool {
	for _, e := range es {
		if e.RefsOuter() {
			return true
		}
	}
	return false
}

func exprsJSON(es []Expr) []any {
	out := make([]any, len(es))
	for i, e := range es {
		out[i] = e.ToJSON()
	}
	return out
}

// SplitAnd 把合取表达式拆成合取项列表。
func SplitAnd(e Expr) []Expr {
	if a, ok := e.(*And); ok {
		var out []Expr
		for _, c := range a.Args {
			out = append(out, SplitAnd(c)...)
		}
		return out
	}
	return []Expr{e}
}

// JoinAnd 把合取项列表合成一个表达式；空列表为 TRUE。
func JoinAnd(es []Expr) Expr {
	switch len(es) {
	case 0:
		return &Lit{V: true}
	case 1:
		return es[0]
	default:
		return &And{Args: es}
	}
}
