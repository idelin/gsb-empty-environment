package solver

import (
	"fmt"
	"strings"
	"testing"
)

func solve(t *testing.T, in *Input) *Report {
	t.Helper()
	if in.AsOf == "" {
		in.AsOf = "2026-01-01"
	}
	if in.MaxProjects <= 0 {
		in.MaxProjects = DefaultMaxProjects
	}
	return Solve(in)
}

func directTo(rep *Report, projID string) Cents {
	var sum Cents
	for _, f := range rep.Flows {
		if f.Kind == "direct" && f.To == projID {
			sum += f.Amount
		}
	}
	return sum
}

func matchedTo(rep *Report, projID string) Cents {
	var sum Cents
	for _, f := range rep.Flows {
		if f.Kind == "match" && f.To == projID {
			sum += f.Amount
		}
	}
	return sum
}

// 项目只有整笔达标才计收益：资金不足时不得部分计收益。
func TestFullTargetRequired(t *testing.T) {
	rep := solve(t, &Input{
		Sources:  []Source{{ID: "S1", Amount: 60_00}},
		Projects: []Project{{ID: "P1", Target: 100_00, Benefit: 10_00}},
	})
	if len(rep.FundedProjects) != 0 || rep.TotalBenefit != 0 {
		t.Fatalf("未达标项目不应计收益: %+v", rep)
	}
	if len(rep.Flows) != 0 {
		t.Fatalf("未达标项目不应有资金流向: %+v", rep.Flows)
	}
}

// 来源限定用途必须严格执行。
func TestPurposeRestriction(t *testing.T) {
	rep := solve(t, &Input{
		Sources: []Source{{ID: "S1", Amount: 100_00, Purpose: "教育"}},
		Projects: []Project{
			{ID: "P_HEALTH", Purpose: "医疗", Target: 50_00, Benefit: 10_00},
			{ID: "P_EDU", Purpose: "教育", Target: 50_00, Benefit: 8_00},
		},
	})
	if len(rep.FundedProjects) != 1 || rep.FundedProjects[0] != "P_EDU" {
		t.Fatalf("限定用途来源只能投同用途项目: %v", rep.FundedProjects)
	}
	for _, f := range rep.Flows {
		if f.To == "P_HEALTH" {
			t.Fatalf("教育专款流向了医疗项目: %+v", f)
		}
	}
}

// 配捐按规则触发，且配捐资金不能再次触发配捐。
func TestMatchingAndNoCascade(t *testing.T) {
	rep := solve(t, &Input{
		Sources:  []Source{{ID: "S1", Amount: 100_00, Purpose: "教育"}},
		Projects: []Project{{ID: "P1", Purpose: "教育", Target: 130_00, Benefit: 5_00}},
		MatchRules: []MatchRule{
			{ID: "M1", Funder: "基金会A", SourceID: "S1", Purpose: "教育", Ratio: 1.0, Cap: 30_00},
		},
	})
	if len(rep.FundedProjects) != 1 {
		t.Fatalf("配捐后应整笔达标: %+v", rep)
	}
	if got := directTo(rep, "P1"); got != 100_00 {
		t.Fatalf("直接拨款应为 100.00，得到 %s", FormatMoney(got))
	}
	if got := matchedTo(rep, "P1"); got != 30_00 {
		t.Fatalf("配捐应封顶在 30.00，得到 %s", FormatMoney(got))
	}
	matchFlows := 0
	for _, f := range rep.Flows {
		if f.Kind == "match" {
			matchFlows++
			if f.From != "基金会A" {
				t.Fatalf("配捐资金应来自出资方: %+v", f)
			}
			if !strings.Contains(f.Reason, "M1") {
				t.Fatalf("配捐流向缺少触发依据: %+v", f)
			}
		}
	}
	if matchFlows != 1 {
		t.Fatalf("配捐不能再次触发配捐，应只有 1 笔配捐流向，实际 %d 笔", matchFlows)
	}
}

// 配捐额度用尽后，剩余缺口用本金补足。
func TestMatchCapExhausted(t *testing.T) {
	rep := solve(t, &Input{
		Sources:  []Source{{ID: "S1", Amount: 200_00}},
		Projects: []Project{{ID: "P1", Target: 150_00, Benefit: 5_00}},
		MatchRules: []MatchRule{
			{ID: "M1", Funder: "F", Ratio: 0.5, Cap: 20_00},
		},
	})
	if len(rep.FundedProjects) != 1 {
		t.Fatalf("应达标: %+v", rep)
	}
	if got := matchedTo(rep, "P1"); got != 20_00 {
		t.Fatalf("配捐应为 20.00，得到 %s", FormatMoney(got))
	}
	if got := directTo(rep, "P1"); got != 130_00 {
		t.Fatalf("本金应为 130.00（110 触发 20 配捐 + 40 补足），得到 %s", FormatMoney(got))
	}
	if d, m := directTo(rep, "P1"), matchedTo(rep, "P1"); d+m != 150_00 {
		t.Fatalf("到账应恰好等于目标额，不得超额: %s + %s", FormatMoney(d), FormatMoney(m))
	}
}

// 最多资助十五个项目，且优先高收益。
func TestMaxProjects(t *testing.T) {
	var projects []Project
	for i := 1; i <= 20; i++ {
		projects = append(projects, Project{
			ID: fmt.Sprintf("P%02d", i), Target: 10_00, Benefit: Cents(i * 100),
		})
	}
	rep := solve(t, &Input{
		Sources:  []Source{{ID: "S1", Amount: 1_000_000_00}},
		Projects: projects,
	})
	if len(rep.FundedProjects) != 15 {
		t.Fatalf("资助项目数应为 15，实际 %d", len(rep.FundedProjects))
	}
	var want Cents
	for i := 6; i <= 20; i++ { // 收益最高的 15 个
		want += Cents(i * 100)
	}
	if rep.TotalBenefit != want {
		t.Fatalf("总收益应为 %s，得到 %s", FormatMoney(want), FormatMoney(rep.TotalBenefit))
	}
}

// 已过期的来源不可用，且全额计入过期闲置。
func TestExpiredSourceUnusable(t *testing.T) {
	rep := solve(t, &Input{
		AsOf:     "2026-06-01",
		Sources:  []Source{{ID: "S1", Amount: 100_00, Expires: "2026-01-01"}},
		Projects: []Project{{ID: "P1", Target: 50_00, Benefit: 10_00}},
	})
	if len(rep.FundedProjects) != 0 {
		t.Fatalf("已过期来源不得使用: %+v", rep)
	}
	if rep.ExpiredIdle != 100_00 {
		t.Fatalf("过期闲置应为 100.00，得到 %s", FormatMoney(rep.ExpiredIdle))
	}
}

// 次目标：同等收益下优先动用有有效期的资金，使过期闲置最少。
func TestSecondaryObjectiveMinExpiredIdle(t *testing.T) {
	rep := solve(t, &Input{
		AsOf: "2026-01-01",
		Sources: []Source{
			{ID: "S_EXP", Amount: 60_00, Expires: "2026-12-31"},
			{ID: "S_PERP", Amount: 100_00},
		},
		Projects: []Project{{ID: "P1", Target: 100_00, Benefit: 10_00}},
	})
	if len(rep.FundedProjects) != 1 {
		t.Fatalf("应达标: %+v", rep)
	}
	if rep.ExpiredIdle != 0 {
		t.Fatalf("应优先用尽临期资金使过期闲置为 0，得到 %s", FormatMoney(rep.ExpiredIdle))
	}
	usedExp := false
	for _, f := range rep.Flows {
		if f.Kind == "direct" && f.From == "S_EXP" {
			usedExp = true
		}
	}
	if !usedExp {
		t.Fatalf("应优先动用临期来源 S_EXP: %+v", rep.Flows)
	}
}

// 配捐规则的来源与用途过滤。
func TestMatchRuleFilters(t *testing.T) {
	rep := solve(t, &Input{
		Sources: []Source{
			{ID: "S1", Amount: 100_00, Purpose: "教育"},
			{ID: "S2", Amount: 100_00, Purpose: "医疗"},
		},
		Projects: []Project{
			{ID: "P_EDU", Purpose: "教育", Target: 150_00, Benefit: 9_00},
			{ID: "P_MED", Purpose: "医疗", Target: 100_00, Benefit: 9_00},
		},
		MatchRules: []MatchRule{
			{ID: "M1", Funder: "F", SourceID: "S1", Purpose: "教育", Ratio: 0.5, Cap: 100_00},
		},
	})
	if len(rep.FundedProjects) != 2 {
		t.Fatalf("两项目都应达标: %+v", rep)
	}
	if got := matchedTo(rep, "P_MED"); got != 0 {
		t.Fatalf("医疗项目不应触发教育配捐: %s", FormatMoney(got))
	}
	if got := matchedTo(rep, "P_EDU"); got != 50_00 {
		t.Fatalf("教育项目应获配捐 50.00，得到 %s", FormatMoney(got))
	}
	if got := directTo(rep, "P_EDU"); got != 100_00 {
		t.Fatalf("教育项目本金应为 100.00，得到 %s", FormatMoney(got))
	}
}

// 主目标优先：配捐杠杆应帮助撬动更多项目达标。
func TestMatchLeveragesMoreProjects(t *testing.T) {
	rep := solve(t, &Input{
		Sources: []Source{{ID: "S1", Amount: 150_00}},
		Projects: []Project{
			{ID: "P1", Target: 100_00, Benefit: 10_00},
			{ID: "P2", Target: 100_00, Benefit: 10_00},
		},
		MatchRules: []MatchRule{
			{ID: "M1", Funder: "F", Ratio: 1.0, Cap: 100_00},
		},
	})
	// 150 本金触发 100 配捐，共 250，可整笔达标两个项目。
	if len(rep.FundedProjects) != 2 || rep.TotalBenefit != 20_00 {
		t.Fatalf("配捐杠杆应使两项目都达标: 收益 %s, 达标 %v",
			FormatMoney(rep.TotalBenefit), rep.FundedProjects)
	}
}

// 输入校验。
func TestParseValidation(t *testing.T) {
	if _, err := ParseInput([]byte(`{"sources":[{"id":"S1","amount":-5}]}`)); err == nil {
		t.Fatal("负金额应报错")
	}
	if _, err := ParseInput([]byte(`{"sources":[{"id":"S1","amount":1,"expires":"2026/01/01"}]}`)); err == nil {
		t.Fatal("非法日期应报错")
	}
	in, err := ParseInput([]byte(`{"projects":[{"id":"P1","target":100,"benefit":5}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if in.MaxProjects != DefaultMaxProjects {
		t.Fatalf("缺省项目上限应为 %d，得到 %d", DefaultMaxProjects, in.MaxProjects)
	}
	if in.AsOf == "" {
		t.Fatal("as_of 缺省应取当天")
	}
}
