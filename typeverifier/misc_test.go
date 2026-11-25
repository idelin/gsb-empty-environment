package typeverifier

import "testing"

// 局部槽合流两个已初始化的兄弟类：置为最近公共祖先，仍可用于祖先类型。
func TestLocalMergeLCA(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 1,
		MaxStack:  1,
		Code: []Instruction{
			branchIns(1, 5),                   // 0
			classIns(OpNew, "Concrete"),       // 1
			classIns(OpInit, "Concrete"),      // 2
			slotIns(OpAStore, 0),              // 3 弹栈入槽
			gotoIns(9),                        // 4
			classIns(OpNew, "OtherConcrete"),  // 5
			classIns(OpInit, "OtherConcrete"), // 6
			slotIns(OpAStore, 0),              // 7
			gotoIns(9),                        // 8
			slotIns(OpALoad, 0),               // 9 槽0 = init(Middle)
			classIns(OpUse, "Base"),           // 10 Middle 是 Base 子类，弹栈
			newIns(OpReturn),                  // 11 栈空
		},
	}
	expectAccept(t, h, m)
}

// throw 未初始化引用必须拒绝。
func TestThrowRejectsUninitialized(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  1,
		Code: []Instruction{
			classIns(OpNew, "Throwable"), // 0 未初始化
			newIns(OpThrow),              // 1 拒绝
		},
	}
	expectReject(t, h, m, 1)
}

// throw 非 Throwable 类型的已初始化引用必须拒绝。
func TestThrowRejectsNonThrowable(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  1,
		Code: []Instruction{
			classIns(OpNew, "X"),  // 0
			classIns(OpInit, "X"), // 1
			newIns(OpThrow),       // 2 X 不是 Throwable
		},
	}
	expectReject(t, h, m, 2)
}

// 操作数栈合流长度不一致直接拒绝。
func TestStackMergeLengthMismatch(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  1,
		Code: []Instruction{
			branchIns(1, 4),      // 0
			classIns(OpNew, "X"), // 1 留下一个栈值
			gotoIns(5),           // 2
			gotoIns(5),           // 3 另一分支栈空
			gotoIns(5),           // 4
			newIns(OpReturn),     // 5 合流栈深不同（0 与 1）
		},
	}
	expectReject(t, h, m, 5)
}

// return 要求操作数栈为空。
func TestReturnRequiresEmptyStack(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  1,
		Code: []Instruction{
			classIns(OpNew, "X"), // 0
			newIns(OpReturn),     // 1 栈非空
		},
	}
	expectReject(t, h, m, 1)
}

// init 只接受同类未初始化接收者：父类构造器不能用于子类 new 出的引用。
func TestInitRejectsDifferentClass(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  1,
		Code: []Instruction{
			classIns(OpNew, "SubX"), // 0
			classIns(OpInit, "X"),   // 1 类不匹配
			newIns(OpReturn),        // 2
		},
	}
	expectReject(t, h, m, 1)
}

// use 接受所需类的子类，并在消费后保持栈空返回。
func TestUseAcceptsSubclass(t *testing.T) {
	h := testHierarchy(t)
	m := &Method{
		MaxLocals: 0,
		MaxStack:  1,
		Code: []Instruction{
			classIns(OpNew, "SubSubX"),  // 0
			classIns(OpInit, "SubSubX"), // 1
			classIns(OpUse, "X"),        // 2 弹栈消费
			newIns(OpReturn),            // 3 栈空
		},
	}
	expectAccept(t, h, m)
}
