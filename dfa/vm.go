package dfa

// Input 是一次执行的外部输入。
//   - Public[i] 是公开输入位 p_i，长度需与 Program.Width 一致。
//   - SignatureValid 是只读验签结果位，任何故障都不能修改它。
type Input struct {
	Public         []bool
	SignatureValid bool
}

// State 是 VM 的可变机器状态。
type State struct {
	PC      int
	Bools   [MaxBoolRegs]bool
	Counter int // 始终保持在 0..15
	Steps   int
}

// Step 记录一个执行步，便于宿主回归对故障轨迹做审计。
type Step struct {
	PC    int
	Op    Op
	Fault Fault
}

// Trace 是一次完整执行的可观察结果。
type Trace struct {
	UnlockCount  int
	Emits        []int
	Terminated   bool // 命中 halt
	BudgetExceed bool // 执行步数达到 MaxSteps 仍未 halt
	LastState    State
	Steps        []Step
}

// FaultKind 枚举三类单步故障。
type FaultKind string

const (
	FaultSkip   FaultKind = "skip"   // 跳过当前非 halt 指令
	FaultFlip   FaultKind = "flip"   // 翻转一个布尔寄存器
	FaultInvert FaultKind = "invert" // 反转一次条件跳转方向
)

// Fault 是一次故障注入。Step 为相对执行起始的步号（0 起算）。
type Fault struct {
	Step int
	Kind FaultKind
	Reg  int // flip 时指定 r0..r5
}

func cloneState(s State) State {
	return State{PC: s.PC, Bools: s.Bools, Counter: s.Counter, Steps: s.Steps}
}

func validateInput(p *Program, in Input) error {
	if len(in.Public) != p.Width {
		return asmErr(0, "公开输入位数=%d，与程序宽度 %d 不符", len(in.Public), p.Width)
	}
	return nil
}

// Run 执行无故障程序并返回完整轨迹。
func Run(p *Program, in Input) (*Trace, error) {
	return Replay(p, in, nil)
}

// Replay 按显式故障列表重放。故障必须按 Step 严格递增；halt 步不允许
// 注入故障，flip 的寄存器必须落在 r0..r5，invert 只能落在条件跳转上。
func Replay(p *Program, in Input, faults []Fault) (*Trace, error) {
	if err := validateInput(p, in); err != nil {
		return nil, err
	}
	st := State{}
	tr := &Trace{}
	fi := 0
	for {
		if st.Steps >= MaxSteps {
			tr.BudgetExceed = true
			tr.LastState = st
			return tr, nil
		}
		if st.PC < 0 || st.PC >= len(p.Instrs) {
			// 程序“跑出末尾”等价于停机，但不记为 halt 事件。
			tr.LastState = st
			return tr, nil
		}
		ins := p.Instrs[st.PC]
		var f Fault
		hasFault := false
		if fi < len(faults) && faults[fi].Step == st.Steps {
			f = faults[fi]
			hasFault = true
			fi++
		} else if fi < len(faults) && faults[fi].Step < st.Steps {
			return nil, asmErr(0, "故障列表必须按步号严格递增")
		}
		if hasFault {
			if ins.Op == OpHalt {
				return nil, asmErr(0, "halt 指令不可作为故障目标（步 %d）", st.Steps)
			}
			if f.Kind == FaultFlip && (f.Reg < 0 || f.Reg >= MaxBoolRegs) {
				return nil, asmErr(0, "flip 故障寄存器越界（步 %d）", st.Steps)
			}
			if f.Kind == FaultInvert && ins.Op != OpJz && ins.Op != OpJt {
				return nil, asmErr(0, "invert 故障只能落在 jz/jt 上（步 %d）", st.Steps)
			}
		}
		tr.Steps = append(tr.Steps, Step{PC: st.PC, Op: ins.Op, Fault: f})
		if hasFault && f.Kind == FaultFlip {
			st.Bools[f.Reg] = !st.Bools[f.Reg]
		}
		stepVM(p, in, &st, ins, hasFault, f)
		st.Steps++
		if ins.Op == OpHalt && !(hasFault && f.Kind == FaultSkip) {
			tr.Terminated = true
			tr.LastState = st
			return tr, nil
		}
		if ins.Op == OpUnlock && !(hasFault && f.Kind == FaultSkip) {
			tr.UnlockCount++
		}
		if ins.Op == OpEmit && !(hasFault && f.Kind == FaultSkip) {
			tr.Emits = append(tr.Emits, ins.Tag)
		}
	}
}

// stepVM 执行单条指令的语义。故障 skip 仅推进 PC；invert 仅影响
// jz/jt 的跳转判定；flip 在调用前已作用于寄存器。
func stepVM(p *Program, in Input, st *State, ins Instr, hasFault bool, f Fault) {
	next := st.PC + 1
	if hasFault && f.Kind == FaultSkip {
		st.PC = next
		return
	}
	switch ins.Op {
	case OpInp:
		st.Bools[ins.Dst] = in.Public[ins.InputBit]
	case OpLds:
		// 验签位只读，直接来自输入，故障模型不提供改写通道。
		st.Bools[ins.Dst] = in.SignatureValid
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
	case OpEmit, OpUnlock, OpHalt:
		// 可观察/停机副作用由 Replay 统一记账。
	}
	st.PC = next
}

// AllInputs 枚举指定宽度下的全部公开输入组合，共 2^width 个。
func AllInputs(width int, sigValid bool) []Input {
	if width < 0 || width > MaxInputBits {
		return nil
	}
	total := 1 << width
	out := make([]Input, 0, total)
	for mask := 0; mask < total; mask++ {
		bits := make([]bool, width)
		for i := 0; i < width; i++ {
			bits[i] = (mask>>i)&1 == 1
		}
		out = append(out, Input{Public: bits, SignatureValid: sigValid})
	}
	return out
}
