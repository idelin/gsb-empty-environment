package main

import (
	"strings"
	"testing"
)

func solveOrFail(t *testing.T, in *Input) *Plan {
	t.Helper()
	p, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve 出错: %v", err)
	}
	return p
}

func resultOf(p *Plan, id string) CriterionResult {
	for _, r := range p.Results {
		if r.ID == id {
			return r
		}
	}
	return CriterionResult{}
}

func selectedIDs(p *Plan) map[string]bool {
	m := map[string]bool{}
	for _, ev := range p.Selected {
		m[ev.ID] = true
	}
	return m
}

// OR 是“任一满足”：只买最便宜的一个即可，不得要求全部满足。
func TestOrMeansAnyNotAll(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf: "2026-10-05",
		Evidence: []Evidence{
			{ID: "E1", Cost: 100},
			{ID: "E2", Cost: 10},
		},
		Criteria: []Criterion{{ID: "C1", Expr: "E1 OR E2"}},
	})
	if !p.Feasible || p.TotalCost != 10 {
		t.Fatalf("OR 应只选最便宜的 E2(10)，得到 %+v", p)
	}
	if !selectedIDs(p)["E2"] || selectedIDs(p)["E1"] {
		t.Fatalf("应只采集 E2，实际 %+v", p.Selected)
	}
	if !resultOf(p, "C1").Satisfied {
		t.Fatal("C1 应满足")
	}
}

// AND 是“全部满足”：缺一个都不行。
func TestAndMeansAllNotAny(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf: "2026-10-05",
		Evidence: []Evidence{
			{ID: "E1", Cost: 10},
			{ID: "E2", Cost: 20},
		},
		Criteria: []Criterion{{ID: "C1", Expr: "E1 AND E2"}},
	})
	if !p.Feasible || p.TotalCost != 30 {
		t.Fatalf("AND 必须两者都选，成本 30，得到 %+v", p)
	}
	sel := selectedIDs(p)
	if !sel["E1"] || !sel["E2"] {
		t.Fatalf("AND 需全部采集，实际 %+v", p.Selected)
	}
}

// 过期证据不计入：表达式引用过期证据时不可满足，并给出过期原因。
func TestExpiredEvidenceExcluded(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf: "2026-10-05",
		Evidence: []Evidence{
			{ID: "E1", Cost: 5, ValidUntil: "2026-10-01"}, // 已过期
			{ID: "E2", Cost: 50},
		},
		Criteria: []Criterion{{ID: "C1", Expr: "E1 AND E2"}},
	})
	if p.Feasible {
		t.Fatalf("E1 已过期，计划应不可行: %+v", p)
	}
	r := resultOf(p, "C1")
	if r.Satisfied || !strings.Contains(r.Reason, "已过期") {
		t.Fatalf("未满足原因应说明 E1 已过期，实际: %q", r.Reason)
	}
	if len(p.Expired) != 1 || p.Expired[0].ID != "E1" {
		t.Fatalf("过期清单应含 E1: %+v", p.Expired)
	}
}

// 有效期含当日：基准日当天仍有效。
func TestValidUntilInclusive(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf:     "2026-10-05",
		Evidence: []Evidence{{ID: "E1", Cost: 5, ValidUntil: "2026-10-05"}},
		Criteria: []Criterion{{ID: "C1", Expr: "E1"}},
	})
	if !p.Feasible {
		t.Fatal("有效期含基准日当天，E1 应可用")
	}
}

// 互斥证据不能同选，求解器须绕开冲突选替代组合。
func TestMutexExclusion(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf: "2026-10-05",
		Evidence: []Evidence{
			{ID: "E1", Cost: 10, MutexGroup: "g"},
			{ID: "E2", Cost: 12, MutexGroup: "g"}, // 与 E1 互斥
			{ID: "E3", Cost: 30},
		},
		Criteria: []Criterion{
			{ID: "C1", Expr: "E1 OR E2"},
			{ID: "C2", Expr: "E2 OR E3"},
		},
	})
	// E2 同时满足两条但成本 12；E1+E3=40。最低成本是只选 E2。
	if !p.Feasible || p.TotalCost != 12 {
		t.Fatalf("最低成本应为 12（仅 E2），得到 cost=%d %+v", p.TotalCost, p.Selected)
	}
	sel := selectedIDs(p)
	if sel["E1"] && sel["E2"] {
		t.Fatal("互斥证据被同时选中")
	}
}

// 互斥导致不可行时，报告组合约束原因。
func TestMutexInfeasible(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf: "2026-10-05",
		Evidence: []Evidence{
			{ID: "E1", Cost: 10, MutexGroup: "g"},
			{ID: "E2", Cost: 20, MutexGroup: "g"},
		},
		Criteria: []Criterion{{ID: "C1", Expr: "E1 AND E2"}},
	})
	if p.Feasible {
		t.Fatal("E1、E2 互斥却需同时满足，应不可行")
	}
	r := resultOf(p, "C1")
	if !strings.Contains(r.Reason, "互斥") && !strings.Contains(p.Note, "互斥") {
		t.Fatalf("应说明互斥约束，reason=%q note=%q", r.Reason, p.Note)
	}
}

// 数量上限：超过 MaxItems 的组合不可行。
func TestMaxItemsLimit(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf:     "2026-10-05",
		MaxItems: 1,
		Evidence: []Evidence{
			{ID: "E1", Cost: 1},
			{ID: "E2", Cost: 1},
		},
		Criteria: []Criterion{{ID: "C1", Expr: "E1 AND E2"}},
	})
	if p.Feasible {
		t.Fatal("上限 1 项时 E1 AND E2 应不可行")
	}
}

// 满足路径应体现 AND 全部分支、OR 所选分支。
func TestSatisfactionPath(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf: "2026-10-05",
		Evidence: []Evidence{
			{ID: "E1", Cost: 5},
			{ID: "E2", Cost: 7},
			{ID: "E3", Cost: 9},
		},
		Criteria: []Criterion{{ID: "C1", Expr: "E1 AND (E2 OR E3)"}},
	})
	r := resultOf(p, "C1")
	if !r.Satisfied {
		t.Fatalf("C1 应满足: %q", r.Reason)
	}
	if !strings.Contains(r.Path, "E1") || !strings.Contains(r.Path, "E2") {
		t.Fatalf("路径应含 E1 与所选分支 E2: %q", r.Path)
	}
	if strings.Contains(r.Path, "E3") {
		t.Fatalf("OR 未选分支 E3 不应出现在路径中: %q", r.Path)
	}
	if !strings.Contains(r.Path, "AND") {
		t.Fatalf("AND 路径应体现全部满足语义: %q", r.Path)
	}
}

// 共享证据降低总成本：两条验收复用同一证据。
func TestSharedEvidence(t *testing.T) {
	p := solveOrFail(t, &Input{
		AsOf: "2026-10-05",
		Evidence: []Evidence{
			{ID: "E1", Cost: 40},
			{ID: "E2", Cost: 25},
			{ID: "E3", Cost: 25},
		},
		Criteria: []Criterion{
			{ID: "C1", Expr: "E1 OR E2"},
			{ID: "C2", Expr: "E1 OR E3"},
		},
	})
	// 共享 E1(40) 优于 E2+E3(50)。
	if !p.Feasible || p.TotalCost != 40 || len(p.Selected) != 1 {
		t.Fatalf("应共享 E1，成本 40: %+v", p)
	}
}

// 引用不存在的证据应报错；引用过期证据不报错但恒不满足。
func TestUnknownEvidence(t *testing.T) {
	_, err := Solve(&Input{
		AsOf:     "2026-10-05",
		Criteria: []Criterion{{ID: "C1", Expr: "E9"}},
		Evidence: []Evidence{{ID: "E1", Cost: 1}},
	})
	if err == nil || !strings.Contains(err.Error(), "E9") {
		t.Fatalf("应报告未知证据 E9: %v", err)
	}
}

// 25 项证据规模下求解应在合理时间内完成。
func TestTwentyFiveItems(t *testing.T) {
	in := &Input{AsOf: "2026-10-05"}
	var andParts []string
	for i := 0; i < 25; i++ {
		id := strings.Repeat("E", 1) + string(rune('A'+i))
		in.Evidence = append(in.Evidence, Evidence{ID: id, Cost: int64(i + 1)})
		andParts = append(andParts, id)
	}
	in.Criteria = []Criterion{{ID: "C1", Expr: strings.Join(andParts, " AND ")}}
	p := solveOrFail(t, in)
	if !p.Feasible || len(p.Selected) != 25 {
		t.Fatalf("25 项 AND 应全部选中: cost=%d n=%d", p.TotalCost, len(p.Selected))
	}
	if p.TotalCost != 325 {
		t.Fatalf("总成本应为 325，实际 %d", p.TotalCost)
	}
}
