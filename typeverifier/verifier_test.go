package typeverifier

import "testing"

func testHierarchy(t *testing.T) *Hierarchy {
	t.Helper()
	parents := map[string]string{
		"Throwable":     RootClass,
		"X":             RootClass,
		"Y":             RootClass,
		"SubX":          "X",
		"SubSubX":       "SubX",
		"Base":          RootClass,
		"Middle":        "Base",
		"Concrete":      "Middle",
		"OtherConcrete": "Middle",
	}
	h, err := NewHierarchy(parents)
	if err != nil {
		t.Fatalf("构造类层次失败: %v", err)
	}
	return h
}

func newIns(op Opcode) Instruction                 { return Instruction{Op: op} }
func classIns(op Opcode, class string) Instruction { return Instruction{Op: op, Class: class} }
func slotIns(op Opcode, slot int) Instruction      { return Instruction{Op: op, Slot: slot} }
func gotoIns(target int) Instruction               { return Instruction{Op: OpGoto, TargetA: target} }
func branchIns(a, b int) Instruction {
	return Instruction{Op: OpBranch, TargetA: a, TargetB: b}
}

func expectReject(t *testing.T, h *Hierarchy, m *Method, wantPC int) *VerifyError {
	t.Helper()
	err := Verify(h, m)
	if err == nil {
		t.Fatalf("期望在指令 %d 拒绝，但校验通过", wantPC)
	}
	ve, ok := err.(*VerifyError)
	if !ok {
		t.Fatalf("期望 *VerifyError，得到 %T: %v", err, err)
	}
	if ve.PC != wantPC {
		t.Fatalf("期望拒绝点 %d，得到 %d（%s）", wantPC, ve.PC, ve.Reason)
	}
	return ve
}

func expectAccept(t *testing.T, h *Hierarchy, m *Method) {
	t.Helper()
	if err := Verify(h, m); err != nil {
		t.Fatalf("期望校验通过，得到拒绝: %v", err)
	}
}
