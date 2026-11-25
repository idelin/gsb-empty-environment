package typeverifier

import "fmt"

// propagate 把输出帧 out 沿边 from->to 传播，执行回边检查与帧合流。
func (e *engine) propagate(step EdgeStep, out *frame) error {
	if step.To <= step.From && out.hasUninit() {
		return e.failEdge(step, "回边携带未初始化引用")
	}
	if len(out.stack) > e.method.MaxStack {
		return e.failEdge(step, fmt.Sprintf("操作数栈深度 %d 超过 MaxStack %d", len(out.stack), e.method.MaxStack))
	}
	existing := e.frames[step.To]
	if existing == nil {
		e.frames[step.To] = out
		e.pred[step.To] = &step
		e.enqueue(step.To)
		return nil
	}
	changed, err := e.mergeInto(existing, out)
	if err != nil {
		return e.failEdge(step, err.Error())
	}
	if changed {
		e.pred[step.To] = &step
		e.enqueue(step.To)
	}
	return nil
}

// mergeInto 把 incoming 合流到 target：局部槽不相容置不可用，栈不相容直接拒绝。
// 已初始化类取最近公共祖先；未初始化引用要求同一 new 身份。
func (e *engine) mergeInto(target, incoming *frame) (bool, error) {
	changed := false
	for i := range target.locals {
		merged, incompatible := e.mergeValue(target.locals[i], incoming.locals[i])
		if incompatible {
			if !target.locals[i].isTop() {
				target.locals[i] = topValue()
				changed = true
			}
			continue
		}
		if merged != target.locals[i] {
			target.locals[i] = merged
			changed = true
		}
	}
	if len(target.stack) != len(incoming.stack) {
		return false, fmt.Errorf("栈合流长度不相容: %d 与 %d", len(target.stack), len(incoming.stack))
	}
	for i := range target.stack {
		merged, incompatible := e.mergeValue(target.stack[i], incoming.stack[i])
		if incompatible {
			return false, fmt.Errorf("栈合流类型不相容: %s 与 %s", target.stack[i], incoming.stack[i])
		}
		if merged != target.stack[i] {
			target.stack[i] = merged
			changed = true
		}
	}
	return changed, nil
}

// mergeValue 合流两个值。incompatible 为 true 表示类型不相容：
// 局部槽据此置不可用，操作数栈则应直接拒绝。
func (e *engine) mergeValue(a, b value) (merged value, incompatible bool) {
	if a.isTop() {
		return b, false
	}
	if b.isTop() {
		return a, false
	}
	if a.tag != b.tag {
		return topValue(), true
	}
	switch a.tag {
	case valUninit:
		if a.newPC == b.newPC && a.class == b.class {
			return a, false
		}
		return topValue(), true
	default:
		if a.class == b.class {
			return a, false
		}
		return initValue(e.hierarchy.LCA(a.class, b.class)), false
	}
}
