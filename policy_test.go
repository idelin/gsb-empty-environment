package scheduler

import (
	"math/rand"
	"testing"
)

// 适应性示例：阶段一运行 X 观测收益以区分情景，
// 阶段二据此决定是否运行高方差的 B。
func adaptiveProblem() TwoStageProblem {
	return TwoStageProblem{
		Cooldown: 0,
		Candidates: []Experiment{
			{ID: 1, Device: 0, Slot: 0}, // X
			{ID: 2, Device: 1, Slot: 0}, // B
		},
		Scenarios: []Scenario{
			{ID: 1, Profits: []int{5, 100}},
			{ID: 2, Profits: []int{6, -100}},
		},
	}
}

func TestTwoStageAdaptiveBeatsStatic(t *testing.T) {
	pol := SolveTwoStage(adaptiveProblem())
	if err := VerifyPolicy(adaptiveProblem(), pol); err != nil {
		t.Fatalf("策略回放验证失败: %v", err)
	}
	if pol.Value != 6 {
		t.Fatalf("适应性策略最坏收益应为 6, 得到 %d (%+v)", pol.Value, pol)
	}
	if len(pol.Stage1) != 1 || pol.Stage1[0] != 0 {
		t.Fatalf("阶段一应选 X, 得到 %v", pol.Stage1)
	}
	// 观测 5（情景1）应运行 B，观测 6（情景2）应跳过 B。
	if got := pol.Stage2["5"]; len(got) != 1 || got[0] != 1 {
		t.Fatalf("观测 5 应选 B, 得到 %v", got)
	}
	if got := pol.Stage2["6"]; len(got) != 0 {
		t.Fatalf("观测 6 应选空, 得到 %v", got)
	}
	// 任何不观测的静态策略最坏收益至多为 5（只跑 X）。
}

func TestVerifyRejectsMissingBranch(t *testing.T) {
	p := adaptiveProblem()
	pol := SolveTwoStage(p)
	delete(pol.Stage2, "6") // 删掉一个分支：情景 2 无法回放
	if err := VerifyPolicy(p, pol); err == nil {
		t.Fatal("缺少分支的策略应被拒绝")
	}
}

func TestVerifyRejectsCrossStageViolation(t *testing.T) {
	p := TwoStageProblem{
		Cooldown: 5,
		Candidates: []Experiment{
			{ID: 1, Device: 0, Slot: 0},
			{ID: 2, Device: 0, Slot: 1}, // 与阶段一冲突（冷却 5）
		},
		Scenarios: []Scenario{{ID: 1, Profits: []int{1, 1}}},
	}
	pol := Policy{
		Stage1: []int{0},
		Stage2: map[string][]int{"1": {1}},
		Value:  2,
	}
	if err := VerifyPolicy(p, pol); err == nil {
		t.Fatal("跨阶段冷却违例应被拒绝")
	}
}

func TestVerifyRejectsValueMismatch(t *testing.T) {
	p := adaptiveProblem()
	pol := SolveTwoStage(p)
	pol.Value++
	if err := VerifyPolicy(p, pol); err == nil {
		t.Fatal("声明值与回放不一致应被拒绝")
	}
}

// oracleTwoStage 独立预言机：枚举每个情景各自的阶段二决策表，
// 过滤掉观测相同却决策不同（预知未来）的表，取最坏收益最大。
// 仅适用于极小规模，与 SolveTwoStage 的代码路径完全不同。
func oracleTwoStage(p TwoStageProblem) int {
	n := len(p.Candidates)
	ns := len(p.Scenarios)
	nmask := 1 << uint(n)
	legal := func(mask int) bool {
		return LegalSubset(p.Candidates, p.Cooldown, maskIndices(mask, n))
	}
	profit := func(s, mask int) int {
		sum := 0
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				sum += p.Scenarios[s].Profits[i]
			}
		}
		return sum
	}
	best := 0 // 全空策略收益 0
	for m1 := 0; m1 < nmask; m1++ {
		if !legal(m1) {
			continue
		}
		s1 := maskIndices(m1, n)
		keys := make([]string, ns)
		for s := 0; s < ns; s++ {
			keys[s] = obsKey(p, s, s1)
		}
		// 枚举每个情景的阶段二掩码组合（决策表）。
		table := make([]int, ns)
		var rec func(s int)
		rec = func(s int) {
			if s == ns {
				// 非预知性：观测相同则决策必须相同。
				for a := 0; a < ns; a++ {
					for b := a + 1; b < ns; b++ {
						if keys[a] == keys[b] && table[a] != table[b] {
							return
						}
					}
				}
				worst := 0
				for sc := 0; sc < ns; sc++ {
					v := profit(sc, m1) + profit(sc, table[sc])
					if sc == 0 || v < worst {
						worst = v
					}
				}
				if worst > best {
					best = worst
				}
				return
			}
			for m2 := 0; m2 < nmask; m2++ {
				if m2&m1 == 0 && legal(m1|m2) {
					table[s] = m2
					rec(s + 1)
				}
			}
		}
		rec(0)
	}
	return best
}

func TestTwoStageCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for trial := 0; trial < 60; trial++ {
		n := 1 + rng.Intn(4)
		ns := 1 + rng.Intn(3)
		p := TwoStageProblem{Cooldown: rng.Intn(3)}
		for i := 0; i < n; i++ {
			g := 0
			if rng.Intn(3) == 0 {
				g = 1 + rng.Intn(2)
			}
			p.Candidates = append(p.Candidates, Experiment{
				ID:     10 + i,
				Device: rng.Intn(2),
				Slot:   rng.Intn(4),
				Group:  g,
			})
		}
		for s := 0; s < ns; s++ {
			sc := Scenario{ID: s + 1, Profits: make([]int, n)}
			for i := range sc.Profits {
				sc.Profits[i] = rng.Intn(14) - 5
			}
			p.Scenarios = append(p.Scenarios, sc)
		}
		pol := SolveTwoStage(p)
		if err := VerifyPolicy(p, pol); err != nil {
			t.Fatalf("trial %d: 策略验证失败: %v\n问题: %+v", trial, err, p)
		}
		if want := oracleTwoStage(p); pol.Value != want {
			t.Fatalf("trial %d: 求解值 %d != 预言机 %d\n问题: %+v",
				trial, pol.Value, want, p)
		}
	}
}
