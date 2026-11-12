package netsec

import (
	"errors"
	"fmt"
)

// EventKind 标识解释器记录的可观察事件类型。
type EventKind int

const (
	EvCheck  EventKind = iota // 检测回调被调用（访问前）
	EvRead                    // 实际发生的读
	EvWrite                   // 实际发生的写
	EvTrap                    // 进入陷阱块
	EvReturn                  // 函数返回
)

func (e EventKind) String() string {
	switch e {
	case EvCheck:
		return "check"
	case EvRead:
		return "read"
	case EvWrite:
		return "write"
	case EvTrap:
		return "trap"
	case EvReturn:
		return "return"
	default:
		return "?"
	}
}

// Event 是一次执行中的可观察事件，用于比较返回值与异常/访问顺序。
type Event struct {
	Kind   EventKind
	Source InstrID // 关联的原内存节点或检测节点
	Pos    Position
	Addr   int64
	Size   int
	Val    int64 // 写的值或返回值
	Err    error // 检测拒绝原因
}

// HostFunc 是解释器可调用的宿主函数。
type HostFunc func(args []int64) int64

// Interpreter 是与插桩逻辑解耦的独立 SSA 解释器。
// 同一份引擎既可跑原图，也可跑插桩后的图，保证对照公平。
type Interpreter struct {
	Mem     *Memory
	Hosts   map[string]HostFunc
	Checker Checker
	// MaxSteps 限制执行步数，防止输入图非有限时失控；0 表示默认值。
	MaxSteps int
}

// Result 是一次执行的产物。
type Result struct {
	Return int64
	Events []Event
	// Trap 为 true 表示通过陷阱块异常结束；Err 为检测拒绝错误。
	Trap bool
	Err  error
	// MemSnapshot 是执行结束时的内存副本，用于核对被拒写入无副作用。
	MemSnapshot map[int64]byte
}

// Run 从入口块 entry 开始解释 f，args 按 f.Params 顺序提供实参。
func (it *Interpreter) Run(f *Func, entry BlockID, args []int64) (*Result, error) {
	if f == nil {
		return nil, errors.New("netsec: nil func")
	}
	if f.BlockByID(entry) == nil {
		return nil, errors.New("netsec: unknown entry block")
	}
	if len(args) != len(f.Params) {
		return nil, fmt.Errorf("netsec: want %d args, got %d", len(f.Params), len(args))
	}
	max := it.MaxSteps
	if max <= 0 {
		max = 1_000_000
	}

	reg := make(map[Reg]int64, f.NumRegs+1)
	for i, r := range f.Params {
		reg[r] = args[i]
	}
	instrByID := make(map[InstrID]*Instr)
	for _, b := range f.Blocks {
		for _, ins := range b.Instrs {
			instrByID[ins.ID] = ins
		}
	}
	guarded := make(map[InstrID]bool)
	for _, b := range f.Blocks {
		for _, ins := range b.Instrs {
			if ins.Op == OpCheck {
				for _, a := range ins.Args {
					guarded[InstrID(a)] = true
				}
			}
		}
	}
	var mem *Memory
	if it.Mem != nil {
		mem = it.Mem.Clone()
	} else {
		mem = NewMemory()
	}
	res := &Result{}

	cur := entry
	var pred BlockID // 上一个执行块，用于解析 phi
	havePred := false
	steps := 0
	for {
		if steps++; steps > max {
			return nil, errors.New("netsec: step limit exceeded (graph may not be finite)")
		}
		b := f.BlockByID(cur)
		if b == nil {
			return nil, fmt.Errorf("netsec: missing block %d", cur)
		}

		for _, ins := range b.Instrs {
			switch ins.Op {
			case OpPhi:
				v, ok := phiValue(ins, pred, havePred)
				if !ok {
					return nil, fmt.Errorf("netsec: phi %d has no incoming from pred %d", ins.ID, pred)
				}
				reg[ins.Result] = reg[v]

			case OpConst:
				reg[ins.Result] = ins.Val

			case OpAdd:
				reg[ins.Result] = reg[ins.Args[0]] + reg[ins.Args[1]]

			case OpSub:
				reg[ins.Result] = reg[ins.Args[0]] - reg[ins.Args[1]]

			case OpCheck:
				// 优先按携带的源节点 ID 全局定位被守护访问（trap 模式跨块），
				// 回调模式下回退到块内紧随其后的内存指令。
				var memInstr *Instr
				if len(ins.Args) > 0 {
					memInstr = instrByID[InstrID(ins.Args[0])]
				}
				if memInstr == nil || !memInstr.Op.IsMemory() {
					memInstr = findMemoryAfter(b, ins)
				}
				if memInstr == nil {
					return nil, fmt.Errorf("netsec: check %d not guarding a memory op", ins.ID)
				}
				addr := reg[memInstr.Args[0]]
				acc := Access{
					Kind:   kindOf(memInstr.Op),
					Addr:   addr,
					Size:   memInstr.Size,
					Source: memInstr.ID,
					Pos:    ins.Pos,
				}
				var err error
				if it.Checker != nil {
					err = it.Checker.Check(acc)
				}
				res.Events = append(res.Events, Event{
					Kind: EvCheck, Source: memInstr.ID, Pos: ins.Pos,
					Addr: addr, Size: memInstr.Size, Err: err,
				})
				if ins.Result != 0 {
					// trap 模式用寄存器承载“是否拒绝”，非零=拒绝；
					// callback 模式 check 无结果寄存器，直接按异常边终止。
					if err != nil {
						reg[ins.Result] = 1
					} else {
						reg[ins.Result] = 0
					}
				} else if err != nil {
					res.Err = err
					res.MemSnapshot = mem.Snapshot()
					return res, nil // 访问前拒绝：纯回调模式按异常边语义终止
				}

			case OpLoad:
				addr := reg[ins.Args[0]]
				if !guarded[ins.ID] {
					if rej := it.preCheck(ins, addr, KindRead, res, mem); rej != nil {
						return res, nil
					}
				}
				v, err := mem.Read(addr, ins.Size)
				if err != nil {
					res.Err = err
					res.MemSnapshot = mem.Snapshot()
					return res, nil
				}
				reg[ins.Result] = v
				res.Events = append(res.Events, Event{
					Kind: EvRead, Source: ins.ID, Pos: ins.Pos, Addr: addr, Size: ins.Size, Val: v,
				})

			case OpStore:
				addr := reg[ins.Args[0]]
				v := reg[ins.Args[1]]
				if !guarded[ins.ID] {
					if rej := it.preCheck(ins, addr, KindWrite, res, mem); rej != nil {
						return res, nil
					}
				}
				if err := mem.Write(addr, ins.Size, v); err != nil {
					res.Err = err
					res.MemSnapshot = mem.Snapshot()
					return res, nil
				}
				res.Events = append(res.Events, Event{
					Kind: EvWrite, Source: ins.ID, Pos: ins.Pos, Addr: addr, Size: ins.Size, Val: v,
				})

			case OpCall:
				fn := it.Hosts[ins.Func]
				if fn == nil {
					return nil, fmt.Errorf("netsec: unknown host function %q", ins.Func)
				}
				callArgs := make([]int64, len(ins.Args))
				for i, a := range ins.Args {
					callArgs[i] = reg[a]
				}
				if ins.Result != 0 {
					reg[ins.Result] = fn(callArgs)
				} else {
					fn(callArgs)
				}

			case TermJump:
			case TermBranch:
			case TermReturn:
			case TermTrap:
				res.Trap = true
				res.Events = append(res.Events, Event{Kind: EvTrap, Pos: ins.Pos})
				res.MemSnapshot = mem.Snapshot()
				return res, nil

			default:
				return nil, fmt.Errorf("netsec: unsupported op %s", ins.Op)
			}
		}

		term := b.Instrs[len(b.Instrs)-1]
		switch term.Op {
		case TermJump:
			pred, havePred = cur, true
			cur = term.Succ[0]
		case TermBranch:
			cond := reg[term.Args[0]]
			next := term.Succ[1]
			if cond != 0 {
				next = term.Succ[0]
			}
			pred, havePred = cur, true
			cur = next
		case TermReturn:
			if len(term.Args) > 0 {
				res.Return = reg[term.Args[0]]
			}
			res.Events = append(res.Events, Event{Kind: EvReturn, Pos: term.Pos, Val: res.Return})
			res.MemSnapshot = mem.Snapshot()
			return res, nil
		default:
			return nil, fmt.Errorf("netsec: block %d not terminated correctly", b.ID)
		}
	}
}

func phiValue(ins *Instr, pred BlockID, havePred bool) (ValueID, bool) {
	for _, e := range ins.Incoming {
		if havePred && e.Pred == pred {
			return e.Val, true
		}
	}
	// 入口块首条即 phi 且无前驱时，取第一条入边作为 SSA 初始值。
	if !havePred && len(ins.Incoming) > 0 {
		return ins.Incoming[0].Val, true
	}
	return 0, false
}

func findMemoryAfter(b *Block, check *Instr) *Instr {
	found := false
	for _, ins := range b.Instrs {
		if ins == check {
			found = true
			continue
		}
		if found {
			if ins.Op.IsMemory() {
				return ins
			}
			if ins.Op.IsTerminator() {
				return nil
			}
		}
	}
	return nil
}

// preCheck 在解释“原始” load/store 前运行宿主检测，使未插桩基线图
// 也能在访问前体现拒绝语义，便于与插桩图比较异常顺序。返回非 nil 表示已拒绝。
func (it *Interpreter) preCheck(ins *Instr, addr int64, kind Kind, res *Result, mem *Memory) error {
	if it.Checker == nil {
		return nil
	}
	acc := Access{Kind: kind, Addr: addr, Size: ins.Size, Source: ins.ID, Pos: ins.Pos}
	err := it.Checker.Check(acc)
	res.Events = append(res.Events, Event{
		Kind: EvCheck, Source: ins.ID, Pos: ins.Pos, Addr: addr, Size: ins.Size, Err: err,
	})
	if err != nil {
		res.Err = err
		res.MemSnapshot = mem.Snapshot()
		return err
	}
	return nil
}
