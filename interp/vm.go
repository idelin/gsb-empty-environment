// Package interp implements a sandboxed integer script interpreter.
//
// Cost model (deterministic, identical across pause/resume):
//   - every executed instruction costs costInstr
//   - taken control transfers (jmp, taken jz, call, ret) cost an extra costControl
//   - raising an error (throw op or runtime fault) costs an extra costError
//
// Budget exhaustion is reported as an uncatchable error (CodeBudget): it
// unwinds the whole call stack and can never be intercepted by try/catch.
package interp

import (
	"encoding/json"
	"fmt"
)

type Op int

const (
	OpPush Op = iota
	OpLoad
	OpStore
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpMod
	OpNeg
	OpEq
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpJmp
	OpJz
	OpCall
	OpRet
	OpTry
	OpUntry
	OpThrow
	OpPrint
	OpPause
	OpHalt
)

// Instr is a single decoded instruction. Arg/Arg2 hold immediates
// (constant, local slot, jump target, argument count).
type Instr struct {
	Op   Op
	Arg  int64
	Arg2 int64
}

// FuncInfo describes a function entry point in the code segment.
type FuncInfo struct {
	Name    string
	NArgs   int
	NLocals int
}

// Program is an immutable, assembled script. It is shared by VMs and is
// not part of the serializable state.
type Program struct {
	Code  []Instr
	Funcs map[int]FuncInfo // entry pc -> info
	Entry int
}

// Billing constants.
const (
	costInstr   int64 = 1 // every executed instruction
	costControl int64 = 1 // extra for taken jumps / call / ret
	costError   int64 = 1 // extra when an error is raised
)

// Script error codes. Negative codes are reserved for the runtime and are
// uncatchable; script-thrown codes are arbitrary non-negative values.
const (
	CodeBudget    int64 = -1 // budget exhausted, uncatchable
	CodeDivZero   int64 = 1
	CodeUnderflow int64 = 2
	CodeBadLocal  int64 = 3
	CodeBadPC     int64 = 4
	CodeBadArgs   int64 = 5
)

// ScriptError is a catchable (or, for CodeBudget, uncatchable) script error.
type ScriptError struct {
	Code int64  `json:"code"`
	Msg  string `json:"msg"`
}

func (e *ScriptError) Error() string {
	return fmt.Sprintf("script error %d: %s", e.Code, e.Msg)
}

// Status of the VM.
type Status string

const (
	StatusReady  Status = "ready"
	StatusPaused Status = "paused"
	StatusHalted Status = "halted"
	StatusFailed Status = "failed"
)

// Handler is an active try/catch registration inside a frame.
type Handler struct {
	CatchPC    int `json:"catchPC"`
	StackDepth int `json:"stackDepth"`
}

// Frame is one call-stack frame. It fully captures a function activation:
// program counter, local variables, operand stack and active handlers.
type Frame struct {
	Func     string    `json:"func"`
	PC       int       `json:"pc"`
	Locals   []int64   `json:"locals"`
	Stack    []int64   `json:"stack"`
	Handlers []Handler `json:"handlers,omitempty"`
}

func (f *Frame) pop() int64 {
	v := f.Stack[len(f.Stack)-1]
	f.Stack = f.Stack[:len(f.Stack)-1]
	return v
}

// VM is a script execution instance. Its entire dynamic state (call stack,
// locals, operand stacks, handlers, output, cost, first error, status) can
// be serialized with Snapshot and restored with Restore.
type VM struct {
	prog     *Program
	budget   int64 // -1 = unlimited
	frames   []Frame
	output   []int64
	cost     int64
	firstErr *ScriptError
	status   Status
}

// NewVM creates a VM starting at the program entry function. budget < 0
// means unlimited.
func NewVM(prog *Program, budget int64, args ...int64) *VM {
	fi := prog.Funcs[prog.Entry]
	locals := make([]int64, fi.NLocals)
	copy(locals, args)
	return &VM{
		prog:   prog,
		budget: budget,
		frames: []Frame{{Func: fi.Name, PC: prog.Entry, Locals: locals}},
		status: StatusReady,
	}
}

// Output returns a copy of everything printed so far.
func (v *VM) Output() []int64 {
	out := make([]int64, len(v.output))
	copy(out, v.output)
	return out
}

// Cost returns the total billed cost so far.
func (v *VM) Cost() int64 { return v.cost }

// Status returns the current VM status.
func (v *VM) Status() Status { return v.status }

// FirstError returns the first error ever raised, or nil. It is recorded
// even if the script caught it.
func (v *VM) FirstError() *ScriptError { return v.firstErr }

// charge bills n units. If the budget is exceeded it raises the
// uncatchable budget error and returns false.
func (v *VM) charge(n int64) bool {
	v.cost += n
	if v.budget >= 0 && v.cost > v.budget {
		v.raise(&ScriptError{Code: CodeBudget, Msg: "budget exhausted"})
		return false
	}
	return true
}

// fault bills the error surcharge and raises a runtime error.
func (v *VM) fault(code int64, msg string) {
	if !v.charge(costError) {
		return
	}
	v.raise(&ScriptError{Code: code, Msg: msg})
}

// raise records the first error and either transfers control to the
// innermost handler or fails the VM. CodeBudget is never catchable.
func (v *VM) raise(e *ScriptError) {
	if v.firstErr == nil {
		v.firstErr = e
	}
	if e.Code == CodeBudget {
		v.frames = nil
		v.status = StatusFailed
		return
	}
	for len(v.frames) > 0 {
		f := &v.frames[len(v.frames)-1]
		if len(f.Handlers) > 0 {
			h := f.Handlers[len(f.Handlers)-1]
			f.Handlers = f.Handlers[:len(f.Handlers)-1]
			f.Stack = f.Stack[:h.StackDepth]
			f.Stack = append(f.Stack, e.Code)
			f.PC = h.CatchPC
			return
		}
		v.frames = v.frames[:len(v.frames)-1]
	}
	v.status = StatusFailed
}

// Run executes until the VM pauses, halts or fails.
func (v *VM) Run() Status {
	if v.status == StatusPaused {
		v.status = StatusReady
	}
	for v.status == StatusReady {
		v.step()
	}
	return v.status
}

// RunQuantum executes at most n instructions, then pauses. It allows
// suspending the VM at an arbitrary instruction boundary.
func (v *VM) RunQuantum(n int64) Status {
	if v.status == StatusPaused {
		v.status = StatusReady
	}
	var i int64
	for v.status == StatusReady && i < n {
		v.step()
		i++
	}
	if v.status == StatusReady {
		v.status = StatusPaused
	}
	return v.status
}

func (v *VM) step() {
	f := &v.frames[len(v.frames)-1]
	if f.PC < 0 || f.PC >= len(v.prog.Code) {
		v.fault(CodeBadPC, "pc out of range")
		return
	}
	ins := v.prog.Code[f.PC]
	f.PC++
	if !v.charge(costInstr) {
		return
	}
	switch ins.Op {
	case OpPush:
		f.Stack = append(f.Stack, ins.Arg)
	case OpLoad:
		if ins.Arg < 0 || int(ins.Arg) >= len(f.Locals) {
			v.fault(CodeBadLocal, "local slot out of range")
			return
		}
		f.Stack = append(f.Stack, f.Locals[ins.Arg])
	case OpStore:
		if ins.Arg < 0 || int(ins.Arg) >= len(f.Locals) {
			v.fault(CodeBadLocal, "local slot out of range")
			return
		}
		if len(f.Stack) < 1 {
			v.fault(CodeUnderflow, "stack underflow")
			return
		}
		f.Locals[ins.Arg] = f.pop()
	case OpAdd, OpSub, OpMul, OpDiv, OpMod, OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		if len(f.Stack) < 2 {
			v.fault(CodeUnderflow, "stack underflow")
			return
		}
		b := f.pop()
		a := f.pop()
		switch ins.Op {
		case OpAdd:
			f.Stack = append(f.Stack, a+b)
		case OpSub:
			f.Stack = append(f.Stack, a-b)
		case OpMul:
			f.Stack = append(f.Stack, a*b)
		case OpDiv:
			if b == 0 {
				v.fault(CodeDivZero, "division by zero")
				return
			}
			f.Stack = append(f.Stack, a/b)
		case OpMod:
			if b == 0 {
				v.fault(CodeDivZero, "modulo by zero")
				return
			}
			f.Stack = append(f.Stack, a%b)
		case OpEq:
			f.Stack = append(f.Stack, boolInt(a == b))
		case OpNe:
			f.Stack = append(f.Stack, boolInt(a != b))
		case OpLt:
			f.Stack = append(f.Stack, boolInt(a < b))
		case OpLe:
			f.Stack = append(f.Stack, boolInt(a <= b))
		case OpGt:
			f.Stack = append(f.Stack, boolInt(a > b))
		case OpGe:
			f.Stack = append(f.Stack, boolInt(a >= b))
		}
	case OpNeg:
		if len(f.Stack) < 1 {
			v.fault(CodeUnderflow, "stack underflow")
			return
		}
		f.Stack = append(f.Stack, -f.pop())
	case OpJmp:
		if !v.charge(costControl) {
			return
		}
		f.PC = int(ins.Arg)
	case OpJz:
		if len(f.Stack) < 1 {
			v.fault(CodeUnderflow, "stack underflow")
			return
		}
		if f.pop() == 0 {
			if !v.charge(costControl) {
				return
			}
			f.PC = int(ins.Arg)
		}
	case OpCall:
		if !v.charge(costControl) {
			return
		}
		fi, ok := v.prog.Funcs[int(ins.Arg)]
		if !ok {
			v.fault(CodeBadPC, "call target is not a function")
			return
		}
		nargs := int(ins.Arg2)
		if len(f.Stack) < nargs {
			v.fault(CodeBadArgs, "not enough arguments on stack")
			return
		}
		locals := make([]int64, fi.NLocals)
		copy(locals, f.Stack[len(f.Stack)-nargs:])
		f.Stack = f.Stack[:len(f.Stack)-nargs]
		v.frames = append(v.frames, Frame{Func: fi.Name, PC: int(ins.Arg), Locals: locals})
	case OpRet:
		if !v.charge(costControl) {
			return
		}
		var ret int64
		hasRet := len(f.Stack) > 0
		if hasRet {
			ret = f.Stack[len(f.Stack)-1]
		}
		v.frames = v.frames[:len(v.frames)-1]
		if len(v.frames) == 0 {
			v.status = StatusHalted
			return
		}
		if hasRet {
			caller := &v.frames[len(v.frames)-1]
			caller.Stack = append(caller.Stack, ret)
		}
	case OpTry:
		f.Handlers = append(f.Handlers, Handler{CatchPC: int(ins.Arg), StackDepth: len(f.Stack)})
	case OpUntry:
		if len(f.Handlers) == 0 {
			v.fault(CodeUnderflow, "untry without try")
			return
		}
		f.Handlers = f.Handlers[:len(f.Handlers)-1]
	case OpThrow:
		if len(f.Stack) < 1 {
			v.fault(CodeUnderflow, "stack underflow")
			return
		}
		code := f.pop()
		if !v.charge(costError) {
			return
		}
		v.raise(&ScriptError{Code: code, Msg: "thrown by script"})
	case OpPrint:
		if len(f.Stack) < 1 {
			v.fault(CodeUnderflow, "stack underflow")
			return
		}
		v.output = append(v.output, f.pop())
	case OpPause:
		v.status = StatusPaused
	case OpHalt:
		v.status = StatusHalted
	default:
		v.fault(CodeBadPC, "unknown opcode")
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// state is the serializable form of the whole dynamic VM state.
type state struct {
	Frames   []Frame      `json:"frames"`
	Output   []int64      `json:"output"`
	Cost     int64        `json:"cost"`
	Budget   int64        `json:"budget"`
	FirstErr *ScriptError `json:"firstErr,omitempty"`
	Status   Status       `json:"status"`
}

// Snapshot serializes the complete VM state (call stack, locals, operand
// stacks, handlers, output, cost, first error, status) to JSON.
func (v *VM) Snapshot() ([]byte, error) {
	return json.Marshal(state{
		Frames:   v.frames,
		Output:   v.output,
		Cost:     v.cost,
		Budget:   v.budget,
		FirstErr: v.firstErr,
		Status:   v.status,
	})
}

// Restore rebuilds a VM from a snapshot produced by Snapshot. The program
// must be the same one the snapshot was taken from.
func Restore(prog *Program, data []byte) (*VM, error) {
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	return &VM{
		prog:     prog,
		frames:   s.Frames,
		output:   s.Output,
		cost:     s.Cost,
		budget:   s.Budget,
		firstErr: s.FirstErr,
		status:   s.Status,
	}, nil
}
