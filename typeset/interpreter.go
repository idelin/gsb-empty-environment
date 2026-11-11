package typeset

// ExecOp enumerates the instructions of the finite-object execution model.
type ExecOp int

const (
	OpAlloc ExecOp = iota
	OpCopy
	OpCast
	OpFieldStore
	OpFieldLoad
)

// ExecInstr is one concrete instruction executed by the reference
// interpreter. It mirrors the fact kinds but keeps an explicit ordering,
// which is what real execution has.
type ExecInstr struct {
	Op         ExecOp
	AllocPoint string
	Class      string
	Base       string
	Src        string
	Dst        string
	FieldName  string
	Path       []PathStep
}

// ConcreteCall is one virtual call made while executing a concrete program.
type ConcreteCall struct {
	CallSite string
	Method   string
	Target   MethodDecl

	// AllocPoint and MostDerived identify the receiver object at runtime.
	AllocPoint  string
	MostDerived string

	// KeyString is the canonical subobject key of the receiver.
	KeyString string
}

// ConcreteProgram is a deterministic, finite program: an instruction list
// plus the virtual calls issued between instructions (modelled as part of
// the trace so every call sees a concrete receiver state).
type ConcreteProgram struct {
	Instructions []ExecInstr

	// Calls is executed in parallel with Instructions: each entry lists the
	// calls made after the instruction at the same index. A trailing entry
	// beyond the last instruction index is also allowed.
	Calls [][]CallFact
}

type concreteValue struct {
	allocPoint  string
	mostDerived string
	key         subKey
}

type concreteCell struct {
	value     concreteValue
	fieldName string
}

// ConcreteTrace is the observable result of one finite execution.
type ConcreteTrace struct {
	Calls []ConcreteCall
}

// Execute runs the finite-object program deterministically. A variable that
// has never been assigned is the nil pointer; a call through it is skipped
// (the program would have crashed before reaching the virtual dispatch).
func (a *Analyzer) Execute(p *ConcreteProgram) (*ConcreteTrace, error) {
	if p == nil {
		return nil, programError("nil concrete program")
	}
	vars := make(map[string]concreteValue)
	cells := make(map[concreteCell]concreteValue)
	trace := &ConcreteTrace{}

	emitCalls := func(calls []CallFact) error {
		for _, call := range calls {
			recv, ok := vars[call.Recv]
			if !ok {
				continue // nil receiver: no virtual dispatch occurs
			}
			table, err := a.OverrideTableFor(recv.mostDerived)
			if err != nil {
				return err
			}
			outcome := table.Resolve(recv.key, call.Method)
			if !outcome.HasSlot || outcome.Ambiguous {
				// Ambiguous programs do not compile/link; the generator
				// avoids producing such executions.
				continue
			}
			trace.Calls = append(trace.Calls, ConcreteCall{
				CallSite:    call.CallSite,
				Method:      call.Method,
				Target:      outcome.Targets[0],
				AllocPoint:  recv.allocPoint,
				MostDerived: recv.mostDerived,
				KeyString:   recv.key.String(),
			})
		}
		return nil
	}

	for i, ins := range p.Instructions {
		switch ins.Op {
		case OpAlloc:
			vars[ins.Dst] = concreteValue{
				allocPoint:  ins.AllocPoint,
				mostDerived: ins.Class,
				key:         rootKey(ins.Class),
			}
		case OpCopy:
			if v, ok := vars[ins.Src]; ok {
				vars[ins.Dst] = v
			} else {
				delete(vars, ins.Dst)
			}
		case OpCast:
			v, ok := vars[ins.Src]
			if !ok {
				delete(vars, ins.Dst)
				break
			}
			vars[ins.Dst] = concreteValue{
				allocPoint:  v.allocPoint,
				mostDerived: v.mostDerived,
				key:         appendKey(v.key, ins.Path),
			}
		case OpFieldStore:
			if base, ok := vars[ins.Base]; ok {
				if src, ok := vars[ins.Src]; ok {
					cells[concreteCell{value: base, fieldName: ins.FieldName}] = src
				}
			}
		case OpFieldLoad:
			base, ok := vars[ins.Base]
			if !ok {
				delete(vars, ins.Dst)
				break
			}
			if src, ok := cells[concreteCell{value: base, fieldName: ins.FieldName}]; ok {
				vars[ins.Dst] = src
			} else {
				delete(vars, ins.Dst)
			}
		default:
			return nil, programError("unknown exec opcode %d", ins.Op)
		}

		if i < len(p.Calls) {
			if err := emitCalls(p.Calls[i]); err != nil {
				return nil, err
			}
		}
	}
	if len(p.Calls) > len(p.Instructions) {
		if err := emitCalls(p.Calls[len(p.Instructions)]); err != nil {
			return nil, err
		}
	}
	return trace, nil
}

// ToFacts translates an executable program into the unordered fact set
// consumed by Analyze. Allocation provenance is fully preserved.
func (p *ConcreteProgram) ToFacts() []Fact {
	facts := make([]Fact, 0, len(p.Instructions))
	for _, ins := range p.Instructions {
		switch ins.Op {
		case OpAlloc:
			facts = append(facts, AllocFact{AllocPoint: ins.AllocPoint, Class: ins.Class, Dst: ins.Dst})
		case OpCopy:
			facts = append(facts, CopyFact{Src: ins.Src, Dst: ins.Dst})
		case OpCast:
			facts = append(facts, CastFact{Src: ins.Src, Dst: ins.Dst, Path: ins.Path})
		case OpFieldStore:
			facts = append(facts, FieldStoreFact{Base: ins.Base, FieldName: ins.FieldName, Src: ins.Src})
		case OpFieldLoad:
			facts = append(facts, FieldLoadFact{Base: ins.Base, FieldName: ins.FieldName, Dst: ins.Dst})
		}
	}
	for _, calls := range p.Calls {
		for _, call := range calls {
			facts = append(facts, call)
		}
	}
	return facts
}
