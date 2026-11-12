package taskpkg

import (
	"math/big"
	"slices"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rat: " + s)
	}
	return r
}

// dotComma 给出同一字符串集合的两种主流解释：千分位点小数 vs 欧陆逗号小数。
func dotComma(namePrefix string, scaleDot, scaleComma *big.Rat) []Candidate {
	return []Candidate{
		{Name: namePrefix + "/dot-grouped", Decimal: '.', Grouping: GroupComma, UnitScale: scaleDot},
		{Name: namePrefix + "/comma-decimal", Decimal: ',', Grouping: GroupNone, UnitScale: scaleComma},
	}
}

func valueAt(t *testing.T, combo Combo, r, c int) string {
	t.Helper()
	v := combo.Values[r][c]
	if v == nil {
		t.Fatalf("单元 (%d,%d) 为缺失", r, c)
	}
	return v.RatString()
}

// bruteForceOracle 独立重放全组合枚举，交叉核对可行组合、关系满足情况与预算计数。
func bruteForceOracle(t *testing.T, in *Input) []struct {
	picked []int
	mask   uint32
} {
	t.Helper()
	s, err := newSolver(in)
	if err != nil {
		t.Fatalf("newSolver 失败: %v", err)
	}
	var out []struct {
		picked []int
		mask   uint32
	}
	var dfs func(col int, picked []int)
	dfs = func(col int, picked []int) {
		if col == s.cols {
			values := make([][]*big.Rat, s.rows)
			for r := 0; r < s.rows; r++ {
				values[r] = make([]*big.Rat, s.cols)
				for c := 0; c < s.cols; c++ {
					if v := s.parsed[c][picked[c]][r]; v != nil {
						values[r][c] = new(big.Rat).Set(v)
					}
				}
			}
			var mask uint32
			for i, rel := range s.rels {
				if rel.satisfied(values) {
					mask |= 1 << uint(i)
				}
			}
			out = append(out, struct {
				picked []int
				mask   uint32
			}{append([]int(nil), picked...), mask})
			return
		}
		for _, k := range s.viable[col] {
			dfs(col+1, append(picked, k))
		}
	}
	dfs(0, nil)
	return out
}

func assertCrossCheck(t *testing.T, in *Input, rep *Report) {
	t.Helper()
	oracle := bruteForceOracle(t, in)
	var wantFeasible [][]int
	allMask := uint32(1)<<uint(len(in.Relations)) - 1
	for _, o := range oracle {
		if o.mask == allMask {
			wantFeasible = append(wantFeasible, o.picked)
		}
	}
	if len(wantFeasible) != len(rep.Feasible) {
		t.Fatalf("交叉核对：期望 %d 个可行组合，报告给出 %d", len(wantFeasible), len(rep.Feasible))
	}
	for i, combo := range rep.Feasible {
		if !slices.Equal(combo.Picked, wantFeasible[i]) {
			t.Fatalf("第 %d 个组合顺序/内容不一致: %v vs %v", i, combo.Picked, wantFeasible[i])
		}
	}
}

func TestThousandsAmbiguityBothInterpretations(t *testing.T) {
	in := &Input{
		Keys: []string{"k1", "k2"},
		Rows: [][]string{
			{"1,234"},
			{"5,000"},
		},
		Candidates: [][]Candidate{dotComma("c0", nil, nil)},
		Relations:  nil,
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 2 {
		t.Fatalf("期望两个可行解释，得到 status=%s n=%d", rep.Status, len(rep.Feasible))
	}
	if valueAt(t, rep.Feasible[0], 0, 0) != "1234" {
		t.Fatalf("千分位解释应为 1234，得到 %s", rep.Feasible[0].Values[0][0].RatString())
	}
	if valueAt(t, rep.Feasible[1], 0, 0) != "617/500" {
		t.Fatalf("逗号小数解释应为 617/500，得到 %s", rep.Feasible[1].Values[0][0].RatString())
	}
	if len(rep.Classes) != 2 {
		t.Fatalf("两种解释规范值不同，应分属两个等价类，得到 %d", len(rep.Classes))
	}
	if len(rep.Undecidable) != 1 || !rep.Undecidable[0].ValueVaries {
		t.Fatalf("第 0 列应被标记为取值变化的不可判定列，得到 %+v", rep.Undecidable)
	}
	assertCrossCheck(t, in, rep)
}

func TestThousandsAmbiguityDisambiguatedByRelation(t *testing.T) {
	in := &Input{
		Keys: []string{"k1"},
		Rows: [][]string{{"1,234", "1"}},
		Candidates: [][]Candidate{
			{
				{Name: "dot-grouped", Decimal: '.', Grouping: GroupComma},
				{Name: "comma-decimal", Decimal: ',', Grouping: GroupNone},
			},
			{
				{Name: "plain", Decimal: '.', Grouping: GroupNone},
			},
		},
		Relations: []Relation{
			{ID: "R1", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1"), 1: rat("-1")}, RHS: rat("1233"), RHSCol: NoRHSColumn},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 1 {
		t.Fatalf("关系应唯一锁定千分位解释，得到 status=%s n=%d", rep.Status, len(rep.Feasible))
	}
	if rep.Feasible[0].Picked[0] != 0 {
		t.Fatalf("应选择第 0 个候选（千分位），得到 %v", rep.Feasible[0].Picked)
	}
	if len(rep.Undecidable) != 0 {
		t.Fatalf("唯一解释下不应有不可判定列，得到 %+v", rep.Undecidable)
	}
	assertCrossCheck(t, in, rep)
}
