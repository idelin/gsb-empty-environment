package sheetengine

import (
	"reflect"
	"strings"
	"testing"
)

// 构建验收依赖图：A1 -> B1 -> C1，D1 -> C1（D1 本身是公式 D1=E1+1，
// 以便观测它是否被重算）。
//
//	E1 --+--> D1 --+
//	             v
//	A1 ----> B1 -> C1
func buildGraph(t *testing.T) *Engine {
	t.Helper()
	e := New()
	e.SetValue("E1", 100)
	e.SetValue("A1", 1)
	mustFormula(t, e, "D1", "E1 + 1") // D1 = 101
	mustFormula(t, e, "B1", "A1 * 2") // B1 = 2
	mustFormula(t, e, "C1", "B1 + D1") // C1 = 103
	return e
}

func mustFormula(t *testing.T, e *Engine, name, formula string) {
	t.Helper()
	if err := e.SetFormula(name, formula); err != nil {
		t.Fatalf("SetFormula(%s, %q) unexpected error: %v", name, formula, err)
	}
}

// 验收点 1：A1 变更时，仅重算 B1 和 C1，D1 保持不变且不被重算。
func TestIncrementalRecomputeOnlyAffectedSubgraph(t *testing.T) {
	e := buildGraph(t)

	if got := e.GetValue("C1"); got != 103 {
		t.Fatalf("initial C1 = %v, want 103", got)
	}

	// 清零计数器，只观测 A1 变更后的增量行为。
	for _, name := range []string{"A1", "B1", "C1", "D1", "E1"} {
		e.recalcCount[name] = 0
	}

	e.SetValue("A1", 5) // B1 = 10, C1 = 10 + 101 = 111

	if got := e.GetValue("B1"); got != 10 {
		t.Errorf("B1 = %v, want 10", got)
	}
	if got := e.GetValue("C1"); got != 111 {
		t.Errorf("C1 = %v, want 111", got)
	}
	if got := e.GetValue("D1"); got != 101 {
		t.Errorf("D1 = %v, want 101 (unchanged)", got)
	}

	if got := e.RecalcCount("B1"); got != 1 {
		t.Errorf("B1 recalculated %d times, want exactly 1", got)
	}
	if got := e.RecalcCount("C1"); got != 1 {
		t.Errorf("C1 recalculated %d times, want exactly 1", got)
	}
	if got := e.RecalcCount("D1"); got != 0 {
		t.Errorf("D1 recalculated %d times, want 0 (not in affected subgraph)", got)
	}
}

// 验收点 2：尝试添加 C1 -> A1 的公式（即令 A1 依赖 C1）时，
// 系统立即拒绝并返回完整循环路径，且原图状态被隔离不受影响。
func TestIndirectCycleRejectedWithFullPath(t *testing.T) {
	e := buildGraph(t)
	before := e.GetValue("C1")

	err := e.SetFormula("A1", "C1 + 1") // A1 -> B1 -> C1 -> A1 成环
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	cycleErr, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("error type = %T, want *CycleError", err)
	}

	wantPath := []string{"A1", "B1", "C1", "A1"}
	if !reflect.DeepEqual(cycleErr.Path, wantPath) {
		t.Errorf("cycle path = %v, want %v", cycleErr.Path, wantPath)
	}
	if !strings.Contains(err.Error(), "A1 -> B1 -> C1 -> A1") {
		t.Errorf("error message %q does not contain full cycle path", err.Error())
	}

	// 隔离性：A1 仍是字面量 1，C1 公式与值均未受影响。
	if got := e.GetValue("A1"); got != 1 {
		t.Errorf("A1 = %v, want 1 (graph unchanged)", got)
	}
	if got := e.GetValue("C1"); got != before {
		t.Errorf("C1 = %v, want %v (graph unchanged)", got, before)
	}
	if _, stillLiteral := e.formulas["A1"]; stillLiteral {
		t.Error("A1 must not have a formula after rejected SetFormula")
	}
	if _, hasEdge := e.dependents["C1"]["A1"]; hasEdge {
		t.Error("rejected edge C1 -> A1 leaked into dependents graph")
	}
}

// 直接自引用也应被拒绝。
func TestDirectSelfCycleRejected(t *testing.T) {
	e := New()
	e.SetValue("A1", 1)
	err := e.SetFormula("A1", "A1 + 1")
	if err == nil {
		t.Fatal("expected cycle error for self reference, got nil")
	}
	cycleErr, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("error type = %T, want *CycleError", err)
	}
	wantPath := []string{"A1", "A1"}
	if !reflect.DeepEqual(cycleErr.Path, wantPath) {
		t.Errorf("cycle path = %v, want %v", cycleErr.Path, wantPath)
	}
}

// 两点直接互依：A1=B1 后再设 B1=A1，应立即拒绝。
func TestTwoNodeCycleRejected(t *testing.T) {
	e := New()
	mustFormula(t, e, "A1", "B1 + 1")
	e.SetValue("B1", 0) // B1 先为字面量
	mustFormula(t, e, "B1", "5") // 覆盖为公式（无引用），合法
	err := e.SetFormula("B1", "A1 * 2") // A1 <-> B1 成环
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	cycleErr := err.(*CycleError)
	wantPath := []string{"B1", "A1", "B1"}
	if !reflect.DeepEqual(cycleErr.Path, wantPath) {
		t.Errorf("cycle path = %v, want %v", cycleErr.Path, wantPath)
	}
}

// 拓扑顺序正确性：菱形依赖中，汇合点必须在其所有上游之后求值。
func TestDiamondDependencyTopologicalOrder(t *testing.T) {
	e := New()
	e.SetValue("A1", 2)
	mustFormula(t, e, "B1", "A1 + 1")  // 3
	mustFormula(t, e, "C1", "A1 * 10") // 20
	mustFormula(t, e, "D1", "B1 + C1") // 23

	if got := e.GetValue("D1"); got != 23 {
		t.Fatalf("D1 = %v, want 23", got)
	}
	e.SetValue("A1", 3) // B1=4, C1=30, D1=34
	if got := e.GetValue("D1"); got != 34 {
		t.Errorf("D1 = %v, want 34 after A1 change", got)
	}
}

// 修改中间节点公式，下游应随之更新。
func TestFormulaUpdatePropagates(t *testing.T) {
	e := buildGraph(t)
	mustFormula(t, e, "B1", "A1 + 100") // B1 = 101, C1 = 101 + 101 = 202
	if got := e.GetValue("C1"); got != 202 {
		t.Errorf("C1 = %v, want 202 after B1 formula update", got)
	}
}

// 公式语法错误应报错且不污染图。
func TestInvalidFormulaRejected(t *testing.T) {
	e := New()
	e.SetValue("A1", 1)
	if err := e.SetFormula("B1", "A1 +"); err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if _, ok := e.formulas["B1"]; ok {
		t.Error("invalid formula must not be stored")
	}
}
