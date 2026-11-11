package netsec

// Op identifies one of the twelve bounded instructions understood by the
// abstract machine.
type Op uint8

const (
	// OpConst writes Dst = Immediate.
	OpConst Op = iota
	// OpLoad writes Dst = Src.
	OpLoad
	// OpAdd writes Dst = Src1 + Src2 with checked unsigned overflow.
	OpAdd
	// OpSub writes Dst = Src1 - Src2 with checked unsigned underflow.
	OpSub
	// OpMul writes Dst = Src1 * Src2 with checked unsigned overflow.
	OpMul
	// OpDiv writes Dst = Src1 / Src2; a zero divisor raises ExcDivByZero.
	OpDiv
	// OpCmp writes Dst = 1 if Src1 < Src2, else 0.
	OpCmp
	// OpAssert raises ExcAssertion when Src == 0, otherwise it is a no-op.
	OpAssert
	// OpBranch jumps to Target when Src != 0, otherwise execution falls through.
	OpBranch
	// OpHost invokes the verification host with Src as argument and stores HostID.
	OpHost
	// OpReturn terminates execution and returns Src as the fixed return value.
	OpReturn
	opCount
)

// MaxInstr is the inclusive upper bound on the number of instructions in a
// program.
const MaxInstr = 12

// MaxInputBytes bounds the total width, in bytes, of the secret input space.
// With every variable at least one bit wide there can therefore never be more
// than 48 secret variables, and the combined secret domain never exceeds
// 2^(8*MaxInputBytes).
const MaxInputBytes = 6

// ExcCode is an explicit, typed exception raised by an instruction. ExcNone
// means that no exception occurred.
type ExcCode int

const (
	// ExcNone means normal termination without an exception.
	ExcNone ExcCode = 0
	// ExcOverflow is raised by OpAdd and OpMul on unsigned overflow.
	ExcOverflow ExcCode = 1
	// ExcUnderflow is raised by OpSub when Src1 < Src2.
	ExcUnderflow ExcCode = 2
	// ExcDivByZero is raised by OpDiv when the divisor is zero.
	ExcDivByZero ExcCode = 3
	// ExcAssertion is raised by OpAssert when its operand is zero.
	ExcAssertion ExcCode = 4
	// ExcHost is raised when the verification host reports a failure.
	ExcHost ExcCode = 5
	// ExcNoReturn is raised when the program runs past its last instruction
	// without executing OpReturn.
	ExcNoReturn ExcCode = 6
)

// String renders an exception code using its canonical name.
func (e ExcCode) String() string {
	switch e {
	case ExcNone:
		return "ExcNone"
	case ExcOverflow:
		return "ExcOverflow"
	case ExcUnderflow:
		return "ExcUnderflow"
	case ExcDivByZero:
		return "ExcDivByZero"
	case ExcAssertion:
		return "ExcAssertion"
	case ExcHost:
		return "ExcHost"
	case ExcNoReturn:
		return "ExcNoReturn"
	default:
		return "ExcUnknown"
	}
}

// Visibility marks a variable as public or secret.
type Visibility uint8

const (
	// Public variables are supplied by the verifier and fixed across a pair.
	Public Visibility = iota
	// Secret variables are enumerated by the verifier.
	Secret
)

// Variable is a bounded-width unsigned register. Values are always reduced
// modulo 2^Bits.
type Variable struct {
	Name       string
	Visibility Visibility
	Bits       uint8
	Init       uint64
}

// Instr is one machine instruction. The semantics of each field depend on Op:
//
//	OpConst   : Dst, Immediate
//	OpLoad    : Dst, Src
//	OpAdd/Sub/Mul/Div: Dst, Src, Src2
//	OpCmp     : Dst, Src, Src2
//	OpAssert  : Src
//	OpBranch  : Src, Target
//	OpHost    : Dst, Src, HostID
//	OpReturn  : Src
type Instr struct {
	Op        Op
	Dst       uint8
	Src       uint8
	Src2      uint8
	Target    uint8
	HostID    uint8
	Immediate uint64
}

// Program is a bounded machine program: at most MaxInstr instructions acting
// on a fixed set of bounded variables.
type Program struct {
	Vars  []Variable
	Code  []Instr
	Entry uint8
}

// Call records one interaction with the verification host. It captures the
// host identifier, the argument supplied by the program and the result handed
// back, so that an entire run can be replayed deterministically.
type Call struct {
	HostID uint8
	Arg    uint64
	Result uint64
	Fault  bool
}

// State is a snapshot of the machine immediately before executing the
// instruction at PC.
type State struct {
	PC   uint8
	Vars []uint64
}

// Step records one executed instruction together with the machine state that
// led to it and any host interaction it performed.
type Step struct {
	State State
	Instr Instr
	Call  *Call
}

// Trace is a complete, replayable execution: the initial variable assignment,
// every executed step and the terminal observation.
type Trace struct {
	Initial []uint64
	Steps   []Step
	Return  uint64
	Exc     ExcCode
	ExcPC   uint8
}

// Observation is everything the Go verification host is allowed to distinguish
// about a run when its return value is otherwise fixed: the return value, the
// typed exception (and where it was raised) and the ordered host-call
// sequence.
type Observation struct {
	Return uint64
	Exc    ExcCode
	ExcPC  uint8
	Calls  []Call
}

// Observe extracts the externally visible behavior of a completed trace.
func (t *Trace) Observe() Observation {
	calls := make([]Call, 0, len(t.Steps))
	for _, step := range t.Steps {
		if step.Call != nil {
			calls = append(calls, *step.Call)
		}
	}
	return Observation{Return: t.Return, Exc: t.Exc, ExcPC: t.ExcPC, Calls: calls}
}

// Input assigns values to public variables, keyed by variable index.
type Input map[uint8]uint64
