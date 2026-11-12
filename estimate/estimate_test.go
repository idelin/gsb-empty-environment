package estimate

import (
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
)

func rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func mustAdd(t *testing.T, a *Aggregator, run Run) {
	t.Helper()
	if err := a.Add(run); err != nil {
		t.Fatalf("Add(%q): %v", run.ID, err)
	}
}

// TestExhaustiveExpectation 枚举 R 个独立运行的完整入样空间（2^R 种结果），
// 用各结果的精确发生概率（Π p 与 Π (1-p) 之积）加权验证：
//   - 目标签名的加权计数期望等于覆盖它的运行数（每次贡献期望 p·1/p = 1）；
//   - 平方和期望为 Σ 1/p（即维护量 Σ w²I 的期望），而 HT 估计量方差
//     Σ (1-p)/p 与它只相差一个运行数 R。
func TestExhaustiveExpectation(t *testing.T) {
	const R = 5
	probs := []*big.Rat{
		rat(1, 2),
		rat(1, 3),
		rat(2, 3),
		rat(1, 4),
		rat(1, 1),
	}
	const (
		target  Signature = "target"  // 每个运行都覆盖
		partial Signature = "partial" // 仅前 3 个运行覆盖
	)

	outcomes := 1 << R
	sumEstTarget := new(big.Rat)
	sumEstPartial := new(big.Rat)
	sumSqTarget := new(big.Rat)
	totalWeight := new(big.Rat)

	for mask := 0; mask < outcomes; mask++ {
		agg := New()
		direct := new(big.Rat)         // 独立累加 1/p，交叉校验聚合器
		probWeight := big.NewRat(1, 1) // 该入样结果的精确发生概率
		for r := 0; r < R; r++ {
			if mask&(1<<r) == 0 {
				probWeight.Mul(probWeight, new(big.Rat).Sub(big.NewRat(1, 1), probs[r]))
				continue
			}
			probWeight.Mul(probWeight, probs[r])
			sigs := []Signature{target, Signature(fmt.Sprintf("only-%d", r))}
			if r < 3 {
				sigs = append(sigs, partial)
			}
			mustAdd(t, agg, Run{
				ID:          fmt.Sprintf("run-%d", r),
				Probability: probs[r],
				Signatures:  sigs,
			})
			direct.Add(direct, new(big.Rat).Inv(probs[r]))
		}

		if got := agg.Estimate(target); got.Cmp(direct) != 0 {
			t.Fatalf("mask=%0*b: estimate %s != direct sum %s",
				R, mask, got.RatString(), direct.RatString())
		}
		totalWeight.Add(totalWeight, probWeight)
		sumEstTarget.Add(sumEstTarget,
			new(big.Rat).Mul(probWeight, agg.Estimate(target)))
		sumEstPartial.Add(sumEstPartial,
			new(big.Rat).Mul(probWeight, agg.Estimate(partial)))
		sumSqTarget.Add(sumSqTarget,
			new(big.Rat).Mul(probWeight, agg.SumSquared(target)))
	}

	if totalWeight.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("outcome probabilities sum to %s, want 1", totalWeight.RatString())
	}
	meanTarget := sumEstTarget
	meanPartial := sumEstPartial
	meanSquare := sumSqTarget

	if meanTarget.Cmp(big.NewRat(R, 1)) != 0 {
		t.Errorf("E[estimate target] = %s, want %d", meanTarget.RatString(), R)
	}
	if meanPartial.Cmp(big.NewRat(3, 1)) != 0 {
		t.Errorf("E[estimate partial] = %s, want 3", meanPartial.RatString())
	}

	// E[Σ w²I] = Σ 1/p：2 + 3 + 3/2 + 4 + 1 = 23/2。
	wantSquareMean := rat(23, 2)
	if meanSquare.Cmp(wantSquareMean) != 0 {
		t.Errorf("E[sum of squares] = %s, want %s",
			meanSquare.RatString(), wantSquareMean.RatString())
	}

	// HT 估计量方差 E[(ΣwI - R)²] = Σ (1-p)/p = 1 + 2 + 1/2 + 3 + 0 = 13/2。
	wantVariance := rat(13, 2)
	if got := new(big.Rat).Sub(meanSquare, big.NewRat(R, 1)); got.Cmp(wantVariance) != 0 {
		t.Errorf("variance = %s, want %s", got.RatString(), wantVariance.RatString())
	}
}

// TestExhaustiveConfidenceInterval 在 p=1/2、R=6 的完整空间上验证：
// z=2 的正态区间恰好覆盖 k>=1 的全部结果（63/64 的精确覆盖率），
// 并检查区间关于点估计对称。
func TestExhaustiveConfidenceInterval(t *testing.T) {
	const R = 6
	target := Signature("target")
	z := big.NewFloat(2)

	covered := 0
	for mask := 0; mask < 1<<R; mask++ {
		agg := New()
		for r := 0; r < R; r++ {
			if mask&(1<<r) != 0 {
				mustAdd(t, agg, Run{
					ID:          fmt.Sprintf("run-%d", r),
					Probability: rat(1, 2),
					Signatures:  []Signature{target},
				})
			}
		}
		lo, hi, err := agg.ConfidenceInterval(target, z)
		if err != nil {
			t.Fatalf("ConfidenceInterval: %v", err)
		}

		// 区间中心必须等于点估计。
		est := new(big.Float).SetRat(agg.Estimate(target))
		center := new(big.Float).Mul(big.NewFloat(0.5), new(big.Float).Add(lo, hi))
		if center.Cmp(est) != 0 {
			t.Errorf("mask=%0*b: interval center != estimate", R, mask)
		}

		truth := big.NewFloat(R) // 总体中目标签名的真实运行计数
		if lo.Cmp(truth) <= 0 && truth.Cmp(hi) <= 0 {
			covered++
		}
	}

	if covered != 63 {
		t.Errorf("coverage = %d/64, want 63/64", covered)
	}

	if _, _, err := New().ConfidenceInterval(target, nil); err == nil {
		t.Error("nil z: expected error")
	}
	if _, _, err := New().ConfidenceInterval(target, big.NewFloat(-1)); err == nil {
		t.Error("negative z: expected error")
	}

	// 未见过的签名给出 (0, 0)。
	lo, hi, err := New().ConfidenceInterval(Signature("missing"), big.NewFloat(1.96))
	if err != nil || lo.Sign() != 0 || hi.Sign() != 0 {
		t.Errorf("unseen signature interval = (%v, %v), err=%v", lo, hi, err)
	}
}

// TestRetractMatchesRecompute 每撤回一次运行，就与用剩余运行重算的
// 全新聚合器逐签名比对精确计数、平方和与贡献运行数。
func TestRetractMatchesRecompute(t *testing.T) {
	runs := []Run{
		{ID: "a", Probability: rat(1, 2), Signatures: []Signature{"x", "y", "x"}},
		{ID: "b", Probability: rat(1, 3), Signatures: []Signature{"y", "z"}},
		{ID: "c", Probability: rat(2, 5), Signatures: []Signature{"x", "z", "z"}},
		{ID: "d", Probability: rat(3, 4), Signatures: []Signature{"w"}},
		{ID: "e", Probability: rat(1, 7), Signatures: []Signature{"x", "y", "w"}},
	}

	agg := New()
	for _, run := range runs {
		mustAdd(t, agg, run)
	}

	retractOrder := []string{"c", "a", "e", "b", "d"}
	remaining := append([]Run(nil), runs...)

	for _, id := range retractOrder {
		if err := agg.Retract(id); err != nil {
			t.Fatalf("Retract(%q): %v", id, err)
		}
		for i, run := range remaining {
			if run.ID == id {
				remaining = append(remaining[:i], remaining[i+1:]...)
				break
			}
		}

		rebuilt := New()
		for _, run := range remaining {
			mustAdd(t, rebuilt, run)
		}
		assertAggregatorsEqual(t, agg, rebuilt)
	}

	if agg.RunCount() != 0 || len(agg.Signatures()) != 0 {
		t.Errorf("after full retraction: runs=%d sigs=%v, want empty",
			agg.RunCount(), agg.Signatures())
	}
	if agg.Estimate("x").Sign() != 0 || agg.SumSquared("x").Sign() != 0 {
		t.Error("fully retracted estimate/square sum must be zero")
	}

	if err := agg.Retract("ghost"); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("retract unknown: err=%v, want ErrRunNotFound", err)
	}
}

func assertAggregatorsEqual(t *testing.T, got, want *Aggregator) {
	t.Helper()
	gotSigs := got.Signatures()
	wantSigs := want.Signatures()
	if !equalSignatureSet(gotSigs, wantSigs) {
		t.Fatalf("signature sets differ: %v vs %v", gotSigs, wantSigs)
	}
	if got.RunCount() != want.RunCount() {
		t.Fatalf("run count: %d vs %d", got.RunCount(), want.RunCount())
	}
	for _, sig := range wantSigs {
		if got.Estimate(sig).Cmp(want.Estimate(sig)) != 0 {
			t.Errorf("sig %q estimate: %s vs %s",
				sig, got.Estimate(sig), want.Estimate(sig))
		}
		if got.SumSquared(sig).Cmp(want.SumSquared(sig)) != 0 {
			t.Errorf("sig %q square sum: %s vs %s",
				sig, got.SumSquared(sig), want.SumSquared(sig))
		}
		if got.ContributingRuns(sig) != want.ContributingRuns(sig) {
			t.Errorf("sig %q contribs: %d vs %d",
				sig, got.ContributingRuns(sig), want.ContributingRuns(sig))
		}
	}
}

// TestIdempotentAndConflict 验证重复提交幂等、内容冲突拒绝且状态不变。
func TestIdempotentAndConflict(t *testing.T) {
	agg := New()
	original := Run{
		ID:          "run-1",
		Probability: rat(1, 3),
		Signatures:  []Signature{"a", "b", "a"},
	}
	mustAdd(t, agg, original)

	// 同值不同指针、签名乱序重复：视为同一运行，幂等。
	dup := Run{
		ID:          "run-1",
		Probability: rat(2, 6),
		Signatures:  []Signature{"b", "a", "b"},
	}
	if err := agg.Add(dup); err != nil {
		t.Fatalf("idempotent re-add: %v", err)
	}
	if agg.RunCount() != 1 {
		t.Fatalf("run count after duplicate = %d, want 1", agg.RunCount())
	}

	// 每个运行内重复签名只计一次：p=1/3 => 权重恰好为 3。
	if got := agg.Estimate("a"); got.Cmp(rat(3, 1)) != 0 {
		t.Errorf("dedup estimate = %s, want 3", got.RatString())
	}
	if agg.ContributingRuns("a") != 1 {
		t.Errorf("contributing runs = %d, want 1", agg.ContributingRuns("a"))
	}

	beforeCount := agg.RunCount()
	beforeEst := agg.Estimate("a")

	conflictProb := original
	conflictProb.Probability = rat(1, 4)
	if err := agg.Add(conflictProb); !errors.Is(err, ErrRunConflict) {
		t.Errorf("probability conflict: err=%v, want ErrRunConflict", err)
	}

	conflictSigs := original
	conflictSigs.Signatures = []Signature{"a", "c"}
	if err := agg.Add(conflictSigs); !errors.Is(err, ErrRunConflict) {
		t.Errorf("signature conflict: err=%v, want ErrRunConflict", err)
	}

	// 冲突不得改变既有状态。
	if agg.RunCount() != beforeCount || agg.Estimate("a").Cmp(beforeEst) != 0 {
		t.Error("conflicting add mutated aggregator state")
	}
	if !agg.Has("run-1") {
		t.Error("Has: run-1 should be present")
	}
}

func TestInvalidRuns(t *testing.T) {
	valid := []Signature{"s"}
	cases := []Run{
		{ID: "", Probability: rat(1, 2), Signatures: valid},
		{ID: "no-prob", Probability: nil, Signatures: valid},
		{ID: "zero", Probability: rat(0, 1), Signatures: valid},
		{ID: "neg", Probability: rat(-1, 2), Signatures: valid},
		{ID: "gt-one", Probability: rat(3, 2), Signatures: valid},
	}
	for _, run := range cases {
		if err := New().Add(run); !errors.Is(err, ErrInvalidRun) {
			t.Errorf("run %q: err=%v, want ErrInvalidRun", run.ID, err)
		}
	}

	// 无签名的运行是合法的，只是不产生任何贡献。
	agg := New()
	mustAdd(t, agg, Run{ID: "empty", Probability: rat(1, 2)})
	if agg.RunCount() != 1 || len(agg.Signatures()) != 0 {
		t.Error("signature-less run should be recorded without rows")
	}
}

// TestExactRetractionArithmetic 用非平凡有理概率验证撤回后精确归零，
// 不残留浮点误差。
func TestExactRetractionArithmetic(t *testing.T) {
	agg := New()
	runs := []Run{
		{ID: "r1", Probability: rat(3, 7), Signatures: []Signature{"k"}},
		{ID: "r2", Probability: rat(5, 11), Signatures: []Signature{"k"}},
	}
	for _, run := range runs {
		mustAdd(t, agg, run)
	}

	want := new(big.Rat).Add(rat(7, 3), rat(11, 5))
	if got := agg.Estimate("k"); got.Cmp(want) != 0 {
		t.Fatalf("estimate = %s, want %s", got.RatString(), want.RatString())
	}

	wantSquare := new(big.Rat).Add(
		new(big.Rat).Mul(rat(7, 3), rat(7, 3)),
		new(big.Rat).Mul(rat(11, 5), rat(11, 5)),
	)
	if got := agg.SumSquared("k"); got.Cmp(wantSquare) != 0 {
		t.Fatalf("square sum = %s, want %s", got.RatString(), wantSquare.RatString())
	}

	if err := agg.Retract("r1"); err != nil {
		t.Fatalf("retract r1: %v", err)
	}
	if got := agg.Estimate("k"); got.Cmp(rat(11, 5)) != 0 {
		t.Errorf("after r1 retraction estimate = %s, want 11/5", got.RatString())
	}
	if err := agg.Retract("r2"); err != nil {
		t.Fatalf("retract r2: %v", err)
	}
	if agg.Estimate("k").Sign() != 0 || agg.SumSquared("k").Sign() != 0 {
		t.Error("full retraction must leave exact zero")
	}
}

// TestRetractThenReadd 撤回后同一 ID 可以作为新内容重新录入。
func TestRetractThenReadd(t *testing.T) {
	agg := New()
	mustAdd(t, agg, Run{ID: "r", Probability: rat(1, 2), Signatures: []Signature{"s"}})
	if err := agg.Retract("r"); err != nil {
		t.Fatalf("retract: %v", err)
	}
	mustAdd(t, agg, Run{ID: "r", Probability: rat(1, 4), Signatures: []Signature{"t"}})
	if got := agg.Estimate("t"); got.Cmp(rat(4, 1)) != 0 {
		t.Errorf("readd estimate = %s, want 4", got.RatString())
	}
	if agg.ContributingRuns("s") != 0 {
		t.Error("old signature must be gone after retraction")
	}
}

// TestConcurrentAdd 并发提交互不相同的运行 ID，结果与串行汇总一致。
func TestConcurrentAdd(t *testing.T) {
	const n = 64
	var wg sync.WaitGroup
	agg := New()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := agg.Add(Run{
				ID:          fmt.Sprintf("run-%d", i),
				Probability: rat(1, 2),
				Signatures:  []Signature{"shared", Signature(fmt.Sprintf("sig-%d", i%4))},
			})
			if err != nil {
				t.Errorf("concurrent Add(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if agg.RunCount() != n {
		t.Errorf("run count = %d, want %d", agg.RunCount(), n)
	}
	// 每个运行对 shared 贡献权重 2，精确结果为 2n。
	if got := agg.Estimate("shared"); got.Cmp(big.NewRat(2*n, 1)) != 0 {
		t.Errorf("shared estimate = %s, want %d", got.RatString(), 2*n)
	}
	if agg.ContributingRuns("shared") != n {
		t.Errorf("shared contribs = %d, want %d", agg.ContributingRuns("shared"), n)
	}
}
