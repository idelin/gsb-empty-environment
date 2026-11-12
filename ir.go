package netsec

// Op 标识一条 SSA 指令或终结符的操作码。
type Op int

const (
	OpInvalid Op = iota
	// OpConst 把常量写入结果寄存器（Val 字段携带 int64）。
	OpConst
	// OpAdd 整数加法，参数为两个 ValueID。
	OpAdd
	// OpSub 整数减法 Args[0]-Args[1]。
	OpSub
	// OpLoad 从内存地址 Args[0] 读取 Size 字节。
	OpLoad
	// OpStore 把 Args[1] 的值写入地址 Args[0]，长度 Size。
	OpStore
	// OpCall 调用宿主函数 Func（参数 Args，结果写入 Result）。
	OpCall
	// OpCheck 是插桩产生的纯检测回调，绝不访问内存、绝不产生副作用。
	OpCheck
	// OpPhi 仅可作为块首指令，Incoming 给出 (前驱块ID -> 值)。
	OpPhi
)

const (
	// TermJump 无条件跳转，Succ 长度为 1。
	TermJump Op = 100 + iota
	// TermBranch 以 Args[0] 为条件，Succ[0]=true 目标，Succ[1]=false 目标。
	TermBranch
	// TermReturn 返回 Args[0]（无返回值时 Args 为空）。
	TermReturn
	// TermTrap 终止于陷阱块，表示异常控制流终点。
	TermTrap
)

func (o Op) String() string {
	switch o {
	case OpConst:
		return "const"
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpLoad:
		return "load"
	case OpStore:
		return "store"
	case OpCall:
		return "call"
	case OpCheck:
		return "check"
	case OpPhi:
		return "phi"
	case TermJump:
		return "jump"
	case TermBranch:
		return "branch"
	case TermReturn:
		return "return"
	case TermTrap:
		return "trap"
	default:
		return "invalid"
	}
}

// IsTerminator 判断操作码是否为块终结符。
func (o Op) IsTerminator() bool {
	return o == TermJump || o == TermBranch || o == TermReturn || o == TermTrap
}

// IsMemory 判断操作码是否为需要插桩的内存访问。
func (o Op) IsMemory() bool {
	return o == OpLoad || o == OpStore
}

// Position 是源码位置；插桩产生的节点保留被检测访问的原源位置。
type Position struct {
	File string
	Line int
	Col  int
}

// PhiEdge 是 Phi 指令的一条入边：来自块 Pred 的值 Val。
type PhiEdge struct {
	Pred BlockID
	Val  ValueID
}

// Instr 是有限 SSA 图中的一条指令（也是一个带源节点 ID 的节点）。
type Instr struct {
	ID       InstrID
	Op       Op
	Pos      Position
	Result   Reg // 无结果时为 0
	Size     int // OpLoad/OpStore 的访问字节数
	Val      int64
	Args     []ValueID
	Incoming []PhiEdge
	Func     string // OpCall 的宿主函数名；OpCheck 的回调名
	// 终结符字段
	Succ []BlockID
}

// Block 是基本块，指令以 Phi 开头、以终结符结尾。
type Block struct {
	ID     BlockID
	Instrs []*Instr
}

// Func 是一个有限 SSA 函数：块表、参数、值（寄存器）编号空间。
type Func struct {
	Name    string
	Params  []Reg
	Blocks  []*Block
	NumRegs int // 已使用的最大寄存器编号（含），分配新寄存器时递增

	nextBlock int
	nextInstr int
}

// BlockByID 按 ID 查找块，未找到返回 nil。
func (f *Func) BlockByID(id BlockID) *Block {
	for _, b := range f.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

// blockIndex 返回块在 Blocks 中的下标，未找到返回 -1。
func (f *Func) blockIndex(id BlockID) int {
	for i, b := range f.Blocks {
		if b.ID == id {
			return i
		}
	}
	return -1
}

// NewReg 在函数的寄存器空间中分配一个新寄存器。
func (f *Func) NewReg() Reg {
	f.NumRegs++
	return Reg(f.NumRegs)
}

// NewBlock 分配一个新合成块（位置由调用方按原源位置设置）。
func (f *Func) NewBlock() *Block {
	f.nextBlock++
	return &Block{ID: BlockID(f.nextBlock)}
}

// NewInstr 分配一条带源位置的新指令。
func (f *Func) NewInstr(op Op, pos Position) *Instr {
	f.nextInstr++
	return &Instr{ID: InstrID(f.nextInstr), Op: op, Pos: pos}
}

// initCounters 依据已有 ID 初始化合成计数器，使克隆后的函数仍能唯一分配。
func (f *Func) initCounters() {
	for _, b := range f.Blocks {
		if int(b.ID) > f.nextBlock {
			f.nextBlock = int(b.ID)
		}
		for _, ins := range b.Instrs {
			if int(ins.ID) > f.nextInstr {
				f.nextInstr = int(ins.ID)
			}
			if int(ins.Result) > f.NumRegs {
				f.NumRegs = int(ins.Result)
			}
		}
	}
	for _, r := range f.Params {
		if int(r) > f.NumRegs {
			f.NumRegs = int(r)
		}
	}
}

// Predecessors 扫描所有终结符，构造每个块的前驱块 ID 集合（去重）。
func (f *Func) Predecessors() map[BlockID][]BlockID {
	preds := make(map[BlockID][]BlockID)
	for _, b := range f.Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		term := b.Instrs[len(b.Instrs)-1]
		for _, s := range term.Succ {
			preds[s] = appendUniqueBlock(preds[s], b.ID)
		}
	}
	return preds
}

func appendUniqueBlock(xs []BlockID, x BlockID) []BlockID {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}
