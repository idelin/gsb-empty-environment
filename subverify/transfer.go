package subverify

var okResult = Result{Conclusion: ConclusionAccept}

// transfer 解释执行 pc 处的一条指令并返回后继。任何安全违例都以
// ConclusionReject 终止，并保留拒绝前的调用点链。
func (v *verifier) transfer(pc int, st *verifState) ([]successor, Result) {
	if !v.validTarget(pc) {
		return nil, v.reject(pc, st, "指令位置越界")
	}
	ins := v.prog.Code[pc]
	chain := chainText(st.frames)

	switch ins.Op {
	case OpJsr:
		target := ins.Arg
		if !v.validTarget(target) {
			return nil, v.reject(pc, st, "jsr 目标 %d 越界", target)
		}
		if len(st.frames) >= maxCallDepth {
			return nil, v.reject(pc, st, "子程序嵌套超过 %d 层", maxCallDepth)
		}
		for _, f := range st.frames {
			if f.entryPC == target {
				return nil, v.reject(pc, st, "活动调用中再次进入同一子程序 %d，按递归拒绝", target)
			}
		}
		if len(st.stack) >= v.prog.MaxStack {
			return nil, v.reject(pc, st, "jsr 压入返回令牌将超过栈深上限 %d", v.prog.MaxStack)
		}
		saved := append([]slotValue(nil), st.stack...)
		newChain := extendChain(chain, pc)
		// 返回令牌仅属于本次调用，其值（下一指令位置）不参与控制流判定；
		// ret 的跳转目标始终从受保护的调用帧中读取。
		st.stack = append(st.stack, slotValue{kind: vReturnToken, owner: newChain})
		st.frames = append(st.frames, callFrame{
			sitePC:     pc,
			entryPC:    target,
			returnPC:   pc + 1,
			savedStack: saved,
		})
		return []successor{{pc: target, chain: newChain, state: st}}, okResult

	case OpRet:
		slot := ins.Arg
		if slot < 0 || slot >= v.prog.NumSlots {
			return nil, v.reject(pc, st, "ret 槽号 %d 越界", slot)
		}
		if len(st.frames) == 0 {
			return nil, v.reject(pc, st, "ret 出现在根上下文中，没有活动子程序调用")
		}
		tok := st.locals[slot]
		switch tok.kind {
		case vTop:
			return nil, v.reject(pc, st, "ret 槽 %d 不可用（未初始化或合流失效）", slot)
		case vInt:
			return nil, v.reject(pc, st, "ret 槽 %d 持有整数，整数与返回令牌不可转换", slot)
		case vDeadToken:
			return nil, v.reject(pc, st, "ret 槽 %d 持有已失效的返回令牌别名", slot)
		case vReturnToken:
		default:
			return nil, v.reject(pc, st, "ret 槽 %d 类型非法", slot)
		}
		frame := st.frames[len(st.frames)-1]
		expectedOwner := chainText(st.frames)
		if tok.owner != expectedOwner {
			if !liveAt(tok.owner, expectedOwner) {
				return nil, v.reject(pc, st, "ret 令牌属于已结束的调用 %s，跨调用复用返回位置被拒绝", tok.owner)
			}
			return nil, v.reject(pc, st, "ret 令牌属于外层调用 %s，而最内层调用为 %s，返回位置归属不匹配", tok.owner, expectedOwner)
		}
		// 调用点栈必须恢复到 jsr 瞬间的深度，避免任何值借栈跨越调用边界；
		// 元素类型差异沿用合流规则（同位不相容在合流点降级为不可用）。
		if len(frame.savedStack) != len(st.stack) {
			return nil, v.reject(pc, st, "ret 时操作数栈深度 %d 与 jsr 快照 %d 不相容，禁止携带跨调用栈值", len(st.stack), len(frame.savedStack))
		}
		// 返回后，该令牌的全部别名与子程序内部派生拷贝在所有槽、栈中失效。
		invalidate(st, expectedOwner)

		// 栈恢复到 jsr 瞬间的快照；快照中不可能包含本次调用铸造的令牌。
		st.stack = append([]slotValue(nil), frame.savedStack...)
		st.frames = st.frames[:len(st.frames)-1]
		parentChain := chainText(st.frames)
		return []successor{{pc: frame.returnPC, chain: parentChain, state: st}}, okResult

	case OpStoreReturn:
		slot := ins.Arg
		if slot < 0 || slot >= v.prog.NumSlots {
			return nil, v.reject(pc, st, "storeReturn 槽号 %d 越界", slot)
		}
		if len(st.stack) == 0 {
			return nil, v.reject(pc, st, "storeReturn 时空栈弹栈")
		}
		top := st.stack[len(st.stack)-1]
		if top.kind != vReturnToken {
			if top.kind == vInt {
				return nil, v.reject(pc, st, "storeReturn 不能接收整数，整数与返回令牌不可转换")
			}
			if top.kind == vDeadToken {
				return nil, v.reject(pc, st, "storeReturn 不能保存已失效的返回令牌")
			}
			return nil, v.reject(pc, st, "storeReturn 栈顶不是可用的返回令牌")
		}
		st.locals[slot] = top
		st.stack = st.stack[:len(st.stack)-1]
		return v.seq(pc, chain, st)

	case OpLoadReturn:
		slot := ins.Arg
		if slot < 0 || slot >= v.prog.NumSlots {
			return nil, v.reject(pc, st, "loadReturn 槽号 %d 越界", slot)
		}
		tok := st.locals[slot]
		switch tok.kind {
		case vTop:
			return nil, v.reject(pc, st, "loadReturn 槽 %d 不可用（未初始化或合流失效）", slot)
		case vInt:
			return nil, v.reject(pc, st, "loadReturn 槽 %d 持有整数，整数与返回令牌不可转换", slot)
		case vDeadToken:
			return nil, v.reject(pc, st, "loadReturn 槽 %d 是已失效的返回令牌别名，拒绝加载", slot)
		case vReturnToken:
			if !liveAt(tok.owner, chain) {
				return nil, v.reject(pc, st, "loadReturn 令牌属于已结束的调用 %s，跨调用复用被拒绝", tok.owner)
			}
		default:
			return nil, v.reject(pc, st, "loadReturn 槽 %d 类型非法", slot)
		}
		if len(st.stack) >= v.prog.MaxStack {
			return nil, v.reject(pc, st, "loadReturn 压栈将超过栈深上限 %d", v.prog.MaxStack)
		}
		st.stack = append(st.stack, tok)
		return v.seq(pc, chain, st)

	case OpPushInt:
		if len(st.stack) >= v.prog.MaxStack {
			return nil, v.reject(pc, st, "pushInt 将超过栈深上限 %d", v.prog.MaxStack)
		}
		st.stack = append(st.stack, slotValue{kind: vInt})
		return v.seq(pc, chain, st)

	case OpStoreInt:
		slot := ins.Arg
		if slot < 0 || slot >= v.prog.NumSlots {
			return nil, v.reject(pc, st, "storeInt 槽号 %d 越界", slot)
		}
		if len(st.stack) == 0 {
			return nil, v.reject(pc, st, "storeInt 时空栈弹栈")
		}
		top := st.stack[len(st.stack)-1]
		if top.kind != vInt {
			if top.kind == vReturnToken {
				return nil, v.reject(pc, st, "storeInt 不能接收返回令牌，整数与返回令牌不可转换")
			}
			if top.kind == vDeadToken {
				return nil, v.reject(pc, st, "storeInt 不能接收已失效的返回令牌")
			}
			return nil, v.reject(pc, st, "storeInt 栈顶不是整数")
		}
		st.locals[slot] = top
		st.stack = st.stack[:len(st.stack)-1]
		return v.seq(pc, chain, st)

	case OpLoadInt:
		slot := ins.Arg
		if slot < 0 || slot >= v.prog.NumSlots {
			return nil, v.reject(pc, st, "loadInt 槽号 %d 越界", slot)
		}
		loc := st.locals[slot]
		if loc.kind != vInt {
			if loc.kind == vReturnToken || loc.kind == vDeadToken {
				return nil, v.reject(pc, st, "loadInt 槽 %d 持有返回令牌，整数与返回令牌不可转换", slot)
			}
			return nil, v.reject(pc, st, "loadInt 槽 %d 不可用（未初始化或合流失效）", slot)
		}
		if len(st.stack) >= v.prog.MaxStack {
			return nil, v.reject(pc, st, "loadInt 压栈将超过栈深上限 %d", v.prog.MaxStack)
		}
		st.stack = append(st.stack, loc)
		return v.seq(pc, chain, st)

	case OpPop:
		if len(st.stack) == 0 {
			return nil, v.reject(pc, st, "pop 时空栈弹栈")
		}
		st.stack = st.stack[:len(st.stack)-1]
		return v.seq(pc, chain, st)

	case OpGoto:
		target := ins.Arg
		if !v.validTarget(target) {
			return nil, v.reject(pc, st, "goto 目标 %d 越界", target)
		}
		return []successor{{pc: target, chain: chain, state: st}}, okResult

	case OpBranch:
		target := ins.Arg
		if !v.validTarget(target) {
			return nil, v.reject(pc, st, "非确定分支目标 %d 越界", target)
		}
		if !v.validTarget(pc + 1) {
			return nil, v.reject(pc, st, "非确定分支的顺序后继 pc+1 越界")
		}
		// 两个后继共享同一上下文；合流时按格规则合流，普通控制流环因此可收敛。
		return []successor{
			{pc: target, chain: chain, state: st.clone()},
			{pc: pc + 1, chain: chain, state: st},
		}, okResult

	case OpReturn:
		if len(st.frames) != 0 {
			return nil, v.reject(pc, st, "方法 return 时仍有 %d 个活动子程序调用", len(st.frames))
		}
		if len(st.stack) != 0 {
			return nil, v.reject(pc, st, "方法 return 时操作数栈非空（深度 %d），必须为空", len(st.stack))
		}
		return nil, okResult

	default:
		return nil, v.reject(pc, st, "未知或不支持的操作码 %d", ins.Op)
	}
}

// invalidate 把 owner 标识的返回令牌在全部局部槽与操作数栈中的出现替换为
// 失效令牌；失效后的任何加载或 ret 都会被拒绝。
func invalidate(st *verifState, owner string) {
	for i := range st.locals {
		if st.locals[i].kind == vReturnToken && st.locals[i].owner == owner {
			st.locals[i] = slotValue{kind: vDeadToken}
		}
	}
	for i := range st.stack {
		if st.stack[i].kind == vReturnToken && st.stack[i].owner == owner {
			st.stack[i] = slotValue{kind: vDeadToken}
		}
	}
}

func (v *verifier) seq(pc int, chain string, st *verifState) ([]successor, Result) {
	next := pc + 1
	if !v.validTarget(next) {
		return nil, v.reject(pc, st, "顺序后继 pc+1 越界，程序脱离指令数组")
	}
	return []successor{{pc: next, chain: chain, state: st}}, okResult
}
