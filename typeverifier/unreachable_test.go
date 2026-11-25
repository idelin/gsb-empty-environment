package typeverifier

import "testing"

func TestSanityUnreachableNotVerified(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{MaxLocals: 0, MaxStack: 1, Code: []Instruction{
		newIns(OpReturn), // 0 立即返回
		newIns(OpDup),    // 1 不可达，栈下溢不应被报告
	}}
	if err := Verify(h, m); err != nil {
		t.Fatalf("不可达代码不应导致拒绝: %v", err)
	}
}
