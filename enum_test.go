package netsec_test

import (
	"context"
	"math/big"
	"testing"

	netsec "example.org/netsec/q0441"
)

// 本测试独立重新实现有理矩阵的整数权重解析与联合分布枚举，
// 不调用被测包的任何未导出函数，从而交叉核对包的实际抽样输出。

type frac struct{ num, den int64 }

type intMatrix [][]frac

func lcm64(a, b int64) int64 {
	g := new(big.Int).GCD(nil, nil, big.NewInt(a), big.NewInt(b)).Int64()
	return a / g * b
}

// rowWeights 独立解析：以行分母最小公倍数为总权重，返回各符号整数权重。
func rowWeights(m intMatrix, row int) (weights []int64, total int64) {
	total = 1
	for _, f := range m[row] {
		total = lcm64(total, f.den)
	}
	weights = make([]int64, len(m[row]))
	for i, f := range m[row] {
		weights[i] = f.num * (total / f.den)
	}
	return weights, total
}

// drawFromWeights 按整数权重把一次均匀抽取 x 映射到符号。
func drawFromWeights(weights []int64, x int64) int {
	var cumulative int64
	for symbol, w := range weights {
		cumulative += w
		if x < cumulative {
			return symbol
		}
	}
	return len(weights) - 1
}

// jointDistribution 枚举从真值 truth 出发的全部等概率抽样分支
// （PRR 一次 + IRR 两次），统计 (y1,y2) 联合输出的精确有理分布以及
// 永久符号 m 的边缘分布。
func jointDistribution(prr, irr intMatrix, truth int) (map[[2]int]*big.Rat, map[int]*big.Rat) {
	n := len(prr)
	prrW, prrT := rowWeights(prr, truth)
	irrTotals := make([]int64, n)
	irrWeights := make([][]int64, n)
	for m := 0; m < n; m++ {
		irrWeights[m], irrTotals[m] = rowWeights(irr, m)
	}

	joint := make(map[[2]int]*big.Rat)
	marginalM := make(map[int]*big.Rat)
	for m, pw := range prrW {
		if pw == 0 {
			continue
		}
		pmProb := big.NewRat(pw, prrT)
		addRat(marginalM, m, pmProb)
		for x1 := int64(0); x1 < irrTotals[m]; x1++ {
			y1 := drawFromWeights(irrWeights[m], x1)
			for x2 := int64(0); x2 < irrTotals[m]; x2++ {
				y2 := drawFromWeights(irrWeights[m], x2)
				branch := new(big.Rat).Mul(pmProb, big.NewRat(1, irrTotals[m]*irrTotals[m]))
				addRat(joint, [2]int{y1, y2}, branch)
			}
		}
	}
	return joint, marginalM
}

func addRat[K comparable](m map[K]*big.Rat, k K, r *big.Rat) {
	if existing, ok := m[k]; ok {
		existing.Add(existing, r)
		return
	}
	m[k] = new(big.Rat).Set(r)
}

// scriptedRand 是测试侧独立实现的确定性随机源。
type scriptedRand struct {
	values []int
	pos    int
	bounds []int
}

func (s *scriptedRand) Intn(n int) int {
	s.bounds = append(s.bounds, n)
	v := s.values[s.pos]
	s.pos++
	if v < 0 || v >= n {
		panic("scripted rand out of range")
	}
	return v
}

// enumerateReporter 用被测包逐一遍历所有等概率抽样分支，统计联合输出。
// 每次都新建 Reporter+内存存储，使每个分支独立抽取自己的永久符号。
func enumerateReporter(t *testing.T, prr, irr netsec.Matrix, truth int) map[[2]int]*big.Rat {
	t.Helper()
	prrTotal := rowDenomLCM(toIntMatrix(prr), truth)
	joint := make(map[[2]int]*big.Rat)
	ctx := context.Background()

	for px := int64(0); px < prrTotal; px++ {
		// 先用一次 PRR 抽取确定该分支的永久符号。
		probeStore := netsec.NewMemoryStore()
		probe := newEnumReporter(t, probeStore, prr, irr, &scriptedRand{values: []int{int(px)}})
		permanent, err := probe.Permanent(ctx, truth)
		if err != nil {
			t.Fatalf("probe Permanent: %v", err)
		}
		irrTotal := rowDenomLCM(toIntMatrix(irr), permanent)

		for x1 := int64(0); x1 < irrTotal; x1++ {
			for x2 := int64(0); x2 < irrTotal; x2++ {
				store := netsec.NewMemoryStore()
				rng := &scriptedRand{values: []int{int(px), int(x1), int(x2)}}
				r := newEnumReporter(t, store, prr, irr, rng)
				y1, err := r.Report(ctx, truth)
				if err != nil {
					t.Fatalf("Report y1: %v", err)
				}
				y2, err := r.Report(ctx, truth)
				if err != nil {
					t.Fatalf("Report y2: %v", err)
				}
				// 第二次 Report 不得再触发 PRR：调用上界序列必须是 PRR、IRR、IRR。
				if len(rng.bounds) != 3 {
					t.Fatalf("expected 3 random draws per branch, got bounds %v", rng.bounds)
				}
				// 每条分支等权：1 / (prrTotal * irrTotal^2)，
				// 汇总后与独立枚举的有理分布逐格比较。
				addRat(joint, [2]int{y1, y2},
					big.NewRat(1, prrTotal*irrTotal*irrTotal))
			}
		}
	}
	return joint
}

func newEnumReporter(t *testing.T, store netsec.Store, prr, irr netsec.Matrix, rng netsec.Source) *netsec.Reporter {
	t.Helper()
	r, err := netsec.NewReporter(netsec.Config{
		DeviceID:      "dev-enum",
		Field:         "field-enum",
		ParamVersion:  "enum-v1",
		Permanent:     prr,
		Instantaneous: irr,
		Store:         store,
		Source:        rng,
	})
	if err != nil {
		t.Fatalf("NewReporter: %v", err)
	}
	return r
}

func toIntMatrix(m netsec.Matrix) intMatrix {
	out := make(intMatrix, m.Size())
	for i, row := range m.Row {
		out[i] = make([]frac, len(row))
		for j, r := range row {
			out[i][j] = frac{num: r.Numerator, den: r.Denominator}
		}
	}
	return out
}

func rowDenomLCM(m intMatrix, row int) int64 {
	total := int64(1)
	for _, f := range m[row] {
		total = lcm64(total, f.den)
	}
	return total
}

func TestJointDistribution2x2(t *testing.T) {
	// PRR 行 0：3/4 留在 0，1/4 翻到 1（总权重 4）。
	// IRR 行内分母为 3（总权重 3），两层总权重不同以暴露 LCM 接线错误。
	prr := netsec.Matrix{Row: [][]netsec.Rational{
		{{Numerator: 3, Denominator: 4}, {Numerator: 1, Denominator: 4}},
		{{Numerator: 1, Denominator: 4}, {Numerator: 3, Denominator: 4}},
	}}
	irr := netsec.Matrix{Row: [][]netsec.Rational{
		{{Numerator: 2, Denominator: 3}, {Numerator: 1, Denominator: 3}},
		{{Numerator: 1, Denominator: 3}, {Numerator: 2, Denominator: 3}},
	}}

	want, marginalM := jointDistribution(toIntMatrix(prr), toIntMatrix(irr), 0)

	// 分布归一化为 1。
	sum := big.NewRat(0, 1)
	for _, p := range want {
		sum.Add(sum, p)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("joint distribution sums to %s, want 1", sum.RatString())
	}

	// 永久符号边缘分布必须等于 PRR 行 0。
	if marginalM[0].Cmp(big.NewRat(3, 4)) != 0 || marginalM[1].Cmp(big.NewRat(1, 4)) != 0 {
		t.Fatalf("permanent marginal = %v, want {0:3/4, 1:1/4}", marginalM)
	}

	// 手算几个格子做 sanity check：
	// P(m=0)=3/4 时 P(y1=y2=0)=(2/3)^2；P(m=1)=1/4 时 =(1/3)^2。
	hand00 := big.NewRat(0, 1).Add(
		new(big.Rat).Mul(big.NewRat(3, 4), big.NewRat(4, 9)),
		new(big.Rat).Mul(big.NewRat(1, 4), big.NewRat(1, 9)),
	)
	if want[[2]int{0, 0}].Cmp(hand00) != 0 {
		t.Fatalf("P(0,0) = %s, want %s", want[[2]int{0, 0}].RatString(), hand00.RatString())
	}

	got := enumerateReporter(t, prr, irr, 0)
	for k, p := range want {
		g, ok := got[k]
		if !ok || g.Cmp(p) != 0 {
			t.Fatalf("cell %v: package output %s != enumeration %s", k, ratString(g), p.RatString())
		}
	}
	if len(got) != len(want) {
		t.Fatalf("joint cell count = %d, want %d", len(got), len(want))
	}

	// 记忆化使两次上报相关：与“两次独立 IRR”的乘积分布必须不同。
	independent00 := new(big.Rat).Mul(marginalY(want, 0, true), marginalY(want, 0, false))
	if want[[2]int{0, 0}].Cmp(independent00) == 0 {
		t.Fatalf("memoized joint P(0,0) unexpectedly equals independent product %s", independent00.RatString())
	}
}

// marginalY 从联合分布求 y1（first=true）或 y2 的边缘概率。
func marginalY(joint map[[2]int]*big.Rat, symbol int, first bool) *big.Rat {
	out := big.NewRat(0, 1)
	for k, p := range joint {
		if (first && k[0] == symbol) || (!first && k[1] == symbol) {
			out.Add(out, p)
		}
	}
	return out
}

func TestJointDistribution3x3(t *testing.T) {
	// 统一分母 2 的 3×3 矩阵，含零权重，验证更大字母表与零概率分支。
	prr := netsec.Matrix{Row: [][]netsec.Rational{
		{{1, 2}, {1, 2}, {0, 1}},
		{{0, 1}, {1, 2}, {1, 2}},
		{{1, 2}, {0, 1}, {1, 2}},
	}}
	irr := netsec.Matrix{Row: [][]netsec.Rational{
		{{1, 2}, {0, 1}, {1, 2}},
		{{1, 2}, {1, 2}, {0, 1}},
		{{0, 1}, {1, 2}, {1, 2}},
	}}

	want, _ := jointDistribution(toIntMatrix(prr), toIntMatrix(irr), 0)
	sum := big.NewRat(0, 1)
	for _, p := range want {
		sum.Add(sum, p)
	}
	if sum.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("3x3 joint distribution sums to %s, want 1", sum.RatString())
	}
	got := enumerateReporter(t, prr, irr, 0)
	for k, p := range want {
		if g, ok := got[k]; !ok || g.Cmp(p) != 0 {
			t.Fatalf("cell %v: package output %s != enumeration %s", k, ratString(g), p.RatString())
		}
	}

	// PRR 行 0 的符号 2 权重为 0，任何经过 m=2 的输出格子概率必须为 0/不存在。
	// IRR 行 0 不可能输出符号 1，因此 (1,*) 只能来自 m=1：该路径概率为 1/2。
	if p := marginalY(got, 1, true); p.Cmp(big.NewRat(1, 4)) != 0 {
		t.Fatalf("P(y1=1) = %s, want 1/4", p.RatString())
	}
}

func ratString(r *big.Rat) string {
	if r == nil {
		return "<missing>"
	}
	return r.RatString()
}
