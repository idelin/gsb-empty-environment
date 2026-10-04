package main

import (
	"strings"
	"testing"
)

func testFuncTable() FuncTable {
	return FuncTable{
		"f": {Reads: []string{"x"}, Writes: []string{"x"}},
		"g": {Throws: true},
		"h": {Reads: []string{"x"}},
		"q": {Writes: []string{"y"}},
	}
}

func hasCond(viols []Violation, cond string) bool {
	for _, v := range viols {
		if v.Cond == cond {
			return true
		}
	}
	return false
}

// checkAllow 断言检查器放行，且解释器对照确认行为一致。
func checkAllow(t *testing.T, prog []Stmt, e Expr) {
	t.Helper()
	ft := testFuncTable()
	allow, viols, ref, err := CheckExtract(prog, ft, e)
	if err != nil {
		t.Fatalf("CheckExtract 出错: %v", err)
	}
	if !allow {
		t.Fatalf("期望允许，实际拒绝: %v", viols)
	}
	eq, why := equivalent(prog, ref, ft, e)
	if !eq {
		t.Fatalf("检查器放行但解释器发现差异: %s", why)
	}
}

// checkReject 断言检查器以给定条件拒绝，依赖路径包含 want 子串，
// 且解释器对照确认两程序行为确有差异。
func checkReject(t *testing.T, prog []Stmt, e Expr, cond string, wantPath string) {
	t.Helper()
	ft := testFuncTable()
	allow, viols, ref, err := CheckExtract(prog, ft, e)
	if err != nil {
		t.Fatalf("CheckExtract 出错: %v", err)
	}
	if allow {
		t.Fatalf("期望以 %s 拒绝，实际允许", cond)
	}
	if !hasCond(viols, cond) {
		t.Fatalf("期望条件 %s，实际: %v", cond, viols)
	}
	found := false
	for _, v := range viols {
		if v.Cond == cond && strings.Contains(v.Path, wantPath) {
			found = true
		}
	}
	if !found {
		t.Fatalf("依赖路径未包含 %q: %v", wantPath, viols)
	}
	eq, why := equivalent(prog, ref, ft, e)
	if eq {
		t.Fatalf("检查器拒绝但解释器认为行为一致（过度保守）")
	} else {
		t.Logf("行为差异佐证: %s", why)
	}
}

func TestAllowIfElse(t *testing.T) {
	h := Call{Fn: "h"}
	prog := []Stmt{
		If{Cond: Var{"x"},
			Then: []Stmt{Assign{"y", h}},
			Else: []Stmt{Assign{"y", h}}},
	}
	checkAllow(t, prog, h)
}

func TestAllowShortCircuitComplement(t *testing.T) {
	h := Call{Fn: "h"}
	prog := []Stmt{
		Assign{"y", And{Var{"x"}, h}},
		Assign{"y", Or{Var{"x"}, h}},
	}
	checkAllow(t, prog, h)
}

func TestRejectShortCircuitCount(t *testing.T) {
	h := Call{Fn: "h"}
	prog := []Stmt{
		ExprStmt{And{Var{"x"}, h}},
		ExprStmt{And{Var{"x"}, h}},
	}
	checkReject(t, prog, h, "求值次数", "S0")
}

func TestRejectWriteBetween(t *testing.T) {
	h := Call{Fn: "h"}
	prog := []Stmt{
		If{Cond: Var{"x"},
			Then: []Stmt{Assign{"y", h}},
			Else: []Stmt{Assign{"x", Num{9}}, Assign{"y", h}}},
	}
	checkReject(t, prog, h, "读取版本", "write(x)@S0.else[0]")
}

func TestRejectSequentialTwice(t *testing.T) {
	h := Call{Fn: "h"}
	prog := []Stmt{
		Assign{"y", h},
		Assign{"y", h},
	}
	checkReject(t, prog, h, "求值次数", "S0")
}

func TestAllowThrowingExclusive(t *testing.T) {
	g := Call{Fn: "g"}
	prog := []Stmt{
		If{Cond: Var{"x"},
			Then: []Stmt{ExprStmt{g}},
			Else: []Stmt{ExprStmt{g}}},
	}
	checkAllow(t, prog, g)
}

func TestRejectThrowSkipsWrite(t *testing.T) {
	g := Call{Fn: "g"}
	prog := []Stmt{
		ExprStmt{Add{Call{Fn: "q"}, g}},
		ExprStmt{g},
	}
	checkReject(t, prog, g, "首错", "write(y)@S0")
}

func TestRejectAntiDependency(t *testing.T) {
	q := Call{Fn: "q"}
	prog := []Stmt{
		If{Cond: Var{"x"},
			Then: []Stmt{ExprStmt{q}},
			Else: []Stmt{Assign{"x", Var{"y"}}, ExprStmt{q}}},
	}
	checkReject(t, prog, q, "读取版本", "read(y)@S0.else[0]")
}

func TestRejectEffectfulCall(t *testing.T) {
	f := Call{Fn: "f"} // 读写 x
	prog := []Stmt{
		ExprStmt{f},
		ExprStmt{f},
	}
	checkReject(t, prog, f, "求值次数", "S0")
}

// TestEnumerate 枚举小程序：检查器判定与解释器对照交叉验证。
// 不变式：检查器放行 => 两程序在所有初始环境下行为一致（可靠性）。
func TestEnumerate(t *testing.T) {
	ft := testFuncTable()
	h := Call{Fn: "h"}
	targets := []Expr{
		h,                // 纯读 x
		Add{Var{"x"}, h}, // 纯读 x（复合）
		Call{Fn: "f"},    // 读写 x
		Call{Fn: "g"},    // 抛错
		And{Var{"y"}, h}, // 内部短路
		Call{Fn: "q"},    // 只写 y
	}
	templates := []func(Expr) Stmt{
		func(e Expr) Stmt { return ExprStmt{e} },
		func(e Expr) Stmt { return Assign{"y", e} },
		func(e Expr) Stmt { return If{Cond: Var{"x"}, Then: []Stmt{ExprStmt{e}}} },
		func(e Expr) Stmt { return If{Cond: Var{"x"}, Else: []Stmt{Assign{"y", e}}} },
		func(e Expr) Stmt { return ExprStmt{And{Var{"x"}, e}} },
		func(e Expr) Stmt { return ExprStmt{Or{Var{"x"}, e}} },
		func(e Expr) Stmt {
			return If{Cond: Var{"y"}, Then: []Stmt{Assign{"x", e}}, Else: []Stmt{ExprStmt{Num{0}}}}
		},
		func(e Expr) Stmt {
			return If{Cond: Var{"y"}, Then: []Stmt{ExprStmt{Num{0}}}, Else: []Stmt{Assign{"y", e}}}
		},
		func(e Expr) Stmt { return ExprStmt{Add{Call{Fn: "q"}, e}} },
		func(e Expr) Stmt { return Assign{"y", Add{e, Var{"x"}}} },
	}
	fillers := []Stmt{
		Assign{"x", Num{1}},
		ExprStmt{Call{Fn: "f"}},
		ExprStmt{Call{Fn: "q"}},
		Assign{"y", Var{"x"}},
		ExprStmt{Call{Fn: "g"}},
		If{Cond: Var{"y"}, Then: []Stmt{Assign{"x", Num{0}}}},
	}
	pres := [][]Stmt{nil, {fillers[0]}, {fillers[1]}, {fillers[4]}}
	mids := [][]Stmt{
		nil,
		{fillers[0]}, {fillers[1]}, {fillers[2]}, {fillers[3]}, {fillers[4]}, {fillers[5]},
		{fillers[1], fillers[2]}, {fillers[2], fillers[1]},
	}

	total, skipped := 0, 0
	allows, trueRejects, conservative := 0, 0, 0
	conds := map[string]int{}
	for _, target := range targets {
		for _, t1 := range templates {
			for _, t2 := range templates {
				for _, pre := range pres {
					for _, mid := range mids {
						prog := append(append(append(
							append([]Stmt{}, pre...), t1(target)),
							mid...), t2(target))
						if countOccurProg(prog, target) != 2 {
							skipped++
							continue
						}
						total++
						allow, viols, ref, err := CheckExtract(prog, ft, target)
						if err != nil {
							t.Fatalf("CheckExtract 出错: %v", err)
						}
						eq, why := equivalent(prog, ref, ft, target)
						if allow {
							allows++
							if !eq {
								t.Errorf("可靠性违反：放行 %s，但 %s",
									ProgString(prog), why)
							}
						} else {
							for _, v := range viols {
								conds[v.Cond]++
							}
							if eq {
								conservative++
							} else {
								trueRejects++
							}
						}
					}
				}
			}
		}
	}
	t.Logf("枚举 %d 个程序（跳过 %d 个出现次数不为 2 的）", total, skipped)
	t.Logf("放行 %d（全部经解释器确认一致）；拒绝 %d：真拒绝 %d，保守拒绝 %d",
		allows, trueRejects+conservative, trueRejects, conservative)
	t.Logf("拒绝条件分布: %v", conds)
	if allows == 0 {
		t.Errorf("没有放行任何程序，枚举覆盖不足")
	}
	if trueRejects == 0 {
		t.Errorf("没有真拒绝，枚举覆盖不足")
	}
	for _, c := range []string{"求值次数", "首错", "读取版本"} {
		if conds[c] == 0 {
			t.Errorf("枚举中未出现条件 %s 的拒绝", c)
		}
	}
}
