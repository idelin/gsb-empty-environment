package main

import (
	"fmt"
	"strings"
)

type analysis struct {
	scanTable  string
	scanSchema []string
	innerPreds []Expr
	innerKeys  []string
	outerKeys  []string
}

func (a analysis) innerPlan() Plan {
	var p Plan = Scan{Table: a.scanTable}
	if len(a.innerPreds) > 0 {
		p = Filter{Input: p, Pred: andOf(a.innerPreds)}
	}
	return p
}

func (a analysis) keyPairs() string {
	var ps []string
	for i := range a.innerKeys {
		ps = append(ps, a.innerKeys[i]+" = "+a.outerKeys[i])
	}
	if len(ps) == 0 {
		return "(无关联条件，子查询不相关)"
	}
	return strings.Join(ps, ", ")
}

func analyzeCorrelated(sub Plan, outerSchema []string, env *Env) (analysis, error) {
	var preds []Expr
	cur := sub
	for {
		switch n := cur.(type) {
		case Filter:
			if n.Pred == nil {
				return analysis{}, fmt.Errorf("拒绝改写：子查询中的 FILTER 缺少谓词")
			}
			preds = append(preds, splitAnd(n.Pred)...)
			cur = n.Input
		case Scan:
			schema, err := env.schema(n.Table)
			if err != nil {
				return analysis{}, err
			}
			a := analysis{scanTable: n.Table, scanSchema: schema}
			inner := map[string]bool{}
			for _, c := range schema {
				inner[c] = true
			}
			outer := map[string]bool{}
			for _, c := range outerSchema {
				outer[c] = true
			}
			for _, p := range preds {
				if containsSubq(p) {
					return analysis{}, fmt.Errorf("拒绝改写：子查询谓词中嵌套了子查询：%s", exprString(p))
				}
				hasInner, hasOuter := false, false
				for r := range colRefs(p) {
					switch {
					case inner[r] && outer[r]:
						return analysis{}, fmt.Errorf("拒绝改写：列 %s 同时是内层和外层列（自相关需要表别名，暂不支持）", r)
					case inner[r]:
						hasInner = true
					case outer[r]:
						hasOuter = true
					default:
						return analysis{}, fmt.Errorf("拒绝改写：谓词引用了未知列 %s", r)
					}
				}
				if !hasOuter {
					a.innerPreds = append(a.innerPreds, p)
					continue
				}
				if !hasInner {
					return analysis{}, fmt.Errorf("拒绝改写：谓词只引用外层列（应提升到外层），得到 %s", exprString(p))
				}
				cmp, ok := p.(Cmp)
				if !ok || cmp.Op != "eq" {
					return analysis{}, fmt.Errorf("拒绝改写：相关条件必须是 内层列=外层列 的等值条件，得到 %s", exprString(p))
				}
				lc, lok := cmp.L.(Col)
				rc, rok := cmp.R.(Col)
				if !lok || !rok {
					return analysis{}, fmt.Errorf("拒绝改写：相关等值条件两侧必须都是列引用，得到 %s", exprString(p))
				}
				switch {
				case inner[lc.Name] && !inner[rc.Name] && outer[rc.Name]:
					a.innerKeys = append(a.innerKeys, lc.Name)
					a.outerKeys = append(a.outerKeys, rc.Name)
				case inner[rc.Name] && !inner[lc.Name] && outer[lc.Name]:
					a.innerKeys = append(a.innerKeys, rc.Name)
					a.outerKeys = append(a.outerKeys, lc.Name)
				default:
					return analysis{}, fmt.Errorf("拒绝改写：相关条件必须一端是内层列、另一端是外层列，得到 %s", exprString(p))
				}
			}
			return a, nil
		default:
			return analysis{}, fmt.Errorf("拒绝改写：相关子查询只支持 FILTER over SCAN 形态，得到算子 %T", cur)
		}
	}
}

type rewriter struct {
	env     *Env
	notes   []string
	counter int
}

func Rewrite(p Plan, env *Env) (Plan, []string, error) {
	rw := &rewriter{env: env}
	out, err := rw.plan(p)
	if err != nil {
		return nil, nil, err
	}
	if len(rw.notes) == 0 {
		rw.notes = append(rw.notes, "计划中没有可改写的相关子查询，原样返回")
	}
	return out, rw.notes, nil
}

func (rw *rewriter) plan(p Plan) (Plan, error) {
	switch n := p.(type) {
	case Scan:
		return n, nil

	case Filter:
		input, err := rw.plan(n.Input)
		if err != nil {
			return nil, err
		}
		if n.Pred == nil {
			return nil, fmt.Errorf("FILTER requires a predicate")
		}
		schema, err := evalSchema(input, rw.env)
		if err != nil {
			return nil, err
		}
		cur := input
		var rest []Expr
		for _, c := range splitAnd(n.Pred) {
			if ex, ok := c.(Exists); ok {
				cur, err = rw.existsToSemiJoin(cur, ex, schema)
				if err != nil {
					return nil, err
				}
				continue
			}
			if containsSubq(c) {
				return nil, fmt.Errorf("拒绝改写：EXISTS/COUNT 子查询必须是 WHERE 顶层 AND 合取项（不支持 OR/NOT 之下或谓词内嵌套）：%s", exprString(c))
			}
			rest = append(rest, c)
		}
		if len(rest) > 0 {
			cur = Filter{Input: cur, Pred: andOf(rest)}
		}
		return cur, nil

	case Project:
		input, err := rw.plan(n.Input)
		if err != nil {
			return nil, err
		}
		schema, err := evalSchema(input, rw.env)
		if err != nil {
			return nil, err
		}
		cur := input
		cols := make([]ProjCol, len(n.Cols))
		for i, pc := range n.Cols {
			cs, ok := pc.Expr.(CountSubq)
			if !ok {
				if containsSubq(pc.Expr) {
					return nil, fmt.Errorf("拒绝改写：输出列 %q 的表达式内嵌套子查询（仅支持顶层标量 COUNT）", pc.Name)
				}
				cols[i] = pc
				continue
			}
			a, err := analyzeCorrelated(cs.Sub, schema, rw.env)
			if err != nil {
				return nil, err
			}
			if cs.Arg != "*" && !containsStr(a.scanSchema, cs.Arg) {
				return nil, fmt.Errorf("拒绝改写：COUNT 参数列 %s 不是内层表的列", cs.Arg)
			}
			rw.counter++
			alias := fmt.Sprintf("g%d", rw.counter)
			agg := Aggregate{
				Alias:   alias,
				Input:   a.innerPlan(),
				GroupBy: append([]string(nil), a.innerKeys...),
				Aggs:    []AggSpec{{Name: "cnt", Fn: "count", Arg: cs.Arg}},
			}
			var conds []Expr
			for k := range a.innerKeys {
				conds = append(conds, Cmp{Op: "eq", L: Col{Name: a.outerKeys[k]}, R: Col{Name: alias + "." + shortName(a.innerKeys[k])}})
			}
			cur = Join{Kind: LeftJoin, Left: cur, Right: agg, On: andOf(conds)}
			cols[i] = ProjCol{Name: pc.Name, Expr: Coalesce{Args: []Expr{Col{Name: alias + ".cnt"}, Lit{V: IntV(0)}}}}
			rw.notes = append(rw.notes, fmt.Sprintf(
				"标量 COUNT（输出列 %q）→ 按内层键预聚合后 LEFT JOIN：关联条件 %s；聚合键唯一 ⇒ 外层行的重复度不变；无匹配时 COALESCE(%s.cnt, 0) 保证 COUNT=0",
				pc.Name, a.keyPairs(), alias))
		}
		return Project{Input: cur, Cols: cols}, nil

	case Join:
		l, err := rw.plan(n.Left)
		if err != nil {
			return nil, err
		}
		r, err := rw.plan(n.Right)
		if err != nil {
			return nil, err
		}
		if containsSubq(n.On) {
			return nil, fmt.Errorf("拒绝改写：JOIN ON 条件中出现子查询")
		}
		return Join{Kind: n.Kind, Left: l, Right: r, On: n.On}, nil

	case SemiJoin:
		l, err := rw.plan(n.Left)
		if err != nil {
			return nil, err
		}
		r, err := rw.plan(n.Right)
		if err != nil {
			return nil, err
		}
		if containsSubq(n.On) {
			return nil, fmt.Errorf("拒绝改写：SEMIJOIN ON 条件中出现子查询")
		}
		return SemiJoin{Left: l, Right: r, On: n.On}, nil

	case Aggregate:
		input, err := rw.plan(n.Input)
		if err != nil {
			return nil, err
		}
		return Aggregate{Alias: n.Alias, Input: input, GroupBy: n.GroupBy, Aggs: n.Aggs}, nil
	}
	return nil, fmt.Errorf("unknown plan node %T", p)
}

func (rw *rewriter) existsToSemiJoin(input Plan, ex Exists, outerSchema []string) (Plan, error) {
	a, err := analyzeCorrelated(ex.Sub, outerSchema, rw.env)
	if err != nil {
		return nil, err
	}
	var conds []Expr
	for k := range a.innerKeys {
		conds = append(conds, Cmp{Op: "eq", L: Col{Name: a.outerKeys[k]}, R: Col{Name: a.innerKeys[k]}})
	}
	rw.notes = append(rw.notes, fmt.Sprintf(
		"EXISTS → 半连接（semijoin）：关联条件 %s；每个外层行最多保留一次 ⇒ 重复度不变；NULL 键等值为 unknown，不会误匹配",
		a.keyPairs()))
	return SemiJoin{Left: input, Right: a.innerPlan(), On: andOf(conds)}, nil
}
