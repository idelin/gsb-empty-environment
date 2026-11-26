package dfa

// AttackResult 是一次故障可达性搜索的结论。
type AttackResult struct {
	Reachable    bool
	FaultsUsed   int
	Witness      []Fault // 到达 unlock 的具体故障序列（可直接交给 Replay 复核）
	UnlockStep   int
	BudgetExceed bool
}

type stateKey struct {
	pc      int
	bools   uint8 // 6 个布尔寄存器压缩成 6 位
	counter int
}

type nodeKey struct {
	state     stateKey
	remaining int
}

type queueEntry struct {
	state     State
	remaining int
}

type predInfo struct {
	parent   nodeKey
	fault    Fault // Kind 为空表示无故障边
	hasFault bool
}

// Attack 在故障预算 budget 内搜索任意到达 unlock 的轨迹。
//
// 搜索空间按 (pc, 6 个布尔位, 计数器, 剩余故障数) 记忆化，使用 BFS 展开，
// 因而返回的 Witness 是步数最浅、且故障数最少的攻击见证之一。该见证可
// 直接交给 Replay 独立重放，确认 UnlockCount 确实大于 0。
func Attack(p *Program, in Input, budget int) (*AttackResult, error) {
	if err := validateInput(p, in); err != nil {
		return nil, err
	}
	if budget < 0 {
		budget = 0
	}
	a := &analyzer{prog: p, in: in, budget: budget}
	return a.run(false)
}

// SecureAgainstK 仅返回布尔结论，供批量安全回归使用，跳过前驱记账。
func SecureAgainstK(p *Program, in Input, budget int) (bool, error) {
	if err := validateInput(p, in); err != nil {
		return false, err
	}
	if budget < 0 {
		budget = 0
	}
	a := &analyzer{prog: p, in: in, budget: budget}
	res, err := a.run(true)
	if err != nil {
		return false, err
	}
	return !res.Reachable, nil
}

type analyzer struct {
	prog         *Program
	in           Input
	budget       int
	seen         map[nodeKey]bool
	pred         map[nodeKey]predInfo
	goalNode     nodeKey
	goalFault    Fault
	goalHasFault bool
	goalStep     int
	goalUsed     int
	budgetExceed bool
}

func keyOf(st State) stateKey {
	var packed uint8
	for i := 0; i < MaxBoolRegs; i++ {
		if st.Bools[i] {
			packed |= 1 << i
		}
	}
	return stateKey{pc: st.PC, bools: packed, counter: st.Counter}
}

func (a *analyzer) run(lightweight bool) (*AttackResult, error) {
	a.seen = map[nodeKey]bool{}
	if !lightweight {
		a.pred = map[nodeKey]predInfo{}
	}
	root := queueEntry{state: State{}, remaining: a.budget}
	rootNK := nodeKey{state: keyOf(root.state), remaining: root.remaining}
	a.seen[rootNK] = true
	queue := []queueEntry{root}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		st := cur.state
		if st.Steps >= MaxSteps {
			a.budgetExceed = true
			continue
		}
		if st.PC < 0 || st.PC >= len(a.prog.Instrs) {
			continue
		}
		ins := a.prog.Instrs[st.PC]
		parentNK := nodeKey{state: keyOf(st), remaining: cur.remaining}

		// 无故障边（halt 步唯一允许的分支）。
		base := cloneState(st)
		baseTerm := a.apply(&base, ins, false, Fault{})
		if ins.Op == OpUnlock {
			a.goalNode = parentNK
			a.goalStep = st.Steps
			a.goalUsed = a.budget - cur.remaining
			return a.buildResult(lightweight)
		}
		if !baseTerm {
			a.push(&queue, base, cur.remaining, parentNK, Fault{}, false, lightweight)
		}

		// 故障边：skip / flip(r0..r5) / invert（仅条件跳转）。
		if cur.remaining > 0 && ins.Op != OpHalt {
			skip := cloneState(st)
			skipF := Fault{Step: st.Steps, Kind: FaultSkip}
			skipTerm := a.apply(&skip, ins, true, skipF)
			if ins.Op == OpUnlock {
				a.goalNode = parentNK
				a.goalHasFault = true
				a.goalFault = skipF
				a.goalStep = st.Steps
				a.goalUsed = a.budget - cur.remaining + 1
				return a.buildResult(lightweight)
			}
			if !skipTerm {
				a.push(&queue, skip, cur.remaining-1, parentNK, skipF, true, lightweight)
			}

			for r := 0; r < MaxBoolRegs; r++ {
				fl := cloneState(st)
				fl.Bools[r] = !fl.Bools[r]
				flF := Fault{Step: st.Steps, Kind: FaultFlip, Reg: r}
				flTerm := a.apply(&fl, ins, true, flF)
				if ins.Op == OpUnlock {
					a.goalNode = parentNK
					a.goalHasFault = true
					a.goalFault = flF
					a.goalStep = st.Steps
					a.goalUsed = a.budget - cur.remaining + 1
					return a.buildResult(lightweight)
				}
				if !flTerm {
					a.push(&queue, fl, cur.remaining-1, parentNK, flF, true, lightweight)
				}
			}

			if ins.Op == OpJz || ins.Op == OpJt {
				iv := cloneState(st)
				ivF := Fault{Step: st.Steps, Kind: FaultInvert}
				ivTerm := a.apply(&iv, ins, true, ivF)
				if !ivTerm {
					a.push(&queue, iv, cur.remaining-1, parentNK, ivF, true, lightweight)
				}
			}
		}
	}
	return &AttackResult{BudgetExceed: a.budgetExceed}, nil
}

// push 把新状态压入 BFS 队列；已访问的 (状态, 剩余故障数) 直接丢弃。
func (a *analyzer) push(queue *[]queueEntry, st State, remaining int, parent nodeKey, f Fault, hasF bool, lightweight bool) {
	nk := nodeKey{state: keyOf(st), remaining: remaining}
	if a.seen[nk] {
		return
	}
	a.seen[nk] = true
	if !lightweight {
		if _, ok := a.pred[nk]; !ok {
			a.pred[nk] = predInfo{parent: parent, fault: f, hasFault: hasF}
		}
	}
	*queue = append(*queue, queueEntry{state: st, remaining: remaining})
}

// apply 与 vm.go 的 stepVM 保持同一语义，但独立实现（不共享代码），
// 这样两个检查器不会因为同一段实现 bug 一起出错。
func (a *analyzer) apply(st *State, ins Instr, hasFault bool, f Fault) bool {
	next := st.PC + 1
	if hasFault && f.Kind == FaultSkip {
		st.PC = next
		st.Steps++
		return false
	}
	switch ins.Op {
	case OpInp:
		st.Bools[ins.Dst] = a.in.Public[ins.InputBit]
	case OpLds:
		st.Bools[ins.Dst] = a.in.SignatureValid
	case OpAnd:
		st.Bools[ins.Dst] = st.Bools[ins.Dst] && st.Bools[ins.Src]
	case OpNot:
		st.Bools[ins.Dst] = !st.Bools[ins.Src]
	case OpInc:
		st.Counter = (st.Counter + 1) % CounterMod
	case OpDec:
		st.Counter = (st.Counter - 1 + CounterMod) % CounterMod
	case OpJz:
		taken := st.Counter == 0
		if hasFault && f.Kind == FaultInvert {
			taken = !taken
		}
		if taken {
			next = ins.Target
		}
	case OpJt:
		taken := st.Bools[ins.Src]
		if hasFault && f.Kind == FaultInvert {
			taken = !taken
		}
		if taken {
			next = ins.Target
		}
	case OpJmp:
		next = ins.Target
	case OpHalt:
		st.PC = next
		st.Steps++
		return true
	}
	st.PC = next
	st.Steps++
	return false
}

func (a *analyzer) buildResult(lightweight bool) (*AttackResult, error) {
	res := &AttackResult{
		Reachable:    true,
		FaultsUsed:   a.goalUsed,
		UnlockStep:   a.goalStep,
		BudgetExceed: a.budgetExceed,
	}
	if lightweight {
		return res, nil
	}
	// 沿 pred 链回溯到根，收集沿途故障；再翻转回执行顺序。
	var faults []Fault
	cur := a.goalNode
	for {
		info, ok := a.pred[cur]
		if !ok {
			break
		}
		if info.hasFault {
			faults = append(faults, info.fault)
		}
		cur = info.parent
	}
	if a.goalHasFault {
		faults = append(faults, a.goalFault)
	}
	for i, j := 0, len(faults)-1; i < j; i, j = i+1, j-1 {
		faults[i], faults[j] = faults[j], faults[i]
	}
	res.Witness = faults
	return res, nil
}
