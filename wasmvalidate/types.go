// Package wasmvalidate implements a single-pass, linear WebAssembly 2.0
// function-body validator based on the structured control-flow stack typing
// algorithm of the official specification (Appendix: Validation Algorithm).
//
// The validator maintains two stacks:
//
//   - an operand type stack whose entries may be the polymorphic placeholder
//     Unknown (pushed implicitly when a control frame is unreachable); and
//   - a control frame stack whose frames record opcode, start_types,
//     end_types, height and an unreachable flag.
//
// After a frame becomes unreachable (via unreachable/br/br_table/return) the
// remaining instructions are still fully type checked against a polymorphic
// stack, exactly as required by the specification.
package wasmvalidate

// ValType identifies a value type used on the operand stack.
type ValType byte

const (
	I32       ValType = 0x7F
	I64       ValType = 0x7E
	F32       ValType = 0x7D
	F64       ValType = 0x7C
	V128      ValType = 0x7B
	Unknown   ValType = 0x00 // polymorphic placeholder (never a real wasm type)
	FuncRef   ValType = 0x70
	ExternRef ValType = 0x6F
)

// IsNumeric reports whether t is one of the numeric or vector value types
// permitted for the untyped select instruction.
func (t ValType) IsNumeric() bool {
	switch t {
	case I32, I64, F32, F64, V128:
		return true
	default:
		return false
	}
}

func (t ValType) String() string {
	switch t {
	case I32:
		return "i32"
	case I64:
		return "i64"
	case F32:
		return "f32"
	case F64:
		return "f64"
	case V128:
		return "v128"
	case Unknown:
		return "unknown"
	case FuncRef:
		return "funcref"
	case ExternRef:
		return "externref"
	default:
		return "invalid"
	}
}

// FuncType is a function (type index) signature: parameter and result types.
type FuncType struct {
	Params  []ValType
	Results []ValType
}

// GlobalType describes a global: its value type and mutability.
type GlobalType struct {
	ValType ValType
	Mutable bool
}

// TableType is the minimal table description needed by call_indirect.
type TableType struct {
	ElemType ValType // typically FuncRef
}

// Module carries the module-level declarations required for body validation.
type Module struct {
	// Types is the module type signature table.
	Types []FuncType
	// Funcs maps each function index to its resolved function type.
	Funcs   []FuncType
	Globals []GlobalType
	Tables  []TableType
}

// Body carries everything local to a single function body.
type Body struct {
	// Locals holds every local's value type in index order: parameters first
	// (matching Type.Params), followed by the locally declared locals.
	Locals []ValType
	// Type is the resolved signature of the function being validated.
	Type FuncType
	// Instrs is the instruction sequence. Structured control instructions are
	// matched by opcode alone; there is no explicit End for the implicit
	// outermost function frame.
	Instrs []Instr
}

// BlockType encodes the block type used by block/loop/if.
type BlockType struct {
	// Empty marks the canonical empty block type (no inputs, no outputs).
	Empty bool
	// TypeIndex, when >= 0, references a multi-value entry in Module.Types.
	TypeIndex int64
	// Single, when SingleValid is true, is the one-result shorthand form.
	Single      ValType
	SingleValid bool
}

// Instr is one decoded instruction. Only the fields relevant to a given
// opcode are populated:
//
//   - BlockType: block/loop/if
//   - Depth: br/br_if; Targets+Depth: br_table (Depth is the default target)
//   - FuncIndex: call; TableIndex+TypeIndex: call_indirect
//   - LocalIndex: local.*; GlobalIndex: global.*
//   - ValTypes: typed select
//   - ValType: const/numeric producers (e.g. i32.const, v128.const)
type Instr struct {
	Op          Opcode
	BlockType   BlockType
	Depth       uint32
	Targets     []uint32
	FuncIndex   uint32
	TableIndex  uint32
	TypeIndex   uint32
	LocalIndex  uint32
	GlobalIndex uint32
	ValType     ValType
	ValTypes    []ValType
}

// ErrorKind categorises a validation failure.
type ErrorKind string

const (
	ErrTypeMismatch       ErrorKind = "type mismatch"
	ErrStackUnderflow     ErrorKind = "stack underflow"
	ErrExtraOperands      ErrorKind = "extra operands on stack"
	ErrUnknownLabel       ErrorKind = "unknown label / branch depth out of range"
	ErrBranchTypeMismatch ErrorKind = "branch label types mismatch"
	ErrSelectType         ErrorKind = "invalid select operand type"
	ErrUnknownTypeIndex   ErrorKind = "unknown type index"
	ErrUnknownFunc        ErrorKind = "unknown function index"
	ErrUnknownLocal       ErrorKind = "unknown local index"
	ErrUnknownGlobal      ErrorKind = "unknown global index"
	ErrImmutableGlobal    ErrorKind = "write to immutable global"
	ErrUnknownTable       ErrorKind = "unknown table index"
	ErrUnexpectedElse     ErrorKind = "else without matching if"
	ErrUnexpectedEnd      ErrorKind = "end without matching structured instruction"
	ErrUnclosedBlock      ErrorKind = "unclosed structured instruction at function end"
	ErrIfNoElseMultiValue ErrorKind = "if without else requires identical input and output types"
	ErrInvalidBlockType   ErrorKind = "invalid block type"
	ErrInvalidValType     ErrorKind = "invalid value type"
	ErrUnknownOpcode      ErrorKind = "unknown or unsupported opcode"
	ErrEmptyBrTable       ErrorKind = "br_table requires at least one target"
)

// ValidationError is a precise validation failure. InstrIndex is the zero
// based index into Body.Instrs of the offending instruction, or -1 when the
// failure is not tied to a single instruction (e.g. an unclosed block).
type ValidationError struct {
	Kind       ErrorKind
	InstrIndex int
	Message    string
}

func (e *ValidationError) Error() string {
	if e.InstrIndex >= 0 {
		return "wasmvalidate: " + string(e.Kind) + " at instruction " +
			itoa(e.InstrIndex) + ": " + e.Message
	}
	return "wasmvalidate: " + string(e.Kind) + ": " + e.Message
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
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
