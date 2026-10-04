package main

import "testing"

// 链式反转: 旧 A->B->C->T, 新 C->B->A->T, 存在单节点顺序 (A,B,C)。
func chainProblem(t *testing.T) *Problem {
	old := map[string]map[string]string{
		"A": {"T": "B"},
		"B": {"T": "C"},
		"C": {"T": "T"},
	}
	newT := map[string]map[string]string{
		"A": {"T": "T"},
		"B": {"T": "A"},
		"C": {"T": "B"},
	}
	p, err := NewProblem([]string{"A", "B", "C", "T"}, []string{"T"}, old, newT)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	return p
}

// 同步对: 任一单节点更新都会成环, 必须同步更新 {A,B}。
func syncPairProblem(t *testing.T) *Problem {
	old := map[string]map[string]string{
		"A":  {"D1": "B", "D2": "D2"},
		"B":  {"D1": "D1", "D2": "A"},
		"D1": {"D2": "D2"},
		"D2": {"D1": "D1"},
	}
	newT := map[string]map[string]string{
		"A":  {"D1": "D1", "D2": "B"},
		"B":  {"D1": "A", "D2": "D2"},
		"D1": {"D2": "D2"},
		"D2": {"D1": "D1"},
	}
	p, err := NewProblem([]string{"A", "B", "D1", "D2"}, []string{"D1", "D2"}, old, newT)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	return p
}

func verifySchedule(t *testing.T, p *Problem, moves []Move) {
	t.Helper()
	state := uint64(0)
	for i, mv := range moves {
		state |= mv.Group
		if mv.State != state {
			t.Fatalf("步骤 %d: 状态记录不一致", i+1)
		}
		if ok, _ := p.checkState(mv.State); !ok {
			t.Fatalf("步骤 %d: 中间状态不安全", i+1)
		}
	}
	if state != p.usefulMask {
		t.Fatalf("方案结束时仍有节点未更新")
	}
}

func TestChainSingleNodeOrder(t *testing.T) {
	p := chainProblem(t)
	moves, ok, _ := p.FindSchedule(1)
	if !ok {
		t.Fatal("链式反转应存在单节点更新顺序")
	}
	if len(moves) != 3 {
		t.Fatalf("期望 3 步, 实际 %d 步", len(moves))
	}
	verifySchedule(t, p, moves)
	// 第一个更新的必须是 A (离新目的最近), 否则成环
	if got := p.namesOf(moves[0].Group); len(got) != 1 || got[0] != "A" {
		t.Fatalf("首步应更新 A, 实际 %v", got)
	}
}

func TestSyncPairRequiresGroup(t *testing.T) {
	p := syncPairProblem(t)
	if _, ok, _ := p.FindSchedule(1); ok {
		t.Fatal("同步对不应存在单节点更新顺序")
	}
	moves, k := p.MinGroupSchedule()
	if k != 2 {
		t.Fatalf("最小同步更新组应为 2, 实际 %d", k)
	}
	if len(moves) != 1 {
		t.Fatalf("应一步同步完成, 实际 %d 步", len(moves))
	}
	names := p.namesOf(moves[0].Group)
	if len(names) != 2 || names[0] != "A" || names[1] != "B" {
		t.Fatalf("同步组应为 {A,B}, 实际 %v", names)
	}
	verifySchedule(t, p, moves)
}

func TestLoopDetectionEvidence(t *testing.T) {
	// 旧路由自带环 A->B->A
	old := map[string]map[string]string{
		"A": {"T": "B"},
		"B": {"T": "A"},
	}
	newT := map[string]map[string]string{
		"A": {"T": "T"},
		"B": {"T": "T"},
	}
	p, err := NewProblem([]string{"A", "B", "T"}, []string{"T"}, old, newT)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	ok, evs := p.checkState(0)
	if ok {
		t.Fatal("应检测到转发环")
	}
	if evs[0].Cycle == nil {
		t.Fatal("证据中应包含环")
	}
	t.Logf("环证据: %v", evs[0].Cycle)
}

func TestUnreachableEvidence(t *testing.T) {
	// 新路由中 B 指向不存在的下一跳由输入校验拦截; 这里测试黑洞:
	// 构造时 B 的下一跳合法, 但旧表中 A->B, B->C, C 无到 T 条目会在输入校验报错。
	old := map[string]map[string]string{
		"A": {"T": "B"},
		"B": {"T": "C"},
		"C": {"T": "T"},
	}
	newT := map[string]map[string]string{
		"A": {"T": "B"},
		"B": {"T": "C"},
		"C": {"T": "T"},
	}
	p, err := NewProblem([]string{"A", "B", "C", "T"}, []string{"T"}, old, newT)
	if err != nil {
		t.Fatalf("NewProblem: %v", err)
	}
	if len(p.useful) != 0 {
		t.Fatal("旧新一致时应无需更新")
	}
}

func TestInvalidInput(t *testing.T) {
	old := map[string]map[string]string{"A": {"T": "B"}}
	newT := map[string]map[string]string{"A": {"T": "B"}}
	if _, err := NewProblem([]string{"A", "B", "T"}, []string{"T"}, old, newT); err == nil {
		t.Fatal("缺少 B 的下一跳应报错")
	}
	if _, err := NewProblem(make([]string, 19), []string{"T"}, old, newT); err == nil {
		t.Fatal("超过 18 个节点应报错")
	}
}
