package minijit

import "fmt"

// DefaultMaxInlineDepth is the default number of inlined callee layers
// allowed on top of the root physical frame.
const DefaultMaxInlineDepth = 4

// OptimizedRunner is a model of a speculative JIT tier. Calls are inlined
// into a single physical frame and non-escaping objects are scalar-replaced
// into a virtual-object table instead of being heap-allocated. A failed
// speculative guard triggers deoptimization through Deopt.
type OptimizedRunner struct {
	prog           *Program
	MaxInlineDepth int
	deoptCount     int
	lastMeta       *DeoptMetadata

	// OnDeopt, when non-nil, is invoked with the guard reason and the number
	// of reconstructed inlined layers every time deoptimization fires.
	OnDeopt func(reason string, reconstructedFrames int)
}

// NewOptimizedRunner builds an optimizing runner for prog.
func NewOptimizedRunner(prog *Program) *OptimizedRunner {
	return &OptimizedRunner{prog: prog, MaxInlineDepth: DefaultMaxInlineDepth}
}

// DeoptCount reports how many deoptimizations have occurred.
func (r *OptimizedRunner) DeoptCount() int { return r.deoptCount }

// LastMetadata returns a copy of the metadata used by the most recent
// deoptimization, or nil if none has happened.
func (r *OptimizedRunner) LastMetadata() *DeoptMetadata { return r.lastMeta }

type optFrame struct {
	fn     *Function
	pc     int
	locals []Input
	stack  []Input
	parent *optFrame
	depth  int
}

type optState struct {
	runner   *OptimizedRunner
	slots    []Value
	virtuals []VirtualObject
	refs     map[int]*Object
	filled   map[int]bool
	frames   []*optFrame
}

func (st *optState) emit(v Value) Input {
	idx := len(st.slots)
	st.slots = append(st.slots, v)
	return SlotInput(idx)
}

func (fr *optFrame) push(in Input) { fr.stack = append(fr.stack, in) }

func (fr *optFrame) pop() Input {
	last := len(fr.stack) - 1
	in := fr.stack[last]
	fr.stack = fr.stack[:last]
	return in
}

// Run executes entry with args under speculation, transparently falling back
// to the interpreter via deoptimization on guard failure.
func (r *OptimizedRunner) Run(entry string, args []Value) (Value, error) {
	fn, ok := r.prog.Lookup(entry)
	if !ok {
		return NilValue(), fmt.Errorf("minijit: unknown entry function %q", entry)
	}
	if len(args) != fn.NArgs {
		return NilValue(), fmt.Errorf("minijit: %s expects %d args, got %d", entry, fn.NArgs, len(args))
	}
	st := &optState{
		runner: r,
		refs:   map[int]*Object{},
		filled: map[int]bool{},
	}
	rootLocals := make([]Input, fn.NLocals)
	for i, a := range args {
		rootLocals[i] = st.emit(a)
	}
	for i := len(args); i < fn.NLocals; i++ {
		rootLocals[i] = LiteralInput(NilValue())
	}
	st.frames = []*optFrame{{fn: fn, locals: rootLocals}}
	return st.run()
}

func (st *optState) run() (Value, error) {
	for len(st.frames) > 0 {
		fr := st.frames[len(st.frames)-1]
		if fr.pc >= len(fr.fn.Code) {
			return NilValue(), fmt.Errorf("minijit: optimized %s ran past last instruction", fr.fn.Name)
		}
		ins := fr.fn.Code[fr.pc]
		fr.pc++
		switch ins.Op {
		case OpLoadConst:
			fr.push(LiteralInput(fr.fn.Consts[ins.B]))
		case OpLoadLocal:
			fr.push(fr.locals[ins.A])
		case OpStoreLocal:
			fr.locals[ins.A] = fr.pop()
		case OpAdd:
			bIn := fr.pop()
			aIn := fr.pop()
			a, err := st.resolve(aIn)
			if err != nil {
				return NilValue(), err
			}
			b, err := st.resolve(bIn)
			if err != nil {
				return NilValue(), err
			}
			if a.IsInt() && b.IsInt() && intOverflowsAdd(a.AsInt(), b.AsInt()) {
				// Speculated integer addition failed: restore operands and
				// re-execute this pc in the interpreter, which promotes to
				// float.
				fr.push(aIn)
				fr.push(bIn)
				fr.pc--
				return st.deoptAndResume("integer overflow in Add")
			}
			fr.push(st.emit(addValues(a, b)))
		case OpSub:
			bIn := fr.pop()
			aIn := fr.pop()
			a, _ := st.resolve(aIn)
			b, _ := st.resolve(bIn)
			fr.push(st.emit(subValues(a, b)))
		case OpLess:
			bIn := fr.pop()
			aIn := fr.pop()
			a, _ := st.resolve(aIn)
			b, _ := st.resolve(bIn)
			fr.push(st.emit(BoolValue(a.AsNumber() < b.AsNumber())))
		case OpEqual:
			bIn := fr.pop()
			aIn := fr.pop()
			a, _ := st.resolve(aIn)
			b, _ := st.resolve(bIn)
			fr.push(st.emit(BoolValue(StrictEqual(a, b))))
		case OpJump:
			fr.pc = ins.A
		case OpJumpIfFalse:
			v, err := st.resolve(fr.pop())
			if err != nil {
				return NilValue(), err
			}
			if !isTruthy(v) {
				fr.pc = ins.A
			}
		case OpNewObject:
			klass, err := st.resolve(fr.pop())
			if err != nil {
				return NilValue(), err
			}
			id := len(st.virtuals)
			st.virtuals = append(st.virtuals, VirtualObject{ID: id, Class: klass.AsString()})
			fr.push(VirtualInput(id))
		case OpGetField:
			name, err := st.resolve(fr.pop())
			if err != nil {
				return NilValue(), err
			}
			objIn := fr.pop()
			if objIn.kind == inputVirtual {
				vo := &st.virtuals[objIn.virt]
				for _, fi := range vo.Fields {
					if fi.Name == name.AsString() {
						fr.push(fi.Value)
						goto nextInstr
					}
				}
				fr.pc--
				return st.deoptAndResume("missing field on virtual object")
			}
			obj, err := st.resolve(objIn)
			if err != nil {
				return NilValue(), err
			}
			v, ok := obj.AsObject().Field(name.AsString())
			if !ok {
				fr.pc--
				return st.deoptAndResume("missing field on heap object")
			}
			fr.push(st.emit(v))
		case OpSetField:
			name, err := st.resolve(fr.pop())
			if err != nil {
				return NilValue(), err
			}
			valIn := fr.pop()
			objIn := fr.pop()
			if objIn.kind == inputVirtual {
				setVirtualField(&st.virtuals[objIn.virt], name.AsString(), valIn)
				goto nextInstr
			}
			obj, err := st.resolve(objIn)
			if err != nil {
				return NilValue(), err
			}
			val, err := st.resolve(valIn)
			if err != nil {
				return NilValue(), err
			}
			obj.AsObject().SetField(name.AsString(), val)
		case OpTransitionClass:
			klass, err := st.resolve(fr.pop())
			if err != nil {
				return NilValue(), err
			}
			objIn := fr.pop()
			if objIn.kind == inputVirtual {
				st.virtuals[objIn.virt].Class = klass.AsString()
				goto nextInstr
			}
			obj, err := st.resolve(objIn)
			if err != nil {
				return NilValue(), err
			}
			obj.AsObject().SetClass(klass.AsString())
		case OpGuardClass:
			klass, err := st.resolve(fr.pop())
			if err != nil {
				return NilValue(), err
			}
			objIn := fr.pop()
			actual := ""
			if objIn.kind == inputVirtual {
				actual = st.virtuals[objIn.virt].Class
			} else {
				v, err := st.resolve(objIn)
				if err != nil {
					return NilValue(), err
				}
				actual = v.AsObject().Class()
			}
			if actual != klass.AsString() {
				fr.push(LiteralInput(klass))
				fr.push(objIn)
				fr.pc--
				return st.deoptAndResume("hidden class guard failed: want " + klass.AsString() + ", got " + actual)
			}
		case OpCall:
			callArgs := make([]Input, ins.A)
			for i := ins.A - 1; i >= 0; i-- {
				callArgs[i] = fr.pop()
			}
			calleeIn := fr.pop()
			callee, err := st.resolve(calleeIn)
			if err != nil {
				return NilValue(), err
			}
			called := callee.AsFunction()
			if called == nil || len(callArgs) != called.NArgs {
				fr.pc--
				return st.deoptAndResume("invalid call target or arity")
			}
			if fr.depth < st.runner.MaxInlineDepth {
				locals := make([]Input, called.NLocals)
				copy(locals, callArgs)
				for i := len(callArgs); i < called.NLocals; i++ {
					locals[i] = LiteralInput(NilValue())
				}
				inner := &optFrame{
					fn:     called,
					locals: locals,
					parent: fr,
					depth:  fr.depth + 1,
				}
				st.frames = append(st.frames, inner)
				goto nextInstr
			}
			// Inline budget exhausted: execute through the interpreter tier.
			concreteArgs := make([]Value, len(callArgs))
			for i, in := range callArgs {
				v, err := st.resolve(in)
				if err != nil {
					return NilValue(), err
				}
				concreteArgs[i] = v
			}
			result, err := NewInterpreter(st.runner.prog).Run(called.Name, concreteArgs)
			if err != nil {
				fr.pc--
				return st.deoptAndResume("uninlined callee threw: " + err.Error())
			}
			fr.push(st.emit(result))
		case OpReturn:
			retIn := fr.pop()
			st.frames = st.frames[:len(st.frames)-1]
			if len(st.frames) == 0 {
				return st.resolve(retIn)
			}
			st.frames[len(st.frames)-1].push(retIn)
		case OpThrow:
			thrown, err := st.resolve(fr.pop())
			if err != nil {
				return NilValue(), err
			}
			return st.deoptAndThrow(thrown)
		case OpPop:
			fr.pop()
		default:
			return NilValue(), fmt.Errorf("minijit: unknown opcode %d in optimized tier", ins.Op)
		}
	nextInstr:
	}
	return NilValue(), fmt.Errorf("minijit: optimized stack empty without return")
}

func setVirtualField(vo *VirtualObject, name string, in Input) {
	for i := range vo.Fields {
		if vo.Fields[i].Name == name {
			vo.Fields[i].Value = in
			return
		}
	}
	vo.Fields = append(vo.Fields, FieldInput{Name: name, Value: in})
}

func intOverflowsAdd(x, y int64) bool {
	r := x + y
	return (y > 0 && r < x) || (y < 0 && r > x)
}

// resolve forces an Input to a concrete tagged value, materializing virtual
// objects on demand while preserving identity across repeated resolutions.
func (st *optState) resolve(in Input) (Value, error) {
	switch in.kind {
	case inputLiteral:
		return in.lit, nil
	case inputSlot:
		return st.slots[in.slot], nil
	case inputVirtual:
		o, err := st.materialize(in.virt)
		if err != nil {
			return NilValue(), err
		}
		return ObjectValue(o), nil
	default:
		return NilValue(), fmt.Errorf("minijit: unknown optimized input kind %d", in.kind)
	}
}

// materialize runs the same two-phase protocol as Materializer, but lazily
// against the live virtual table so an object that escapes mid-execution gets
// a stable heap identity and later mutations stay visible.
func (st *optState) materialize(id int) (*Object, error) {
	// Phase 1: shells for every virtual id known so far.
	for idx := range st.virtuals {
		vid := st.virtuals[idx].ID
		if _, ok := st.refs[vid]; !ok {
			st.refs[vid] = NewObject(st.virtuals[idx].Class)
		}
	}
	// Phase 2: fill only objects not filled yet; cycles resolve through refs.
	for idx := range st.virtuals {
		vo := &st.virtuals[idx]
		if st.filled[vo.ID] {
			continue
		}
		target := st.refs[vo.ID]
		for _, fi := range vo.Fields {
			v, err := st.resolveForFill(fi.Value)
			if err != nil {
				return nil, err
			}
			target.SetField(fi.Name, v)
		}
		st.filled[vo.ID] = true
	}
	o, ok := st.refs[id]
	if !ok {
		return nil, fmt.Errorf("minijit: unknown virtual id %d", id)
	}
	return o, nil
}

// resolveForFill resolves field inputs during phase 2. Virtual references
// resolve to the already-allocated shell without re-entering materialize,
// which breaks circular references such as A.next = B, B.next = A.
func (st *optState) resolveForFill(in Input) (Value, error) {
	if in.kind == inputVirtual {
		if o, ok := st.refs[in.virt]; ok {
			return ObjectValue(o), nil
		}
		return NilValue(), fmt.Errorf("minijit: virtual id %d has no shell yet", in.virt)
	}
	return st.resolve(in)
}

// snapshot captures the single physical frame plus the outer-to-inner
// inlined frame tree at the deoptimization point.
func (st *optState) snapshot(reason string) *DeoptMetadata {
	var root, prev *FrameState
	for _, of := range st.frames {
		locals := make([]Input, len(of.locals))
		copy(locals, of.locals)
		stack := make([]Input, len(of.stack))
		copy(stack, of.stack)
		fs := &FrameState{Function: of.fn, PC: of.pc, Locals: locals, Stack: stack}
		if root == nil {
			root = fs
		} else {
			prev.Inner = fs
		}
		prev = fs
	}
	virtuals := make([]VirtualObject, len(st.virtuals))
	for i, vo := range st.virtuals {
		fields := make([]FieldInput, len(vo.Fields))
		copy(fields, vo.Fields)
		virtuals[i] = VirtualObject{ID: vo.ID, Class: vo.Class, Fields: fields}
	}
	slots := make([]Value, len(st.slots))
	copy(slots, st.slots)
	return &DeoptMetadata{
		Reason: reason,
		Machine: &MachineFrame{
			Slots:    slots,
			Virtuals: virtuals,
			Root:     root,
		},
	}
}

// deoptAndResume builds deoptimization metadata, rebuilds interpreter frames
// outer-to-inner, materializes virtual objects and resumes execution at the
// guard's bytecode pc.
func (st *optState) deoptAndResume(reason string) (Value, error) {
	meta := st.snapshot(reason)
	st.runner.lastMeta = meta
	res, err := Deopt(st.runner.prog, meta)
	if err != nil {
		return NilValue(), err
	}
	st.runner.deoptCount++
	if st.runner.OnDeopt != nil {
		st.runner.OnDeopt(reason, res.Depth)
	}
	return res.Interpreter.Resume()
}

// deoptAndThrow rebuilds the interpreter frames and then throws v through
// the interpreter's handler tables, unwinding the reconstructed stack
// layer-by-layer exactly as a bytecode-level throw would.
func (st *optState) deoptAndThrow(v Value) (Value, error) {
	meta := st.snapshot("throw after deoptimization point")
	st.runner.lastMeta = meta
	res, err := Deopt(st.runner.prog, meta)
	if err != nil {
		return NilValue(), err
	}
	st.runner.deoptCount++
	if st.runner.OnDeopt != nil {
		st.runner.OnDeopt(meta.Reason, res.Depth)
	}
	return res.Interpreter.throw(v)
}
