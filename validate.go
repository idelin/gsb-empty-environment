package netsec

import "fmt"

// ValidateProgram 对 IR 做结构性与声明一致性校验。
func ValidateProgram(prog *Program) error {
	if prog == nil {
		return fmt.Errorf("validate: 程序为空")
	}
	if len(prog.Blocks) == 0 {
		return fmt.Errorf("validate: 至少需要一个基本块")
	}
	if int(prog.Entry) >= len(prog.Blocks) {
		return fmt.Errorf("validate: 入口块 %d 越界", prog.Entry)
	}
	for i, r := range prog.Regions {
		if r.Size <= 0 {
			return fmt.Errorf("validate: 区域 %d 大小必须为正", i)
		}
		if r.Base < 0 {
			return fmt.Errorf("validate: 区域 %d 基址不能为负", i)
		}
		if _, overflow := addChecked(r.Base, r.Size); overflow {
			return fmt.Errorf("validate: 区域 %d 的 [Base,Base+Size) 溢出 int64", i)
		}
	}
	for i, in := range prog.Inputs {
		if len(in.Values) == 0 {
			return fmt.Errorf("validate: 输入 %d 必须声明非空有限域", i)
		}
	}
	for b := range prog.Blocks {
		if err := validateBlock(prog, BlockID(b)); err != nil {
			return err
		}
	}
	for h := range prog.Invariants {
		if int(h) >= len(prog.Blocks) {
			return fmt.Errorf("validate: 不变量引用了不存在的循环头 %d", h)
		}
	}
	return nil
}

func validateBlock(prog *Program, b BlockID) error {
	blk := &prog.Blocks[b]
	for i := range blk.Ops {
		op := &blk.Ops[i]
		switch op.Kind {
		case OpInput:
			if int(op.Input) >= len(prog.Inputs) {
				return bad(b, i, "OpInput 引用未声明输入")
			}
		case OpBase:
			if int(op.Region) >= len(prog.Regions) {
				return bad(b, i, "OpBase 引用未声明区域")
			}
		case OpAddConst, OpCopy:
			// 寄存器为跨块 SSA 风格，定义可达性由抽象框架在分析期处理。
		case OpLoad:
			if op.Size <= 0 {
				return bad(b, i, "读取大小必须为正")
			}
			if op.Region != IndirectRegion && int(op.Region) >= len(prog.Regions) {
				return bad(b, i, "OpLoad 引用未声明区域")
			}
		default:
			return bad(b, i, fmt.Sprintf("未知操作类型 %d", op.Kind))
		}
	}
	return validateTerm(prog, b, &blk.Term)
}

func validateTerm(prog *Program, b BlockID, term *Terminator) error {
	switch term.Kind {
	case TermReturn:
		return nil
	case TermJump:
		if int(term.Then) >= len(prog.Blocks) {
			return fmt.Errorf("validate: 块 %d 跳转目标越界", b)
		}
	case TermBranch:
		if int(term.Then) >= len(prog.Blocks) || int(term.Else) >= len(prog.Blocks) {
			return fmt.Errorf("validate: 块 %d 区间分支目标越界", b)
		}
		if term.Cmp < CmpEQ || term.Cmp > CmpGT {
			return fmt.Errorf("validate: 块 %d 区间分支比较符非法", b)
		}
	default:
		return fmt.Errorf("validate: 块 %d 终结符类型 %d 非法", b, term.Kind)
	}
	return nil
}

func bad(b BlockID, i int, msg string) error {
	return fmt.Errorf("validate: 块 %d 操作 %d: %s", b, i, msg)
}
