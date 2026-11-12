package taskpkg

import (
	"math/big"
	"testing"
)

func TestUnitScalesOnlyOneFeasible(t *testing.T) {
	// 同一数值 1000，元 vs 千元两种倍率；关系要求规范值为 1。
	in := &Input{
		Keys: []string{"k1"},
		Rows: [][]string{{"1"}},
		Candidates: [][]Candidate{{
			{Name: "yuan", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1000")},
			{Name: "thousand-yuan", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
		}},
		Relations: []Relation{
			{ID: "norm", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1")}, RHS: rat("1"), RHSCol: NoRHSColumn},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 1 {
		t.Fatalf("期望唯一单位解释，得到 %s n=%d", rep.Status, len(rep.Feasible))
	}
	if rep.Feasible[0].Picked[0] != 1 || valueAt(t, rep.Feasible[0], 0, 0) != "1" {
		t.Fatalf("应选中千元倍率并规范为 1，得到 %v", rep.Feasible[0].Picked)
	}
	assertCrossCheck(t, in, rep)
}

func TestUnitScalesBothFeasibleEquivalent(t *testing.T) {
	// 两列分别携带单位，关系 c1 == 1000*c0 在两种单位搭配下同时成立，
	// 且规范值矩阵完全相同，应归入同一等价类、不标记 ValueVaries。
	in := &Input{
		Keys: []string{"k1"},
		Rows: [][]string{{"1", "1"}},
		Candidates: [][]Candidate{
			{
				{Name: "c0-yuan", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
				{Name: "c0-alt", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
			},
			{
				{Name: "c1-yuan", Decimal: ',', Grouping: GroupNone, UnitScale: rat("1")},
				{Name: "c1-alt", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
			},
		},
		Relations: []Relation{
			{ID: "eq", KeyCol: -1, Coeffs: map[int]*big.Rat{1: rat("1"), 0: rat("-1")}, RHS: rat("0"), RHSCol: NoRHSColumn},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 4 {
		t.Fatalf("两列各两个等价候选，期望 4 个可行组合，得到 %s n=%d", rep.Status, len(rep.Feasible))
	}
	if len(rep.Classes) != 1 {
		t.Fatalf("4 个组合规范值等价，期望 1 个等价类，得到 %d", len(rep.Classes))
	}
	if len(rep.Undecidable) != 2 {
		t.Fatalf("两列都应有多个候选，得到 %+v", rep.Undecidable)
	}
	for _, u := range rep.Undecidable {
		if u.ValueVaries {
			t.Fatalf("列 %d 的候选不改变规范值，不应 ValueVaries", u.Column)
		}
	}
	assertCrossCheck(t, in, rep)
}

func TestDuplicateBusinessKeyConflict(t *testing.T) {
	// 同一业务键两行同列取值不同，且每列只有单一候选 -> 唯一 MUS 即该 KeyEq 关系。
	in := &Input{
		Keys: []string{"dup", "dup"},
		Rows: [][]string{
			{"100"},
			{"200"},
		},
		Candidates: [][]Candidate{
			{{Name: "plain", Decimal: '.', Grouping: GroupNone}},
		},
		Relations: []Relation{
			{ID: "same-key", KeyCol: 0, Coeffs: map[int]*big.Rat{0: rat("1")}},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusInfeasible {
		t.Fatalf("期望 infeasible，得到 %s", rep.Status)
	}
	if len(rep.Conflicts) != 1 || len(rep.Conflicts[0].RelationIDs) != 1 {
		t.Fatalf("期望单条 MUS，得到 %+v", rep.Conflicts)
	}
	if rep.Conflicts[0].RelationIDs[0] != "same-key" {
		t.Fatalf("MUS ID 错误: %v", rep.Conflicts[0].RelationIDs)
	}
	w := rep.Conflicts[0].Witnesses[0]
	if w.RemovedRelation != "same-key" || w.Combo == nil {
		t.Fatalf("删除关系后的可行解释缺失: %+v", w)
	}
	if w.ConflictOnly {
		t.Fatalf("单关系冲突删除后应得到全局可行解释，而不是 ConflictOnly")
	}
}

func TestDuplicateBusinessKeyResolvedByFormat(t *testing.T) {
	// 键 "dup" 两行原始值不同，但千分位解释都为 1234；逗号小数解释为 1.234 与 1.235，冲突。
	in := &Input{
		Keys: []string{"dup", "dup"},
		Rows: [][]string{
			{"1,234"},
			{"1,234.0"},
		},
		Candidates: [][]Candidate{
			{
				{Name: "dot-grouped", Decimal: '.', Grouping: GroupComma},
				{Name: "comma-decimal", Decimal: ',', Grouping: GroupNone},
			},
		},
		Relations: []Relation{
			{ID: "keq", KeyCol: 0, Coeffs: map[int]*big.Rat{0: rat("1")}},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 1 {
		t.Fatalf("期望千分位解释唯一可行，得到 %s n=%d", rep.Status, len(rep.Feasible))
	}
	if valueAt(t, rep.Feasible[0], 0, 0) != "1234" || valueAt(t, rep.Feasible[0], 1, 0) != "1234" {
		t.Fatalf("千分位解释下两行都应规范为 1234，得到 %s, %s",
			rep.Feasible[0].Values[0][0].RatString(), rep.Feasible[0].Values[1][0].RatString())
	}
	if rep.Feasible[0].Picked[0] != 0 {
		t.Fatalf("应锁定千分位候选 0，得到 %v", rep.Feasible[0].Picked)
	}
	assertCrossCheck(t, in, rep)
}

func TestFullyMissingColumnIsUndecidable(t *testing.T) {
	// 第 1 列整体缺失，两个候选均“可行”但无任何取值，必须报告为不可判定且不产生约束。
	in := &Input{
		Keys: []string{"k1", "k2"},
		Rows: [][]string{
			{"100", ""},
			{"200", ""},
		},
		Candidates: [][]Candidate{
			{{Name: "a", Decimal: '.', Grouping: GroupNone}},
			{
				{Name: "b1", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1")},
				{Name: "b2", Decimal: '.', Grouping: GroupNone, UnitScale: rat("1000")},
			},
		},
		Relations: []Relation{
			{ID: "r0", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1")}, RHSCol: 0},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 2 {
		t.Fatalf("缺失列两个候选都应可行，得到 %s n=%d", rep.Status, len(rep.Feasible))
	}
	if len(rep.Undecidable) != 1 || rep.Undecidable[0].Column != 1 {
		t.Fatalf("应仅第 1 列不可判定，得到 %+v", rep.Undecidable)
	}
	if rep.Undecidable[0].ValueVaries {
		t.Fatalf("全缺失列没有取值差异，不应 ValueVaries")
	}
	for _, combo := range rep.Feasible {
		if combo.Values[0][1] != nil || combo.Values[1][1] != nil {
			t.Fatalf("缺失单元必须保持 nil，不能补零")
		}
	}
	assertCrossCheck(t, in, rep)
}

func TestMissingValuesAreNotZero(t *testing.T) {
	// 第二行缺第 0 列：关系 c0 == 10 对该行不约束，仅约束第一行，整体可行。
	in := &Input{
		Keys: []string{"k1", "k2"},
		Rows: [][]string{
			{"10", "1"},
			{"", "1"},
		},
		Candidates: [][]Candidate{
			{{Name: "p0", Decimal: '.', Grouping: GroupNone}},
			{{Name: "p1", Decimal: '.', Grouping: GroupNone}},
		},
		Relations: []Relation{
			{ID: "c0-is-10", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1")}, RHS: rat("10"), RHSCol: NoRHSColumn},
			{ID: "c1-is-1", KeyCol: -1, Coeffs: map[int]*big.Rat{1: rat("1")}, RHS: rat("1"), RHSCol: NoRHSColumn},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 1 {
		t.Fatalf("缺失行应跳过约束，期望唯一可行，得到 %s n=%d", rep.Status, len(rep.Feasible))
	}
	// 反向证据：若把缺失当零，c0==10 在第二行就会失败，这里确认确实只有一条活跃行被校验。
	// 再给一个必然失败的关系，仅在齐备行生效，确保零解走 MUS。
	bad := in
	bad.Relations = append(append([]Relation(nil), in.Relations...),
		Relation{ID: "c0-is-99", KeyCol: -1, Coeffs: map[int]*big.Rat{0: rat("1")}, RHS: rat("99"), RHSCol: NoRHSColumn})
	rep2, err := Solve(bad)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Status != StatusInfeasible || len(rep2.Conflicts[0].RelationIDs) != 1 ||
		rep2.Conflicts[0].RelationIDs[0] != "c0-is-99" {
		t.Fatalf("期望 MUS 为 c0-is-99，得到 %+v", rep2.Conflicts)
	}
}
