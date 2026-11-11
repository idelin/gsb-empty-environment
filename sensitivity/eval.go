package sensitivity

import (
	"fmt"
	"math/big"
)

// Result 是 AST 节点在具体数据上的求值结果：
// 表节点填充 Table，标量节点（计数、求和）填充 Scalar。
type Result struct {
	Table  *Table
	Scalar Cell
}

// IsScalar 报告该结果是否来自标量节点。
func (r Result) IsScalar() bool { return r.Table == nil }

// Eval 依据输入表字典对 AST 求值。tables 的键为 [Source.Rel]。
// 所有表与中间结果均被深拷贝，求值不会修改入参。
func Eval(root Node, tables map[string]Table) (Result, error) {
	if err := validateAST(root); err != nil {
		return Result{}, err
	}
	memo := map[Node]Result{}
	var eval func(Node) (Result, error)
	eval = func(n Node) (Result, error) {
		if r, ok := memo[n]; ok {
			return r, nil
		}
		childResults := make([]Result, 0, len(n.children()))
		for _, c := range n.children() {
			r, err := eval(c)
			if err != nil {
				return Result{}, err
			}
			childResults = append(childResults, r)
		}
		r, err := evalNode(n, childResults, tables)
		if err != nil {
			return Result{}, err
		}
		memo[n] = r
		return r, nil
	}
	return eval(root)
}

func evalNode(n Node, in []Result, tables map[string]Table) (Result, error) {
	switch v := n.(type) {
	case *Source:
		tab, ok := tables[v.Rel]
		if !ok {
			return Result{}, fmt.Errorf("sensitivity: 缺少输入表 %q", v.Rel)
		}
		if err := tab.validate(); err != nil {
			return Result{}, err
		}
		if len(tab.Schema) != len(v.Cols) {
			return Result{}, fmt.Errorf("sensitivity: 源 %q 模式宽度不匹配（AST %d 列，数据 %d 列）",
				v.Rel, len(v.Cols), len(tab.Schema))
		}
		for i := range v.Cols {
			if v.Cols[i] != tab.Schema[i] {
				return Result{}, fmt.Errorf("sensitivity: 源 %q 第 %d 列名不匹配（AST %q，数据 %q）",
					v.Rel, i, v.Cols[i], tab.Schema[i])
			}
		}
		return Result{Table: cloneTable(tab)}, nil

	case *BoundedCount:
		tab := in[0].Table
		count := int64(len(tab.Rows))
		if count > v.Cap {
			count = v.Cap
		}
		return Result{Scalar: big.NewRat(count, 1)}, nil

	case *TruncatedSum:
		tab := in[0].Table
		idx, err := tab.columnIndex(v.Col)
		if err != nil {
			return Result{}, err
		}
		total := new(big.Rat)
		for _, row := range tab.Rows {
			cell := row.Values[idx]
			if cell == nil {
				continue
			}
			total.Add(total, clamp(cell, v.Lower, v.Upper))
		}
		return Result{Scalar: total}, nil

	case *Project:
		tab := in[0].Table
		drop := map[string]bool{}
		for _, d := range v.Drop {
			drop[d] = true
		}
		keep := make([]int, 0, len(tab.Schema))
		schema := make([]string, 0, len(tab.Schema))
		for i, name := range tab.Schema {
			if !drop[name] {
				keep = append(keep, i)
				schema = append(schema, name)
			}
		}
		rows := make([]Row, len(tab.Rows))
		for i, row := range tab.Rows {
			values := make([]Cell, len(keep))
			for k, colIdx := range keep {
				values[k] = ratClone(row.Values[colIdx])
			}
			rows[i] = Row{Values: values}
		}
		return Result{Table: &Table{Name: v.Name(), Schema: schema, Rows: rows}}, nil

	case *Union:
		l, r := in[0].Table, in[1].Table
		rows := make([]Row, 0, len(l.Rows)+len(r.Rows))
		for _, row := range l.Rows {
			rows = append(rows, rowClone(row))
		}
		for _, row := range r.Rows {
			rows = append(rows, rowClone(row))
		}
		return Result{Table: &Table{Name: v.Name(), Schema: append([]string(nil), l.Schema...), Rows: rows}}, nil

	case *Join:
		l, r := in[0].Table, in[1].Table
		li, err := l.columnIndex(v.LeftKey)
		if err != nil {
			return Result{}, err
		}
		ri, err := r.columnIndex(v.RightKey)
		if err != nil {
			return Result{}, err
		}
		index := map[string][]Row{}
		for _, row := range r.Rows {
			key := row.Values[ri]
			if key == nil {
				continue
			}
			index[key.RatString()] = append(index[key.RatString()], rowClone(row))
		}
		schema := append(append([]string(nil), l.Schema...), r.Schema...)
		var rows []Row
		for _, left := range l.Rows {
			key := left.Values[li]
			if key == nil {
				continue
			}
			for _, right := range index[key.RatString()] {
				values := make([]Cell, 0, len(left.Values)+len(right.Values))
				for _, c := range left.Values {
					values = append(values, ratClone(c))
				}
				for _, c := range right.Values {
					values = append(values, ratClone(c))
				}
				rows = append(rows, Row{Values: values})
			}
		}
		return Result{Table: &Table{Name: v.Name(), Schema: schema, Rows: rows}}, nil

	default:
		return Result{}, fmt.Errorf("sensitivity: 未知节点类型 %T", n)
	}
}

func clamp(x, lower, upper Cell) Cell {
	out := new(big.Rat).Set(x)
	if lower != nil && out.Cmp(lower) < 0 {
		out.Set(lower)
	}
	if upper != nil && out.Cmp(upper) > 0 {
		out.Set(upper)
	}
	return out
}

func cloneTable(t Table) *Table {
	out := &Table{Name: t.Name, Schema: append([]string(nil), t.Schema...)}
	out.Rows = make([]Row, len(t.Rows))
	for i, row := range t.Rows {
		out.Rows[i] = rowClone(row)
	}
	return out
}
