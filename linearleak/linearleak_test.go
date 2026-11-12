package linearleak

import (
	"testing"
)

// bruteForce 穷举 0/1 解，逐位返回是否存在取 0 / 取 1 的可行解。
// 仅用于测试，n <= 20 时最坏 2^20 次枚举。
func bruteForce(n int, coeff [][]int64, rhs []int64, seed map[int]int) (zeroOK, oneOK []bool, feasible bool) {
	zeroOK = make([]bool, n)
	oneOK = make([]bool, n)
	assign := make([]int, n)
	for k, v := range seed {
		assign[k] = v
	}

	var walk func(j int)
	walk = func(j int) {
		if j == n {
			for i, row := range coeff {
				sum := int64(0)
				for k, c := range row {
					sum += c * int64(assign[k])
				}
				if sum != rhs[i] {
					return
				}
			}
			feasible = true
			for k := 0; k < n; k++ {
				if assign[k] == 0 {
					zeroOK[k] = true
				} else {
					oneOK[k] = true
				}
			}
			return
		}
		if _, fixed := seed[j]; fixed {
			walk(j + 1)
			return
		}
		for v := 0; v <= 1; v++ {
			assign[j] = v
			walk(j + 1)
		}
	}
	walk(0)
	return zeroOK, oneOK, feasible
}

func TestAnalyzeBooleanOnlyForced(t *testing.T) {
	// x0 + x1 = 0：实数域两个变量都自由；布尔域只能全 0。
	eqs := []Equation{{Coeff: []int64{1, 1}, RHS: 0, Tag: "q0"}}
	rep, err := Analyze(2, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusLeak {
		t.Fatalf("status = %s, want leak", rep.Status)
	}
	if len(rep.Findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(rep.Findings))
	}
	for _, f := range rep.Findings {
		if f.Value != 0 || !f.ExactMin || f.RealFixed || len(f.Proof) != 1 {
			t.Fatalf("bad finding %+v", f)
		}
		if len(f.Tags) != 1 || f.Tags[0] != "q0" {
			t.Fatalf("tags = %v, want [q0]", f.Tags)
		}
	}
}

func TestAnalyzeRealUniqueBoolean(t *testing.T) {
	// x0 + x1 = 1, x0 - x1 = 1：实数唯一解 (1,0)，同时也是布尔解。
	eqs := []Equation{
		{Coeff: []int64{1, 1}, RHS: 1},
		{Coeff: []int64{1, -1}, RHS: 1},
	}
	rep, err := Analyze(2, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusLeak {
		t.Fatalf("status = %s, want leak", rep.Status)
	}
	want := map[int]int{0: 1, 1: 0}
	if len(rep.Findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(rep.Findings))
	}
	for _, f := range rep.Findings {
		if f.Value != want[f.Bit] || !f.RealFixed || !f.ExactMin {
			t.Fatalf("bad finding %+v", f)
		}
	}
}

func TestAnalyzeSafeFreeBits(t *testing.T) {
	// 单个计数 x0 + x1 = 1：两个位都可取 0 或 1，无泄漏。
	eqs := []Equation{{Coeff: []int64{1, 1}, RHS: 1}}
	rep, err := Analyze(2, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusSafe || len(rep.Findings) != 0 || len(rep.UnknownBits) != 0 {
		t.Fatalf("report = %+v, want safe with no findings", rep)
	}
}

func TestAnalyzeRealUniqueFractionIsInconsistent(t *testing.T) {
	// 2*x0 = 1：实数唯一解 x0=1/2，布尔域无解。
	eqs := []Equation{{Coeff: []int64{2}, RHS: 1}}
	rep, err := Analyze(1, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusInconsistent {
		t.Fatalf("status = %s, want inconsistent", rep.Status)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Value != -1 {
		t.Fatalf("findings = %+v", rep.Findings)
	}
}

func TestAnalyzeLinearContradiction(t *testing.T) {
	eqs := []Equation{
		{Coeff: []int64{1, 1}, RHS: 0},
		{Coeff: []int64{1, 1}, RHS: 1},
	}
	rep, err := Analyze(2, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusInconsistent {
		t.Fatalf("status = %s, want inconsistent", rep.Status)
	}
	// 最小矛盾子集只需其中两条方程。
	if len(rep.Findings) == 0 || len(rep.Findings[0].Proof) != 2 || !rep.Findings[0].ExactMin {
		t.Fatalf("bad proof: %+v", rep.Findings)
	}
}

func TestAnalyzePublicSubstitution(t *testing.T) {
	// x0 + x1 = 1，x0 已公开为 1，则 x1 被确定为 0。
	eqs := []Equation{{Coeff: []int64{1, 1}, RHS: 1}}
	rep, err := Analyze(2, eqs, []BitValue{{Index: 0, Value: 1}}, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusLeak || len(rep.Findings) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	f := rep.Findings[0]
	if f.Bit != 1 || f.Value != 0 || !f.RealFixed {
		t.Fatalf("bad finding %+v", f)
	}
}

func TestAnalyzeMinimalProofSelection(t *testing.T) {
	// 冗余方程 0=0 不应进入证明；x0+x1=0 单独即足以强制全 0。
	eqs := []Equation{
		{Coeff: []int64{0, 0}, RHS: 0, Tag: "noise"},
		{Coeff: []int64{1, 1}, RHS: 0, Tag: "core"},
		{Coeff: []int64{0, 0}, RHS: 0, Tag: "noise2"},
	}
	rep, err := Analyze(2, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusLeak {
		t.Fatalf("status = %s, want leak", rep.Status)
	}
	for _, f := range rep.Findings {
		if len(f.Proof) != 1 || f.Proof[0] != 1 || !f.ExactMin {
			t.Fatalf("proof = %+v, want only equation 1", f)
		}
		if len(f.Tags) != 1 || f.Tags[0] != "core" {
			t.Fatalf("tags = %v", f.Tags)
		}
	}
}

func TestAnalyzeBudgetExhaustedUnknown(t *testing.T) {
	n := 20
	eqs := []Equation{{Coeff: make([]int64, n), RHS: 0}}
	for j := range eqs[0].Coeff {
		eqs[0].Coeff[j] = 1
	}

	// 正常预算下 sum=0 强制全 0。
	rep, err := Analyze(n, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusLeak || len(rep.Findings) != n {
		t.Fatalf("status = %s, findings = %d; want leak with all 20 bits forced", rep.Status, len(rep.Findings))
	}

	// 单节点预算下，欠定系统的判定必须降级为 Unknown 而非 Safe。
	loose := []Equation{{Coeff: append([]int64{1, 1}, make([]int64, n-2)...), RHS: 1}}
	rep2, err := Analyze(n, loose, nil, Budget{FeasibilityNodes: 1, ProofChecks: 5})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Status != StatusUnknown || len(rep2.UnknownBits) == 0 {
		t.Fatalf("report = %+v, want unknown with unresolved bits", rep2)
	}
}

func TestAnalyzeValidation(t *testing.T) {
	if _, err := Analyze(0, nil, nil, Budget{}); err == nil {
		t.Fatal("want error for nBits=0")
	}
	if _, err := Analyze(21, nil, nil, Budget{}); err == nil {
		t.Fatal("want error for nBits=21")
	}
	bad := []Equation{{Coeff: []int64{1, 2, 3}, RHS: 0}}
	if _, err := Analyze(2, bad, nil, Budget{}); err == nil {
		t.Fatal("want error for coefficient length mismatch")
	}
	if _, err := Analyze(2, nil, []BitValue{{Index: 0, Value: 2}}, Budget{}); err == nil {
		t.Fatal("want error for invalid public value")
	}
}

func TestAnalyzeMatchesBruteForce(t *testing.T) {
	// 一组带正负系数的方程，与穷举结果逐位对照。
	eqs := []Equation{
		{Coeff: []int64{1, 1, 1}, RHS: 2},
		{Coeff: []int64{1, -1, 0}, RHS: 0},
		{Coeff: []int64{0, 1, 2}, RHS: 1},
	}
	n := 3
	rep, err := Analyze(n, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	coeff := make([][]int64, len(eqs))
	rhs := make([]int64, len(eqs))
	for i, e := range eqs {
		coeff[i] = e.Coeff
		rhs[i] = e.RHS
	}
	zeroOK, oneOK, feasible := bruteForce(n, coeff, rhs, nil)
	if !feasible {
		t.Fatal("test setup: brute force found no solution")
	}
	forced := map[int]int{}
	for j := 0; j < n; j++ {
		if zeroOK[j] && !oneOK[j] {
			forced[j] = 0
		}
		if oneOK[j] && !zeroOK[j] {
			forced[j] = 1
		}
	}
	if len(forced) != len(rep.Findings) {
		t.Fatalf("findings = %d, brute-force forced = %d", len(rep.Findings), len(forced))
	}
	for _, f := range rep.Findings {
		v, ok := forced[f.Bit]
		if !ok || v != f.Value {
			t.Fatalf("finding %+v not supported by brute force", f)
		}
	}
}

func TestAnalyzeTwentyBitsSafe(t *testing.T) {
	n := 20
	eqs := []Equation{{Coeff: make([]int64, n), RHS: 5}}
	for j := range eqs[0].Coeff {
		eqs[0].Coeff[j] = 1
	}
	rep, err := Analyze(n, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusSafe {
		t.Fatalf("status = %s, want safe", rep.Status)
	}
}

func TestStatusString(t *testing.T) {
	cases := map[Status]string{
		StatusUnknown:      "unknown",
		StatusSafe:         "safe",
		StatusLeak:         "leak",
		StatusInconsistent: "inconsistent",
	}
	for s, want := range cases {
		if s.String() != want {
			t.Fatalf("%d.String() = %q, want %q", s, s.String(), want)
		}
	}
}

func TestAnalyzeLargeCoefficientsOverflowPath(t *testing.T) {
	// 系数接近 int64 边界，区间求和会溢出 int64，强制走 big.Int 路径。
	bigC := int64(1) << 62
	eqs := []Equation{
		{Coeff: []int64{bigC, bigC, bigC}, RHS: bigC},
	}
	rep, err := Analyze(3, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	// 三个非负大系数之和等于一个系数：恰有一个变量为 1，其余为 0，
	// 没有任何一位被唯一确定。
	if rep.Status != StatusSafe {
		t.Fatalf("status = %s, want safe; report = %+v", rep.Status, rep)
	}

	// 右端为 0 时三个变量被同时钉死为 0。
	eqsZero := []Equation{{Coeff: []int64{bigC, bigC, bigC}, RHS: 0}}
	rep2, err := Analyze(3, eqsZero, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Status != StatusLeak || len(rep2.Findings) != 3 {
		t.Fatalf("report = %+v, want leak with 3 forced bits", rep2)
	}
	for _, f := range rep2.Findings {
		if f.Value != 0 {
			t.Fatalf("finding = %+v, want value 0", f)
		}
	}
}

func TestAnalyzeEmptyAndAllPublic(t *testing.T) {
	// 没有观测方程：任何秘密位都无法确定。
	rep, err := Analyze(3, nil, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusSafe || len(rep.Findings) != 0 || len(rep.UnknownBits) != 0 {
		t.Fatalf("empty equations report = %+v, want safe", rep)
	}

	// 所有位均已公开：不存在未公开秘密位，结论为安全。
	pub := []BitValue{{Index: 0, Value: 1}, {Index: 1, Value: 0}}
	rep2, err := Analyze(2, []Equation{{Coeff: []int64{1, 1}, RHS: 1}}, pub, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Status != StatusSafe || len(rep2.Findings) != 0 {
		t.Fatalf("all-public report = %+v, want safe with no findings", rep2)
	}

	// 全部公开但代入后方程矛盾：仍属于观测矛盾。
	badPub := []BitValue{{Index: 0, Value: 0}, {Index: 1, Value: 0}}
	rep3, err := Analyze(2, []Equation{{Coeff: []int64{1, 1}, RHS: 1}}, badPub, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep3.Status != StatusInconsistent {
		t.Fatalf("report = %+v, want inconsistent", rep3)
	}
}

func TestAnalyzeProofIsSufficient(t *testing.T) {
	// 多条方程中只有子集能强制 x2=1；验证返回的证明单独仍能强制该位。
	eqs := []Equation{
		{Coeff: []int64{1, 0, 0}, RHS: 0},             // 强制 x0=0
		{Coeff: []int64{1, 1, 1}, RHS: 1},             // 配合下条
		{Coeff: []int64{0, 1, 0}, RHS: 0},             // 强制 x1=0
		{Coeff: []int64{0, 0, 0}, RHS: 0},             // 恒真噪声
		{Coeff: []int64{1, 1, 1}, RHS: 1, Tag: "dup"}, // 重复观测
	}
	rep, err := Analyze(3, eqs, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusLeak {
		t.Fatalf("status = %s, want leak", rep.Status)
	}
	var x2 Finding
	found := false
	for _, f := range rep.Findings {
		if f.Bit == 2 {
			x2, found = f, true
		}
	}
	if !found {
		t.Fatalf("bit 2 not found in findings %+v", rep.Findings)
	}
	if x2.Value != 1 || len(x2.Proof) < 1 {
		t.Fatalf("bad bit-2 finding %+v", x2)
	}

	// 用证明子集重新分析，且只公开其余变量以隔离证明效力。
	sub := make([]Equation, 0, len(x2.Proof))
	for _, idx := range x2.Proof {
		sub = append(sub, eqs[idx])
	}
	repSub, err := Analyze(3, sub, nil, Budget{})
	if err != nil {
		t.Fatal(err)
	}
	stillForced := false
	for _, f := range repSub.Findings {
		if f.Bit == 2 && f.Value == 1 {
			stillForced = true
		}
	}
	if !stillForced {
		t.Fatalf("proof subset %v does not force bit 2: %+v", x2.Proof, repSub)
	}
}
