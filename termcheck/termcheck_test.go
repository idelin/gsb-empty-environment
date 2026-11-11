package termcheck

import "testing"

// loop 构造单参数自调用边的快捷方式。
func loop(fn string, r Rel) Edge {
	return Edge{From: fn, To: fn, M: NewMatrix(1, 1, r)}
}

func TestSimpleDecrease(t *testing.T) {
	rep := Prove([]Edge{loop("f", Less)})
	if !rep.Terminating {
		t.Fatalf("f(x)->f(x-1) 应判定终止, 得到违反: %+v", rep.Violations)
	}
}

func TestNonIncreasingLoopRejected(t *testing.T) {
	rep := Prove([]Edge{loop("f", Leq)})
	if rep.Terminating {
		t.Fatal("f(x)->f(x) 不增循环不应通过")
	}
	if len(rep.Violations) != 1 || rep.Violations[0].Func != "f" {
		t.Fatalf("应报告 f 处一条违反链, 得到 %+v", rep.Violations)
	}
	if n := len(rep.Violations[0].Chain); n != 1 {
		t.Fatalf("单边自环的违反链长度应为 1, 得到 %d", n)
	}
}

func TestUnknownLoopRejected(t *testing.T) {
	if Prove([]Edge{loop("f", Unknown)}).Terminating {
		t.Fatal("未知关系的自循环不应通过")
	}
}

func TestMutualRecursionDecreaseAcrossPair(t *testing.T) {
	// f(x) 调 g(x) 不增, g(x) 调 f(x-1) 严格下降: 绕一圈严格变小。
	edges := []Edge{
		{From: "f", To: "g", M: NewMatrix(1, 1, Leq)},
		{From: "g", To: "f", M: NewMatrix(1, 1, Less)},
	}
	if rep := Prove(edges); !rep.Terminating {
		t.Fatalf("跨互递归对下降的循环应判定终止, 得到 %+v", rep.Violations)
	}
}

func TestMutualRecursionNoDecrease(t *testing.T) {
	edges := []Edge{
		{From: "f", To: "g", M: NewMatrix(1, 1, Leq)},
		{From: "g", To: "f", M: NewMatrix(1, 1, Leq)},
	}
	rep := Prove(edges)
	if rep.Terminating {
		t.Fatal("全程不增的互递归不应通过")
	}
	// f 与 g 处各闭合一条 Leq 幂等循环。
	if len(rep.Violations) != 2 {
		t.Fatalf("应在 f、g 各报一条违反链, 得到 %+v", rep.Violations)
	}
}

func TestPurePermutationRejected(t *testing.T) {
	// f(x,y) 调 f(y,x): 纯置换, 绕两圈回到不增恒等, 无严格下降。
	swap := NewMatrix(2, 2, Unknown, Leq, Leq, Unknown)
	rep := Prove([]Edge{{From: "f", To: "f", M: swap}})
	if rep.Terminating {
		t.Fatal("纯参数置换循环不应通过")
	}
}

func TestDecreasingSwapAccepted(t *testing.T) {
	// f(x,y) 调 f(y, x-1): 置换伴随下降, 复合两圈后两个参数都严格变小。
	m := NewMatrix(2, 2, Unknown, Leq, Less, Unknown)
	if rep := Prove([]Edge{{From: "f", To: "f", M: m}}); !rep.Terminating {
		t.Fatalf("带下降的置换应判定终止, 得到 %+v", rep.Violations)
	}
}

func TestArityMismatchTreatedAsUnknown(t *testing.T) {
	// f 有两个参数, g 只有一个: g 的参数严格小于 f 的第二参数,
	// 但回边只保证 f 的第一参数不增、第二参数无来源保证, 下降链
	// 在缺失维度上断开, 不能凭空判安全。
	edges := []Edge{
		{From: "f", To: "g", M: NewMatrix(1, 2, Unknown, Less)},
		{From: "g", To: "f", M: NewMatrix(2, 1, Leq, Unknown)},
	}
	rep := Prove(edges)
	if rep.Terminating {
		t.Fatal("f 第二参数无来源保证, 互递归不应通过")
	}
	// g 处环路关系未知、 f 处两圈后对角无严格下降, 各报一条。
	got := map[string]bool{}
	for _, v := range rep.Violations {
		got[v.Func] = true
	}
	if len(rep.Violations) != 2 || !got["f"] || !got["g"] {
		t.Fatalf("应在 f、g 各报一条违反链, 得到 %+v", rep.Violations)
	}
}

func TestArityMismatchWithClosedDecrease(t *testing.T) {
	// 同上但回边把两个参数都接到 g 的唯一参数上: f 第一参数绕一圈
	// 严格变小, 应判定终止。
	edges := []Edge{
		{From: "f", To: "g", M: NewMatrix(1, 2, Less, Leq)},
		{From: "g", To: "f", M: NewMatrix(2, 1, Leq, Leq)},
	}
	if rep := Prove(edges); !rep.Terminating {
		t.Fatalf("下降链闭合的异元互递归应判定终止, 得到 %+v", rep.Violations)
	}
}

func TestAcyclicCallsAlwaysTerminate(t *testing.T) {
	edges := []Edge{
		{From: "f", To: "g", M: NewMatrix(1, 1, Unknown)},
		{From: "g", To: "h", M: NewMatrix(1, 1, Unknown)},
	}
	if rep := Prove(edges); !rep.Terminating {
		t.Fatalf("无环调用图应判定终止, 得到 %+v", rep.Violations)
	}
}

// refCombine 是测试侧的独立关系复合表, 与实现分开书写以便交叉核对。
func refCombine(a, b Rel) Rel {
	table := [3][3]Rel{
		{Unknown, Unknown, Unknown},
		{Unknown, Leq, Less},
		{Unknown, Less, Less},
	}
	return table[a][b]
}

// refCompose 是测试侧的独立矩阵复合实现。
func refCompose(a, b Matrix) Matrix {
	rows, cols := b.Rows, a.Cols
	cells := make([]Rel, rows*cols)
	for i := 0; i < rows; i++ {
		for k := 0; k < cols; k++ {
			best := Unknown
			for j := 0; j < a.Rows && j < b.Cols; j++ {
				if r := refCombine(a.At(j, k), b.At(i, j)); r > best {
					best = r
				}
			}
			cells[i*cols+k] = best
		}
	}
	return Matrix{Rows: rows, Cols: cols, Cells: cells}
}

// refProve 是测试侧的朴素闭包实现: 反复复合直到不动点, 再逐个检查
// 幂等循环矩阵是否有严格下降对角项。
func refProve(edges []Edge) bool {
	type key = string
	mkey := func(m Matrix) string {
		s := ""
		for _, c := range m.Cells {
			s += string(rune('0' + c))
		}
		return s
	}
	seen := map[key]bool{}
	add := func(from, to string, m Matrix) bool {
		k := from + "|" + to + "|" + mkey(m)
		if seen[k] {
			return false
		}
		seen[k] = true
		return true
	}
	type g struct {
		from, to string
		m        Matrix
	}
	var gs []g
	for _, e := range edges {
		if add(e.From, e.To, e.M) {
			gs = append(gs, g{e.From, e.To, e.M})
		}
	}
	for i := 0; i < len(gs); i++ {
		for j := 0; j < len(gs); j++ {
			if gs[i].to != gs[j].from {
				continue
			}
			m := refCompose(gs[i].m, gs[j].m)
			if add(gs[i].from, gs[j].to, m) {
				gs = append(gs, g{gs[i].from, gs[j].to, m})
			}
		}
	}
	for _, x := range gs {
		if x.from != x.to {
			continue
		}
		if !refCompose(x.m, x.m).Equal(x.m) {
			continue
		}
		ok := false
		for d := 0; d < x.m.Rows && d < x.m.Cols; d++ {
			if x.m.At(d, d) == Less {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// checkAgree 对给定边集断言 Prove 与参考实现结论一致, 且每条违反链
// 首尾相接、复合结果等于所报矩阵、确为无严格下降对角的幂等矩阵。
func checkAgree(t *testing.T, edges []Edge) {
	t.Helper()
	rep := Prove(edges)
	if rep.Terminating != refProve(edges) {
		t.Fatalf("结论不一致: Prove=%v ref=%v edges=%+v", rep.Terminating, !rep.Terminating, edges)
	}
	for _, v := range rep.Violations {
		if len(v.Chain) == 0 {
			t.Fatalf("违反链为空: %+v", v)
		}
		if v.Chain[0].From != v.Func || v.Chain[len(v.Chain)-1].To != v.Func {
			t.Fatalf("违反链未在 %s 闭合: %+v", v.Func, v)
		}
		m := v.Chain[0].M
		for i := 1; i < len(v.Chain); i++ {
			if v.Chain[i-1].To != v.Chain[i].From {
				t.Fatalf("违反链在 %d 处断开: %+v", i, v)
			}
			m = Compose(m, v.Chain[i].M)
		}
		if !m.Equal(v.M) {
			t.Fatalf("违反链复合结果 %v 与所报矩阵 %v 不符", m.Cells, v.M.Cells)
		}
		if !Compose(v.M, v.M).Equal(v.M) {
			t.Fatalf("所报矩阵非幂等: %+v", v.M)
		}
		if hasLessDiag(v.M) {
			t.Fatalf("所报矩阵已有严格下降对角项: %+v", v.M)
		}
	}
}

func TestEnumerateUnarySelfLoops(t *testing.T) {
	rels := []Rel{Unknown, Leq, Less}
	for _, r := range rels {
		edges := []Edge{loop("f", r)}
		checkAgree(t, edges)
		want := r == Less
		if got := Prove(edges).Terminating; got != want {
			t.Fatalf("1x1 自环 %s: 期望 %v, 得到 %v", r, want, got)
		}
	}
}

func TestEnumerateUnaryMutualRecursion(t *testing.T) {
	rels := []Rel{Unknown, Leq, Less}
	for _, a := range rels {
		for _, b := range rels {
			edges := []Edge{
				{From: "f", To: "g", M: NewMatrix(1, 1, a)},
				{From: "g", To: "f", M: NewMatrix(1, 1, b)},
			}
			checkAgree(t, edges)
			// 单参数互递归: 环路关系为 then(a,b), 幂等, 仅严格下降时终止。
			want := then(a, b) == Less
			if got := Prove(edges).Terminating; got != want {
				t.Fatalf("互递归 (%s,%s): 期望 %v, 得到 %v", a, b, want, got)
			}
		}
	}
}

func TestEnumerateBinarySelfLoops(t *testing.T) {
	// 枚举全部 3^4 = 81 个 2x2 自环矩阵, 与参考实现逐一核对。
	rels := []Rel{Unknown, Leq, Less}
	for _, c00 := range rels {
		for _, c01 := range rels {
			for _, c10 := range rels {
				for _, c11 := range rels {
					m := NewMatrix(2, 2, c00, c01, c10, c11)
					checkAgree(t, []Edge{{From: "f", To: "f", M: m}})
				}
			}
		}
	}
}

func TestEnumeratePermutations(t *testing.T) {
	// 枚举所有 2 参数置换型自环: 非对角取自 {Leq, Less}, 对角未知。
	// 纯 Leq 置换必被拒绝; 任一方向严格下降则被接受。
	rels := []Rel{Leq, Less}
	for _, a := range rels {
		for _, b := range rels {
			m := NewMatrix(2, 2, Unknown, a, b, Unknown)
			edges := []Edge{{From: "f", To: "f", M: m}}
			checkAgree(t, edges)
			want := a == Less || b == Less
			if got := Prove(edges).Terminating; got != want {
				t.Fatalf("置换 (%s,%s): 期望 %v, 得到 %v", a, b, want, got)
			}
		}
	}
}

func TestEnumerateBinaryMutualRecursion(t *testing.T) {
	// 两函数各两个参数, 抽样枚举互递归边矩阵(取置换与下降的组合)。
	mats := []Matrix{
		NewMatrix(2, 2, Less, Unknown, Unknown, Leq),
		NewMatrix(2, 2, Leq, Unknown, Unknown, Less),
		NewMatrix(2, 2, Unknown, Leq, Less, Unknown),
		NewMatrix(2, 2, Unknown, Less, Leq, Unknown),
		NewMatrix(2, 2, Leq, Leq, Leq, Leq),
	}
	for _, a := range mats {
		for _, b := range mats {
			edges := []Edge{
				{From: "f", To: "g", M: a},
				{From: "g", To: "f", M: b},
			}
			checkAgree(t, edges)
		}
	}
}

func TestViolationChainForThreeCycle(t *testing.T) {
	// f -> g -> h -> f 全程不增: 违反链应覆盖整条三环。
	edges := []Edge{
		{From: "f", To: "g", M: NewMatrix(1, 1, Leq)},
		{From: "g", To: "h", M: NewMatrix(1, 1, Leq)},
		{From: "h", To: "f", M: NewMatrix(1, 1, Leq)},
	}
	rep := Prove(edges)
	if rep.Terminating {
		t.Fatal("不增三环不应通过")
	}
	if len(rep.Violations) != 3 {
		t.Fatalf("三个函数各应有一条违反链, 得到 %+v", rep.Violations)
	}
	for _, v := range rep.Violations {
		if len(v.Chain) != 3 {
			t.Fatalf("%s 处违反链应含 3 条边, 得到 %d", v.Func, len(v.Chain))
		}
	}
}
