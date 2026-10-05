package main

import (
	"math"
	"math/rand"
	"testing"
)

func mkSource(id string, amount float64, cats []string, expires string) Source {
	return Source{ID: id, Name: id, Amount: amount, Categories: cats, Expires: expires}
}

func mkPool(id string, amount, ratio, cap float64, cats []string, expires string) Source {
	return Source{ID: id, Name: id, Amount: amount, Categories: cats, Expires: expires,
		Match: &MatchRule{Ratio: ratio, PerProjectCap: cap}}
}

func mkProject(id, cat string, goal, benefit float64, deadline string) Project {
	return Project{ID: id, Name: id, Category: cat, Goal: goal, Benefit: benefit, Deadline: deadline}
}

func solveOrFail(t *testing.T, in *Input) *Result {
	t.Helper()
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	return res
}

func fundedIDs(r *Result) map[string]bool {
	m := map[string]bool{}
	for _, p := range r.Funded {
		m[p.ID] = true
	}
	return m
}

// 整笔达标：资金不足时项目不得部分资助、不计收益。
func TestFullFundingOnly(t *testing.T) {
	in := &Input{
		Sources:  []Source{mkSource("S1", 60, nil, "")},
		Projects: []Project{mkProject("P1", "a", 100, 10, "")},
	}
	res := solveOrFail(t, in)
	if res.Benefit != 0 || len(res.Funded) != 0 {
		t.Fatalf("expected no funding, got benefit=%v funded=%v", res.Benefit, res.Funded)
	}
}

// 配捐不递归：配捐池之间不得相互触发。
func TestMatchNotRecursive(t *testing.T) {
	// 基础资金 50；若允许递归，M1 配 50 后 M2 可按 100 配 -> 总额 200 >= 160 可行。
	// 不允许递归时最多 50+50+50=150 < 160，项目不可达标。
	in := &Input{
		Sources: []Source{
			mkSource("S1", 50, nil, ""),
			mkPool("M1", 1000, 1, 0, nil, ""),
			mkPool("M2", 1000, 1, 0, nil, ""),
		},
		Projects: []Project{mkProject("P1", "a", 160, 10, "")},
	}
	res := solveOrFail(t, in)
	if res.Benefit != 0 {
		t.Fatalf("recursive match should not be allowed, got benefit=%v", res.Benefit)
	}
	// 目标降到 150 则可行：50 基础 + 50 + 50 配捐。
	in.Projects[0].Goal = 150
	res = solveOrFail(t, in)
	if res.Benefit != 10 {
		t.Fatalf("expected benefit 10, got %v", res.Benefit)
	}
	var matchTotal float64
	for _, f := range res.Flows {
		if f.Type == "match" {
			matchTotal += f.Amount
		}
	}
	if math.Abs(matchTotal-100) > 0.01 {
		t.Fatalf("expected match total 100, got %v", matchTotal)
	}
}

// 配捐比例与单项目上限。
func TestMatchRatioAndCap(t *testing.T) {
	in := &Input{
		Sources: []Source{
			mkSource("S1", 100, nil, ""),
			mkPool("M1", 1000, 0.5, 30, nil, ""),
		},
		Projects: []Project{mkProject("P1", "a", 130, 5, "")},
	}
	res := solveOrFail(t, in)
	if res.Benefit != 5 {
		t.Fatalf("expected benefit 5, got %v", res.Benefit)
	}
	for _, f := range res.Flows {
		if f.Type == "match" && f.Amount > 30.001 {
			t.Fatalf("match %v exceeds per-project cap 30", f.Amount)
		}
	}
}

// 有效期约束：来源有效期早于项目截止日则不可投向该项目。
func TestExpiryEligibility(t *testing.T) {
	in := &Input{
		Sources: []Source{
			mkSource("S1", 100, nil, "2026-06-30"),
			mkSource("S2", 100, nil, ""),
		},
		Projects: []Project{
			mkProject("P1", "a", 100, 1, "2026-12-31"), // 只能用 S2
			mkProject("P2", "a", 100, 2, "2026-05-31"), // 两者皆可
		},
	}
	res := solveOrFail(t, in)
	if res.Benefit != 3 {
		t.Fatalf("expected benefit 3, got %v", res.Benefit)
	}
	for _, f := range res.Flows {
		if f.From == "S1" && f.To == "P1" {
			t.Fatalf("S1 expired before P1 deadline but funded it")
		}
	}
}

// 用途限定：类别不匹配不得资助。
func TestCategoryRestriction(t *testing.T) {
	in := &Input{
		Sources: []Source{mkSource("S1", 100, []string{"教育"}, "")},
		Projects: []Project{
			mkProject("P1", "医疗", 100, 10, ""),
			mkProject("P2", "教育", 100, 5, ""),
		},
	}
	res := solveOrFail(t, in)
	if !fundedIDs(res)["P2"] || fundedIDs(res)["P1"] {
		t.Fatalf("category restriction violated: %v", res.Funded)
	}
}

// 项目数上限：默认最多 15 个。
func TestMaxProjectsLimit(t *testing.T) {
	var projects []Project
	for i := 0; i < 20; i++ {
		projects = append(projects, mkProject("P"+string(rune('a'+i)), "a", 10, 1, ""))
	}
	in := &Input{
		Sources:  []Source{mkSource("S1", 10000, nil, "")},
		Projects: projects,
	}
	res := solveOrFail(t, in)
	if len(res.Funded) != 15 {
		t.Fatalf("expected 15 funded, got %d", len(res.Funded))
	}
	if res.Benefit != 15 {
		t.Fatalf("expected benefit 15, got %v", res.Benefit)
	}
}

// 次目标：同等收益下优先使用临期资金，过期闲置最小。
func TestSecondaryObjectivePrefersExpiringFunds(t *testing.T) {
	in := &Input{
		Sources: []Source{
			mkSource("S1", 100, nil, "2026-06-30"), // 临期
			mkSource("S2", 100, nil, ""),           // 长期
		},
		Projects: []Project{mkProject("P1", "a", 100, 1, "2026-05-31")},
	}
	res := solveOrFail(t, in)
	if res.ExpiredIdle != 0 {
		t.Fatalf("expected expired idle 0, got %v", res.ExpiredIdle)
	}
	if len(res.Flows) != 1 || res.Flows[0].From != "S1" {
		t.Fatalf("expected funding from expiring S1, got %+v", res.Flows)
	}
}

// 主目标优先于次目标：不能为减少闲置而牺牲收益。
func TestPrimaryDominatesSecondary(t *testing.T) {
	in := &Input{
		Sources: []Source{
			mkSource("S1", 100, nil, "2026-06-30"),
			mkSource("S2", 100, nil, ""),
		},
		Projects: []Project{
			mkProject("P1", "a", 100, 10, "2026-12-31"), // 只能用 S2（S1 有效期不够）
			mkProject("P2", "a", 100, 1, "2026-05-31"),  // 可用 S1
		},
	}
	res := solveOrFail(t, in)
	if res.Benefit != 11 {
		t.Fatalf("expected benefit 11, got %v", res.Benefit)
	}
}

// 随机小规模对拍：与暴力枚举比较最优收益，并校验方案合法性。
func TestBruteForceCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cats := []string{"教育", "医疗", "环保"}
	dates := []string{"", "2026-06-30", "2026-12-31", "2027-06-30"}
	for trial := 0; trial < 60; trial++ {
		var in Input
		in.MaxProjects = 1 + rng.Intn(4)
		nSrc := 1 + rng.Intn(4)
		for i := 0; i < nSrc; i++ {
			s := mkSource(srcID(i), float64(10+rng.Intn(90)),
				randCats(rng, cats), dates[rng.Intn(len(dates))])
			if rng.Intn(3) == 0 {
				s.Match = &MatchRule{Ratio: float64(rng.Intn(3)) * 0.5,
					PerProjectCap: float64(rng.Intn(4)) * 20}
			}
			in.Sources = append(in.Sources, s)
		}
		nProj := 1 + rng.Intn(7)
		for i := 0; i < nProj; i++ {
			in.Projects = append(in.Projects,
				mkProject(projID(i), cats[rng.Intn(len(cats))],
					float64(10+rng.Intn(60)), float64(rng.Intn(20)),
					dates[rng.Intn(len(dates))]))
		}
		res, err := Solve(&in)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		best := bruteForceBenefit(&in)
		if math.Abs(res.Benefit-best) > 1e-4 {
			t.Fatalf("trial %d: solver=%v brute=%v\ninput=%+v", trial, res.Benefit, best, in)
		}
		checkResultValid(t, &in, res)
	}
}

func srcID(i int) string  { return "S" + string(rune('A'+i)) }
func projID(i int) string { return "P" + string(rune('a'+i)) }

func randCats(rng *rand.Rand, cats []string) []string {
	if rng.Intn(2) == 0 {
		return nil
	}
	return []string{cats[rng.Intn(len(cats))]}
}

// bruteForceBenefit 枚举所有大小不超过上限的项目子集，返回最大可行收益。
func bruteForceBenefit(in *Input) float64 {
	n := len(in.Projects)
	best := 0.0
	var set []int
	var rec func(i int, benefit float64)
	rec = func(i int, benefit float64) {
		if len(set) > 0 && feasibleSet(in, set) && benefit > best {
			best = benefit
		}
		if i == n || len(set) >= in.MaxProjects {
			return
		}
		set = append(set, i)
		rec(i+1, benefit+in.Projects[i].Benefit)
		set = set[:len(set)-1]
		rec(i+1, benefit)
	}
	rec(0, 0)
	return best
}

// checkResultValid 校验求解结果满足全部约束。
func checkResultValid(t *testing.T, in *Input, res *Result) {
	t.Helper()
	if len(res.Funded) > in.MaxProjects {
		t.Fatalf("funded %d projects > max %d", len(res.Funded), in.MaxProjects)
	}
	// 每个入选项目恰好足额
	fundedBy := map[string]float64{}
	baseBy := map[string]float64{}
	usedBy := map[string]float64{}
	for _, f := range res.Flows {
		fundedBy[f.To] += f.Amount
		usedBy[f.From] += f.Amount
		if f.Type == "direct" {
			baseBy[f.To] += f.Amount
		}
	}
	var benefit float64
	for _, p := range res.Funded {
		if math.Abs(fundedBy[p.ID]-p.Goal) > 0.02 {
			t.Fatalf("project %s funded %v != goal %v", p.ID, fundedBy[p.ID], p.Goal)
		}
		benefit += p.Benefit
	}
	if math.Abs(benefit-res.Benefit) > 1e-6 {
		t.Fatalf("benefit mismatch: %v vs %v", benefit, res.Benefit)
	}
	// 来源额度、资格、配捐规则
	srcByID := map[string]*Source{}
	for i := range in.Sources {
		srcByID[in.Sources[i].ID] = &in.Sources[i]
	}
	projByID := map[string]*Project{}
	for i := range in.Projects {
		projByID[in.Projects[i].ID] = &in.Projects[i]
	}
	for id, used := range usedBy {
		if used > srcByID[id].Amount+0.01 {
			t.Fatalf("source %s overdrawn: %v > %v", id, used, srcByID[id].Amount)
		}
	}
	matchByPoolProj := map[[2]string]float64{}
	for _, f := range res.Flows {
		s := srcByID[f.From]
		p := projByID[f.To]
		if !eligible(s, p) {
			t.Fatalf("ineligible flow %s -> %s", f.From, f.To)
		}
		if f.Type == "match" {
			if s.Match == nil {
				t.Fatalf("non-pool source %s issued match", f.From)
			}
			matchByPoolProj[[2]string{f.From, f.To}] += f.Amount
		} else if s.Match != nil {
			t.Fatalf("pool %s issued direct flow", f.From)
		}
	}
	for key, amt := range matchByPoolProj {
		s := srcByID[key[0]]
		base := baseBy[key[1]]
		if amt > s.Match.Ratio*base+0.02 {
			t.Fatalf("match %v > ratio*base (%v * %v) for %s->%s",
				amt, s.Match.Ratio, base, key[0], key[1])
		}
		if s.Match.PerProjectCap > 0 && amt > s.Match.PerProjectCap+0.01 {
			t.Fatalf("match %v exceeds cap %v", amt, s.Match.PerProjectCap)
		}
	}
	// 过期闲置复算
	var idle float64
	for _, f := range res.Idle {
		if f.Expired {
			idle += f.Amount
		}
	}
	if math.Abs(idle-res.ExpiredIdle) > 0.02 {
		t.Fatalf("expired idle mismatch: %v vs %v", idle, res.ExpiredIdle)
	}
}
