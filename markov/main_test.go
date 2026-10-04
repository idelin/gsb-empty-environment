package main

import (
	"math/big"
	"strings"
	"testing"
)

// buildChain 从字符串矩阵构造链，absorbing 为吸收态下标。
func buildChain(t *testing.T, rows [][]string, absorbing ...int) *Chain {
	t.Helper()
	n := len(rows)
	c := &Chain{N: n, Absorbing: make([]bool, n)}
	for _, s := range absorbing {
		c.Absorbing[s] = true
	}
	c.P = make([][]*big.Rat, n)
	for i, row := range rows {
		if len(row) != n {
			t.Fatalf("row %d: got %d entries, want %d", i, len(row), n)
		}
		c.P[i] = make([]*big.Rat, n)
		for j, s := range row {
			r, ok := new(big.Rat).SetString(s)
			if !ok {
				t.Fatalf("bad rational %q", s)
			}
			c.P[i][j] = r
		}
	}
	return c
}

func analyzeAndVerify(t *testing.T, c *Chain) *Analysis {
	t.Helper()
	a, err := Analyze(c)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	v := a.Verify()
	if v.HitResidual.Sign() != 0 {
		t.Fatalf("hit residual = %s, want 0", v.HitResidual)
	}
	if v.TimeResidual.Sign() != 0 {
		t.Fatalf("time residual = %s, want 0", v.TimeResidual)
	}
	if !v.HitSumOK || !v.RangeOK || !v.InfOK {
		t.Fatalf("verification flags: sum=%v range=%v inf=%v", v.HitSumOK, v.RangeOK, v.InfOK)
	}
	return a
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic(s)
	}
	return r
}

// 场景 1：初态本身已吸收 —— 期望 0 步，命中自身类概率 1。
func TestAlreadyAbsorbedInitialState(t *testing.T) {
	c := buildChain(t, [][]string{
		{"1", "0"},
		{"1/2", "1/2"},
	}, 0)
	a := analyzeAndVerify(t, c)

	if got := a.ExpSteps[0]; got == nil || got.Sign() != 0 {
		t.Fatalf("ExpSteps[0] = %v, want 0", got)
	}
	if a.Hit[0][0].Cmp(ratOne) != 0 {
		t.Fatalf("Hit[0][own class] = %s, want 1", a.Hit[0][0])
	}
	// 状态 1 几乎必然被吸收，期望 = 2。
	if got := a.ExpSteps[1]; got == nil || got.Cmp(big.NewRat(2, 1)) != 0 {
		t.Fatalf("ExpSteps[1] = %v, want 2", got)
	}
}

// 场景 2：存在不可达的吸收点 —— 其命中概率必须为 0。
func TestUnreachableAbsorbingState(t *testing.T) {
	c := buildChain(t, [][]string{
		{"1", "0", "0"},
		{"0", "1", "0"},
		{"1", "0", "0"},
	}, 0, 1)
	a := analyzeAndVerify(t, c)

	if len(a.Classes) != 2 {
		t.Fatalf("got %d closed classes, want 2", len(a.Classes))
	}
	// 从状态 2 出发：命中 {0} 概率 1，命中 {1} 概率 0。
	if a.Hit[2][0].Cmp(ratOne) != 0 {
		t.Fatalf("Hit[2][{0}] = %s, want 1", a.Hit[2][0])
	}
	if a.Hit[2][1].Sign() != 0 {
		t.Fatalf("Hit[2][{1}] = %s, want 0 (unreachable)", a.Hit[2][1])
	}
	if got := a.ExpSteps[2]; got == nil || got.Cmp(ratOne) != 0 {
		t.Fatalf("ExpSteps[2] = %v, want 1", got)
	}
	// 不可达吸收点不应出现在可达证据中。
	rep := a.Report([]int{2}, a.Verify())
	if strings.Contains(rep, "class 1 {1} (absorbing) via") {
		t.Fatalf("report claims unreachable class is reachable:\n%s", rep)
	}
}

// 场景 3：概率极小但非零的通路 —— 精确有理数结果，不得因数值下溢丢失。
func TestTinyButNonzeroProbabilityPath(t *testing.T) {
	p := "1e-30"
	q := new(big.Rat).Sub(ratOne, rat(p)).RatString() // 1 - 1e-30，精确有理数
	c := buildChain(t, [][]string{
		{"1", "0", "0"},
		{p, "0", q},
		{"0", "1", "0"},
	}, 0)
	a := analyzeAndVerify(t, c)

	// 吸收概率恰为 1（通路虽小但非零，几何级数收敛到 1）。
	if a.Hit[1][0].Cmp(ratOne) != 0 {
		t.Fatalf("Hit[1][absorbing] = %s, want 1", a.Hit[1][0])
	}
	// 期望步数 = (2-p)/p = 2*10^30 - 1，必须是精确整数。
	want := new(big.Rat).Sub(big.NewRat(2, 1), rat(p))
	want.Quo(want, rat(p))
	if got := a.ExpSteps[1]; got == nil || got.Cmp(want) != 0 {
		t.Fatalf("ExpSteps[1] = %v, want %s", got, want)
	}
	if got := a.ExpSteps[2]; got == nil || got.Cmp(new(big.Rat).Add(want, ratOne)) != 0 {
		t.Fatalf("ExpSteps[2] = %v, want %s + 1", got, want)
	}
}

// 场景 4：存在非吸收闭类 —— 不得硬解奇异方程给出有限期望；
// 期望必须为 +∞，并输出可达闭类证据。
func TestNonAbsorbingClosedClass(t *testing.T) {
	c := buildChain(t, [][]string{
		{"0", "1", "0", "0"},
		{"1", "0", "0", "0"},
		{"1/2", "0", "0", "1/2"},
		{"0", "0", "0", "1"},
	}, 3)
	a := analyzeAndVerify(t, c)

	if len(a.Classes) != 2 {
		t.Fatalf("got %d closed classes, want 2", len(a.Classes))
	}
	// 状态 2 是瞬态，但只能以 1/2 概率被吸收。
	if a.ExpSteps[2] != nil {
		t.Fatalf("ExpSteps[2] = %s, want +inf (nil)", a.ExpSteps[2])
	}
	// 非吸收闭类内状态期望也是 +∞。
	if a.ExpSteps[0] != nil || a.ExpSteps[1] != nil {
		t.Fatalf("closed-class states must have infinite expectation")
	}
	// 命中概率各 1/2。
	half := big.NewRat(1, 2)
	if a.Hit[2][0].Cmp(half) != 0 || a.Hit[2][1].Cmp(half) != 0 {
		t.Fatalf("Hit[2] = [%s, %s], want [1/2, 1/2]", a.Hit[2][0], a.Hit[2][1])
	}
	// 报告必须包含 +inf 与两个闭类的见证路径。
	rep := a.Report([]int{2}, a.Verify())
	for _, want := range []string{"+inf", "2 -> 0", "2 -> 3"} {
		if !strings.Contains(rep, want) {
			t.Fatalf("report missing %q:\n%s", want, rep)
		}
	}
}

// 场景 4b：非吸收闭类使全体非吸收状态的 (I-Q) 奇异 —— 求解器必须拒绝。
func TestSingularSystemRejected(t *testing.T) {
	// 状态 0<->1 构成非吸收闭类，若把它们当瞬态求解 (I-Q)t=1 则矩阵奇异。
	iq := [][]*big.Rat{
		{rat("1"), rat("-1")},
		{rat("-1"), rat("1")},
	}
	rhs := [][]*big.Rat{{ratOne}, {ratOne}}
	if _, ok := solveLinear(iq, rhs); ok {
		t.Fatal("solveLinear must report singularity, not fabricate a solution")
	}
}

// 全状态查询 + 报告冒烟测试：所有验证标志为真，残差为 0。
func TestReportSmoke(t *testing.T) {
	c := buildChain(t, [][]string{
		{"1", "0", "0", "0"},
		{"1/3", "1/3", "1/3", "0"},
		{"0", "1/2", "0", "1/2"},
		{"0", "0", "0", "1"},
	}, 0, 3)
	a := analyzeAndVerify(t, c)
	rep := a.Report([]int{0, 1, 2, 3}, a.Verify())
	if !strings.Contains(rep, "hitting-probability equation residual: 0") {
		t.Fatalf("report missing zero residual:\n%s", rep)
	}
	if !strings.Contains(rep, "infinity iff absorption prob < 1: true") {
		t.Fatalf("report missing infinity check:\n%s", rep)
	}
}

// 非法输入：行和不为 1 必须报错。
func TestValidationRejectsBadChain(t *testing.T) {
	c := buildChain(t, [][]string{
		{"1", "0"},
		{"1/2", "1/3"},
	}, 0)
	if _, err := Analyze(c); err == nil {
		t.Fatal("expected validation error for row not summing to 1")
	}
}
