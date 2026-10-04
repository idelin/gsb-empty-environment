package reorder

import "testing"

var binDomain = []uint64{0, 1}

func TestExhaustiveFindsDivergence(t *testing.T) {
	orig := mustParse(t, "r1 = li 0\nr2 = mov r1\n")
	cand := mustParse(t, "r2 = mov r1\nr1 = li 0\n")

	res := Check(orig, cand)
	if res.Legal() {
		t.Fatal("应判非法")
	}
	ex := ExhaustiveCheck(orig, cand, binDomain, 1<<20)
	if !ex.Exhaustive {
		t.Fatal("小状态空间应完全穷举")
	}
	if ex.Divergence == nil {
		t.Fatal("应发现发散（r1=1 时 r2 结果不同）")
	}
	if ex.Divergence.Init.Regs["r1"] != 1 {
		t.Errorf("发散初始状态 r1 = %d，期望 1", ex.Divergence.Init.Regs["r1"])
	}
	found := false
	for _, d := range ex.Divergence.Diffs {
		if d.Loc == Reg("r2") {
			found = true
		}
	}
	if !found {
		t.Error("分歧应体现在 r2 上")
	}
}

func TestExhaustiveMemAliasDivergence(t *testing.T) {
	orig := mustParse(t, `
r1 = li 1
[0, 2) = store r1
r2 = load [1, 3)
`)
	cand := mustParse(t, `
r1 = li 1
r2 = load [1, 3)
[0, 2) = store r1
`)
	ex := ExhaustiveCheck(orig, cand, binDomain, 1<<20)
	if ex.Divergence == nil {
		t.Fatal("store/load 越序应发现发散")
	}
}

func TestExhaustiveLegalNoDivergence(t *testing.T) {
	orig := mustParse(t, "r1 = li 1\nr2 = li 2\nr3 = add r1, r2\n")
	cand := mustParse(t, "r2 = li 2\nr1 = li 1\nr3 = add r1, r2\n")
	ex := ExhaustiveCheck(orig, cand, binDomain, 1<<20)
	if !ex.Exhaustive || ex.Divergence != nil {
		t.Fatalf("合法重排不应发散: %+v", ex)
	}
}

// 关键用例：碰巧结果相同不能当作无依赖证明。
// 两条指令都把 r1 置 0，交换后任何初始状态下最终状态都相同，
// 穷举永远找不到发散，但 WAW 依赖边仍然存在，必须判非法。
func TestCoincidentalEqualityIsNotProof(t *testing.T) {
	orig := mustParse(t, "r1 = li 0\nr1 = xor r2, r2\n")
	cand := mustParse(t, "r1 = xor r2, r2\nr1 = li 0\n")

	res := Check(orig, cand)
	if res.Legal() {
		t.Fatal("WAW 被违反，应判非法")
	}
	e, _ := res.Earliest()
	if e.Kind != DepWAW {
		t.Errorf("最早冲突边类型 = %v，期望 WAW", e.Kind)
	}

	ex := ExhaustiveCheck(orig, cand, binDomain, 1<<20)
	if ex.Divergence != nil {
		t.Fatal("该用例最终状态恒相同，穷举不应发现发散")
	}
	// 尽管穷举无发散，结论仍以依赖分析为准：非法。
	if res.Legal() {
		t.Fatal("穷举无发散不得推翻依赖分析结论")
	}
}

func TestExhaustiveFallbackNotExhaustive(t *testing.T) {
	// 构造触及大量单元的序列，使 domain^cells 超过上限
	src := ""
	for i := 0; i < 30; i++ {
		src += "r" + itoa(i) + " = li 0\n"
	}
	insts := mustParse(t, src)
	ex := ExhaustiveCheck(insts, insts, binDomain, 1<<10)
	if ex.Exhaustive {
		t.Fatal("超过上限应标记为非穷举")
	}
	if ex.Tested == 0 || ex.Tested > 1<<10 {
		t.Errorf("采样数 %d 超出预算", ex.Tested)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
