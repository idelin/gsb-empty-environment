package taskpkg

import (
	"math/big"
	"strings"
	"testing"
)

func TestMUSLeastIDsTiebreakAndWitness(t *testing.T) {
	// 两列各两个候选，全局组合取值 c0∈{1,1/2}、c1∈{2,1}。
	// r-a1/r-a2 分别要求 c0=1、c0=1/2，任一单条都能被某个候选满足，
	// 合起来构成 2 条 MUS；r-b1 是所有组合都满足的伴随关系，
	// 用来验证删除 MUS 关系后能得到满足其余全部关系的全局证人。
	in := &Input{
		Keys: []string{"row"},
		Rows: [][]string{{"1", "2"}},
		Candidates: [][]Candidate{
			{
				{Name: "c0-one", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
				{Name: "c0-half", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1/2")},
			},
			{
				{Name: "c1-keep", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
				{Name: "c1-keep2", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
			},
		},
		Relations: []Relation{
			// c1 恒为 2，该伴随关系在全部组合下成立，不参与冲突。
			{ID: "r-b1", KeyCol: -1, Coeffs: map[int]*big.Rat{1: rat("1")}, RHS: rat("2"), RHSCol: NoRHSColumn},
			{ID: "r-a2", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1")}, RHS: rat("1/2"), RHSCol: NoRHSColumn},
			{ID: "r-a1", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1")}, RHS: rat("1"), RHSCol: NoRHSColumn},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusInfeasible {
		t.Fatalf("期望 infeasible，得到 %s", rep.Status)
	}
	got := rep.Conflicts[0].RelationIDs
	want := []string{"r-a1", "r-a2"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("MUS 决胜错误，期望 %v，得到 %v", want, got)
	}
	for _, w := range rep.Conflicts[0].Witnesses {
		if w.Combo == nil || w.ConflictOnly {
			t.Fatalf("删除 MUS 内关系后应给出全局可行解释: %+v", w)
		}
	}
}

func TestWitnessConflictOnlyFallback(t *testing.T) {
	// c0 两候选（值 1 或 10），c1、c2、c3 单候选。
	// R1/R2 仅在第一行活跃（引用 c1），各自能被某个 c0 候选满足，合起来构成 2 条 MUS。
	// R3 仅在第二行活跃（引用 c2）并要求 c0=10；R4 仅在第三行活跃（引用 c3）并要求 c0=1。
	// R3、R4 自身都可满足，因此都不是单条 MUS；但：
	//   - 删除 R1 后要满足 R2(c0=1) 就违反 R3(c0=10)；
	//   - 删除 R2 后要满足 R1(c0=10) 就违反 R4(c0=1)。
	// 两个删除方向都没有全局证人，只能回退为 ConflictOnly 的局部证人。
	in := &Input{
		Keys: []string{"row-1", "row-2", "row-3"},
		Rows: [][]string{
			{"1", "1", "", ""},
			{"1", "", "1", ""},
			{"1", "", "", "1"},
		},
		Candidates: [][]Candidate{{
			{Name: "one", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
			{Name: "ten", Decimal: '.', Grouping: GroupNone, UnitScale: rat("10")},
		}, {
			{Name: "c1", Decimal: '.', Grouping: GroupNone},
		}, {
			{Name: "c2", Decimal: '.', Grouping: GroupNone},
		}, {
			{Name: "c3", Decimal: '.', Grouping: GroupNone},
		}},
		Relations: []Relation{
			// R3 只在第二行活跃：c0 - c2 == 9，要求第二行 c0=10。
			{ID: "R3-row2", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1"), 2: rat("-1")}, RHS: rat("9"), RHSCol: NoRHSColumn},
			// R4 只在第三行活跃：c0 == c3，要求第三行 c0=1。
			{ID: "R4-row3", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1"), 3: rat("-1")}, RHS: rat("0"), RHSCol: NoRHSColumn},
			{ID: "R2-eq1", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1"), 1: rat("-1")}, RHS: rat("0"), RHSCol: NoRHSColumn},
			{ID: "R1-eq10", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1"), 1: rat("-1")}, RHS: rat("9"), RHSCol: NoRHSColumn},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusInfeasible {
		t.Fatalf("期望 infeasible，得到 %s", rep.Status)
	}
	got := rep.Conflicts[0].RelationIDs
	want := []string{"R1-eq10", "R2-eq1"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("期望 MUS 为 %v，得到 %v", want, got)
	}
	for _, w := range rep.Conflicts[0].Witnesses {
		if !w.ConflictOnly || w.Combo == nil {
			t.Fatalf("R3 阻断全局松弛，证人应为 ConflictOnly，得到 %+v", w)
		}
	}
}

func TestBudgetExhaustionIsIncomplete(t *testing.T) {
	// 两列各 4 候选 => 16 个组合；预算只允许检查 5 个。
	// 让 4 个候选都能解析同一简单数字。
	simple := []Candidate{
		{Name: "a", Decimal: '.', Grouping: GroupNone},
		{Name: "b", Decimal: ',', Grouping: GroupNone},
		{Name: "c", Decimal: '.', Grouping: GroupComma},
		{Name: "d", Decimal: ',', Grouping: GroupDot},
	}
	in := &Input{
		Keys:       []string{"r1"},
		Rows:       [][]string{{"42", "7"}},
		Candidates: [][]Candidate{simple, simple},
		Relations:  nil,
		Budget:     5,
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusIncomplete {
		t.Fatalf("预算耗尽应为 incomplete，得到 %s", rep.Status)
	}
	if rep.BudgetUsed != 6 {
		t.Fatalf("期望在第 6 次检查时耗尽（used=6），得到 %d", rep.BudgetUsed)
	}
	if len(rep.Feasible) != 5 {
		t.Fatalf("中断前应保留 5 个部分结果，得到 %d", len(rep.Feasible))
	}
	if strings.TrimSpace(rep.IncompleteReason) == "" {
		t.Fatal("IncompleteReason 不应为空")
	}

	// 预算充足时必须完成全枚举，不能提前宣称唯一。
	cp := *in
	cp.Budget = 0
	full, err := Solve(&cp)
	if err != nil {
		t.Fatal(err)
	}
	if full.Status != StatusOK || len(full.Feasible) != 16 {
		t.Fatalf("无预算限制应枚举全部 16 个组合，得到 %s n=%d", full.Status, len(full.Feasible))
	}
}

func TestMUSBudgetExhaustion(t *testing.T) {
	// 单候选、两条互相矛盾的关系 => 1 个组合，枚举后进入 MUS 搜索；
	// 预算只够枚举，连第一次子集检查都付不起时应返回 incomplete。
	in := &Input{
		Keys: []string{"row"},
		Rows: [][]string{{"1"}},
		Candidates: [][]Candidate{{
			{Name: "only", Decimal: '.', Grouping: GroupNone},
		}},
		Relations: []Relation{
			{ID: "a", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1")}, RHS: rat("1"), RHSCol: NoRHSColumn},
			{ID: "b", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1")}, RHS: rat("2"), RHSCol: NoRHSColumn},
		},
		Budget: 1,
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusIncomplete {
		t.Fatalf("MUS 阶段预算耗尽应为 incomplete，得到 %s", rep.Status)
	}
	if rep.BudgetUsed != 2 || strings.TrimSpace(rep.IncompleteReason) == "" {
		t.Fatalf("预算计数或原因异常: used=%d reason=%q", rep.BudgetUsed, rep.IncompleteReason)
	}

	// 预算放宽后应正常给出 1 条 MUS（关系 b 单条即不可满足）。
	in.Budget = 0
	full, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if full.Status != StatusInfeasible || len(full.Conflicts) != 1 ||
		len(full.Conflicts[0].RelationIDs) != 1 || full.Conflicts[0].RelationIDs[0] != "b" {
		t.Fatalf("期望单条 MUS b，得到 %+v", full.Conflicts)
	}
}

func TestUnparseableColumn(t *testing.T) {
	in := &Input{
		Keys: []string{"r1"},
		Rows: [][]string{{"oops"}},
		Candidates: [][]Candidate{{
			{Name: "only", Decimal: '.', Grouping: GroupNone},
		}},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusInfeasible || len(rep.UnparseableColumns) != 1 ||
		rep.UnparseableColumns[0] != 0 || len(rep.Conflicts) != 0 {
		t.Fatalf("期望报告不可解析列且无 MUS，得到 %+v", rep)
	}
}

func TestRHSColumnRelation(t *testing.T) {
	// 关系 c1 - c0 == 1：第一行 (2,3) 满足；第二行初值 (5,5) 不满足，修正为 (5,6) 后满足。
	in := &Input{
		Keys: []string{"r1", "r2"},
		Rows: [][]string{
			{"2", "3"},
			{"5", "5"},
		},
		Candidates: [][]Candidate{
			{{Name: "p", Decimal: '.', Grouping: GroupNone}},
			{{Name: "q", Decimal: '.', Grouping: GroupNone}},
		},
		Relations: []Relation{
			{ID: "diff", KeyCol: -1, Coeffs: map[int]*big.Rat{1: rat("1"), 0: rat("-1")}, RHS: rat("1"), RHSCol: NoRHSColumn},
		},
	}
	repBad, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if repBad.Status != StatusInfeasible || repBad.Conflicts[0].RelationIDs[0] != "diff" {
		t.Fatalf("期望 diff 关系构成 MUS，得到 %+v", repBad.Conflicts)
	}
	in.Rows[1][1] = "6"
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 1 {
		t.Fatalf("修正后期望唯一可行，得到 %s", rep.Status)
	}
}

func TestValidationErrors(t *testing.T) {
	base := func() *Input {
		return &Input{
			Keys:       []string{"r1"},
			Rows:       [][]string{{"1"}},
			Candidates: [][]Candidate{{{Name: "p", Decimal: '.', Grouping: GroupNone}}},
		}
	}
	cases := []func(*Input){
		func(in *Input) { in.Candidates[0][0].Decimal = 'x' },
		func(in *Input) { in.Candidates[0][0].Grouping = GroupDot },
		func(in *Input) { in.Keys = []string{} },
		func(in *Input) { in.Candidates[0] = nil },
		func(in *Input) { in.Relations = []Relation{{ID: "x"}} },
		func(in *Input) {
			in.Relations = []Relation{{ID: "x", Coeffs: map[int]*big.Rat{9: rat("1")}}}
		},
		func(in *Input) {
			in.Relations = []Relation{
				{ID: "x", Coeffs: map[int]*big.Rat{0: rat("1")}},
				{ID: "x", Coeffs: map[int]*big.Rat{0: rat("1")}},
			}
		},
	}
	for i, mutate := range cases {
		in := base()
		mutate(in)
		if _, err := Solve(in); err == nil {
			t.Fatalf("第 %d 个非法输入应当报错", i)
		}
	}
}
