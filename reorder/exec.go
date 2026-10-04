package reorder

// State 机器状态：寄存器堆 + 字节寻址内存（稀疏）。
type State struct {
	Regs map[string]uint64
	Mem  map[uint64]uint8
}

// NewState 创建全零状态。
func NewState() *State {
	return &State{Regs: make(map[string]uint64), Mem: make(map[uint64]uint8)}
}

// Clone 深拷贝状态。
func (s *State) Clone() *State {
	c := NewState()
	for k, v := range s.Regs {
		c.Regs[k] = v
	}
	for k, v := range s.Mem {
		c.Mem[k] = v
	}
	return c
}

// Execute 在给定状态上顺序执行指令序列。
// load/store 按小端序读写，最多 8 字节；算术按 uint64 回绕。
func Execute(insts []Inst, s *State) {
	for _, in := range insts {
		switch in.Kind {
		case OpBarrier:
			// 单线程顺序执行，屏障无语义动作
		case OpLi:
			s.Regs[in.Dest] = in.Imm
		case OpMov:
			s.Regs[in.Dest] = s.Regs[in.Src]
		case OpAdd:
			s.Regs[in.Dest] = s.Regs[in.Src1] + s.Regs[in.Src2]
		case OpSub:
			s.Regs[in.Dest] = s.Regs[in.Src1] - s.Regs[in.Src2]
		case OpXor:
			s.Regs[in.Dest] = s.Regs[in.Src1] ^ s.Regs[in.Src2]
		case OpAnd:
			s.Regs[in.Dest] = s.Regs[in.Src1] & s.Regs[in.Src2]
		case OpOr:
			s.Regs[in.Dest] = s.Regs[in.Src1] | s.Regs[in.Src2]
		case OpLoad:
			var v uint64
			for k := uint64(0); k < in.Size && k < 8; k++ {
				v |= uint64(s.Mem[in.Addr+k]) << (8 * k)
			}
			s.Regs[in.Dest] = v
		case OpStore:
			v := s.Regs[in.Src]
			for k := uint64(0); k < in.Size && k < 8; k++ {
				s.Mem[in.Addr+k] = uint8(v >> (8 * k))
			}
		}
	}
}
