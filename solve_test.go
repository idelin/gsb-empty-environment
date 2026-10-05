package main

import (
	"strings"
	"testing"
)

func w(v int64) *int64 { return &v }

func mustModel(t *testing.T, in *Input) *model {
	t.Helper()
	m, err := BuildModel(in)
	if err != nil {
		t.Fatalf("BuildModel: %v", err)
	}
	return m
}

func deletedSet(m *model, sol solution) map[RowRef]bool {
	out := make(map[RowRef]bool)
	for i, ref := range m.rowRef {
		if sol.keptMask&(1<<uint(i)) == 0 {
			out[ref] = true
		}
	}
	return out
}

func deletedWeight(m *model, sol solution) int64 {
	var sum int64
	for i := range m.rowRef {
		if sol.keptMask&(1<<uint(i)) == 0 {
			sum += m.weight[i]
		}
	}
	return sum
}

// 关键反例：唯一键 {A(10), B(1)}，指定冲突 A—C，C 权重 11。
// 逐条约束取局部最优再合并：唯一键删 B(1)，冲突删 C(11)，共删 12；
// 全局最优：删 A(10)，保留 B 与 C，共删 10。求解器必须给出后者。
func TestGlobalOptimumBeatsLocalMerge(t *testing.T) {
	in := &Input{
		Tables: []Table{
			{
				Name:       "users",
				UniqueKeys: [][]string{{"email"}},
				Rows: []Row{
					{ID: "A", Weight: w(10), Values: map[string]interface{}{"email": "x@y"}},
					{ID: "B", Weight: w(1), Values: map[string]interface{}{"email": "x@y"}},
				},
			},
			{
				Name: "sessions",
				Rows: []Row{
					{ID: "C", Weight: w(11), Values: map[string]interface{}{"token": "t1"}},
				},
			},
		},
		Conflicts: []Conflict{
			{A: RowRef{"users", "A"}, B: RowRef{"sessions", "C"}, Reason: "账号冻结期间不得有活跃会话"},
		},
	}
	m := mustModel(t, in)
	sol, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	del := deletedSet(m, sol)
	if len(del) != 1 || !del[RowRef{"users", "A"}] {
		t.Fatalf("期望只删 users.A，实际删除: %v", del)
	}
	if got := deletedWeight(m, sol); got != 10 {
		t.Fatalf("期望删除权重 10，实际 %d", got)
	}
	if !VerifyKept(m, sol).OK {
		t.Fatal("保留表核验未通过")
	}
}

// 删除父行必须级联删除引用行，且级联代价要计入优化目标：
// P(1) 与 Q(2) 唯一键冲突，C(100) 引用 P。
// 若忽略级联会删 P(1)；正确全局解是删 Q(2)，保住 P 与 C。
func TestForeignKeyCascadeCountedInObjective(t *testing.T) {
	in := &Input{
		Tables: []Table{
			{
				Name:       "users",
				UniqueKeys: [][]string{{"email"}},
				Rows: []Row{
					{ID: "P", Weight: w(1), Values: map[string]interface{}{"id": 1, "email": "a@b"}},
					{ID: "Q", Weight: w(2), Values: map[string]interface{}{"id": 2, "email": "a@b"}},
				},
			},
			{
				Name: "orders",
				Rows: []Row{
					{ID: "C", Weight: w(100), Values: map[string]interface{}{"user_id": 1}},
				},
			},
		},
		ForeignKeys: []ForeignKey{
			{ChildTable: "orders", ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"},
		},
	}
	m := mustModel(t, in)
	sol, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	del := deletedSet(m, sol)
	if len(del) != 1 || !del[RowRef{"users", "Q"}] {
		t.Fatalf("期望只删 users.Q，实际删除: %v", del)
	}
}

// 父行被删时，子行必须出现在删除集中，且原因闭包标注级联并指向父行。
func TestCascadeReasonClosure(t *testing.T) {
	in := &Input{
		Tables: []Table{
			{
				Name:       "users",
				UniqueKeys: [][]string{{"email"}},
				Rows: []Row{
					{ID: "P", Weight: w(1), Values: map[string]interface{}{"id": 1, "email": "a@b"}},
					{ID: "Q", Weight: w(100), Values: map[string]interface{}{"id": 2, "email": "a@b"}},
				},
			},
			{
				Name: "orders",
				Rows: []Row{
					{ID: "C", Weight: w(2), Values: map[string]interface{}{"user_id": 1}},
				},
			},
		},
		ForeignKeys: []ForeignKey{
			{ChildTable: "orders", ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"},
		},
	}
	m := mustModel(t, in)
	sol, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	del := deletedSet(m, sol)
	if !del[RowRef{"users", "P"}] || !del[RowRef{"orders", "C"}] {
		t.Fatalf("期望级联删除 P 与 C，实际删除: %v", del)
	}
	reasons := ExplainDeletions(m, sol)
	var cascade *DeletionReason
	for i := range reasons {
		if reasons[i].Row == "C" {
			cascade = &reasons[i]
		}
	}
	if cascade == nil || cascade.Kind != ReasonFKCascade {
		t.Fatalf("C 应为级联删除，实际: %+v", reasons)
	}
	if cascade.CausedBy == nil || cascade.CausedBy.Row != "P" {
		t.Fatalf("C 的 caused_by 应指向 P: %+v", cascade)
	}
	// 闭包链：根因 P（唯一键取舍）-> C（级联）。
	if len(cascade.Chain) != 2 || cascade.Chain[0].Row != "P" || cascade.Chain[1].Row != "C" {
		t.Fatalf("传导链应为 P->C: %+v", cascade.Chain)
	}
}

// 引用了不存在的父行：只能删除，原因为 dangling_foreign_key。
func TestDanglingForeignKey(t *testing.T) {
	in := &Input{
		Tables: []Table{
			{Name: "users", Rows: []Row{{ID: "U", Values: map[string]interface{}{"id": 1}}}},
			{Name: "orders", Rows: []Row{{ID: "O", Weight: w(5), Values: map[string]interface{}{"user_id": 99}}}},
		},
		ForeignKeys: []ForeignKey{
			{ChildTable: "orders", ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"},
		},
	}
	m := mustModel(t, in)
	sol, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	del := deletedSet(m, sol)
	if !del[RowRef{"orders", "O"}] || del[RowRef{"users", "U"}] {
		t.Fatalf("期望只删 orders.O，实际: %v", del)
	}
	for _, r := range ExplainDeletions(m, sol) {
		if r.Row == "O" && r.Kind != ReasonDanglingFK {
			t.Fatalf("O 应为悬空外键删除: %+v", r)
		}
	}
}

// 超过 25 行必须报错。
func TestRowLimit(t *testing.T) {
	var rows []Row
	for i := 0; i < MaxRows+1; i++ {
		rows = append(rows, Row{ID: strings.Repeat("r", 1) + string(rune('a'+i%26)) + string(rune('0'+i/26))})
	}
	in := &Input{Tables: []Table{{Name: "t", Rows: rows}}}
	if _, err := BuildModel(in); err == nil {
		t.Fatal("超过 25 行应报错")
	}
}

// 核验器应能发现人为构造的违规保留集。
func TestVerifyDetectsViolations(t *testing.T) {
	in := &Input{
		Tables: []Table{
			{
				Name:       "users",
				UniqueKeys: [][]string{{"email"}},
				Rows: []Row{
					{ID: "A", Values: map[string]interface{}{"email": "x@y"}},
					{ID: "B", Values: map[string]interface{}{"email": "x@y"}},
				},
			},
		},
	}
	m := mustModel(t, in)
	bad := solution{keptMask: 3, keptWeight: 2}
	if VerifyKept(m, bad).OK {
		t.Fatal("同时保留两行同键行，核验应失败")
	}
}

// 多约束联合：唯一键 + 外键 + 指定冲突同时作用时的全局最优。
func TestJointConstraints(t *testing.T) {
	in := &Input{
		Tables: []Table{
			{
				Name:       "users",
				UniqueKeys: [][]string{{"email"}},
				Rows: []Row{
					{ID: "A", Weight: w(4), Values: map[string]interface{}{"id": 1, "email": "x@y"}},
					{ID: "B", Weight: w(4), Values: map[string]interface{}{"id": 2, "email": "x@y"}},
				},
			},
			{
				Name: "orders",
				Rows: []Row{
					{ID: "O1", Weight: w(3), Values: map[string]interface{}{"user_id": 1}},
					{ID: "O2", Weight: w(3), Values: map[string]interface{}{"user_id": 2}},
				},
			},
			{
				Name: "flags",
				Rows: []Row{
					{ID: "F", Weight: w(5), Values: map[string]interface{}{"note": "fraud"}},
				},
			},
		},
		ForeignKeys: []ForeignKey{
			{ChildTable: "orders", ChildColumn: "user_id", ParentTable: "users", ParentColumn: "id"},
		},
		Conflicts: []Conflict{
			{A: RowRef{"orders", "O1"}, B: RowRef{"flags", "F"}, Reason: "风控标记与待支付订单互斥"},
		},
	}
	m := mustModel(t, in)
	sol, err := Solve(m)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// 保留 {B, O2, F}（权重 12），删 {A(4), O1(3)} 共 7，优于其他组合。
	del := deletedSet(m, sol)
	if !del[RowRef{"users", "A"}] || !del[RowRef{"orders", "O1"}] || len(del) != 2 {
		t.Fatalf("期望删除 A 与 O1，实际: %v", del)
	}
	if !VerifyKept(m, sol).OK {
		t.Fatal("保留表核验未通过")
	}
}
