package typeverifier

// emitEdges 发射一条指令的全部控制流边。
// normal 为正常边输出帧（return/throw 等终止指令为 nil）；
// exLocals 为异常边局部槽（nil 表示沿用入口局部槽）；killIDs 给出 init 需要失效的 new 身份。
func (e *engine) emitEdges(pc int, in, normal *frame, exLocals []value, killIDs map[int]bool) error {
	ins := e.method.Code[pc]

	if normal != nil {
		switch ins.Op {
		case OpGoto:
			if err := e.propagate(EdgeStep{From: pc, To: ins.TargetA}, normal); err != nil {
				return err
			}
		case OpBranch:
			if err := e.propagate(EdgeStep{From: pc, To: ins.TargetA}, normal); err != nil {
				return err
			}
			if err := e.propagate(EdgeStep{From: pc, To: ins.TargetB}, normal.clone()); err != nil {
				return err
			}
		default:
			if e.validPC(pc + 1) {
				if err := e.propagate(EdgeStep{From: pc, To: pc + 1}, normal); err != nil {
					return err
				}
			}
		}
	}

	return e.emitExceptionEdges(pc, in, exLocals, killIDs)
}

// emitExceptionEdges 为覆盖 pc 的每个可达异常处理器建立异常边：
// 清空操作数栈并压入已初始化的捕获类（缺省为 Throwable）。
func (e *engine) emitExceptionEdges(pc int, in *frame, exLocals []value, killIDs map[int]bool) error {
	for _, h := range e.method.Handlers {
		if pc < h.Start || pc >= h.End {
			continue
		}
		locals := exLocals
		if locals == nil {
			if len(killIDs) > 0 {
				locals = e.exceptionLocals(in.locals, killIDs)
			} else {
				locals = append([]value(nil), in.locals...)
			}
		}
		catch := h.CatchClass
		if catch == "" {
			catch = e.throwable
		}
		exFrame := &frame{
			locals: locals,
			stack:  []value{initValue(catch)},
		}
		step := EdgeStep{From: pc, To: h.HandlerPC, Exception: true, Handler: h.HandlerPC}
		if err := e.propagate(step, exFrame); err != nil {
			return err
		}
	}
	return nil
}
