package main

import (
	"strings"
	"testing"
)

func v(x string) Expr          { return &Var{x} }
func c(n int) Expr             { return &Const{n} }
func add(l, r Expr) Expr       { return &Add{l, r} }
func and(l, r Expr) Expr       { return &And{l, r} }
func call(f string) Expr       { return &Call{f, nil} }
func pr(e Expr) Stmt           { return &Print{e} }
func as(x string, e Expr) Stmt { return &Assign{x, e} }

type dirCase struct {
	name      string
	prog      []Stmt
	ft        FuncTable
	e         Expr
	o1, o2    []int
	wantKind  string // "" 表示期望接受
	wantEqual bool   // 重构前后轨迹是否一致
}

func directedCases() []dirCase {
	return []dirCase{
		{
			name: "接受：干净重复",
			prog: []Stmt{as("x", c(3)), pr(add(v("x"), v("x"))), pr(add(v("x"), v("x")))},
			ft:   FuncTable{},
			e:    add(v("x"), v("x")), o1: []int{1, 0}, o2: []int{2, 0},
			wantKind: "", wantEqual: true,
		},
		{
			name: "拒绝：两次求值之间变量被写",
			prog: []Stmt{pr(add(v("x"), c(1))), as("x", c(1)), pr(add(v("x"), c(1)))},
			ft:   FuncTable{},
			e:    add(v("x"), c(1)), o1: []int{0, 0}, o2: []int{2, 0},
			wantKind: "读取版本改变", wantEqual: false,
		},
		{
			name: "拒绝：两次求值之间调用写",
			prog: []Stmt{pr(v("x")), pr(call("f")), pr(v("x"))},
			ft:   FuncTable{"f": FuncSig{Writes: []string{"x"}}},
			e:    v("x"), o1: []int{0, 0}, o2: []int{2, 0},
			wantKind: "读取版本改变", wantEqual: false,
		},
		{
			name: "拒绝：if 分支绕过绑定语句",
			prog: []Stmt{
				&If{v("x"), []Stmt{pr(add(v("y"), c(1)))}, nil},
				pr(add(v("y"), c(1))),
			},
			ft: FuncTable{},
			e:  add(v("y"), c(1)), o1: []int{0, 1, 0, 0}, o2: []int{1, 0},
			wantKind: "支配性失败", wantEqual: false,
		},
		{
			name: "拒绝：表达式含写副作用调用",
			prog: []Stmt{pr(call("f")), pr(call("f"))},
			ft:   FuncTable{"f": FuncSig{Writes: []string{"x"}}},
			e:    call("f"), o1: []int{0, 0}, o2: []int{1, 0},
			wantKind: "求值次数改变", wantEqual: false,
		},
		{
			name: "接受：表达式含纯调用",
			prog: []Stmt{as("y", c(5)), pr(add(call("g"), v("x"))), pr(add(call("g"), v("x")))},
			ft:   FuncTable{"g": FuncSig{Reads: []string{"y"}}},
			e:    add(call("g"), v("x")), o1: []int{1, 0}, o2: []int{2, 0},
			wantKind: "", wantEqual: true,
		},
		{
			name: "接受：表达式抛错但 O1 是语句首个求值位置",
			prog: []Stmt{pr(call("f")), pr(c(1))},
			ft:   FuncTable{"f": FuncSig{Throws: true}},
			e:    call("f"), o1: []int{0, 0}, o2: []int{1, 0},
			wantKind: "", wantEqual: true,
		},
		{
			name: "拒绝：表达式抛错且语句内有先于它的求值",
			prog: []Stmt{as("y", add(call("g"), call("f"))), pr(call("f"))},
			ft: FuncTable{
				"f": FuncSig{Throws: true},
				"g": FuncSig{Writes: []string{"z"}},
			},
			e: call("f"), o1: []int{0, 0, 1}, o2: []int{1, 0},
			wantKind: "首错改变", wantEqual: false,
		},
		{
			name: "接受：两次求值之间抛错（首错保持）",
			prog: []Stmt{pr(v("x")), pr(call("f")), pr(v("x"))},
			ft:   FuncTable{"f": FuncSig{Throws: true}},
			e:    v("x"), o1: []int{0, 0}, o2: []int{2, 0},
			wantKind: "", wantEqual: true,
		},
		{
			name: "拒绝：提升点与 O1 之间被写（同语句内）",
			prog: []Stmt{as("y", add(call("g"), v("x"))), pr(v("x"))},
			ft:   FuncTable{"g": FuncSig{Writes: []string{"x"}}},
			e:    v("x"), o1: []int{0, 0, 1}, o2: []int{1, 0},
			wantKind: "读取版本改变", wantEqual: false,
		},
		{
			name: "接受：短路分支（&& 右侧，语句必然执行）",
			prog: []Stmt{as("x", c(7)), pr(and(c(0), v("x"))), pr(v("x"))},
			ft:   FuncTable{},
			e:    v("x"), o1: []int{1, 0, 1}, o2: []int{2, 0},
			wantKind: "", wantEqual: true,
		},
		{
			name: "拒绝：短路右侧的抛错表达式",
			prog: []Stmt{pr(and(c(0), call("f"))), pr(call("f"))},
			ft:   FuncTable{"f": FuncSig{Throws: true}},
			e:    call("f"), o1: []int{0, 0, 1}, o2: []int{1, 0},
			wantKind: "首错改变", wantEqual: false,
		},
		{
			name: "接受：同一 && 内的两次出现",
			prog: []Stmt{pr(and(v("x"), v("x")))},
			ft:   FuncTable{},
			e:    v("x"), o1: []int{0, 0, 0}, o2: []int{0, 0, 1},
			wantKind: "", wantEqual: true,
		},
		{
			name: "拒绝：if 条件在两次求值之间写",
			prog: []Stmt{
				pr(v("x")),
				&If{call("f"), []Stmt{pr(v("x"))}, nil},
			},
			ft: FuncTable{"f": FuncSig{Writes: []string{"x"}}},
			e:  v("x"), o1: []int{0, 0}, o2: []int{1, 1, 0, 0},
			wantKind: "读取版本改变", wantEqual: false,
		},
	}
}

func TestDirected(t *testing.T) {
	for _, tc := range directedCases() {
		rej := CheckExtract(tc.prog, tc.ft, tc.e, tc.o1, tc.o2)
		if tc.wantKind == "" {
			if rej != nil {
				t.Errorf("%s: 期望接受，实际拒绝：%v", tc.name, rej)
				continue
			}
		} else {
			if rej == nil {
				t.Errorf("%s: 期望拒绝[%s]，实际接受", tc.name, tc.wantKind)
				continue
			}
			if !strings.Contains(rej.Kind, tc.wantKind) {
				t.Errorf("%s: 期望拒绝类型含 %q，实际 %q", tc.name, tc.wantKind, rej.Kind)
			}
			if len(rej.Path) < 2 {
				t.Errorf("%s: 拒绝缺少依赖路径", tc.name)
			}
		}
		rp := Refactor(tc.prog, tc.e, tc.o1, tc.o2)
		eq := EqualResults(Run(tc.prog, tc.ft), Run(rp, tc.ft))
		if eq != tc.wantEqual {
			t.Errorf("%s: 轨迹一致=%v，期望 %v", tc.name, eq, tc.wantEqual)
		}
		if rej == nil && !eq {
			t.Errorf("%s: 健全性违反：接受但轨迹不同", tc.name)
		}
	}
}

func TestDifferentialSoundness(t *testing.T) {
	st, fails := RunDifferential(20000, 1)
	for _, f := range fails {
		t.Error(f)
	}
	if st.Violations != 0 {
		t.Fatalf("健全性违反 %d 例", st.Violations)
	}
	if st.Accepted < 1000 {
		t.Errorf("接受样本过少（%d），对照不充分", st.Accepted)
	}
	if st.RejDiffer < 100 {
		t.Errorf("真阳性拒绝样本过少（%d），对照不充分", st.RejDiffer)
	}
	t.Logf("接受 %d | 拒绝且轨迹不同 %d | 保守拒绝 %d | 违反 %d",
		st.Accepted, st.RejDiffer, st.RejSame, st.Violations)
}
