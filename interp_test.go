package ra

import "testing"

func i(v int64) any { return v }

func eq(l, r Expr) *Cmp { return &Cmp{Op: "eq", L: l, R: r} }
func gt(l, r Expr) *Cmp { return &Cmp{Op: "gt", L: l, R: r} }

func run(t *testing.T, n Node) []Row {
	t.Helper()
	rows, err := n.Exec(nil)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	return rows
}

// WHERE 只保留 TRUE：NULL 谓词（Unknown）的行被丢弃。
func TestFilterThreeValued(t *testing.T) {
	s := &Scan{Name: "R", Rows: []Row{{"k": i(1)}, {"k": nil}, {"k": i(2)}}}
	f := &Filter{Input: s, Pred: eq(&Col{Name: "k"}, &Lit{V: i(1)})}
	got := run(t, f)
	if len(got) != 1 || got[0]["k"] != i(1) {
		t.Fatalf("期望只保留 k=1 一行, 得到 %v", got)
	}
}

// 三值逻辑真值表。
func TestTrilean(t *testing.T) {
	if And3(True, Unknown) != Unknown || And3(False, Unknown) != False {
		t.Fatal("AND 三值逻辑错误")
	}
	if Or3(False, Unknown) != Unknown || Or3(True, Unknown) != True {
		t.Fatal("OR 三值逻辑错误")
	}
	if Not3(Unknown) != Unknown {
		t.Fatal("NOT 三值逻辑错误")
	}
	if Eq3(nil, nil) != Unknown || Eq3(nil, i(1)) != Unknown {
		t.Fatal("NULL 等值应为 Unknown")
	}
}

// COUNT(*) 计行，COUNT(column) 不计 NULL。
func TestCountStarVsColumn(t *testing.T) {
	inner := &Scan{Name: "S", Rows: []Row{{"w": i(5)}, {"w": nil}, {"w": i(7)}}}
	star := &CountSub{Col: "*", Sub: inner}
	colC := &CountSub{Col: "w", Sub: inner}
	p := &Project{Input: &Scan{Name: "R", Rows: []Row{{"dummy": i(1)}}}, Exprs: []NamedExpr{
		{Expr: star, As: "cs"},
		{Expr: colC, As: "cc"},
	}}
	got := run(t, p)
	if got[0]["cs"] != int64(3) || got[0]["cc"] != int64(2) {
		t.Fatalf("COUNT(*) 应为 3, COUNT(w) 应为 2, 得到 %v", got[0])
	}
}

// 左连接未匹配时右列补 NULL；内连接丢行。
func TestLeftJoinUnmatched(t *testing.T) {
	l := &Scan{Name: "R", Rows: []Row{{"k": i(1)}, {"k": i(2)}}, Cols: []string{"k"}}
	r := &Scan{Name: "S", Rows: []Row{{"k": i(1), "w": i(5)}}, Cols: []string{"k", "w"}}
	pred := eq(&Col{Name: "k"}, &Col{Name: "r.k"})
	inner := run(t, &Join{Kind: InnerJoin, Left: l, Right: r, Pred: pred})
	if len(inner) != 1 {
		t.Fatalf("内连接应只有 1 行, 得到 %v", inner)
	}
	left := run(t, &Join{Kind: LeftJoin, Left: l, Right: r, Pred: pred})
	if len(left) != 2 {
		t.Fatalf("左连接应有 2 行, 得到 %v", left)
	}
	if left[1]["k"] != i(2) || left[1]["w"] != nil {
		t.Fatalf("未匹配行右列应为 NULL, 得到 %v", left[1])
	}
}

// NULL 与 NULL 等值不成立：连接不上，相关计数为 0。
func TestNullKeysDoNotMatch(t *testing.T) {
	l := &Scan{Name: "R", Rows: []Row{{"k": nil}}, Cols: []string{"k"}}
	r := &Scan{Name: "S", Rows: []Row{{"k": nil, "w": i(5)}}, Cols: []string{"k", "w"}}
	pred := eq(&Col{Name: "k"}, &Col{Name: "r.k"})
	inner := run(t, &Join{Kind: InnerJoin, Left: l, Right: r, Pred: pred})
	if len(inner) != 0 {
		t.Fatalf("NULL=NULL 不应连接成功, 得到 %v", inner)
	}
	left := run(t, &Join{Kind: LeftJoin, Left: l, Right: r, Pred: pred})
	if len(left) != 1 || left[0]["w"] != nil {
		t.Fatalf("左连接应保留 NULL 键行且右侧为 NULL, 得到 %v", left)
	}
}

// 连接过滤条件放在 ON 与放在 WHERE 结果不同（左连接情形下）。
func TestOnVsWhere(t *testing.T) {
	mkLeft := func() Node {
		l := &Scan{Name: "R", Rows: []Row{{"k": i(1)}, {"k": i(2)}}, Cols: []string{"k"}}
		r := &Scan{Name: "S", Rows: []Row{{"k": i(1), "w": i(5)}, {"k": i(1), "w": i(-5)}}, Cols: []string{"k", "w"}}
		return &Join{Kind: LeftJoin, Left: l, Right: r, Pred: eq(&Col{Name: "k"}, &Col{Name: "r.k"})}
	}
	// 条件在 ON：k=2 的左行保留（右侧为 NULL）。
	onPred := &And{Args: []Expr{eq(&Col{Name: "k"}, &Col{Name: "r.k"}), gt(&Col{Name: "w"}, &Lit{V: i(0)})}}
	l := &Scan{Name: "R", Rows: []Row{{"k": i(1)}, {"k": i(2)}}, Cols: []string{"k"}}
	r := &Scan{Name: "S", Rows: []Row{{"k": i(1), "w": i(5)}, {"k": i(1), "w": i(-5)}}, Cols: []string{"k", "w"}}
	onJoin := run(t, &Join{Kind: LeftJoin, Left: l, Right: r, Pred: onPred})
	if len(onJoin) != 2 {
		t.Fatalf("条件在 ON 时应保留 k=2 行, 得到 %v", onJoin)
	}
	// 条件在 WHERE：k=2 的行因 w IS NULL 被过滤。
	where := run(t, &Filter{Input: mkLeft(), Pred: gt(&Col{Name: "w"}, &Lit{V: i(0)})})
	if len(where) != 1 || where[0]["w"] != i(5) {
		t.Fatalf("条件在 WHERE 时应只剩 (1,5), 得到 %v", where)
	}
	if MultisetEqual(onJoin, where) {
		t.Fatal("ON 与 WHERE 的结果不应相同")
	}
}

// bag 语义：重复行被保留，连接产生乘法重复。
func TestBagSemantics(t *testing.T) {
	l := &Scan{Name: "R", Rows: []Row{{"k": i(1)}, {"k": i(1)}}, Cols: []string{"k"}}
	r := &Scan{Name: "S", Rows: []Row{{"k": i(1)}, {"k": i(1)}, {"k": i(1)}}, Cols: []string{"k"}}
	got := run(t, &Join{Kind: InnerJoin, Left: l, Right: r, Pred: eq(&Col{Name: "k"}, &Col{Name: "r.k"})})
	if len(got) != 6 {
		t.Fatalf("2x3 应产生 6 行, 得到 %d", len(got))
	}
	f := run(t, &Filter{Input: l, Pred: &Lit{V: true}})
	if len(f) != 2 {
		t.Fatalf("过滤必须保留重复行, 得到 %d", len(f))
	}
}
