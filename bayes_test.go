package bayes

import (
	"math/big"
	"testing"
)

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

func mustEngine(t *testing.T, prior map[Hypothesis]*big.Rat) *Engine {
	t.Helper()
	e, err := New(prior)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func mustAdd(t *testing.T, e *Engine, id string, lik map[Hypothesis]*big.Rat) {
	t.Helper()
	added, err := e.AddEvidence(id, lik)
	if err != nil {
		t.Fatalf("AddEvidence(%q): %v", id, err)
	}
	if !added {
		t.Fatalf("AddEvidence(%q): 期望新增，返回已存在", id)
	}
}

func assertPost(t *testing.T, e *Engine, want map[Hypothesis]*big.Rat) {
	t.Helper()
	post := e.Posterior()
	for h, w := range want {
		if post[h].Cmp(w) != 0 {
			t.Errorf("后验 P(%s) = %s，期望 %s", h, post[h], w)
		}
	}
	sum := new(big.Rat)
	for _, p := range post {
		sum.Add(sum, p)
	}
	if sum.Cmp(one) != 0 {
		t.Errorf("后验总和 = %s，应为 1", sum)
	}
}

// 先验 + 证据组联合似然 → 精确后验（组内观测不拆分相乘）。
func TestExactPosterior(t *testing.T) {
	e := mustEngine(t, map[Hypothesis]*big.Rat{
		"H1": rat(1, 3), "H2": rat(2, 3),
	})
	// 一组相关观测的联合似然整体给出。
	mustAdd(t, e, "E1", map[Hypothesis]*big.Rat{
		"H1": rat(3, 4), "H2": rat(1, 8),
	})
	// P(H1|E) = (1/3·3/4) / (1/3·3/4 + 2/3·1/8) = (1/4)/(1/4+1/12) = 3/4
	assertPost(t, e, map[Hypothesis]*big.Rat{
		"H1": rat(3, 4), "H2": rat(1, 4),
	})
}

// 重复证据只计一次。
func TestDuplicateEvidenceCountedOnce(t *testing.T) {
	e := mustEngine(t, map[Hypothesis]*big.Rat{
		"H1": rat(1, 2), "H2": rat(1, 2),
	})
	lik := map[Hypothesis]*big.Rat{"H1": rat(1, 5), "H2": rat(4, 5)}
	mustAdd(t, e, "E1", lik)
	want := e.Posterior()

	added, err := e.AddEvidence("E1", lik)
	if err != nil {
		t.Fatalf("重复添加报错: %v", err)
	}
	if added {
		t.Fatal("重复添加应返回 false")
	}
	got := e.Posterior()
	for h := range want {
		if got[h].Cmp(want[h]) != 0 {
			t.Errorf("重复证据改变了后验: P(%s) %s → %s", h, want[h], got[h])
		}
	}
}

// 撤销证据后后验恢复。
func TestRemoveEvidence(t *testing.T) {
	prior := map[Hypothesis]*big.Rat{"H1": rat(1, 2), "H2": rat(1, 2)}
	e := mustEngine(t, prior)
	mustAdd(t, e, "E1", map[Hypothesis]*big.Rat{"H1": rat(1, 3), "H2": rat(2, 3)})
	mustAdd(t, e, "E2", map[Hypothesis]*big.Rat{"H1": rat(1, 7), "H2": rat(6, 7)})

	if !e.RemoveEvidence("E1") {
		t.Fatal("RemoveEvidence(E1) 应返回 true")
	}
	// 只剩 E2: P(H1) = (1/2·1/7)/(1/2·1/7+1/2·6/7) = 1/7
	assertPost(t, e, map[Hypothesis]*big.Rat{"H1": rat(1, 7), "H2": rat(6, 7)})

	if !e.RemoveEvidence("E2") {
		t.Fatal("RemoveEvidence(E2) 应返回 true")
	}
	assertPost(t, e, prior)
	if e.RemoveEvidence("E2") {
		t.Fatal("重复撤销应返回 false")
	}
}

// 零总似然报矛盾，且状态不变。
func TestContradiction(t *testing.T) {
	e := mustEngine(t, map[Hypothesis]*big.Rat{
		"H1": rat(1, 2), "H2": rat(1, 2),
	})
	mustAdd(t, e, "E1", map[Hypothesis]*big.Rat{"H1": rat(0, 1), "H2": rat(1, 1)})
	before := e.Posterior()

	_, err := e.AddEvidence("E2", map[Hypothesis]*big.Rat{"H1": rat(1, 1), "H2": rat(0, 1)})
	if err != ErrContradiction {
		t.Fatalf("期望 ErrContradiction，得到 %v", err)
	}
	if e.HasEvidence("E2") {
		t.Fatal("矛盾证据不应被加入")
	}
	after := e.Posterior()
	for h := range before {
		if before[h].Cmp(after[h]) != 0 {
			t.Errorf("矛盾后状态改变: P(%s) %s → %s", h, before[h], after[h])
		}
	}
}

// 多组证据的后验 == 联合概率枚举 P(h)·∏P(e_i|h) 归一化。
func TestPosteriorMatchesJointEnumeration(t *testing.T) {
	hs := []Hypothesis{"A", "B", "C"}
	prior := map[Hypothesis]*big.Rat{"A": rat(1, 6), "B": rat(1, 3), "C": rat(1, 2)}
	e := mustEngine(t, prior)
	groups := map[string]map[Hypothesis]*big.Rat{
		"E1": {"A": rat(2, 5), "B": rat(3, 5), "C": rat(1, 5)},
		"E2": {"A": rat(7, 9), "B": rat(1, 9), "C": rat(4, 9)},
		"E3": {"A": rat(1, 2), "B": rat(1, 4), "C": rat(1, 3)},
	}
	for id, lik := range groups {
		mustAdd(t, e, id, lik)
	}

	// 独立实现的联合枚举核对。
	joint := make(map[Hypothesis]*big.Rat)
	total := new(big.Rat)
	for _, h := range hs {
		j := new(big.Rat).Set(prior[h])
		for _, lik := range groups {
			j.Mul(j, lik[h])
		}
		joint[h] = j
		total.Add(total, j)
	}
	post := e.Posterior()
	for _, h := range hs {
		want := new(big.Rat).Quo(joint[h], total)
		if post[h].Cmp(want) != 0 {
			t.Errorf("P(%s) = %s，联合枚举得 %s", h, post[h], want)
		}
	}
}

// 候选选择：期望误判率最低者胜出。
func TestBestCandidate(t *testing.T) {
	e := mustEngine(t, map[Hypothesis]*big.Rat{
		"H1": rat(1, 2), "H2": rat(1, 2),
	})
	// C1: 完全区分两个假设 → 期望误判率 0。
	c1 := Candidate{ID: "C1", Outcomes: []Outcome{
		{Prob: map[Hypothesis]*big.Rat{"H1": rat(1, 1), "H2": rat(0, 1)}},
		{Prob: map[Hypothesis]*big.Rat{"H1": rat(0, 1), "H2": rat(1, 1)}},
	}}
	// C2: 无信息 → 期望误判率 1/2。
	c2 := Candidate{ID: "C2", Outcomes: []Outcome{
		{Prob: map[Hypothesis]*big.Rat{"H1": rat(1, 2), "H2": rat(1, 2)}},
		{Prob: map[Hypothesis]*big.Rat{"H1": rat(1, 2), "H2": rat(1, 2)}},
	}}
	best, errRate, err := e.BestCandidate([]Candidate{c2, c1})
	if err != nil {
		t.Fatalf("BestCandidate: %v", err)
	}
	if best.ID != "C1" {
		t.Errorf("期望选中 C1，得到 %s", best.ID)
	}
	if errRate.Sign() != 0 {
		t.Errorf("C1 期望误判率应为 0，得到 %s", errRate)
	}
}

// 平局按编号取小。
func TestBestCandidateTieBreakByID(t *testing.T) {
	e := mustEngine(t, map[Hypothesis]*big.Rat{
		"H1": rat(1, 2), "H2": rat(1, 2),
	})
	mk := func(id string) Candidate {
		return Candidate{ID: id, Outcomes: []Outcome{
			{Prob: map[Hypothesis]*big.Rat{"H1": rat(1, 2), "H2": rat(1, 2)}},
			{Prob: map[Hypothesis]*big.Rat{"H1": rat(1, 2), "H2": rat(1, 2)}},
		}}
	}
	best, _, err := e.BestCandidate([]Candidate{mk("C9"), mk("C3"), mk("C7")})
	if err != nil {
		t.Fatalf("BestCandidate: %v", err)
	}
	if best.ID != "C3" {
		t.Errorf("平局应按编号选 C3，得到 %s", best.ID)
	}
}

// 期望误判率与逐结果归一化的暴力联合枚举一致。
func TestExpectedErrorMatchesBruteForce(t *testing.T) {
	hs := []Hypothesis{"A", "B", "C"}
	prior := map[Hypothesis]*big.Rat{"A": rat(1, 6), "B": rat(2, 6), "C": rat(3, 6)}
	e := mustEngine(t, prior)
	mustAdd(t, e, "E1", map[Hypothesis]*big.Rat{"A": rat(1, 3), "B": rat(2, 3), "C": rat(1, 2)})

	c := Candidate{ID: "Q", Outcomes: []Outcome{
		{Prob: map[Hypothesis]*big.Rat{"A": rat(1, 2), "B": rat(1, 4), "C": rat(1, 8)}},
		{Prob: map[Hypothesis]*big.Rat{"A": rat(1, 4), "B": rat(1, 2), "C": rat(3, 8)}},
		{Prob: map[Hypothesis]*big.Rat{"A": rat(1, 4), "B": rat(1, 4), "C": rat(1, 2)}},
	}}
	got, err := e.ExpectedError(c)
	if err != nil {
		t.Fatalf("ExpectedError: %v", err)
	}

	// 暴力枚举：对每个结果 o，P(h|o) ∝ P(h)P(o|h)，判决取最大者，
	// 误判率 = Σ_o P(o)·(1 − max_h P(h|o))。
	post := e.Posterior()
	want := new(big.Rat)
	for _, o := range c.Outcomes {
		joint := make(map[Hypothesis]*big.Rat)
		po := new(big.Rat)
		for _, h := range hs {
			j := new(big.Rat).Mul(post[h], o.Prob[h])
			joint[h] = j
			po.Add(po, j)
		}
		var bestPost *big.Rat
		for _, h := range hs {
			p := new(big.Rat).Quo(joint[h], po)
			if bestPost == nil || p.Cmp(bestPost) > 0 {
				bestPost = p
			}
		}
		term := new(big.Rat).Sub(one, bestPost)
		want.Add(want, term.Mul(term, po))
	}
	if got.Cmp(want) != 0 {
		t.Errorf("期望误判率 = %s，暴力枚举得 %s", got, want)
	}
}

// 非法输入校验。
func TestValidation(t *testing.T) {
	if _, err := New(map[Hypothesis]*big.Rat{"H1": rat(1, 2)}); err == nil {
		t.Error("先验和不为 1 应报错")
	}
	if _, err := New(map[Hypothesis]*big.Rat{"H1": rat(3, 2), "H2": rat(-1, 2)}); err == nil {
		t.Error("负先验应报错")
	}
	e := mustEngine(t, map[Hypothesis]*big.Rat{"H1": rat(1, 1)})
	if _, err := e.AddEvidence("E1", map[Hypothesis]*big.Rat{}); err == nil {
		t.Error("缺少假设的似然应报错")
	}
	if _, err := e.AddEvidence("E1", map[Hypothesis]*big.Rat{"H1": rat(2, 1)}); err == nil {
		t.Error("似然 > 1 应报错")
	}
	if _, err := e.ExpectedError(Candidate{ID: "Q", Outcomes: []Outcome{
		{Prob: map[Hypothesis]*big.Rat{"H1": rat(1, 1)}},
		{Prob: map[Hypothesis]*big.Rat{"H1": rat(1, 2)}},
	}}); err == nil {
		t.Error("结果概率和不为 1 应报错")
	}
}
