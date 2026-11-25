package typeverifier

import "testing"

// 两个 new 同类却不同身份：操作数栈合流时两个未初始化引用身份不同，直接拒绝。
func TestTwoNewSameClassDifferentIdentityStack(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  2,
		Code: []Instruction{
			branchIns(1, 3),      // 0 非确定双分支
			classIns(OpNew, "X"), // 1
			gotoIns(5),           // 2
			classIns(OpNew, "X"), // 3
			gotoIns(5),           // 4
			newIns(OpReturn),     // 5 合流点，栈长度一致但身份不同
		},
	}
	ve := expectReject(t, h, m, 5)
	if len(ve.Path) == 0 {
		t.Fatalf("拒绝路径不应为空")
	}
}

// 两个 new 同类不同身份写入同一局部槽：局部合流不相容置不可用，随后 aload 被拒绝。
func TestTwoNewSameClassDifferentIdentityLocal(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 1,
		MaxStack:  1,
		Code: []Instruction{
			branchIns(1, 4),      // 0
			classIns(OpNew, "X"), // 1
			slotIns(OpAStore, 0), // 2
			gotoIns(7),           // 3
			classIns(OpNew, "X"), // 4
			slotIns(OpAStore, 0), // 5
			gotoIns(7),           // 6
			slotIns(OpALoad, 0),  // 7 槽已置不可用
			classIns(OpUse, "X"), // 8
			newIns(OpReturn),     // 9
		},
	}
	expectReject(t, h, m, 7)
}

// init 只初始化栈顶 new 身份：另一个 new 身份的同类引用保持未初始化，use 时被拒绝。
func TestInitOnlyInitializesTopIdentity(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  2,
		Code: []Instruction{
			classIns(OpNew, "X"),  // 0 身份 #0 入栈
			classIns(OpNew, "X"),  // 1 身份 #1 入栈，栈: [#0, #1]
			classIns(OpInit, "X"), // 2 栈顶 #1 -> init(X)，#0 仍未初始化
			classIns(OpUse, "X"),  // 3 use 已初始化的 #1，通过并弹栈
			classIns(OpUse, "X"),  // 4 栈顶是未初始化 #0，拒绝
			newIns(OpReturn),      // 5
		},
	}
	expectReject(t, h, m, 4)
}
