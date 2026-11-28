// Package loopkit implements a self-contained compiler middle-end core:
// an SSA-like IR, dominator-tree based natural loop recognition with
// irreducible-control-flow interception, a double-gated LICM pass,
// scalar-evolution (SCEV) additive recurrence analysis with exact trip
// counts under fixed-width integer arithmetic, and an SSA interpreter
// used to validate semantic preservation.
package loopkit

// Type is the set of primitive SSA value types.
type Type int

const (
	Void Type = iota
	I1
	I8
	I16
	I32
	I64
	Ptr
)

// Bits returns the bit width used for fixed-width arithmetic on the type.
func (t Type) Bits() int {
	switch t {
	case I1:
		return 1
	case I8:
		return 8
	case I16:
		return 16
	case I32:
		return 32
	case I64, Ptr:
		return 64
	default:
		return 0
	}
}

func (t Type) String() string {
	switch t {
	case Void:
		return "void"
	case I1:
		return "i1"
	case I8:
		return "i8"
	case I16:
		return "i16"
	case I32:
		return "i32"
	case I64:
		return "i64"
	case Ptr:
		return "ptr"
	default:
		return "?"
	}
}

// Op identifies an SSA instruction opcode.
type Op int

const (
	OpInvalid Op = iota
	OpConst
	OpAdd
	OpSub
	OpMul
	OpUDiv
	OpSDiv
	OpURem
	OpSRem
	OpAnd
	OpOr
	OpXor
	OpShl
	OpLShr
	OpAShr
	OpICmp
	OpLoad
	OpStore
	OpCall
	OpGEP
	OpAlloca
	OpPhi
	OpParam
)

func (op Op) String() string {
	names := [...]string{
		OpInvalid: "invalid", OpConst: "const", OpAdd: "add", OpSub: "sub",
		OpMul: "mul", OpUDiv: "udiv", OpSDiv: "sdiv", OpURem: "urem",
		OpSRem: "srem", OpAnd: "and", OpOr: "or", OpXor: "xor",
		OpShl: "shl", OpLShr: "lshr", OpAShr: "ashr", OpICmp: "icmp",
		OpLoad: "load", OpStore: "store", OpCall: "call", OpGEP: "gep",
		OpAlloca: "alloca", OpPhi: "phi", OpParam: "param",
	}
	if int(op) < len(names) {
		return names[op]
	}
	return "?"
}

// isTraping reports whether the opcode can raise a runtime trap
// (division by zero, signed overflow on INT_MIN/-1).
func (op Op) isTraping() bool {
	switch op {
	case OpUDiv, OpSDiv, OpURem, OpSRem:
		return true
	}
	return false
}

// isPureCheap reports whether the opcode is side-effect free and can
// never trap, so it is hoistable regardless of block placement.
func (op Op) isPureCheap() bool {
	switch op {
	case OpConst, OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor,
		OpShl, OpLShr, OpAShr, OpICmp:
		return true
	}
	return false
}

// Pred is an integer comparison predicate.
type Pred int

const (
	PredEQ Pred = iota
	PredNE
	PredUGT
	PredUGE
	PredULT
	PredULE
	PredSGT
	PredSGE
	PredSLT
	PredSLE
)

func (p Pred) String() string {
	names := [...]string{"eq", "ne", "ugt", "uge", "ult", "ule",
		"sgt", "sge", "slt", "sle"}
	if int(p) < len(names) {
		return names[p]
	}
	return "?"
}

// Signed reports whether the predicate interprets operands as signed.
func (p Pred) Signed() bool { return p >= PredSGT }

// Instr is a single SSA instruction (also the runtime value handle).
type Instr struct {
	ID         int
	Name       string
	Op         Op
	Typ        Type // result type; for OpAlloca this is Ptr and Elem is set
	Elem       Type // alloca element type
	Int        int64
	Pred       Pred
	GepIdx     int64    // scaled element offset for OpGEP
	Callee     string   // OpCall target name
	Operands   []*Instr // binary: [x,y]; load/gep: [addr]; store: [addr,val]; call: args; phi: values
	PredBlocks []*Block // phi: predecessor block per operand, same order
	Block      *Block
}

// TermOp is a basic-block terminator kind.
type TermOp int

const (
	TermRet TermOp = iota
	TermBr
	TermCond
)

// Terminator ends a basic block.
type Terminator struct {
	Op    TermOp
	Args  []*Instr // ret: [value]; cond: [i1]
	Succs []*Block // br: [target]; cond: [then, else]
}

// Block is a basic block: a straight-line instruction list plus terminator.
type Block struct {
	ID     int
	Name   string
	Instrs []*Instr
	Term   *Terminator
	Func   *Func
}

// Succs returns the terminator successors.
func (b *Block) Succs() []*Block {
	if b.Term == nil {
		return nil
	}
	return b.Term.Succs
}

// Phis returns the phi nodes at the block entry.
func (b *Block) Phis() []*Instr {
	var out []*Instr
	for _, ins := range b.Instrs {
		if ins.Op == OpPhi {
			out = append(out, ins)
			continue
		}
		break
	}
	return out
}

// Func is an SSA function.
type Func struct {
	Name   string
	Params []*Instr
	Blocks []*Block
	nextID int
}

// NewFunc creates an empty function.
func NewFunc(name string) *Func {
	return &Func{Name: name}
}

// NewBlock appends a fresh basic block to the function.
func (f *Func) NewBlock(name string) *Block {
	f.nextID++
	b := &Block{ID: f.nextID, Name: name, Func: f}
	f.Blocks = append(f.Blocks, b)
	return b
}

// AddParam declares a function parameter as an SSA value.
func (f *Func) AddParam(name string, typ Type) *Instr {
	p := f.newInstr(OpParam, typ)
	p.Name = name
	f.Params = append(f.Params, p)
	return p
}

func (f *Func) newInstr(op Op, typ Type) *Instr {
	f.nextID++
	return &Instr{ID: f.nextID, Op: op, Typ: typ}
}

// Entry returns the first block.
func (f *Func) Entry() *Block {
	if len(f.Blocks) == 0 {
		return nil
	}
	return f.Blocks[0]
}

// ValueName returns a human-readable name for an SSA value.
func (i *Instr) ValueName() string {
	if i == nil {
		return "<nil>"
	}
	if i.Name != "" {
		return i.Name
	}
	return "v" + itoa(i.ID)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
