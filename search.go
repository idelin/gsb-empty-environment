package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// Outcome 是单次执行的结局。
type Outcome uint8

const (
	OutcomeHalt          Outcome = iota // 正常 halt 结束
	OutcomeBoundReached                 // 达到片段转移上界而停止
	OutcomeTrap                         // 越界访存或目标片段不存在
	OutcomeRejected                     // 目标类别不在允许集合中，被拒绝
	OutcomeVulnerability                // 无能力执行 readProtected，构成漏洞
)

// RunResult 记录单次确定性执行的结果。
type RunResult struct {
	Outcome     Outcome
	Chain       []uint8 // 实际执行的片段 ID 序列
	OpsExecuted uint64  // 执行的操作条数，用于工作预算核算
	FinalRegs   [NumRegisters]uint8
	FinalMem    [MemorySize]uint8
}

// Run 在给定输入下确定性执行模型，最多允许 maxTransitions 次片段转移。
func Run(spec *Spec, in *Input, maxTransitions int) RunResult {
	regs := spec.InitRegs
	mem := spec.InitMem
	for i := 0; i < NumRegisters; i++ {
		if len(spec.RegChoices[i]) > 0 {
			regs[i] = in.Regs[i]
		}
	}
	for j, slot := range spec.MemSlots {
		mem[slot.Index] = in.Mem[j]
	}

	byID := make(map[uint8]*Fragment, len(spec.Fragments))
	for i := range spec.Fragments {
		byID[spec.Fragments[i].ID] = &spec.Fragments[i]
	}

	res := RunResult{}
	cur, ok := byID[spec.Entry]
	if !ok {
		res.Outcome = OutcomeTrap
		return res
	}
	capable := false
	transitions := 0

	for {
		res.Chain = append(res.Chain, cur.ID)
		for _, op := range cur.Ops {
			res.OpsExecuted++
			switch op.Kind {
			case OpConst:
				regs[op.Dst] = op.Val
			case OpAdd:
				regs[op.Dst] += regs[op.Src]
			case OpXor:
				regs[op.Dst] ^= regs[op.Src]
			case OpLoad:
				addr := regs[op.Src]
				if addr >= MemorySize {
					res.Outcome = OutcomeTrap
					return finish(res, regs, mem)
				}
				regs[op.Dst] = mem[addr]
			case OpStore:
				addr := regs[op.Dst]
				if addr >= MemorySize {
					res.Outcome = OutcomeTrap
					return finish(res, regs, mem)
				}
				mem[addr] = regs[op.Src]
			case OpGrant:
				if spec.TrustedGrant[cur.ID] {
					capable = true
				}
			case OpReadProtected:
				if !capable {
					res.Outcome = OutcomeVulnerability
					return finish(res, regs, mem)
				}
			}
		}
		switch cur.End.Kind {
		case EndHalt:
			res.Outcome = OutcomeHalt
			return finish(res, regs, mem)
		case EndJump:
			if transitions >= maxTransitions {
				res.Outcome = OutcomeBoundReached
				return finish(res, regs, mem)
			}
			next, ok := byID[regs[cur.End.JumpReg]]
			if !ok {
				res.Outcome = OutcomeTrap
				return finish(res, regs, mem)
			}
			if !categoryAllowed(cur.End.Allowed, next.Category) {
				res.Outcome = OutcomeRejected
				return finish(res, regs, mem)
			}
			transitions++
			cur = next
		}
	}
}

func finish(res RunResult, regs [NumRegisters]uint8, mem [MemorySize]uint8) RunResult {
	res.FinalRegs = regs
	res.FinalMem = mem
	return res
}

func categoryAllowed(allowed []Category, c Category) bool {
	for _, a := range allowed {
		if a == c {
			return true
		}
	}
	return false
}

// Limits 是搜索界限：片段转移次数上界与总工作预算。
type Limits struct {
	MaxTransitions int    // 片段转移上界，不得超过 8
	WorkBudget     uint64 // 允许执行的操作总条数，0 表示不限
}

// DefaultLimits 返回题目规定的默认界限：至多 8 次片段转移，不限工作预算。
func DefaultLimits() Limits {
	return Limits{MaxTransitions: MaxTransitionsBound}
}

// Verdict 是有界搜索的结论。
type Verdict uint8

const (
	VerdictWitness  Verdict = iota // 找到字典序最小的漏洞见证
	VerdictNotFound                // 界内未发现见证
	VerdictUnknown                 // 超出工作预算，结论未知
)

// SearchResult 是有界搜索的输出。
type SearchResult struct {
	Verdict        Verdict
	Input          Input   // 见证输入（仅 VerdictWitness 时有效）
	Chain          []uint8 // 见证链（仅 VerdictWitness 时有效）
	InputsExplored uint64  // 已完整枚举的输入个数
}

// Search 按字典序枚举攻击者输入，返回第一个（即字典序最小）
// 每一步均通过类别检查、最终以无能力 readProtected 结束的输入与链。
// 所有输入枚举完仍无见证时报 VerdictNotFound；工作预算耗尽时报 VerdictUnknown。
func Search(spec *Spec, lim Limits) (SearchResult, error) {
	if err := validateSpec(spec, lim); err != nil {
		return SearchResult{}, err
	}

	domains := make([][]uint8, 0, NumRegisters+len(spec.MemSlots))
	for i := 0; i < NumRegisters; i++ {
		if len(spec.RegChoices[i]) > 0 {
			domains = append(domains, sortedUnique(spec.RegChoices[i]))
		} else {
			domains = append(domains, []uint8{spec.InitRegs[i]})
		}
	}
	for _, slot := range spec.MemSlots {
		domains = append(domains, sortedUnique(slot.Choices))
	}

	var res SearchResult
	idx := make([]int, len(domains))
	var spent uint64
	for {
		var in Input
		for i := 0; i < NumRegisters; i++ {
			in.Regs[i] = domains[i][idx[i]]
		}
		in.Mem = make([]uint8, len(spec.MemSlots))
		for j := range spec.MemSlots {
			in.Mem[j] = domains[NumRegisters+j][idx[NumRegisters+j]]
		}

		run := Run(spec, &in, lim.MaxTransitions)
		if lim.WorkBudget > 0 {
			if run.OpsExecuted > lim.WorkBudget-spent {
				res.Verdict = VerdictUnknown
				return res, nil
			}
			spent += run.OpsExecuted
		}
		res.InputsExplored++

		if run.Outcome == OutcomeVulnerability {
			res.Verdict = VerdictWitness
			res.Input = in
			res.Chain = run.Chain
			return res, nil
		}

		pos := len(domains) - 1
		for pos >= 0 {
			idx[pos]++
			if idx[pos] < len(domains[pos]) {
				break
			}
			idx[pos] = 0
			pos--
		}
		if pos < 0 {
			res.Verdict = VerdictNotFound
			return res, nil
		}
	}
}

func sortedUnique(vals []uint8) []uint8 {
	out := make([]uint8, len(vals))
	copy(out, vals)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 0
	for _, v := range out {
		if n == 0 || out[n-1] != v {
			out[n] = v
			n++
		}
	}
	return out[:n]
}

func validateSpec(spec *Spec, lim Limits) error {
	if len(spec.Fragments) == 0 {
		return errors.New("netsec: 至少需要一个片段")
	}
	if len(spec.Fragments) > MaxFragments {
		return fmt.Errorf("netsec: 片段数 %d 超过上限 %d", len(spec.Fragments), MaxFragments)
	}
	if lim.MaxTransitions < 0 || lim.MaxTransitions > MaxTransitionsBound {
		return fmt.Errorf("netsec: 转移上界 %d 超出允许范围 [0, %d]", lim.MaxTransitions, MaxTransitionsBound)
	}
	if len(spec.MemSlots) > MaxMemSlots {
		return fmt.Errorf("netsec: 受控内存字节数 %d 超过上限 %d", len(spec.MemSlots), MaxMemSlots)
	}
	seen := make(map[uint8]bool, len(spec.Fragments))
	entryOK := false
	for _, f := range spec.Fragments {
		if seen[f.ID] {
			return fmt.Errorf("netsec: 片段 ID %d 重复", f.ID)
		}
		seen[f.ID] = true
		if f.ID == spec.Entry {
			entryOK = true
		}
		if len(f.Ops) > MaxOps {
			return fmt.Errorf("netsec: 片段 %d 的操作数 %d 超过上限 %d", f.ID, len(f.Ops), MaxOps)
		}
		for _, op := range f.Ops {
			if err := validateOp(f.ID, op); err != nil {
				return err
			}
		}
		if f.End.Kind == EndJump && f.End.JumpReg >= NumRegisters {
			return fmt.Errorf("netsec: 片段 %d 的跳转寄存器编号 %d 越界", f.ID, f.End.JumpReg)
		}
	}
	if !entryOK {
		return fmt.Errorf("netsec: 入口片段 ID %d 不存在", spec.Entry)
	}
	for _, slot := range spec.MemSlots {
		if slot.Index >= MemorySize {
			return fmt.Errorf("netsec: 受控内存地址 %d 越界", slot.Index)
		}
		if len(slot.Choices) == 0 {
			return fmt.Errorf("netsec: 受控内存地址 %d 的候选值集合为空", slot.Index)
		}
	}
	return nil
}

func validateOp(fid uint8, op Op) error {
	regOK := func(r uint8) bool { return r < NumRegisters }
	switch op.Kind {
	case OpConst:
		if !regOK(op.Dst) {
			return fmt.Errorf("netsec: 片段 %d 的 const 目标寄存器 %d 越界", fid, op.Dst)
		}
	case OpAdd, OpXor, OpLoad, OpStore:
		if !regOK(op.Dst) || !regOK(op.Src) {
			return fmt.Errorf("netsec: 片段 %d 的操作寄存器编号越界", fid)
		}
	case OpGrant, OpReadProtected:
	default:
		return fmt.Errorf("netsec: 片段 %d 含未知操作类型 %d", fid, op.Kind)
	}
	return nil
}
