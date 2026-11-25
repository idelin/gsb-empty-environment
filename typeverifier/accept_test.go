package typeverifier

import "testing"

// 正常别名初始化：未初始化引用经 astore/aload 形成多个别名，
// init 后帧中同一 new 身份的全部别名都变为已初始化，可安全 use。
func TestNormalAliasInitialization(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 1,
		MaxStack:  1,
		Code: []Instruction{
			classIns(OpNew, "SubX"),  // 0
			slotIns(OpAStore, 0),     // 1 槽0 = 未初始化 #0
			slotIns(OpALoad, 0),      // 2 别名压栈
			classIns(OpInit, "SubX"), // 3 全部 #0 别名 -> init(SubX)
			classIns(OpUse, "X"),     // 4 消费栈顶已初始化引用
			slotIns(OpALoad, 0),      // 5 槽0 现在是已初始化
			classIns(OpUse, "X"),     // 6 SubX 可用于 X 位置
			newIns(OpReturn),         // 7 栈空
		},
	}
	expectAccept(t, h, m)
}

// 安全共享异常处理器：两条异常边都只携带已初始化 Throwable，
// 合流到同一处理器，捕获类型相同，校验通过。
func TestSafeSharedExceptionHandler(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 1,
		MaxStack:  1,
		Code: []Instruction{
			branchIns(1, 5),               // 0 非确定双分支
			classIns(OpNew, "Throwable"),  // 1 路径 A
			classIns(OpInit, "Throwable"), // 2
			newIns(OpThrow),               // 3 抛出 -> 处理器 9
			gotoIns(8),                    // 4
			classIns(OpNew, "Throwable"),  // 5 路径 B
			classIns(OpInit, "Throwable"), // 6
			newIns(OpThrow),               // 7 抛出 -> 处理器 9
			newIns(OpReturn),              // 8 正常路径
			newIns(OpThrow),               // 9 共享处理器：栈顶是已初始化 Throwable
		},
		Handlers: []Handler{
			{Start: 0, End: 8, HandlerPC: 9, CatchClass: "Throwable"},
		},
	}
	expectAccept(t, h, m)
}

// 回边携带未初始化引用（留在局部槽）一律拒绝。
func TestBackEdgeWithUninitRejected(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 1,
		MaxStack:  1,
		Code: []Instruction{
			classIns(OpNew, "X"), // 0
			slotIns(OpAStore, 0), // 1 槽0 未初始化
			branchIns(3, 0),      // 2 回边到 0，帧仍含未初始化槽
			newIns(OpReturn),     // 3
		},
	}
	ve := expectReject(t, h, m, 0)
	last := ve.Path[len(ve.Path)-1]
	if last.From != 2 || last.To != 0 {
		t.Fatalf("期望 2->0 回边拒绝，得到 %+v", last)
	}
}

// 回边只携带已初始化引用时允许，循环达到不动点。
func TestBackEdgeWithInitializedAccepted(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 1,
		MaxStack:  1,
		Code: []Instruction{
			classIns(OpNew, "X"),  // 0
			classIns(OpInit, "X"), // 1 已初始化
			slotIns(OpAStore, 0),  // 2 弹栈，槽0 已初始化，栈空
			slotIns(OpALoad, 0),   // 3 循环头，压入已初始化值
			classIns(OpUse, "X"),  // 4 弹栈消费，栈空
			branchIns(6, 3),       // 5 回边到 3，帧中只有已初始化值
			newIns(OpReturn),      // 6 栈空
		},
	}
	expectAccept(t, h, m)
}
