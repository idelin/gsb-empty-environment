package typeverifier

import "fmt"

// simulate 在 pc 的入口帧上模拟指令，并传播其全部正常边与异常边。
func (e *engine) simulate(pc int) error {
	ins := e.method.Code[pc]
	in := e.frames[pc]
	if in == nil {
		return nil
	}

	var normal *frame
	var exLocals []value
	killIDs := map[int]bool{}

	switch ins.Op {
	case OpNew:
		out := in.clone()
		out.stack = append(out.stack, uninitValue(ins.Class, pc))
		normal = out
		// new 的异常边不产生对象：局部槽原样保留，仅压入已初始化 Throwable。
		exLocals = append([]value(nil), in.locals...)

	case OpDup:
		if len(in.stack) == 0 {
			return e.failAt(pc, "dup 要求非空操作数栈")
		}
		out := in.clone()
		out.stack = append(out.stack, out.stack[len(out.stack)-1])
		normal = out

	case OpAStore:
		if len(in.stack) == 0 {
			return e.failAt(pc, "astore 弹栈时栈为空")
		}
		out := in.clone()
		out.locals[ins.Slot] = out.stack[len(out.stack)-1]
		out.stack = out.stack[:len(out.stack)-1]
		normal = out

	case OpALoad:
		v := in.locals[ins.Slot]
		if v.isTop() {
			return e.failAt(pc, fmt.Sprintf("aload 读取不可用局部槽 %d", ins.Slot))
		}
		out := in.clone()
		out.stack = append(out.stack, v)
		normal = out

	case OpInit:
		if len(in.stack) == 0 {
			return e.failAt(pc, "init 取接收者时栈为空")
		}
		out := in.clone()
		receiver := out.stack[len(out.stack)-1]
		if !receiver.isUninit() {
			return e.failAt(pc, "init 只接受未初始化接收者，收到 "+receiver.String())
		}
		if receiver.class != ins.Class {
			return e.failAt(pc, fmt.Sprintf("init(%s) 只接受同类未初始化接收者，收到 %s", ins.Class, receiver.String()))
		}
		killIDs[receiver.newPC] = true
		// 接收者出栈后以已初始化引用回到栈顶，栈深不变。
		out.stack[len(out.stack)-1] = initValue(ins.Class)
		e.replaceUninit(out, receiver.newPC, ins.Class)
		normal = out
		// 异常边：局部槽中该 new 身份的所有别名（含父/子类残留）置不可用。
		exLocals = e.exceptionLocals(in.locals, killIDs)

	case OpUse:
		if len(in.stack) == 0 {
			return e.failAt(pc, "use 取参数时栈为空")
		}
		arg := in.stack[len(in.stack)-1]
		if !arg.isInit() {
			return e.failAt(pc, "use 要求已初始化引用，收到 "+arg.String())
		}
		if !e.hierarchy.IsAssignable(arg.class, ins.Class) {
			return e.failAt(pc, fmt.Sprintf("use(%s) 拒绝类型 %s", ins.Class, arg.class))
		}
		out := in.clone()
		out.stack = out.stack[:len(out.stack)-1]
		normal = out

	case OpGoto:
		normal = in.clone()

	case OpBranch:
		normal = in.clone()

	case OpReturn:
		if len(in.stack) != 0 {
			return e.failAt(pc, fmt.Sprintf("return 要求栈空，实际深度 %d", len(in.stack)))
		}

	case OpThrow:
		if len(in.stack) == 0 {
			return e.failAt(pc, "throw 取异常时栈为空")
		}
		thrown := in.stack[len(in.stack)-1]
		if !thrown.isInit() || !e.hierarchy.IsAssignable(thrown.class, e.throwable) {
			return e.failAt(pc, "throw 必须接收已初始化 Throwable，收到 "+thrown.String())
		}
	}

	return e.emitEdges(pc, in, normal, exLocals, killIDs)
}

// replaceUninit 把帧中属于 newID 的所有未初始化别名替换为已初始化的 class。
func (e *engine) replaceUninit(f *frame, newID int, class string) {
	for i, v := range f.locals {
		if v.isUninit() && v.newPC == newID {
			f.locals[i] = initValue(class)
		}
	}
	for i, v := range f.stack {
		if v.isUninit() && v.newPC == newID {
			f.stack[i] = initValue(class)
		}
	}
}

// exceptionLocals 生成异常边的局部槽：属于 killIDs 的未初始化别名置不可用，其余保留。
func (e *engine) exceptionLocals(locals []value, killIDs map[int]bool) []value {
	out := append([]value(nil), locals...)
	for i, v := range out {
		if v.isUninit() && killIDs[v.newPC] {
			out[i] = topValue()
		}
	}
	return out
}
