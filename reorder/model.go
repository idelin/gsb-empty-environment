// Package reorder 实现直线指令序列的重排合法性检查。
//
// 检查分两层：
//  1. 依赖分析（权威判定）：在原始序列上计算真依赖（RAW）、反依赖（WAR）、
//     输出依赖（WAW）与屏障顺序边，要求候选序列保持所有边的先后关系。
//  2. 有限初始状态穷举（辅助核对）：在有界取值域上枚举初始状态，逐条执行
//     两个序列并比较最终状态。结果相同不能证明无依赖，仅作交叉验证。
package reorder

import "fmt"

// Location 表示一个读写对象：命名寄存器，或半开内存区间 [Addr, Addr+Size)。
type Location struct {
	IsReg bool
	Reg   string
	Addr  uint64
	Size  uint64
}

// Reg 构造寄存器位置。
func Reg(name string) Location { return Location{IsReg: true, Reg: name} }

// Mem 构造内存区间位置 [addr, addr+size)。
func Mem(addr, size uint64) Location { return Location{Addr: addr, Size: size} }

// Overlaps 精确判断两个位置是否别名：寄存器按名字相等，
// 内存区间按半开区间是否相交判断。
func (a Location) Overlaps(b Location) bool {
	if a.IsReg || b.IsReg {
		return a.IsReg && b.IsReg && a.Reg == b.Reg
	}
	return a.Addr < b.Addr+b.Size && b.Addr < a.Addr+a.Size
}

func (l Location) String() string {
	if l.IsReg {
		return l.Reg
	}
	return memRef(l.Addr, l.Size)
}

// OpKind 指令操作类型。
type OpKind int

const (
	OpMov OpKind = iota
	OpLi
	OpAdd
	OpSub
	OpXor
	OpAnd
	OpOr
	OpLoad
	OpStore
	OpBarrier
)

func opName(k OpKind) string {
	switch k {
	case OpMov:
		return "mov"
	case OpLi:
		return "li"
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpXor:
		return "xor"
	case OpAnd:
		return "and"
	case OpOr:
		return "or"
	case OpLoad:
		return "load"
	case OpStore:
		return "store"
	case OpBarrier:
		return "barrier"
	}
	return "?"
}

// Inst 一条直线指令。不同操作类型使用不同字段：
//   - mov:  Dest, Src
//   - li:   Dest, Imm
//   - 算术: Dest, Src1, Src2
//   - load: Dest, Addr, Size
//   - store: Src, Addr, Size
type Inst struct {
	Kind OpKind
	Dest string
	Src  string
	Src1 string
	Src2 string
	Imm  uint64
	Addr uint64
	Size uint64
	Line int // 源文件行号（1 起），解析时填写
}

// Reads 返回指令读取的位置集合。
func (i Inst) Reads() []Location {
	switch i.Kind {
	case OpMov:
		return []Location{Reg(i.Src)}
	case OpAdd, OpSub, OpXor, OpAnd, OpOr:
		return []Location{Reg(i.Src1), Reg(i.Src2)}
	case OpLoad:
		return []Location{Mem(i.Addr, i.Size)}
	case OpStore:
		return []Location{Reg(i.Src)}
	default:
		return nil
	}
}

// Writes 返回指令写入的位置集合。
func (i Inst) Writes() []Location {
	switch i.Kind {
	case OpMov, OpLi, OpAdd, OpSub, OpXor, OpAnd, OpOr, OpLoad:
		return []Location{Reg(i.Dest)}
	case OpStore:
		return []Location{Mem(i.Addr, i.Size)}
	default:
		return nil
	}
}

// String 返回指令的规范文本形式，用于两序列间的指令匹配。
func (i Inst) String() string {
	switch i.Kind {
	case OpBarrier:
		return "barrier"
	case OpLi:
		return fmt.Sprintf("%s = li %d", i.Dest, i.Imm)
	case OpMov:
		return fmt.Sprintf("%s = mov %s", i.Dest, i.Src)
	case OpAdd, OpSub, OpXor, OpAnd, OpOr:
		return fmt.Sprintf("%s = %s %s, %s", i.Dest, opName(i.Kind), i.Src1, i.Src2)
	case OpLoad:
		return fmt.Sprintf("%s = load %s", i.Dest, memRef(i.Addr, i.Size))
	case OpStore:
		return fmt.Sprintf("%s = store %s", memRef(i.Addr, i.Size), i.Src)
	}
	return "?"
}

func memRef(addr, size uint64) string {
	if size == 1 {
		return fmt.Sprintf("[%d]", addr)
	}
	return fmt.Sprintf("[%d, %d)", addr, addr+size)
}
