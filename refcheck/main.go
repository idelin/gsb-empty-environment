package main

import (
	"fmt"
	"math/rand"
)

// ---------- 枚举对照 ----------
// 对每个生成的小程序：静态检查器给出 接受/拒绝；
// 解释器分别运行原程序与重构后程序，比较可观察轨迹。
// 健全性要求：接受 => 轨迹必然相同。

type Stats struct {
	Total      int
	Accepted   int
	RejDiffer  int // 拒绝且轨迹确实不同（真阳性）
	RejSame    int // 拒绝但轨迹相同（保守拒绝）
	Violations int // 接受但轨迹不同（健全性违反，必须为 0）
}

func RunDifferential(n int, seed int64) (Stats, []string) {
	r := rand.New(rand.NewSource(seed))
	var st Stats
	var fails []string
	for i := 0; i < n; i++ {
		prog, ft, e, o1, o2 := genCase(r)
		rej := CheckExtract(prog, ft, e, o1, o2)
		rp := Refactor(prog, e, o1, o2)
		a := Run(prog, ft)
		b := Run(rp, ft)
		eq := EqualResults(a, b)
		st.Total++
		if rej == nil {
			st.Accepted++
			if !eq {
				st.Violations++
				fails = append(fails, fmt.Sprintf(
					"健全性违反 #%d\n程序:%s\ne = %s\nO1=%v O2=%v\n原始: %+v\n重构: %+v",
					i, progStr(prog), exprStr(e), o1, o2, a, b))
			}
		} else if eq {
			st.RejSame++
		} else {
			st.RejDiffer++
		}
	}
	return st, fails
}

// ---------- 演示 ----------

func demo(name string, prog []Stmt, ft FuncTable, e Expr, o1, o2 []int) {
	fmt.Printf("== %s ==\n程序:%s\n抽取 e = %s   O1=%s O2=%s\n",
		name, progStr(prog), exprStr(e), locStr(prog, o1), locStr(prog, o2))
	if rej := CheckExtract(prog, ft, e, o1, o2); rej != nil {
		fmt.Printf("判定：%v\n\n", rej)
		return
	}
	rp := Refactor(prog, e, o1, o2)
	a, b := Run(prog, ft), Run(rp, ft)
	fmt.Printf("判定：接受\n重构后:%s\n轨迹一致: %v (原 首错=%q 事件=%d | 新 首错=%q 事件=%d)\n\n",
		progStr(rp), EqualResults(a, b), a.Err, len(a.Events), b.Err, len(b.Events))
}

func main() {
	// 1. 两次求值之间变量被写 -> 拒绝（读取版本）
	demo("写介于两次求值之间",
		[]Stmt{
			&Print{&Add{&Var{"x"}, &Const{1}}},
			&Assign{"x", &Const{7}},
			&Print{&Add{&Var{"x"}, &Const{1}}},
		},
		FuncTable{},
		&Add{&Var{"x"}, &Const{1}}, []int{0, 0}, []int{2, 0})

	// 2. 调用写介于两次求值之间 -> 拒绝（依赖路径经过调用）
	demo("调用写介于两次求值之间",
		[]Stmt{
			&Print{&Var{"x"}},
			&Print{&Call{"f", nil}},
			&Print{&Var{"x"}},
		},
		FuncTable{"f": FuncSig{Writes: []string{"x"}}},
		&Var{"x"}, []int{0, 0}, []int{2, 0})

	// 3. 短路分支：O1 在 && 右侧但语句必然执行，e 纯 -> 接受
	demo("短路分支（可接受）",
		[]Stmt{
			&Assign{"x", &Const{5}},
			&Print{&And{&Const{0}, &Var{"x"}}},
			&Print{&Var{"x"}},
		},
		FuncTable{},
		&Var{"x"}, []int{1, 0, 1}, []int{2, 0})

	// 4. 条件分支：绑定语句在 then 内，O2 在分支外 -> 拒绝（支配性）
	demo("条件分支绕过绑定",
		[]Stmt{
			&If{&Var{"x"},
				[]Stmt{&Print{&Add{&Var{"y"}, &Const{1}}}},
				nil},
			&Print{&Add{&Var{"y"}, &Const{1}}},
		},
		FuncTable{},
		&Add{&Var{"y"}, &Const{1}}, []int{0, 1, 0, 0}, []int{1, 0})

	// 5. 表达式可能抛错且不是语句内首个求值位置 -> 拒绝（首错）
	demo("首错改变",
		[]Stmt{
			&Assign{"y", &Add{&Call{"g", nil}, &Call{"f", nil}}},
			&Print{&Call{"f", nil}},
		},
		FuncTable{
			"f": FuncSig{Throws: true},
			"g": FuncSig{Writes: []string{"z"}},
		},
		&Call{"f", nil}, []int{0, 0, 1}, []int{1, 0})

	// 6. 干净情形 -> 接受
	demo("可安全抽取",
		[]Stmt{
			&Assign{"x", &Const{3}},
			&Print{&Add{&Var{"x"}, &Var{"x"}}},
			&Print{&Add{&Var{"x"}, &Var{"x"}}},
		},
		FuncTable{},
		&Add{&Var{"x"}, &Var{"x"}}, []int{1, 0}, []int{2, 0})

	// 枚举对照
	st, fails := RunDifferential(20000, 42)
	fmt.Printf("== 枚举对照（20000 个小程序） ==\n")
	fmt.Printf("接受 %d | 拒绝且轨迹不同 %d | 拒绝但轨迹相同(保守) %d | 健全性违反 %d\n",
		st.Accepted, st.RejDiffer, st.RejSame, st.Violations)
	for _, f := range fails {
		fmt.Println(f)
	}
}
