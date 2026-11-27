package minijit

import (
	"errors"
	"fmt"
)

// Frame is one independent interpreter activation with its own bytecode pc,
// local variable array and operand stack.
type Frame struct {
	fn     *Function
	pc     int
	locals []Value
	stack  []Value
}

// FrameView is an immutable snapshot of a frame used for inspection.
type FrameView struct {
	FunctionName string
	PC           int
	Locals       []Value
	Stack        []Value
}

// Interpreter executes a Program one bytecode at a time.
type Interpreter struct {
	prog   *Program
	frames []*Frame
}

// NewInterpreter builds an interpreter for the given program.
func NewInterpreter(prog *Program) *Interpreter {
	return &Interpreter{prog: prog}
}

// Run starts entry with args and runs until a value is returned.
func (in *Interpreter) Run(entry string, args []Value) (Value, error) {
	fn, ok := in.prog.Lookup(entry)
	if !ok {
		return NilValue(), fmt.Errorf("minijit: unknown entry function %q", entry)
	}
	if len(args) != fn.NArgs {
		return NilValue(), fmt.Errorf("minijit: %s expects %d args, got %d", entry, fn.NArgs, len(args))
	}
	in.pushFrame(fn, args)
	return in.runLoop()
}

func (in *Interpreter) pushFrame(fn *Function, args []Value) {
	locals := make([]Value, fn.NLocals)
	copy(locals, args)
	in.frames = append(in.frames, &Frame{fn: fn, locals: locals})
}

func (fr *Frame) push(v Value) { fr.stack = append(fr.stack, v) }

func (fr *Frame) pop() Value {
	if len(fr.stack) == 0 {
		panic("minijit: operand stack underflow")
	}
	last := len(fr.stack) - 1
	v := fr.stack[last]
	fr.stack = fr.stack[:last]
	return v
}

func (in *Interpreter) runLoop() (Value, error) {
	for len(in.frames) > 0 {
		fr := in.frames[len(in.frames)-1]
		if fr.pc >= len(fr.fn.Code) {
			return NilValue(), fmt.Errorf("minijit: %s ran past last instruction without return", fr.fn.Name)
		}
		ins := fr.fn.Code[fr.pc]
		fr.pc++
		switch ins.Op {
		case OpLoadConst:
			fr.push(fr.fn.Consts[ins.B])
		case OpLoadLocal:
			fr.push(fr.locals[ins.A])
		case OpStoreLocal:
			fr.locals[ins.A] = fr.pop()
		case OpAdd:
			b := fr.pop()
			a := fr.pop()
			fr.push(addValues(a, b))
		case OpSub:
			b := fr.pop()
			a := fr.pop()
			fr.push(subValues(a, b))
		case OpLess:
			b := fr.pop()
			a := fr.pop()
			fr.push(BoolValue(a.AsNumber() < b.AsNumber()))
		case OpEqual:
			b := fr.pop()
			a := fr.pop()
			fr.push(BoolValue(StrictEqual(a, b)))
		case OpJump:
			fr.pc = ins.A
		case OpJumpIfFalse:
			v := fr.pop()
			if !isTruthy(v) {
				fr.pc = ins.A
			}
		case OpNewObject:
			klass := fr.pop()
			fr.push(ObjectValue(NewObject(klass.AsString())))
		case OpGetField:
			name := fr.pop().AsString()
			obj := fr.pop().AsObject()
			v, ok := obj.Field(name)
			if !ok {
				return NilValue(), in.unwind(StringValue("TypeError: missing field " + name))
			}
			fr.push(v)
		case OpSetField:
			name := fr.pop().AsString()
			v := fr.pop()
			obj := fr.pop().AsObject()
			obj.SetField(name, v)
		case OpTransitionClass:
			klass := fr.pop().AsString()
			obj := fr.pop().AsObject()
			obj.SetClass(klass)
		case OpGuardClass:
			// Guards express the optimizer's speculative assumption and have
			// no observable interpreter semantics: the dynamic tier simply
			// continues with the object's actual hidden class.
			fr.pop()
			fr.pop()
		case OpCall:
			callArgs := make([]Value, ins.A)
			for i := ins.A - 1; i >= 0; i-- {
				callArgs[i] = fr.pop()
			}
			callee := fr.pop()
			fn := callee.AsFunction()
			if fn == nil {
				return NilValue(), in.unwind(StringValue("TypeError: value is not callable"))
			}
			if len(callArgs) != fn.NArgs {
				return NilValue(), in.unwind(StringValue("TypeError: argument count mismatch"))
			}
			in.pushFrame(fn, callArgs)
		case OpReturn:
			result := fr.pop()
			in.frames = in.frames[:len(in.frames)-1]
			if len(in.frames) > 0 {
				in.frames[len(in.frames)-1].push(result)
			} else {
				return result, nil
			}
		case OpThrow:
			thrown := fr.pop()
			if err := in.unwind(thrown); err != nil {
				return NilValue(), err
			}
		case OpPop:
			fr.pop()
		default:
			return NilValue(), fmt.Errorf("minijit: unknown opcode %d", ins.Op)
		}
	}
	return NilValue(), errors.New("minijit: stack empty without return")
}

// throw walks the active frames from inner to outer looking for a handler
// whose protected range contains the current pc. Frames without a handler
// are unwound; the matched frame resumes at the handler target with the
// thrown value on its operand stack.
// unwind walks frames inner-to-outer, discarding frames without a matching
// handler. On a match it resets the frame's operand stack to hold only the
// thrown value and points pc at the handler target, letting the single
// runLoop continue non-recursively.
func (in *Interpreter) unwind(v Value) error {
	for len(in.frames) > 0 {
		fr := in.frames[len(in.frames)-1]
		for _, h := range fr.fn.Handlers {
			if fr.pc >= h.Start && fr.pc < h.End {
				fr.stack = fr.stack[:0]
				fr.push(v)
				fr.pc = h.Target
				return nil
			}
		}
		in.frames = in.frames[:len(in.frames)-1]
	}
	return &UncaughtThrowError{Value: v}
}

// throw unwinds v and, when a handler was found, continues the interpreter
// loop until the function returns. It is the entry point used after
// deoptimization.
func (in *Interpreter) throw(v Value) (Value, error) {
	if err := in.unwind(v); err != nil {
		return NilValue(), err
	}
	return in.runLoop()
}

// UncaughtThrowError is returned when a thrown value has no handler.
type UncaughtThrowError struct {
	Value Value
}

func (e *UncaughtThrowError) Error() string {
	return "minijit: uncaught throw: " + e.Value.String()
}

// Depth returns the number of active interpreter frames.
func (in *Interpreter) Depth() int { return len(in.frames) }

// Frames returns outer-to-inner snapshots of the active frames.
func (in *Interpreter) Frames() []FrameView {
	out := make([]FrameView, 0, len(in.frames))
	for _, fr := range in.frames {
		locals := make([]Value, len(fr.locals))
		copy(locals, fr.locals)
		stack := make([]Value, len(fr.stack))
		copy(stack, fr.stack)
		out = append(out, FrameView{
			FunctionName: fr.fn.Name,
			PC:           fr.pc,
			Locals:       locals,
			Stack:        stack,
		})
	}
	return out
}

// Resume continues execution of an interpreter produced by deoptimization.
func (in *Interpreter) Resume() (Value, error) {
	return in.runLoop()
}

func isTruthy(v Value) bool {
	switch v.tag {
	case tagNil:
		return false
	case tagBool:
		return v.b
	case tagInt:
		return v.i != 0
	case tagFloat:
		return v.f != 0
	case tagString:
		return v.s != ""
	default:
		return true
	}
}

func addValues(a, b Value) Value {
	if a.IsString() || b.IsString() {
		return StringValue(a.String() + b.String())
	}
	if a.IsInt() && b.IsInt() {
		x, y := a.AsInt(), b.AsInt()
		r := x + y
		// Overflow guard: wrap is not representable as int64, so the
		// dynamic language promotes to float.
		if (y > 0 && r < x) || (y < 0 && r > x) {
			return FloatValue(float64(x) + float64(y))
		}
		return IntValue(r)
	}
	return FloatValue(a.AsNumber() + b.AsNumber())
}

func subValues(a, b Value) Value {
	if a.IsInt() && b.IsInt() {
		x, y := a.AsInt(), b.AsInt()
		r := x - y
		if (y > 0 && r > x) || (y < 0 && r < x) {
			return FloatValue(float64(x) - float64(y))
		}
		return IntValue(r)
	}
	return FloatValue(a.AsNumber() - b.AsNumber())
}
