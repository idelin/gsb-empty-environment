package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Op is a VM opcode. Every executed instruction costs exactly 1 gas,
// including jumps (JMP/JZ/JNZ/CALL/RET) and THROW.
type Op int

const (
	OpConst Op = iota
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
	OpJnz
	OpCall
	OpRet
	OpPop
	OpDup
	OpTry
	OpEndTry
	OpThrow
	OpPrint
	OpHalt
)

// Instr is a single decoded instruction. Arg/Arg2 meaning depends on Op:
//   Const: Arg=value
//   Load/Store: Arg=local slot
//   Jmp/Jz/Jnz/Try: Arg=target pc
//   Call: Arg=func index, Arg2=arg count
type Instr struct {
	Op   Op
	Arg  int64
	Arg2 int64
}

// Func describes a function in the program's function table.
type Func struct {
	Name      string
	NumLocals int
	Entry     int
}

// Program is immutable bytecode shared by all VM instances.
type Program struct {
	Code  []Instr
	Funcs []Func
	Main  int
}

// Frame is one call-stack frame. Locals holds arguments (slots 0..nargs-1)
// followed by the remaining local variables.
type Frame struct {
	Func   int
	RetPC  int
	Locals []int64
}

// Handler is a TRY catch block registered on the handler stack.
type Handler struct {
	FrameIdx int
	StackLen int
	CatchPC  int
}

// ErrOutOfBudget is raised when the gas budget is exhausted. It is a
// VM-level fault: it bypasses every script TRY handler and can never be
// caught or swallowed by the script.
var ErrOutOfBudget = errors.New("out of budget")

// divZeroCode is the (catchable) script exception value for division or
// modulo by zero.
const divZeroCode int64 = -1

// VM is the interpreter state. The whole state (pc, frames with locals,
// operand stack, handler stack, gas, output, error) is serializable, so a
// run can be paused at any instruction boundary and resumed later, even in
// another process.
type VM struct {
	prog     *Program
	pc       int
	frames   []Frame
	stack    []int64
	handlers []Handler
	gasUsed  int64
	budget   int64 // 0 means unlimited
	output   []string
	done     bool
	err      error // first (and only) VM-level error
}

// NewVM creates a VM ready to run prog's main function.
func NewVM(prog *Program, budget int64) *VM {
	main := prog.Funcs[prog.Main]
	return &VM{
		prog:   prog,
		pc:     main.Entry,
		frames: []Frame{{Func: prog.Main, RetPC: -1, Locals: make([]int64, main.NumLocals)}},
		budget: budget,
	}
}

func (vm *VM) Done() bool { return vm.done }
func (vm *VM) Err() error { return vm.err }
func (vm *VM) Gas() int64 { return vm.gasUsed }
func (vm *VM) PC() int    { return vm.pc }

func (vm *VM) fail(err error) {
	if vm.err == nil {
		vm.err = err
	}
	vm.done = true
}

func (vm *VM) push(v int64) { vm.stack = append(vm.stack, v) }

func (vm *VM) pop() int64 {
	if len(vm.stack) == 0 {
		vm.fail(errors.New("stack underflow"))
		return 0
	}
	v := vm.stack[len(vm.stack)-1]
	vm.stack = vm.stack[:len(vm.stack)-1]
	return v
}

func (vm *VM) frame() *Frame {
	if len(vm.frames) == 0 {
		vm.fail(errors.New("no active frame"))
		return &Frame{}
	}
	return &vm.frames[len(vm.frames)-1]
}

// raise unwinds to the nearest TRY handler, or fails the VM with an
// uncaught-exception error. Out-of-budget never goes through here.
func (vm *VM) raise(v int64) {
	if len(vm.handlers) == 0 {
		vm.fail(fmt.Errorf("uncaught exception: %d", v))
		return
	}
	h := vm.handlers[len(vm.handlers)-1]
	vm.handlers = vm.handlers[:len(vm.handlers)-1]
	vm.frames = vm.frames[:h.FrameIdx+1]
	vm.stack = vm.stack[:h.StackLen]
	vm.push(v)
	vm.pc = h.CatchPC
}

func (vm *VM) arith(f func(a, b int64) int64) {
	b := vm.pop()
	a := vm.pop()
	if vm.done {
		return
	}
	vm.push(f(a, b))
}

func (vm *VM) cmp(f func(a, b int64) bool) {
	b := vm.pop()
	a := vm.pop()
	if vm.done {
		return
	}
	if f(a, b) {
		vm.push(1)
	} else {
		vm.push(0)
	}
}

// Step executes exactly one instruction (billing 1 gas for it), unless the
// VM is done or the budget is exhausted. Returns false if nothing ran.
func (vm *VM) Step() bool {
	if vm.done {
		return false
	}
	if vm.budget > 0 && vm.gasUsed >= vm.budget {
		// Uncatchable by design: raised before dispatch, never via raise().
		vm.fail(ErrOutOfBudget)
		return false
	}
	if vm.pc < 0 || vm.pc >= len(vm.prog.Code) {
		vm.fail(fmt.Errorf("pc out of range: %d", vm.pc))
		return false
	}
	vm.gasUsed++
	ins := vm.prog.Code[vm.pc]
	vm.pc++

	switch ins.Op {
	case OpConst:
		vm.push(ins.Arg)
	case OpLoad:
		f := vm.frame()
		if ins.Arg < 0 || ins.Arg >= int64(len(f.Locals)) {
			vm.fail(fmt.Errorf("local slot out of range: %d", ins.Arg))
			return true
		}
		vm.push(f.Locals[ins.Arg])
	case OpStore:
		f := vm.frame()
		if ins.Arg < 0 || ins.Arg >= int64(len(f.Locals)) {
			vm.fail(fmt.Errorf("local slot out of range: %d", ins.Arg))
			return true
		}
		f.Locals[ins.Arg] = vm.pop()
	case OpAdd:
		vm.arith(func(a, b int64) int64 { return a + b })
	case OpSub:
		vm.arith(func(a, b int64) int64 { return a - b })
	case OpMul:
		vm.arith(func(a, b int64) int64 { return a * b })
	case OpDiv:
		b := vm.pop()
		a := vm.pop()
		if vm.done {
			return true
		}
		if b == 0 {
			vm.raise(divZeroCode)
			return true
		}
		vm.push(a / b)
	case OpMod:
		b := vm.pop()
		a := vm.pop()
		if vm.done {
			return true
		}
		if b == 0 {
			vm.raise(divZeroCode)
			return true
		}
		vm.push(a % b)
	case OpNeg:
		vm.push(-vm.pop())
	case OpEq:
		vm.cmp(func(a, b int64) bool { return a == b })
	case OpNe:
		vm.cmp(func(a, b int64) bool { return a != b })
	case OpLt:
		vm.cmp(func(a, b int64) bool { return a < b })
	case OpLe:
		vm.cmp(func(a, b int64) bool { return a <= b })
	case OpGt:
		vm.cmp(func(a, b int64) bool { return a > b })
	case OpGe:
		vm.cmp(func(a, b int64) bool { return a >= b })
	case OpJmp:
		vm.pc = int(ins.Arg)
	case OpJz:
		if vm.pop() == 0 {
			vm.pc = int(ins.Arg)
		}
	case OpJnz:
		if vm.pop() != 0 {
			vm.pc = int(ins.Arg)
		}
	case OpCall:
		fi := int(ins.Arg)
		n := int(ins.Arg2)
		if fi < 0 || fi >= len(vm.prog.Funcs) {
			vm.fail(fmt.Errorf("bad function index: %d", fi))
			return true
		}
		if len(vm.stack) < n {
			vm.fail(errors.New("stack underflow in call"))
			return true
		}
		fn := vm.prog.Funcs[fi]
		nl := fn.NumLocals
		if n > nl {
			nl = n
		}
		locals := make([]int64, nl)
		for i := n - 1; i >= 0; i-- {
			locals[i] = vm.pop()
		}
		vm.frames = append(vm.frames, Frame{Func: fi, RetPC: vm.pc, Locals: locals})
		vm.pc = fn.Entry
	case OpRet:
		ret := vm.pop()
		if vm.done {
			return true
		}
		retPC := vm.frames[len(vm.frames)-1].RetPC
		vm.frames = vm.frames[:len(vm.frames)-1]
		if len(vm.frames) == 0 {
			vm.done = true
			return true
		}
		vm.push(ret)
		vm.pc = retPC
	case OpPop:
		vm.pop()
	case OpDup:
		v := vm.pop()
		if vm.done {
			return true
		}
		vm.push(v)
		vm.push(v)
	case OpTry:
		vm.handlers = append(vm.handlers, Handler{
			FrameIdx: len(vm.frames) - 1,
			StackLen: len(vm.stack),
			CatchPC:  int(ins.Arg),
		})
	case OpEndTry:
		if len(vm.handlers) == 0 {
			vm.fail(errors.New("ENDTRY without TRY"))
			return true
		}
		vm.handlers = vm.handlers[:len(vm.handlers)-1]
	case OpThrow:
		v := vm.pop()
		if vm.done {
			return true
		}
		vm.raise(v)
	case OpPrint:
		v := vm.pop()
		if vm.done {
			return true
		}
		vm.output = append(vm.output, strconv.FormatInt(v, 10))
	case OpHalt:
		vm.done = true
	default:
		vm.fail(fmt.Errorf("bad opcode: %d", int(ins.Op)))
	}
	return true
}

// Run executes until the VM halts (normally, by error, or out of budget).
func (vm *VM) Run() {
	for vm.Step() {
	}
}

// Result is the observable outcome of a run: output lines, the first
// VM-level error ("" if none), and total gas spent.
type Result struct {
	Output []string
	Err    string
	Gas    int64
}

// Result extracts the run's observable outcome.
func (vm *VM) Result() Result {
	out := vm.output
	if out == nil {
		out = []string{}
	}
	errStr := ""
	if vm.err != nil {
		errStr = vm.err.Error()
	}
	return Result{Output: append([]string{}, out...), Err: errStr, Gas: vm.gasUsed}
}

// snapshot is the fully serializable VM state.
type snapshot struct {
	PC       int       `json:"pc"`
	Frames   []Frame   `json:"frames"`
	Stack    []int64   `json:"stack"`
	Handlers []Handler `json:"handlers"`
	GasUsed  int64     `json:"gasUsed"`
	Budget   int64     `json:"budget"`
	Output   []string  `json:"output"`
	Done     bool      `json:"done"`
	Err      string    `json:"err"`
}

// Snapshot serializes the complete VM state (call stack, locals, operand
// stack, handlers, gas, output, error) to JSON.
func (vm *VM) Snapshot() ([]byte, error) {
	s := snapshot{
		PC:       vm.pc,
		Frames:   vm.frames,
		Stack:    vm.stack,
		Handlers: vm.handlers,
		GasUsed:  vm.gasUsed,
		Budget:   vm.budget,
		Output:   vm.output,
		Done:     vm.done,
	}
	if vm.err != nil {
		s.Err = vm.err.Error()
	}
	return json.MarshalIndent(s, "", "  ")
}

// RestoreVM rebuilds a VM from a snapshot produced by Snapshot. The program
// must be the same one the snapshot was taken from.
func RestoreVM(prog *Program, data []byte) (*VM, error) {
	var s snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	vm := &VM{
		prog:     prog,
		pc:       s.PC,
		frames:   s.Frames,
		stack:    s.Stack,
		handlers: s.Handlers,
		gasUsed:  s.GasUsed,
		budget:   s.Budget,
		output:   s.Output,
		done:     s.Done,
	}
	if s.Err != "" {
		vm.err = errors.New(s.Err)
	}
	return vm, nil
}
