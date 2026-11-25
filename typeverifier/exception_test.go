package typeverifier

import "testing"

// 构造失败的处理器不得继续使用对象：init 异常边把局部槽中该 new 身份的别名
// 置为不可用，处理器内 aload 立即拒绝。
func TestConstructorFailureHandlerCannotUse(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 1,
		MaxStack:  2,
		Code: []Instruction{
			classIns(OpNew, "X"),  // 0
			slotIns(OpAStore, 0),  // 1 槽0 = 未初始化 #0
			slotIns(OpALoad, 0),   // 2
			classIns(OpInit, "X"), // 3 构造可能失败 -> 处理器 7
			classIns(OpUse, "X"),  // 4 正常路径消费已初始化引用
			newIns(OpReturn),      // 5 栈空
			newIns(OpReturn),      // 6 填充
			slotIns(OpALoad, 0),   // 7 处理器：槽0 已被异常边置不可用
			newIns(OpThrow),       // 8
		},
		Handlers: []Handler{
			{Start: 3, End: 4, HandlerPC: 7, CatchClass: "Throwable"},
		},
	}
	ve := expectReject(t, h, m, 7)
	last := ve.Path[len(ve.Path)-1]
	if !last.Exception || last.Handler != 7 {
		t.Fatalf("拒绝路径末边应为到 7 的异常边，得到 %+v", last)
	}
}

// 父/子类残留别名同样按 new 身份失效，不能在处理器返回后继续 init/use。
func TestConstructorFailureKillsParentChildAliases(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 2,
		MaxStack:  2,
		Code: []Instruction{
			classIns(OpNew, "SubX"),  // 0 身份 #0
			slotIns(OpAStore, 0),     // 1 槽0 残留别名
			slotIns(OpALoad, 0),      // 2
			classIns(OpInit, "SubX"), // 3 异常 -> 处理器 8
			classIns(OpUse, "X"),     // 4
			newIns(OpReturn),         // 5
			newIns(OpReturn),         // 6
			newIns(OpReturn),         // 7
			slotIns(OpALoad, 0),      // 8 槽0 别名（同为 #0）已失效
			newIns(OpThrow),          // 9
		},
		Handlers: []Handler{
			{Start: 3, End: 4, HandlerPC: 8, CatchClass: "Throwable"},
		},
	}
	expectReject(t, h, m, 8)
}

// new 的异常边不产生对象：处理器栈上只有 Throwable，不能借机 use 出目标类。
func TestNewExceptionEdgeProducesNoObject(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  2,
		Code: []Instruction{
			classIns(OpNew, "X"),  // 0 异常 -> 处理器 5
			classIns(OpInit, "X"), // 1
			classIns(OpUse, "X"),  // 2 正常路径消费，栈空
			newIns(OpReturn),      // 3
			newIns(OpReturn),      // 4
			classIns(OpUse, "X"),  // 5 异常处理器：栈顶是 Throwable，不是 X
			newIns(OpThrow),       // 6
		},
		Handlers: []Handler{
			{Start: 0, End: 1, HandlerPC: 5, CatchClass: "Throwable"},
		},
	}
	expectReject(t, h, m, 5)
}
