package netsec

// Op identifies a decoded instruction opcode.
type Op int

const (
	OpNew    Op = iota // new(Class): push uninitialized reference tagged with this pc
	OpDup              // dup: duplicate the top of the operand stack
	OpAstore           // astore(Slot): pop into local slot
	OpAload            // aload(Slot): push local slot
	OpInit             // init(Class): pop receiver, run constructor for Class
	OpUse              // use(Class): pop a reference that must be initialized and a Class-or-subclass
	OpJmp              // jmp Target
	OpBranch           // nondeterministic branch to Target or Target2
	OpReturn           // return: requires an empty operand stack
	OpThrow            // throw: pop an initialized Throwable
)

// Instr is one decoded instruction. The host decodes the bytecode; this
// package never parses class files.
type Instr struct {
	Op      Op
	Class   string // OpNew, OpInit, OpUse
	Slot    int    // OpAstore, OpAload
	Target  int    // OpJmp target; OpBranch first target
	Target2 int    // OpBranch second target
}

// Program is a decoded method plus its exception table.
//
// Handlers maps an instruction index to the set of handler entry pcs that
// may be reached when that instruction raises. Every handler edge is
// verified alongside the normal control-flow edges.
type Program struct {
	Code      []Instr
	NumLocals int
	Handlers  map[int][]int
}

// Instruction constructors keep programs readable.

func New(class string) Instr  { return Instr{Op: OpNew, Class: class} }
func Dup() Instr              { return Instr{Op: OpDup} }
func Astore(slot int) Instr   { return Instr{Op: OpAstore, Slot: slot} }
func Aload(slot int) Instr    { return Instr{Op: OpAload, Slot: slot} }
func Init(class string) Instr { return Instr{Op: OpInit, Class: class} }
func Use(class string) Instr  { return Instr{Op: OpUse, Class: class} }
func Jmp(target int) Instr    { return Instr{Op: OpJmp, Target: target} }
func Branch(t1, t2 int) Instr { return Instr{Op: OpBranch, Target: t1, Target2: t2} }
func Return() Instr           { return Instr{Op: OpReturn} }
func Throw() Instr            { return Instr{Op: OpThrow} }
