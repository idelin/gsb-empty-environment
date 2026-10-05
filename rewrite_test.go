package ra

import (
	"strings"
	"testing"
)

// multisets 枚举 universe 中行的所有大小不超过 maxSize 的多重集。
func multisets(universe []Row, maxSize int) [][]Row {
	var out [][]Row
	var rec func(start int, cur []Row)
	rec = func(start int, cur []Row) {
		out = append(out, append([]Row{}, cur...))
		if len(cur) == maxSize {
			return
		}
		for i := start; i < len(universe); i++ {
			rec(i, append(cur, universe[i]))
		}
	}
	rec(0, nil)
	return out
}

var (
	keyDomain = []any{nil, int64(1), int64(2)}
	rUniverse []Row // (k, v): k 为键, v 为载荷
	sUniverse []Row // (k, w): k 为键, w 可 NULL, 供剩余谓词与 COUNT(w) 使用
	tUniverse []Row // (w): 第二事实表, 与 S 按 w 连接
)

func init() {
	for _, k := range keyDomain {
		for _, v := range []any{nil, int64(10)} {
			rUniverse = append(rUniverse, Row{"k": k, "v": v})
		}
		for _, w := range []any{nil, int64(5), int64(15)} {
			sUniverse = append(sUniverse, Row{"k": k, "w": w})
		}
	}
	for _, w := range []any{nil, int64(5), int64(15)} {
		tUniverse = append(tUniverse, Row{"w": w})
	}
}

func scanOf(name string, rows []Row, cols ...string) *Scan {
	return &Scan{Name: name, Rows: rows, Cols: cols}
}

// corrEq 是相关等值谓词 outer.k = k。
func corrEq() Expr { return eq(&Outer{Name: "k"}, &Col{Name: "k"}) }

// residualW 是不引用外层的剩余谓词 w > 10。
func residualW() Expr { return gt(&Col{Name: "w"}, &Lit{V: int64(10)}) }

func andOf(es ...Expr) Expr {
	var flat []Expr
	for _, e := range es {
		if e != nil {
			flat = append(flat, e)
		}
	}
	return JoinAnd(flat)
}

// 查询变体：EXISTS（含/不含剩余谓词）。
func existsPlan(rRows, sRows []Row, withResidual bool) Node {
	var residual Expr
	if withResidual {
		residual = residualW()
	}
	sub := &Filter{Input: scanOf("S", sRows, "k", "w"), Pred: andOf(corrEq(), residual)}
	return &Filter{Input: scanOf("R", rRows, "k", "v"), Pred: &Exists{Sub: sub}}
}

// 查询变体：标量 COUNT(*) / COUNT(w)（含/不含剩余谓词）。
func countPlan(rRows, sRows []Row, countCol string, withResidual bool) Node {
	var residual Expr
	if withResidual {
		residual = residualW()
	}
	sub := &Filter{Input: scanOf("S", sRows, "k", "w"), Pred: andOf(corrEq(), residual)}
	return &Project{Input: scanOf("R", rRows, "k", "v"), Exprs: []NamedExpr{
		{Expr: &Col{Name: "k"}, As: "k"},
		{Expr: &Col{Name: "v"}, As: "v"},
		{Expr: &CountSub{Col: countCol, Sub: sub}, As: "c"},
	}}
}

// 查询变体：两事实表 S⋈T 上的标量 COUNT(*)，连接使匹配行乘法重复。
func twoFactCountPlan(rRows, sRows, tRows []Row) Node {
	join := &Join{Kind: InnerJoin,
		Left:  scanOf("S", sRows, "k", "w"),
		Right: scanOf("T", tRows, "w"),
		Pred:  eq(&Col{Name: "w"}, &Col{Name: "r.w"})}
	sub := &Filter{Input: join, Pred: corrEq()}
	return &Project{Input: scanOf("R", rRows, "k", "v"), Exprs: []NamedExpr{
		{Expr: &Col{Name: "k"}, As: "k"},
		{Expr: &CountSub{Col: "*", Sub: sub}, As: "c"},
	}}
}

// 查询变体：标量 COUNT 嵌套在更大表达式中（c > 0）。
func nestedCountPlan(rRows, sRows []Row) Node {
	sub := &Filter{Input: scanOf("S", sRows, "k", "w"), Pred: corrEq()}
	return &Project{Input: scanOf("R", rRows, "k", "v"), Exprs: []NamedExpr{
		{Expr: &Col{Name: "k"}, As: "k"},
		{Expr: gt(&CountSub{Col: "*", Sub: sub}, &Lit{V: int64(0)}), As: "has"},
	}}
}

func checkRewrite(t *testing.T, label string, orig Node) {
	t.Helper()
	rw, conds, err := Rewrite(orig)
	if err != nil {
		t.Fatalf("%s: 改写被意外拒绝: %v (条件: %v)", label, err, conds)
	}
	origRows, err := orig.Exec(nil)
	if err != nil {
		t.Fatalf("%s: 原计划执行失败: %v", label, err)
	}
	rwRows, err := rw.Exec(nil)
	if err != nil {
		t.Fatalf("%s: 改写计划执行失败: %v", label, err)
	}
	if !MultisetEqual(origRows, rwRows) {
		t.Fatalf("%s: 结果多重集不一致\n原:   %v\n改写: %v", label, origRows, rwRows)
	}
}

// 核心性质测试：在含 NULL 的有限小域上枚举两表全部小多重集组合，
// 覆盖空表、重复键、NULL 键，逐组合按行多重集比较原计划与改写计划。
func TestRewriteEquivalenceExhaustive(t *testing.T) {
	rSets := multisets(rUniverse, 2)
	sSets := multisets(sUniverse, 2)
	t.Logf("枚举组合: |R|=%d 种, |S|=%d 种", len(rSets), len(sSets))
	variants := []struct {
		name string
		mk   func(r, s []Row) Node
	}{
		{"exists", func(r, s []Row) Node { return existsPlan(r, s, false) }},
		{"exists_residual", func(r, s []Row) Node { return existsPlan(r, s, true) }},
		{"count_star", func(r, s []Row) Node { return countPlan(r, s, "*", false) }},
		{"count_star_residual", func(r, s []Row) Node { return countPlan(r, s, "*", true) }},
		{"count_col", func(r, s []Row) Node { return countPlan(r, s, "w", false) }},
		{"count_col_residual", func(r, s []Row) Node { return countPlan(r, s, "w", true) }},
		{"nested_count", func(r, s []Row) Node { return nestedCountPlan(r, s) }},
	}
	for _, v := range variants {
		for _, rs := range rSets {
			for _, ss := range sSets {
				checkRewrite(t, v.name, v.mk(rs, ss))
			}
		}
	}
}

// 两事实表：S⋈T 的乘法重复必须被原样保留在 COUNT 中。
func TestRewriteEquivalenceTwoFact(t *testing.T) {
	rSets := multisets(rUniverse, 1)
	sSets := multisets(sUniverse, 2)
	tSets := multisets(tUniverse, 2)
	for _, rs := range rSets {
		for _, ss := range sSets {
			for _, ts := range tSets {
				checkRewrite(t, "two_fact", twoFactCountPlan(rs, ss, ts))
			}
		}
	}
}

// 明确的重点用例：空表、重复键、NULL 键、无匹配时 COUNT=0。
func TestRewriteKeyCases(t *testing.T) {
	empty := []Row{}
	dupR := []Row{{"k": int64(1), "v": int64(10)}, {"k": int64(1), "v": int64(10)}}
	nullR := []Row{{"k": nil, "v": int64(10)}}
	dupS := []Row{{"k": int64(1), "w": int64(5)}, {"k": int64(1), "w": nil}}
	nullS := []Row{{"k": nil, "w": int64(5)}}

	cases := []struct {
		name string
		plan Node
	}{
		{"空外表", countPlan(empty, dupS, "*", false)},
		{"空内表", countPlan(dupR, empty, "*", false)},
		{"两表皆空", countPlan(empty, empty, "*", false)},
		{"重复键计数", countPlan(dupR, dupS, "*", false)},
		{"重复键EXISTS", existsPlan(dupR, dupS, false)},
		{"NULL键计数为0", countPlan(nullR, nullS, "*", false)},
		{"NULL键EXISTS为假", existsPlan(nullR, nullS, false)},
		{"COUNT列不计NULL", countPlan(dupR, dupS, "w", false)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkRewrite(t, c.name, c.plan) })
	}

	// 无匹配时 COUNT=0 且外层行不丢失。
	rw, _, err := Rewrite(countPlan(dupR, empty, "*", false))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := rw.Exec(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["c"] != int64(0) || rows[1]["c"] != int64(0) {
		t.Fatalf("空内表时每个外层重复行都应得到 COUNT=0, 得到 %v", rows)
	}
}

// 未支持的相关表达式必须明确拒绝改写。
func TestRewriteRejects(t *testing.T) {
	r := scanOf("R", []Row{{"k": int64(1), "v": int64(10)}}, "k", "v")
	s := func(pred Expr) Node { return &Filter{Input: scanOf("S", sUniverse[:1], "k", "w"), Pred: pred} }

	cases := []struct {
		name string
		plan Node
		want string
	}{
		{"相关非等值", &Filter{Input: r, Pred: &Exists{Sub: s(gt(&Col{Name: "w"}, &Outer{Name: "v"}))}}, "拒绝"},
		{"相关等值但非裸列", &Filter{Input: r, Pred: &Exists{Sub: s(eq(&Outer{Name: "k"}, &Coalesce{Args: []Expr{&Col{Name: "k"}, &Lit{V: int64(0)}}}))}}, "拒绝"},
		{"NOT EXISTS", &Filter{Input: r, Pred: &Not{E: &Exists{Sub: s(corrEq())}}}, "NOT EXISTS"},
		{"过滤中嵌套标量子查询", &Filter{Input: r, Pred: gt(&CountSub{Col: "*", Sub: s(corrEq())}, &Lit{V: int64(0)})}, "拒绝"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, conds, err := Rewrite(c.plan)
			if err == nil {
				t.Fatalf("应拒绝改写, 条件: %v", conds)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("拒绝信息应包含 %q, 实际: %v", c.want, err)
			}
		})
	}
}

// 改写后的 EXISTS 计划必须是半连接形态（不引入内层乘法重复）。
func TestExistsRewritesToSemiJoin(t *testing.T) {
	rw, conds, err := Rewrite(existsPlan(
		[]Row{{"k": int64(1), "v": int64(10)}},
		[]Row{{"k": int64(1), "w": int64(5)}}, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rw.(*SemiJoin); !ok {
		t.Fatalf("EXISTS 应改写为半连接, 得到 %T (条件: %v)", rw, conds)
	}
	if len(conds) == 0 {
		t.Fatal("应输出适用条件")
	}
}
