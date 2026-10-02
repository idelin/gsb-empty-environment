package cteopt

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Row is one result tuple. Column keys are "alias.column".
type Row map[string]interface{}

// Engine executes plans against in-memory tables and instruments how many
// times each plan node is evaluated (to observe reuse vs. recomputation).
type Engine struct {
	Tables     map[string][]Row
	ExecCounts map[*Node]int
}

func NewEngine(tables map[string][]Row) *Engine {
	return &Engine{Tables: tables}
}

// Execute runs the plan and returns all result rows.
func (e *Engine) Execute(n *Node) []Row {
	e.ExecCounts = map[*Node]int{}
	return e.exec(n)
}

func (e *Engine) exec(n *Node) []Row {
	e.ExecCounts[n]++
	switch n.Op {
	case Scan:
		return rekey(e.Tables[n.Table], n.Alias)
	case Filter:
		return filterRows(e.exec(n.Children[0]), n.Pred)
	case Project:
		return projectRows(e.exec(n.Children[0]), n.Cols)
	case Rename:
		return rekey(e.exec(n.Children[0]), n.Alias)
	case Join:
		return joinRows(e.exec(n.Children[0]), e.exec(n.Children[1]), n.JoinKey)
	case Materialize:
		if n.Buf == nil {
			n.Buf = e.exec(n.Children[0])
		}
		return n.Buf
	case CTERef:
		return e.exec(n.Src)
	}
	panic(fmt.Sprintf("unknown op %v", n.Op))
}

// rekey strips any existing prefix and re-prefixes every column.
func rekey(rows []Row, prefix string) []Row {
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		nr := Row{}
		for k, v := range r {
			col := k
			if i := strings.LastIndex(k, "."); i >= 0 {
				col = k[i+1:]
			}
			nr[prefix+"."+col] = v
		}
		out = append(out, nr)
	}
	return out
}

func filterRows(rows []Row, pred string) []Row {
	conds := parsePred(pred)
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		ok := true
		for _, c := range conds {
			if !matchCond(r, c) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, r)
		}
	}
	return out
}

func projectRows(rows []Row, cols []string) []Row {
	if len(cols) == 1 && cols[0] == "*" {
		return rows
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		nr := Row{}
		for _, c := range cols {
			if v, ok := resolve(r, c); ok {
				nr[c] = v
			}
		}
		out = append(out, nr)
	}
	return out
}

func joinRows(left, right []Row, key [2]string) []Row {
	var out []Row
	for _, l := range left {
		lv, ok := resolve(l, key[0])
		if !ok {
			continue
		}
		for _, r := range right {
			rv, ok := resolve(r, key[1])
			if !ok || !equal(lv, rv) {
				continue
			}
			merged := Row{}
			for k, v := range l {
				merged[k] = v
			}
			for k, v := range r {
				merged[k] = v
			}
			out = append(out, merged)
		}
	}
	return out
}

// resolve finds a column, allowing an unqualified name to match a unique
// "alias.column" key by suffix.
func resolve(r Row, col string) (interface{}, bool) {
	if v, ok := r[col]; ok {
		return v, true
	}
	suffix := "." + col
	var found interface{}
	count := 0
	for k, v := range r {
		if strings.HasSuffix(k, suffix) {
			found = v
			count++
		}
	}
	if count == 1 {
		return found, true
	}
	return nil, false
}

func equal(a, b interface{}) bool {
	if fa, ok := a.(float64); ok {
		fb, ok := b.(float64)
		return ok && fa == fb
	}
	return a == b
}

type cond struct {
	col string
	op  string
	val interface{}
}

func parsePred(pred string) []cond {
	var out []cond
	for _, part := range strings.Split(pred, " AND ") {
		f := strings.Fields(part)
		if len(f) != 3 {
			continue
		}
		var v interface{}
		s := f[2]
		if strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'") {
			v = s[1 : len(s)-1]
		} else if num, err := strconv.ParseFloat(s, 64); err == nil {
			v = num
		} else {
			v = s
		}
		out = append(out, cond{col: f[0], op: f[1], val: v})
	}
	return out
}

func matchCond(r Row, c cond) bool {
	v, ok := resolve(r, c.col)
	if !ok {
		return false
	}
	cmp, ok := compare(v, c.val)
	if !ok {
		return false
	}
	switch c.op {
	case "=":
		return cmp == 0
	case "!=":
		return cmp != 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	}
	return false
}

func compare(a, b interface{}) (int, bool) {
	if fa, ok := a.(float64); ok {
		fb, ok := b.(float64)
		if !ok {
			return 0, false
		}
		switch {
		case fa < fb:
			return -1, true
		case fa > fb:
			return 1, true
		}
		return 0, true
	}
	sa, ok1 := a.(string)
	sb, ok2 := b.(string)
	if !ok1 || !ok2 {
		return 0, false
	}
	return strings.Compare(sa, sb), true
}

// RowKey canonicalizes a row for comparison in tests and examples.
func RowKey(r Row) string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%v;", k, r[k])
	}
	return b.String()
}
