package relalg

import (
	"errors"
	"fmt"
	"sort"
)

// BuildPlan 将查询核心 AST 转换为逻辑执行计划树：
//   Project
//     └── Filter (WHERE，可选)
//           └── Join ... (FROM 中的 JOIN 链，可选)
//                 └── Scan
func BuildPlan(stmt *SelectStmt) (Plan, error) {
	if stmt == nil || stmt.From == nil {
		return nil, errors.New("empty SELECT statement")
	}
	for _, e := range stmt.Projection {
		switch e.(type) {
		case *ColumnRef, *Star:
		default:
			return nil, fmt.Errorf("unsupported projection expression: %s", e)
		}
	}
	colsByRef := collectColumns(stmt)
	scan := func(t *TableSource) *Scan {
		return &Scan{Table: t.Name, Alias: t.Alias, Columns: colsByRef[t.Ref()]}
	}
	var plan Plan = scan(stmt.From)
	for _, jc := range stmt.From.Joins {
		plan = &Join{Kind: jc.Kind, Cond: jc.On, Left: plan, Right: scan(jc.Table)}
	}
	if stmt.Where != nil {
		plan = &Filter{Cond: stmt.Where, Input: plan}
	}
	plan = &Project{Exprs: stmt.Projection, Input: plan}
	return plan, nil
}

// collectColumns 收集查询中各表引用名（别名）被引用的列，按列名排序。
func collectColumns(stmt *SelectStmt) map[string][]string {
	sets := map[string]map[string]bool{}
	add := func(e Expr) {
		WalkExpr(e, func(x Expr) {
			if c, ok := x.(*ColumnRef); ok && c.Table != "" {
				if sets[c.Table] == nil {
					sets[c.Table] = map[string]bool{}
				}
				sets[c.Table][c.Name] = true
			}
		})
	}
	for _, e := range stmt.Projection {
		add(e)
	}
	if stmt.Where != nil {
		add(stmt.Where)
	}
	for _, jc := range stmt.From.Joins {
		add(jc.On)
	}
	out := map[string][]string{}
	for ref, set := range sets {
		cols := make([]string, 0, len(set))
		for c := range set {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		out[ref] = cols
	}
	return out
}
