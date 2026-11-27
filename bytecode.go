package minijit

// Opcode enumerates the supported bytecodes. Stack effects are documented on
// each opcode; the interpreter and the speculative optimizer share them.
type Opcode uint8

const (
	// OpLoadConst pushes a constant: -> v
	OpLoadConst Opcode = iota
	// OpLoadLocal pushes local slot A: -> v
	OpLoadLocal
	// OpStoreLocal pops v into local slot A: v ->
	OpStoreLocal
	// OpAdd pops b, a and pushes a+b (int overflow becomes float under
	// speculation): a b -> r
	OpAdd
	// OpSub pops b, a and pushes a-b: a b -> r
	OpSub
	// OpLess pops b, a and pushes a<b: a b -> bool
	OpLess
	// OpEqual pops b, a and pushes StrictEqual(a,b): a b -> bool
	OpEqual
	// OpJump unconditionally jumps to A: ->
	OpJump
	// OpJumpIfFalse pops v and jumps to A when v is falsy: v ->
	OpJumpIfFalse
	// OpNewObject pops the class-name string and pushes a new object:
	// klass -> obj
	OpNewObject
	// OpGetField pops object, field name and pushes the field:
	// obj name -> v
	OpGetField
	// OpSetField pops value, object, field name and stores it:
	// obj v name -> (pushed object, value, name; popped in reverse)
	OpSetField
	// OpTransitionClass pops object and the new class-name string:
	// obj klass ->
	OpTransitionClass
	// OpGuardClass pops object and expected class-name string; under
	// speculation a mismatch deoptimizes, in the interpreter it is a no-op
	// check: obj klass ->
	OpGuardClass
	// OpCall pops callee and A arguments: callee arg0..argN-1 -> result
	// (callee pushed first, arguments after)
	OpCall
	// OpReturn pops the return value and leaves the function: v ->
	OpReturn
	// OpThrow pops the thrown value and transfers to a handler: v ->
	OpThrow
	// OpPop discards the top of the operand stack: v ->
	OpPop

	opCount
)

// Instr is one bytecode instruction. A holds a small integer operand (local
// slot, jump target or argument count); B holds a constant-pool index for
// load-const.
type Instr struct {
	Op Opcode
	A  int
	B  int
}

// Handler describes an exception-protected bytecode range. When an exception
// is thrown while the program counter is in [Start,End) the interpreter
// pushes the thrown value and jumps to Target.
type Handler struct {
	Start  int
	End    int
	Target int
}

var nextFunctionID int64

// Function is a bytecode function with locals, a constant pool and a handler
// table. Local slots 0..NArgs-1 receive call arguments.
type Function struct {
	id       int64
	Name     string
	NArgs    int
	NLocals  int
	Consts   []Value
	Code     []Instr
	Handlers []Handler
}

// NewFunction registers a function definition.
func NewFunction(name string, nArgs, nLocals int) *Function {
	nextFunctionID++
	return &Function{id: nextFunctionID, Name: name, NArgs: nArgs, NLocals: nLocals}
}

// ID returns the unique function id.
func (f *Function) ID() int64 { return f.id }

// Emit appends an instruction and returns its bytecode index.
func (f *Function) Emit(op Opcode, a, b int) int {
	f.Code = append(f.Code, Instr{Op: op, A: a, B: b})
	return len(f.Code) - 1
}

// AddConst appends a constant and returns its pool index.
func (f *Function) AddConst(v Value) int {
	for idx := range f.Consts {
		if StrictEqual(f.Consts[idx], v) {
			return idx
		}
	}
	f.Consts = append(f.Consts, v)
	return len(f.Consts) - 1
}

// AddHandler registers an exception-protected range.
func (f *Function) AddHandler(h Handler) { f.Handlers = append(f.Handlers, h) }

// Program is a collection of named functions.
type Program struct {
	Functions map[string]*Function
}

// NewProgram creates an empty program.
func NewProgram() *Program {
	return &Program{Functions: map[string]*Function{}}
}

// Add registers a function under its name.
func (p *Program) Add(f *Function) { p.Functions[f.Name] = f }

// Lookup resolves a function by name.
func (p *Program) Lookup(name string) (*Function, bool) {
	f, ok := p.Functions[name]
	return f, ok
}

func opcodeName(op Opcode) string {
	names := [opCount]string{
		"LoadConst", "LoadLocal", "StoreLocal", "Add", "Sub", "Less", "Equal",
		"Jump", "JumpIfFalse", "NewObject", "GetField", "SetField",
		"TransitionClass", "GuardClass", "Call", "Return", "Throw", "Pop",
	}
	if int(op) < len(names) {
		return names[op]
	}
	return "?"
}
