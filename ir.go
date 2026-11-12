package netsec

// 本文件定义被验证插件的受限中间表示（IR）。
//
// 基本块内只允许三类地址相关操作：
//   - 基址加常数（OpBase / OpAddConst）
//   - 寄存器复制（OpCopy）
//   - 区间分支（TermBranch）
// 此外用 OpInput 引入外部输入、OpLoad 表示数据读取。
// 外部输入只能通过 TermBranch 表达的显式边界检查收窄。

// RegisterID 是寄存器在单个 Program 内的标识。
type RegisterID uint32

// BlockID 是基本块标识。
type BlockID uint32

// RegionID 是声明内存区域标识。
type RegionID uint32

// InputID 是外部输入通道标识。
type InputID uint32

// Region 是宿主声明的可访问内存区域：半开区间 [Base, Base+Size)。
// Base 与 Size 都在 int64 非负域内，且 Base+Size 不得溢出。
type Region struct {
	Name string
	Base int64
	Size int64
}

// End 返回区域的排他上界。
func (r Region) End() int64 { return r.Base + r.Size }

// ContainsOffset 判断区域是否完整覆盖相对偏移区间 [off, off+size)。
// off 是相对区域基址的偏移，合法范围为 [0, Size)；size 为读取字节数。
func (r Region) ContainsOffset(off, size int64) bool {
	if off < 0 || size <= 0 {
		return false
	}
	end, overflow := addChecked(off, size)
	if overflow {
		return false
	}
	return end <= r.Size
}

// Input 描述一条外部输入通道及其宿主声明的原始有限域。
// 该域是枚举解释器的取值集合；未经边界检查前分析器视其为外部顶。
type Input struct {
	Name string
	// Values 为该输入在有限输入解释器下的所有可能取值。
	Values []int64
}

// 操作类型枚举。
const (
	// OpInput: Dst = 外部输入 Input（未收窄前不可用于寻址）。
	OpInput uint8 = iota + 1
	// OpBase: Dst = 区域 Region 的基址（带来源集合）。
	OpBase
	// OpAddConst: Dst = Src + Const（基址加常数）。
	OpAddConst
	// OpCopy: Dst = Src（寄存器复制）。
	OpCopy
	// OpLoad: 从区域中读取 Size 字节到 Dst。
	OpLoad
)

// Op 是基本块内的一条操作。
type Op struct {
	Kind   uint8
	Dst    RegisterID
	Src    RegisterID // OpAddConst / OpCopy / OpLoad / 部分检查使用
	Base   RegisterID // OpLoad 的基址寄存器（须携带区域来源）
	Const  int64      // OpAddConst 的常数
	Region RegionID   // OpBase 的区域；OpLoad 的目标区域
	Input  InputID    // OpInput 的输入通道
	Size   int64      // OpLoad 的读取字节数（正数）
	// Site 是宿主赋予该操作的可追溯标识（用于证书与违例对齐）。
	Site string
}

// 区间比较条件枚举（仅作用于标量整数区间）。
const (
	CmpEQ uint8 = iota + 1 // 相等
	CmpNE                  // 不等
	CmpLE                  // 小于等于
	CmpLT                  // 小于
	CmpGE                  // 大于等于
	CmpGT                  // 大于
)

// Terminator 是基本块的终结符。
type Terminator struct {
	// 终结符种类：TermBranch / TermJump / TermReturn。
	Kind uint8
	// 区间分支的操作数与条件。
	Src   RegisterID
	Cmp   uint8
	Const int64
	Then  BlockID // 条件成立（或无条件跳转）目标
	Else  BlockID // 条件不成立目标
}

// 终结符种类枚举。
const (
	TermJump uint8 = iota + 1
	TermBranch
	TermReturn
)

// IndirectRegion 用作 OpLoad.Region 的哨兵：读取区域由 Base 寄存器的
// 来源集合决定，而非在操作里写死单一区域。它取值远大于任何合法区域下标。
const IndirectRegion RegionID = 1<<32 - 1

// Block 是一个基本块。
type Block struct {
	Name string
	Ops  []Op
	Term Terminator
}

// Program 是被验证的完整程序及其声明环境。
type Program struct {
	Regions []Region
	Inputs  []Input
	Blocks  []Block
	Entry   BlockID
	// Invariants 给出循环头块的给定不变量（寄存器 -> 仿射值）。
	Invariants map[BlockID]map[RegisterID]Affine
}
