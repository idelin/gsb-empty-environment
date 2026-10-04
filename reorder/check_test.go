package reorder

import "testing"

func TestCheckLegal(t *testing.T) {
	orig := mustParse(t, `
r1 = load [0, 4)
r2 = load [4, 8)
r3 = add r1, r2
`)
	// 两条 load 相互独立，交换合法
	cand := mustParse(t, `
r2 = load [4, 8)
r1 = load [0, 4)
r3 = add r1, r2
`)
	res := Check(orig, cand)
	if !res.Legal() {
		t.Fatalf("应判合法，违反边: %v", res.Violated)
	}
}

func TestCheckIllegalEarliestEdge(t *testing.T) {
	orig := mustParse(t, `
r1 = li 1
r2 = mov r1
r3 = mov r2
`)
	// 完全逆序：违反 #1->#2 与 #2->#3 两条 RAW，最早的是 #1->#2
	cand := mustParse(t, `
r3 = mov r2
r2 = mov r1
r1 = li 1
`)
	res := Check(orig, cand)
	if res.Legal() {
		t.Fatal("应判非法")
	}
	e, ok := res.Earliest()
	if !ok {
		t.Fatal("缺少最早冲突边")
	}
	if e.From != 0 || e.To != 1 || e.Kind != DepRAW {
		t.Errorf("最早冲突边 = %s，期望 #1->#2 RAW", e)
	}
	if len(res.Violated) != 2 {
		t.Errorf("违反边数 = %d，期望 2", len(res.Violated))
	}
}

func TestCheckBarrierCrossing(t *testing.T) {
	orig := mustParse(t, `
r1 = li 1
barrier
r2 = li 2
`)
	cand := mustParse(t, `
r2 = li 2
barrier
r1 = li 1
`)
	res := Check(orig, cand)
	if res.Legal() {
		t.Fatal("越过屏障应判非法")
	}
	e, _ := res.Earliest()
	if e.Kind != DepBarrier {
		t.Errorf("最早冲突边类型 = %v，期望屏障", e.Kind)
	}
}

func TestCheckMemAliasViolation(t *testing.T) {
	// store [0,4) 与 load [2,6) 部分重叠，交换违反 WAR
	orig := mustParse(t, `
r1 = load [2, 6)
[0, 4) = store r1
`)
	cand := mustParse(t, `
[0, 4) = store r1
r1 = load [2, 6)
`)
	res := Check(orig, cand)
	if res.Legal() {
		t.Fatal("部分重叠区间交换应判非法")
	}
	e, _ := res.Earliest()
	if e.Kind != DepWAR && e.Kind != DepRAW {
		t.Errorf("冲突边类型 = %v", e.Kind)
	}
}

func TestCheckNotPermutation(t *testing.T) {
	orig := mustParse(t, "r1 = li 1\nr2 = li 2\n")
	cand := mustParse(t, "r1 = li 1\nr2 = li 3\n")
	res := Check(orig, cand)
	if res.Permutation {
		t.Fatal("不同指令应判非排列")
	}
	if res.Legal() {
		t.Fatal("非排列应判非法")
	}

	cand2 := mustParse(t, "r1 = li 1\n")
	if Check(orig, cand2).Permutation {
		t.Fatal("指令数不同应判非排列")
	}
}

func TestCheckDuplicateInstructions(t *testing.T) {
	// 相同文本按出现次序配对
	orig := mustParse(t, "r1 = li 0\nr2 = mov r1\nr1 = li 0\n")
	cand := mustParse(t, "r1 = li 0\nr1 = li 0\nr2 = mov r1\n")
	res := Check(orig, cand)
	// 候选中第二个 li 0 对应原始 #3；r2=mov r1 在原始中位于 #2，
	// 依赖 #1->#2(RAW) 与 #2->#3(WAR)。候选里 #2 排最后，违反 #2->#3。
	if res.Legal() {
		t.Fatal("应判非法（WAR #2->#3 被违反）")
	}
}
