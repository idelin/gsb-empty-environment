package netsec

// 机器模型常量：4 个 8 位寄存器、16 字节内存、最多 16 个片段、
// 每片段最多 4 条操作、攻击者最多控制 2 个内存字节、搜索至多 8 次片段转移。
const (
	NumRegisters        = 4
	MemorySize          = 16
	MaxFragments        = 16
	MaxOps              = 4
	MaxMemSlots         = 2
	MaxTransitionsBound = 8
)

// Category 是片段的类别标签，间接跳转时按目标类别做检查。
type Category uint8

// OpKind 枚举所有允许的操作。
type OpKind uint8

const (
	OpConst         OpKind = iota // regs[Dst] = Val
	OpAdd                         // regs[Dst] = (regs[Dst] + regs[Src]) mod 256
	OpXor                         // regs[Dst] = regs[Dst] ^ regs[Src]
	OpLoad                        // regs[Dst] = mem[regs[Src]]，越界陷阱
	OpStore                       // mem[regs[Dst]] = regs[Src]，越界陷阱
	OpGrant                       // 当前片段在可信授予集合中时产生能力
	OpReadProtected               // 无能力执行即构成漏洞
)

// Op 是一条机器操作。Dst/Src 为寄存器编号，Val 为常量。
type Op struct {
	Kind OpKind
	Dst  uint8
	Src  uint8
	Val  uint8
}

// Const 将常量 val 写入寄存器 dst。
func Const(dst, val uint8) Op { return Op{Kind: OpConst, Dst: dst, Val: val} }

// Add 计算 regs[dst] = (regs[dst] + regs[src]) mod 256。
func Add(dst, src uint8) Op { return Op{Kind: OpAdd, Dst: dst, Src: src} }

// Xor 计算 regs[dst] = regs[dst] ^ regs[src]。
func Xor(dst, src uint8) Op { return Op{Kind: OpXor, Dst: dst, Src: src} }

// Load 以 regs[addr] 为地址读取内存到 regs[dst]，地址越界立即陷阱。
func Load(dst, addr uint8) Op { return Op{Kind: OpLoad, Dst: dst, Src: addr} }

// Store 以 regs[addr] 为地址把 regs[src] 写入内存，地址越界立即陷阱。
func Store(addr, src uint8) Op { return Op{Kind: OpStore, Dst: addr, Src: src} }

// Grant 在当前片段属于宿主可信授予集合时赋予能力，否则为空操作。
func Grant() Op { return Op{Kind: OpGrant} }

// ReadProtected 检查能力：无能力执行即构成未授权读取漏洞。
func ReadProtected() Op { return Op{Kind: OpReadProtected} }

// EndKind 是片段结束方式：halt 或间接跳转。
type EndKind uint8

const (
	EndHalt EndKind = iota
	EndJump
)

// Ending 描述片段结束方式。间接跳转以 regs[JumpReg] 为目标片段 ID，
// 且目标片段类别必须属于 Allowed，否则该路径被拒绝。
type Ending struct {
	Kind    EndKind
	JumpReg uint8
	Allowed []Category
}

// Halt 构造 halt 结束方式。
func Halt() Ending { return Ending{Kind: EndHalt} }

// Jump 构造间接跳转结束方式：目标 ID 取自 regs[reg]，
// 目标类别必须属于 allowed。
func Jump(reg uint8, allowed ...Category) Ending {
	return Ending{Kind: EndJump, JumpReg: reg, Allowed: allowed}
}

// Fragment 是一个指令片段：类别、最多 4 条操作及结束方式。
type Fragment struct {
	ID       uint8
	Category Category
	Ops      []Op
	End      Ending
}

// MemSlot 是调用方指定的一个攻击者可控制内存字节及其候选值集合。
type MemSlot struct {
	Index   uint8
	Choices []uint8
}

// Spec 是宿主给出的完整评审输入。
type Spec struct {
	Fragments    []Fragment            // 最多 16 个，ID 唯一
	Entry        uint8                 // 入口片段 ID
	TrustedGrant map[uint8]bool        // 可信授予片段 ID 集合
	InitRegs     [NumRegisters]uint8   // 宿主给定的寄存器初值
	InitMem      [MemorySize]uint8     // 宿主给定的内存初值
	RegChoices   [NumRegisters][]uint8 // 攻击者对每个入口寄存器的候选值集合，空表示不可控
	MemSlots     []MemSlot             // 攻击者可控制的内存字节，最多 2 个
}

// Input 是攻击者的一次输入选择：入口寄存器值与受控内存字节值。
// Mem 与 Spec.MemSlots 平行。字典序定义为
// (r0, r1, r2, r3, slot0, slot1) 的升序。
type Input struct {
	Regs [NumRegisters]uint8
	Mem  []uint8
}
