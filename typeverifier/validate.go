package typeverifier

import "fmt"

// validateStructure 做与数据流无关的静态结构检查。
func (e *engine) validateStructure() error {
	n := len(e.method.Code)
	for pc, ins := range e.method.Code {
		switch ins.Op {
		case OpNew, OpInit, OpUse:
			if ins.Class == "" {
				return e.failStatic(pc, "缺少类名")
			}
			if !e.hierarchy.Has(ins.Class) {
				return e.failStatic(pc, "引用了类层次中不存在的类 "+ins.Class)
			}
		case OpAStore, OpALoad:
			if ins.Slot < 0 || ins.Slot >= e.method.MaxLocals {
				return e.failStatic(pc, fmt.Sprintf("局部槽 %d 越界 [0,%d)", ins.Slot, e.method.MaxLocals))
			}
		case OpGoto:
			if !e.validPC(ins.TargetA) {
				return e.failStatic(pc, fmt.Sprintf("跳转目标 %d 越界", ins.TargetA))
			}
		case OpBranch:
			if !e.validPC(ins.TargetA) || !e.validPC(ins.TargetB) {
				return e.failStatic(pc, fmt.Sprintf("双分支目标 %d/%d 越界", ins.TargetA, ins.TargetB))
			}
		case OpDup, OpReturn, OpThrow:
		default:
			return e.failStatic(pc, fmt.Sprintf("未知操作码 %d", ins.Op))
		}
	}
	for i, h := range e.method.Handlers {
		if h.Start < 0 || h.End > n || h.Start >= h.End {
			return e.failStatic(0, fmt.Sprintf("异常表项 %d 覆盖区间非法 [%d,%d)", i, h.Start, h.End))
		}
		if !e.validPC(h.HandlerPC) {
			return e.failStatic(0, fmt.Sprintf("异常表项 %d 处理器 %d 越界", i, h.HandlerPC))
		}
		catch := h.CatchClass
		if catch == "" {
			catch = e.throwable
		}
		if !e.hierarchy.Has(catch) {
			return e.failStatic(0, fmt.Sprintf("异常表项 %d 的捕获类 %s 不存在", i, catch))
		}
		if !e.hierarchy.IsAssignable(catch, e.throwable) {
			return e.failStatic(0, fmt.Sprintf("异常表项 %d 的捕获类 %s 不是 %s 的子类", i, catch, e.throwable))
		}
	}
	return nil
}
