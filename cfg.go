package sccp

// Op 是 8 位回绕运算的操作码。所有算术都在 uint8 上进行，
// 溢出自然回绕（mod 256）；除法在除数为零时触发异常（trap）。
type Op int

const (
	OpConst Op = iota // Dst = B.Imm
	OpCopy            // Dst = A
	OpAdd             // Dst = A + B (mod 256)
	OpSub             // Dst = A - B (mod 256)
	OpMul             // Dst = A * B (mod 256)
	OpDiv             // Dst = A / B；B 为零时 trap
	OpAnd             // Dst = A & B
	OpOr              // Dst = A | B
	OpXor             // Dst = A ^ B
	OpEq              // Dst = (A == B) ? 1 : 0
	OpNe              // Dst = (A != B) ? 1 : 0
	OpLt              // Dst = (A <  B) ? 1 : 0
	OpLe              // Dst = (A <= B) ? 1 : 0
	OpGt              // Dst = (A >  B) ? 1 : 0
	OpGe              // Dst = (A >= B) ? 1 : 0
)

// Operand 是指令的操作数：要么是常量，要么是变量。
type Operand struct {
	IsConst bool
	Var     string
	Imm     uint8
}

// C 构造常量操作数。
func C(v uint8) Operand { return Operand{IsConst: true, Imm: v} }

// V 构造变量操作数。
func V(name string) Operand { return Operand{Var: name} }

// Instr 是一条三地址指令：Dst = A op B。
type Instr struct {
	Dst  string
	Op   Op
	A, B Operand
}

// TermKind 是基本块终结符的种类。
type TermKind int

const (
	TermJump   TermKind = iota // 无条件跳转到 Next
	TermBranch                 // Cond != 0 跳 True，否则跳 False
	TermReturn                 // 正常结束
)

// Terminator 描述基本块末尾的控制流。
type Terminator struct {
	Kind        TermKind
	Cond        Operand // 仅 TermBranch 使用
	True, False int     // 仅 TermBranch 使用（目标块 ID）
	Next        int     // 仅 TermJump 使用
}

// Block 是一个基本块。
type Block struct {
	ID     int
	Name   string
	Instrs []Instr
	Term   Terminator
}

// CFG 是控制流图。Inputs 列出程序的输入变量（分析时视为 ⊤，
// 具体执行时由输入向量赋值）。
type CFG struct {
	Blocks []*Block
	Entry  int
	Inputs []string
}

// block 按 ID 索引，构造时 ID 必须与切片下标一致。
func (g *CFG) block(id int) *Block { return g.Blocks[id] }

// Successors 返回块的所有静态后继边（不做任何可达性判断）。
func (g *CFG) Successors(id int) []Edge {
	b := g.block(id)
	switch b.Term.Kind {
	case TermJump:
		return []Edge{{From: id, To: b.Term.Next}}
	case TermBranch:
		if b.Term.True == b.Term.False {
			return []Edge{{From: id, To: b.Term.True}}
		}
		return []Edge{{From: id, To: b.Term.True}, {From: id, To: b.Term.False}}
	default:
		return nil
	}
}

// Edge 是一条控制流边（From -> To，均为块 ID）。
type Edge struct {
	From, To int
}

// arith 执行具体的 8 位回绕运算。除法调用前必须保证 b != 0。
func arith(op Op, a, b uint8) uint8 {
	switch op {
	case OpAdd:
		return a + b
	case OpSub:
		return a - b
	case OpMul:
		return a * b
	case OpDiv:
		return a / b
	case OpAnd:
		return a & b
	case OpOr:
		return a | b
	case OpXor:
		return a ^ b
	case OpEq:
		if a == b {
			return 1
		}
		return 0
	case OpNe:
		if a != b {
			return 1
		}
		return 0
	case OpLt:
		if a < b {
			return 1
		}
		return 0
	case OpLe:
		if a <= b {
			return 1
		}
		return 0
	case OpGt:
		if a > b {
			return 1
		}
		return 0
	case OpGe:
		if a >= b {
			return 1
		}
		return 0
	default:
		return 0
	}
}
