package taskpkg

import (
	"math/big"
	"testing"
)

func TestNoRelationsAllCombinationsFeasible(t *testing.T) {
	in := &Input{
		Keys: []string{"k1"},
		Rows: [][]string{{"42"}},
		Candidates: [][]Candidate{
			{
				{Name: "a", Decimal: '.', Grouping: GroupNone},
				{Name: "b", Decimal: ',', Grouping: GroupNone},
			},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusOK || len(rep.Feasible) != 2 {
		t.Fatalf("无关系时两个候选都应可行，得到 %s n=%d", rep.Status, len(rep.Feasible))
	}
	if len(rep.Conflicts) != 0 {
		t.Fatalf("无关系不应产生冲突集，得到 %+v", rep.Conflicts)
	}
	assertCrossCheck(t, in, rep)
}

func TestDuplicateKeySingleRowIsNotConflict(t *testing.T) {
	// 业务键只有一行时不构成重复，KeyEq 没有活跃分组，应视为满足。
	in := &Input{
		Keys: []string{"solo"},
		Rows: [][]string{{"7"}},
		Candidates: [][]Candidate{
			{{Name: "p", Decimal: '.', Grouping: GroupNone}},
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
		t.Fatalf("单行 KeyEq 应视为满足，得到 %s n=%d", rep.Status, len(rep.Feasible))
	}
}

func TestKeyEqAcrossMultipleColumns(t *testing.T) {
	// 重复键两行，两列都要求等值；第二列冲突时应进入 MUS。
	in := &Input{
		Keys: []string{"dup", "dup"},
		Rows: [][]string{
			{"1", "10"},
			{"1", "20"},
		},
		Candidates: [][]Candidate{
			{{Name: "p0", Decimal: '.', Grouping: GroupNone}},
			{{Name: "p1", Decimal: '.', Grouping: GroupNone}},
		},
		Relations: []Relation{
			{ID: "keq", KeyCol: 0, Coeffs: map[int]*big.Rat{0: rat("1"), 1: rat("1")}},
		},
	}
	rep, err := Solve(in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusInfeasible || rep.Conflicts[0].RelationIDs[0] != "keq" {
		t.Fatalf("第二列重复键冲突应产出 keq MUS，得到 %+v", rep.Conflicts)
	}
}
