package ra

import "fmt"

// keyEq 记录一对相关等值：外层列 = 内层列。
type keyEq struct {
	outerCol string
	innerCol string
}

// decorrelate 分析子查询计划：把根部的 Filter 谓词拆成
//  1. 相关等值（外层列 = 内层列），
//  2. 不引用外层的剩余谓词（可下推到内层）。
//
// 任何其他形式的相关谓词都导致明确拒绝。
func decorrelate(sub Node) (inner Node, keys []keyEq, residual []Expr, err error) {
	inner = sub
	var pred Expr = &Lit{V: true}
	if f, ok := sub.(*Filter); ok {
		inner = f.Input
		pred = f.Pred
	}
	for _, c := range SplitAnd(pred) {
		if !c.RefsOuter() {
			residual = append(residual, c)
			continue
		}
		ke, ok := asKeyEq(c)
		if !ok {
			return nil, nil, nil, fmt.Errorf("相关条件 %v 不是外层键等值，拒绝改写", c.ToJSON())
		}
		keys = append(keys, ke)
	}
	return inner, keys, residual, nil
}

// asKeyEq 识别 "外层列 = 内层列"（两侧可交换）；
// 内层一侧不得再引用外层，等值两端都必须是裸列引用。
func asKeyEq(e Expr) (keyEq, bool) {
	cmp, ok := e.(*Cmp)
	if !ok || cmp.Op != "eq" {
		return keyEq{}, false
	}
	if o, ok := cmp.L.(*Outer); ok {
		if c, ok := cmp.R.(*Col); ok {
			return keyEq{outerCol: o.Name, innerCol: c.Name}, true
		}
	}
	if o, ok := cmp.R.(*Outer); ok {
		if c, ok := cmp.L.(*Col); ok {
			return keyEq{outerCol: o.Name, innerCol: c.Name}, true
		}
	}
	return keyEq{}, false
}

type rewriter struct {
	fresh int
	conds []string
}

func (r *rewriter) name(prefix string) string {
	r.fresh++
	return fmt.Sprintf("__%s%d", prefix, r.fresh)
}

// Rewrite 对计划做去相关改写，返回改写后的计划与适用条件说明。
// 遇到不支持的相关表达式时返回错误（明确拒绝改写）。
func Rewrite(n Node) (Node, []string, error) {
	r := &rewriter{}
	out, err := r.node(n)
	if err != nil {
		return nil, r.conds, err
	}
	if r.fresh == 0 {
		r.conds = append(r.conds, "计划中没有可改写的相关子查询，原样返回")
	}
	return out, r.conds, nil
}

func (r *rewriter) node(n Node) (Node, error) {
	switch n := n.(type) {
	case *Scan:
		return n, nil
	case *Filter:
		in, err := r.node(n.Input)
		if err != nil {
			return nil, err
		}
		return r.filter(in, n.Pred)
	case *Project:
		in, err := r.node(n.Input)
		if err != nil {
			return nil, err
		}
		return r.project(in, n.Exprs)
	case *Join:
		l, err := r.node(n.Left)
		if err != nil {
			return nil, err
		}
		rt, err := r.node(n.Right)
		if err != nil {
			return nil, err
		}
		if n.Pred.RefsOuter() {
			return nil, fmt.Errorf("连接谓词引用外层列，拒绝改写")
		}
		return &Join{Kind: n.Kind, Left: l, Right: rt, Pred: n.Pred}, nil
	case *SemiJoin:
		l, err := r.node(n.Left)
		if err != nil {
			return nil, err
		}
		rt, err := r.node(n.Right)
		if err != nil {
			return nil, err
		}
		return &SemiJoin{Left: l, Right: rt, Pred: n.Pred}, nil
	case *GroupBy:
		in, err := r.node(n.Input)
		if err != nil {
			return nil, err
		}
		return &GroupBy{Input: in, Keys: n.Keys, Aggs: n.Aggs}, nil
	}
	return nil, fmt.Errorf("未知计划节点 %T", n)
}

// filter 处理 Filter 谓词中的 EXISTS 合取项：每个 EXISTS 改写为一个半连接，
// 其余合取项保留在半连接之上的 Filter 中。
func (r *rewriter) filter(input Node, pred Expr) (Node, error) {
	var exists []*Exists
	var rest []Expr
	for _, c := range SplitAnd(pred) {
		switch c := c.(type) {
		case *Exists:
			exists = append(exists, c)
		case *Not:
			if _, ok := c.E.(*Exists); ok {
				return nil, fmt.Errorf("NOT EXISTS 不在支持范围内，拒绝改写")
			}
			rest = append(rest, c)
		default:
			if containsSubquery(c) {
				return nil, fmt.Errorf("谓词中嵌套的子查询表达式不在支持范围内，拒绝改写")
			}
			rest = append(rest, c)
		}
	}
	cur := input
	for _, ex := range exists {
		sub, err := r.node(ex.Sub)
		if err != nil {
			return nil, err
		}
		semi, err := r.existsToSemi(cur, sub)
		if err != nil {
			return nil, err
		}
		cur = semi
	}
	if len(rest) == 0 {
		return cur, nil
	}
	return &Filter{Input: cur, Pred: JoinAnd(rest)}, nil
}

func (r *rewriter) existsToSemi(outerPlan, sub Node) (Node, error) {
	inner, keys, residual, err := decorrelate(sub)
	if err != nil {
		return nil, err
	}
	if len(residual) > 0 {
		inner = &Filter{Input: inner, Pred: JoinAnd(residual)}
	}
	var joinConds []Expr
	if len(keys) > 0 {
		var proj []NamedExpr
		for _, ke := range keys {
			alias := r.name("ek")
			proj = append(proj, NamedExpr{Expr: &Col{Name: ke.innerCol}, As: alias})
			joinConds = append(joinConds, &Cmp{Op: "eq", L: &Col{Name: ke.outerCol}, R: &Col{Name: alias}})
		}
		inner = &Project{Input: inner, Exprs: proj}
	}
	r.conds = append(r.conds, fmt.Sprintf(
		"EXISTS 去相关：%d 个外层键等值条件，改写为半连接；外层每行至多输出一次，保留外层重复度，不引入内层乘法重复",
		len(keys)))
	return &SemiJoin{Left: outerPlan, Right: inner, Pred: JoinAnd(joinConds)}, nil
}

// project 把 SELECT 列表中的标量 COUNT 子查询逐个提升为
// 左连接 + GROUP BY 预聚合，并用 COALESCE(cnt, 0) 替换原子查询表达式。
func (r *rewriter) project(input Node, exprs []NamedExpr) (Node, error) {
	cur := input
	out := make([]NamedExpr, len(exprs))
	copy(out, exprs)
	for {
		var found *CountSub
		for _, ne := range out {
			found = findCountSub(ne.Expr)
			if found != nil {
				break
			}
		}
		if found == nil {
			break
		}
		sub, err := r.node(found.Sub)
		if err != nil {
			return nil, err
		}
		joined, cntCol, err := r.countToLeftJoin(cur, found, sub)
		if err != nil {
			return nil, err
		}
		cur = joined
		repl := &Coalesce{Args: []Expr{&Col{Name: cntCol}, &Lit{V: int64(0)}}}
		for i, ne := range out {
			out[i] = NamedExpr{Expr: replaceCountSub(ne.Expr, found, repl), As: ne.As}
		}
	}
	return &Project{Input: cur, Exprs: out}, nil
}

func (r *rewriter) countToLeftJoin(outerPlan Node, cs *CountSub, sub Node) (Node, string, error) {
	inner, keys, residual, err := decorrelate(sub)
	if err != nil {
		return nil, "", err
	}
	if len(residual) > 0 {
		inner = &Filter{Input: inner, Pred: JoinAnd(residual)}
	}
	cntCol := r.name("cnt")
	aggCol := cs.Col
	if aggCol == "" {
		aggCol = "*"
	}
	var innerKeys []string
	for _, ke := range keys {
		innerKeys = append(innerKeys, ke.innerCol)
	}
	agg := &GroupBy{Input: inner, Keys: innerKeys, Aggs: []AggSpec{{Func: "count", Col: aggCol, As: cntCol}}}
	var right Node = agg
	var joinConds []Expr
	if len(keys) > 0 {
		var proj []NamedExpr
		for i, ke := range keys {
			alias := r.name("ck")
			proj = append(proj, NamedExpr{Expr: &Col{Name: innerKeys[i]}, As: alias})
			joinConds = append(joinConds, &Cmp{Op: "eq", L: &Col{Name: ke.outerCol}, R: &Col{Name: alias}})
		}
		proj = append(proj, NamedExpr{Expr: &Col{Name: cntCol}, As: cntCol})
		right = &Project{Input: agg, Exprs: proj}
	}
	r.conds = append(r.conds, fmt.Sprintf(
		"标量 COUNT 去相关：%d 个外层键等值条件，改写为左连接 + GROUP BY 预聚合；未匹配外层行由 COALESCE(%s,0) 得到 0，左连接保留外层行多重性",
		len(keys), cntCol))
	return &Join{Kind: LeftJoin, Left: outerPlan, Right: right, Pred: JoinAnd(joinConds)}, cntCol, nil
}

func findCountSub(e Expr) *CountSub {
	switch e := e.(type) {
	case *CountSub:
		return e
	case *And:
		return findInList(e.Args)
	case *Or:
		return findInList(e.Args)
	case *Not:
		return findCountSub(e.E)
	case *Cmp:
		if c := findCountSub(e.L); c != nil {
			return c
		}
		return findCountSub(e.R)
	case *IsNull:
		return findCountSub(e.E)
	case *Coalesce:
		return findInList(e.Args)
	case *Exists:
		return nil
	}
	return nil
}

func findInList(es []Expr) *CountSub {
	for _, e := range es {
		if c := findCountSub(e); c != nil {
			return c
		}
	}
	return nil
}

func replaceCountSub(e, target Expr, repl Expr) Expr {
	if e == target {
		return repl
	}
	switch e := e.(type) {
	case *And:
		return &And{Args: replaceInList(e.Args, target, repl)}
	case *Or:
		return &Or{Args: replaceInList(e.Args, target, repl)}
	case *Not:
		return &Not{E: replaceCountSub(e.E, target, repl)}
	case *Cmp:
		return &Cmp{Op: e.Op, L: replaceCountSub(e.L, target, repl), R: replaceCountSub(e.R, target, repl)}
	case *IsNull:
		return &IsNull{E: replaceCountSub(e.E, target, repl)}
	case *Coalesce:
		return &Coalesce{Args: replaceInList(e.Args, target, repl)}
	}
	return e
}

func replaceInList(es []Expr, target, repl Expr) []Expr {
	out := make([]Expr, len(es))
	for i, e := range es {
		out[i] = replaceCountSub(e, target, repl)
	}
	return out
}

func containsSubquery(e Expr) bool {
	switch e := e.(type) {
	case *Exists, *CountSub:
		return true
	case *And:
		return containsInList(e.Args)
	case *Or:
		return containsInList(e.Args)
	case *Not:
		return containsSubquery(e.E)
	case *Cmp:
		return containsSubquery(e.L) || containsSubquery(e.R)
	case *IsNull:
		return containsSubquery(e.E)
	case *Coalesce:
		return containsInList(e.Args)
	}
	return false
}

func containsInList(es []Expr) bool {
	for _, e := range es {
		if containsSubquery(e) {
			return true
		}
	}
	return false
}
