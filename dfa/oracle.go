package dfa

import "fmt"

// OracleResult 是全枚举预言机的结论。
type OracleResult struct {
	Reachable       bool
	Witness         []Fault
	PlansExplored   int // 枚举到的完整/部分故障计划数（含提前终止分支）
	TrajectoriesRun int // 实际执行到终止或步数上限的轨迹数
}

// ExhaustiveOracle 独立地全枚举所有至多 budget 次故障的故障计划。
//
// 与 analyzer.go 的 BFS 不同，这里：
//   - 不做任何状态记忆化，每条故障计划都从初始状态独立执行；
//   - 用手写递归按“当前步可选的故障动作”展开；
//   - 自己维护一套机器状态推进逻辑，不复用 stepVM/analyzer.apply。
//
// 因此它与 Attack 是两份独立实现，可互为交叉对照。
func ExhaustiveOracle(p *Program, in Input, budget int) (*OracleResult, error) {
	if err := validateInput(p, in); err != nil {
		return nil, err
	}
	if budget < 0 {
		budget = 0
	}
	o := &oracle{prog: p, in: in, budget: budget}
	o.dfs(State{}, nil)
	return &OracleResult{
		Reachable:       o.hit,
		Witness:         o.witness,
		PlansExplored:   o.plans,
		TrajectoriesRun: o.runs,
	}, nil
}

type oracle struct {
	prog    *Program
	in      Input
	budget  int
	hit     bool
	witness []Fault
	plans   int
	runs    int
}

// dfs 在“当前机器状态”上枚举一个执行步的全部合法选择。
// prefix 是截至当前步之前已经注入的故障（按步号递增）。
func (o *oracle) dfs(st State, prefix []Fault) {
	if o.hit {
		return
	}
	if st.Steps >= MaxSteps {
		o.plans++
		o.runs++
		return
	}
	if st.PC < 0 || st.PC >= len(o.prog.Instrs) {
		o.plans++
		o.runs++
		return
	}
	ins := o.prog.Instrs[st.PC]

	// 选择 1：本步不注入故障。
	o.branch(st, ins, prefix, Fault{}, false)

	// 选择 2..：在剩余预算允许时枚举全部故障动作。
	used := len(prefix)
	if used < o.budget && ins.Op != OpHalt {
		o.branch(st, ins, prefix, Fault{Step: st.Steps, Kind: FaultSkip}, true)
		for r := 0; r < MaxBoolRegs; r++ {
			o.branch(st, ins, prefix, Fault{Step: st.Steps, Kind: FaultFlip, Reg: r}, true)
		}
		if ins.Op == OpJz || ins.Op == OpJt {
			o.branch(st, ins, prefix, Fault{Step: st.Steps, Kind: FaultInvert}, true)
		}
	}
}

func (o *oracle) branch(st State, ins Instr, prefix []Fault, f Fault, hasFault bool) {
	if o.hit {
		return
	}
	next := st
	if hasFault && f.Kind == FaultFlip {
		next.Bools[f.Reg] = !next.Bools[f.Reg]
	}
	unlocked := ins.Op == OpUnlock && !(hasFault && f.Kind == FaultSkip)
	terminated := o.advance(&next, ins, hasFault, f)

	if unlocked {
		o.plans++
		o.runs++
		w := make([]Fault, 0, len(prefix)+1)
		w = append(w, prefix...)
		if hasFault {
			w = append(w, f)
		}
		o.hit = true
		o.witness = w
		return
	}
	if terminated {
		o.plans++
		o.runs++
		return
	}
	np := prefix
	if hasFault {
		np = make([]Fault, 0, len(prefix)+1)
		np = append(np, prefix...)
		np = append(np, f)
	}
	o.dfs(next, np)
}

// advance 是预言机自带的单步语义，刻意与其它文件独立。
func (o *oracle) advance(st *State, ins Instr, hasFault bool, f Fault) bool {
	nextPC := st.PC + 1
	if hasFault && f.Kind == FaultSkip {
		st.PC = nextPC
		st.Steps++
		return false
	}
	switch ins.Op {
	case OpInp:
		st.Bools[ins.Dst] = o.in.Public[ins.InputBit]
	case OpLds:
		st.Bools[ins.Dst] = o.in.SignatureValid
	case OpAnd:
		st.Bools[ins.Dst] = st.Bools[ins.Dst] && st.Bools[ins.Src]
	case OpNot:
		st.Bools[ins.Dst] = !st.Bools[ins.Src]
	case OpInc:
		st.Counter = (st.Counter + 1) % CounterMod
	case OpDec:
		st.Counter = (st.Counter - 1 + CounterMod) % CounterMod
	case OpJz:
		jump := st.Counter == 0
		if hasFault && f.Kind == FaultInvert {
			jump = !jump
		}
		if jump {
			nextPC = ins.Target
		}
	case OpJt:
		jump := st.Bools[ins.Src]
		if hasFault && f.Kind == FaultInvert {
			jump = !jump
		}
		if jump {
			nextPC = ins.Target
		}
	case OpJmp:
		nextPC = ins.Target
	case OpHalt:
		st.PC = nextPC
		st.Steps++
		return true
	case OpEmit, OpUnlock:
		// 副作用由调用方/轨迹层处理。
	}
	st.PC = nextPC
	st.Steps++
	return false
}

// CrossCheckDisagreement 描述两份独立检查器的结论分歧。
type CrossCheckDisagreement struct {
	Input       Input
	BFSReach    bool
	OracleReach bool
	BFSWitness  []Fault
	OracleWit   []Fault
	Reason      string
}

// CrossCheck 对同一 (程序, 输入, 预算) 同时跑 BFS 与全枚举预言机，
// 并要求两者结论一致；若任一方声称可达，还会用 Replay 独立重放见证。
// 返回 nil 表示两份实现完全一致且见证可重放。
func CrossCheck(p *Program, in Input, budget int) (*CrossCheckDisagreement, error) {
	bfs, err := Attack(p, in, budget)
	if err != nil {
		return nil, err
	}
	orc, err := ExhaustiveOracle(p, in, budget)
	if err != nil {
		return nil, err
	}
	if bfs.Reachable != orc.Reachable {
		return &CrossCheckDisagreement{
			Input:       in,
			BFSReach:    bfs.Reachable,
			OracleReach: orc.Reachable,
			BFSWitness:  bfs.Witness,
			OracleWit:   orc.Witness,
			Reason:      "BFS 与全枚举预言机对可达性结论不一致",
		}, nil
	}
	if bfs.Reachable {
		if err := replayMustUnlock(p, in, bfs.Witness, "BFS"); err != nil {
			return &CrossCheckDisagreement{Input: in, BFSReach: true, OracleReach: true, Reason: err.Error()}, nil
		}
		if err := replayMustUnlock(p, in, orc.Witness, "oracle"); err != nil {
			return &CrossCheckDisagreement{Input: in, BFSReach: true, OracleReach: true, Reason: err.Error()}, nil
		}
	}
	return nil, nil
}

func replayMustUnlock(p *Program, in Input, faults []Fault, who string) error {
	tr, err := Replay(p, in, faults)
	if err != nil {
		return fmt.Errorf("%s 见证无法重放: %w", who, err)
	}
	if tr.UnlockCount == 0 {
		return fmt.Errorf("%s 声称可达但重放未观察到 unlock", who)
	}
	return nil
}
