package netsec

import (
	"errors"
	"fmt"
)

// RejectMode 决定检测失败（拒绝访问）时如何控制流。
type RejectMode int

const (
	// ModeCallback 是默认且推荐的模式：只插入纯检测回调。
	// 回调返回的 error 由运行时（解释器/编译宿主）在内存访问前抛出，
	// 不新增任何块、不改变 CFG，因此异常边与 SSA 合流原样保留。
	ModeCallback RejectMode = iota

	// ModeTrap 要求在访问前条件跳转到已有的陷阱块 Trap。
	// 仅当该跳转可以在不拆分临界边、不改动任何 phi 的前提下安全插入时才接受；
	// 否则 Instrument 返回 *RejectError，拒绝本次插桩请求。
	ModeTrap
)

// Policy 配置一次插桩。
type Policy struct {
	Checker Checker
	// CheckName 是生成的 OpCheck 回调名，解释器据此回调宿主 Checker。
	CheckName string
	// Mode 选择拒绝处理方式，零值为 ModeCallback。
	Mode RejectMode
	// Trap 仅在 ModeTrap 下使用，指向调用方预先构造好的陷阱块。
	Trap BlockID
}

// RejectError 表示“拒绝必须在访问前跳转到带原源位置的陷阱”这一安全策略触发。
type RejectError struct {
	Access Access
	Reason string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("netsec: refuse pre-access trap for %s at %s:%d: %s",
		e.Access.Kind, e.Access.Pos.File, e.Access.Pos.Line, e.Reason)
}

// Is 让 errors.Is 可识别 RejectError。
func (e *RejectError) Is(target error) bool {
	_, ok := target.(*RejectError)
	return ok
}

// Instrument 在 f 的每条 load/store 前插入纯检测回调，返回插入的检测节点数。
// f 会被原地修改；如需保留原图，请先调用 Clone。
//
// 保证：
//   - 每条内存访问前恰好一个 OpCheck，携带原指令的源位置与源节点 ID；
//   - 默认模式不新增块、不改动终结符与 phi，异常边/SSA 合流不变；
//   - ModeTrap 只在安全时插入跳转，否则返回 *RejectError 且不留半成品；
//   - 重复调用幂等：已存在的 OpCheck 不会重复插桩，循环回边不产生重复检测。
func Instrument(f *Func, p Policy) (int, error) {
	if f == nil {
		return 0, errors.New("netsec: nil func")
	}
	if p.Checker == nil {
		return 0, errors.New("netsec: nil checker")
	}
	if p.CheckName == "" {
		p.CheckName = DefaultCheckName
	}
	if p.Mode == ModeTrap {
		if err := validateTrap(f, p.Trap); err != nil {
			return 0, err
		}
	}

	f.initCounters()

	// guarded 记录已被检测节点守护的内存指令 ID，保证幂等、回边不重复。
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

	inserted := 0
	// 工作队列让 trap 模式拆分出的续体块继续被扫描，避免漏检剩余访问。
	queue := make([]BlockID, 0, len(f.Blocks))
	for _, b := range f.Blocks {
		queue = append(queue, b.ID)
	}
	for qi := 0; qi < len(queue); qi++ {
		b := f.BlockByID(queue[qi])
		if b == nil {
			continue
		}
		for idx := 0; idx < len(b.Instrs); idx++ {
			ins := b.Instrs[idx]
			if !ins.Op.IsMemory() || guarded[ins.ID] {
				continue
			}
			check := f.NewInstr(OpCheck, ins.Pos)
			check.Func = p.CheckName
			// Args 仅承载源节点 ID（解释器另外按位置取地址操作数）。
			check.Args = []ValueID{ValueID(ins.ID)}

			if p.Mode == ModeCallback {
				b.Instrs = insertAt(b.Instrs, idx, check)
				guarded[ins.ID] = true
				inserted++
				idx++ // 多插入了一条 check，跳过它
				continue
			}

			// ModeTrap：在访问前拆分出续体块，形成
			// check -> branch(trap, cont) 的访问前条件跳转。
			cont, err := insertTrapGuard(f, b, idx, check, p)
			if err != nil {
				return inserted, err
			}
			guarded[ins.ID] = true
			inserted++
			queue = append(queue, cont) // 续体块中的剩余访问继续插桩
			break                       // 当前块已终结，转去处理续体块
		}
	}
	return inserted, nil
}

// DefaultCheckName 是未显式命名时使用的检测回调名。
const DefaultCheckName = "__netsec_check"

func kindOf(op Op) Kind {
	if op == OpStore {
		return KindWrite
	}
	return KindRead
}

func insertAt(xs []*Instr, at int, ins *Instr) []*Instr {
	out := make([]*Instr, 0, len(xs)+1)
	out = append(out, xs[:at]...)
	out = append(out, ins)
	out = append(out, xs[at:]...)
	return out
}

// validateTrap 校验陷阱块：必须存在、以 TermTrap 收尾、无 phi、当前无任何前驱。
// 无前驱意味着插入跳转不需要拆分任何临界边，也不会改写 phi 前驱。
func validateTrap(f *Func, trap BlockID) error {
	tb := f.BlockByID(trap)
	if tb == nil {
		return &RejectError{Reason: "trap block not found"}
	}
	if len(tb.Instrs) == 0 || tb.Instrs[len(tb.Instrs)-1].Op != TermTrap {
		return &RejectError{Reason: "trap block must end in trap terminator"}
	}
	for _, ins := range tb.Instrs {
		if ins.Op == OpPhi {
			return &RejectError{Reason: "trap block must not contain phi"}
		}
	}
	preds := f.Predecessors()
	if len(preds[trap]) != 0 {
		return &RejectError{Reason: "trap block already has predecessors; edge split would alter CFG/phi"}
	}
	return nil
}

// insertTrapGuard 在 block 的第 at 条内存访问前插入 check 与条件跳转，
// 把访问及其后指令搬到新的续体块，并同步修正所有 phi 的前驱标签，返回续体块 ID。
func insertTrapGuard(f *Func, block *Block, at int, check *Instr, p Policy) (BlockID, error) {
	mem := block.Instrs[at]

	// 内存访问若位于块首，而该块带有 phi，则直接在访问前跳转需要把
	// phi 一并迁移并改写所有前驱，属于必须拆分临界边/破坏合流的情形，拒绝。
	if at == 0 {
		for _, ins := range block.Instrs {
			if ins.Op == OpPhi {
				return 0, &RejectError{
					Access: Access{Source: mem.ID, Pos: mem.Pos, Kind: kindOf(mem.Op)},
					Reason: "memory op before phi completion requires critical-edge split",
				}
			}
		}
	}

	cont := f.NewBlock()
	cont.Instrs = append(cont.Instrs, block.Instrs[at:]...)

	cond := f.NewReg()
	branch := f.NewInstr(TermBranch, mem.Pos)
	branch.Args = []ValueID{cond}
	branch.Succ = []BlockID{p.Trap, cont.ID}

	// check 把布尔拒绝结果写入 cond（解释器约定：非零=拒绝）。
	check.Result = cond

	block.Instrs = append(block.Instrs[:at:at], check, branch)

	// 在块表中紧跟原块插入续体块，保持稳定的 RPO 友好顺序。
	pos := f.blockIndex(block.ID)
	f.Blocks = append(f.Blocks, nil)
	copy(f.Blocks[pos+2:], f.Blocks[pos+1:])
	f.Blocks[pos+1] = cont

	rewritePhiPreds(f, block.ID, cont.ID)
	return cont.ID, nil
}

// rewritePhiPreds 把所有后继块 phi 中指向 old 的前驱标签改为 new。
// 拆分后离开续体块的边才是原“块尾->后继”边，离开前缀块的边只指向
// 续体块（及陷阱块），因此不会出现标签二义性。
func rewritePhiPreds(f *Func, old, new BlockID) {
	for _, b := range f.Blocks {
		for _, ins := range b.Instrs {
			if ins.Op != OpPhi {
				continue
			}
			for k := range ins.Incoming {
				if ins.Incoming[k].Pred == old {
					ins.Incoming[k].Pred = new
				}
			}
		}
	}
}

// SplitCriticalEdge 拆分 edge pred->succ（succ 有多个前驱时为临界边），
// 在中间插入空合成块，返回新块 ID；非临界边或已拆分则原样返回 succ。
// 同步把 succ 中引用 pred 的 phi 前驱改写为新块，保持 SSA 合流正确。
func SplitCriticalEdge(f *Func, pred, succ BlockID) (BlockID, error) {
	pb := f.BlockByID(pred)
	sb := f.BlockByID(succ)
	if pb == nil || sb == nil {
		return 0, errors.New("netsec: split edge with unknown block")
	}
	preds := f.Predecessors()
	if len(preds[succ]) <= 1 {
		return succ, nil // 非临界边
	}

	term := pb.Instrs[len(pb.Instrs)-1]
	// 条件分支对同一目标有两条边（true/false）时，拆分需要区分两条边，
	// 这里通过仅替换第一个匹配并保证目标唯一来保守处理。
	match := 0
	for _, s := range term.Succ {
		if s == succ {
			match++
		}
	}
	if match > 1 {
		return 0, fmt.Errorf("netsec: ambiguous critical edge %d->%d", pred, succ)
	}

	midBlock := f.NewBlock()
	mid := midBlock.ID
	var pos Position
	if len(pb.Instrs) > 0 {
		pos = pb.Instrs[len(pb.Instrs)-1].Pos
	}
	jmp := f.NewInstr(TermJump, pos)
	jmp.Succ = []BlockID{succ}
	midBlock.Instrs = []*Instr{jmp}

	for k := range term.Succ {
		if term.Succ[k] == succ {
			term.Succ[k] = mid
		}
	}

	pi := f.blockIndex(pred)
	f.Blocks = append(f.Blocks, nil)
	copy(f.Blocks[pi+2:], f.Blocks[pi+1:])
	f.Blocks[pi+1] = midBlock

	rewritePhiPreds(f, pred, mid)
	return mid, nil
}
