package typeset

import (
	"fmt"
	"math/rand"
	"sort"
)

// SoundnessFailure describes a concrete call that the abstract candidate set
// failed to allow. A non-nil failure means the protection rule would block a
// call that the finite execution model can actually make.
type SoundnessFailure struct {
	CallSite    string
	Method      string
	AllocPoint  string
	MostDerived string
	KeyString   string
	Target      MethodDecl
	Reason      string
}

func (f *SoundnessFailure) Error() string {
	return fmt.Sprintf("typeset: soundness failure at %s: %s on object %s (%s), target %s.%s: %s",
		f.CallSite, f.Method, f.AllocPoint, f.MostDerived,
		f.Target.Class, f.Target.Method, f.Reason)
}

// CheckSoundness executes a concrete finite-object program, runs the abstract
// analysis over the same facts, and proves that every concrete dispatch is
// covered by an allowed target. It returns *SoundnessFailure on the first
// missing target; nil means the candidate set is sound for this program.
func (a *Analyzer) CheckSoundness(program *ConcreteProgram) error {
	trace, err := a.Execute(program)
	if err != nil {
		return err
	}
	solution, err := a.Analyze(program.ToFacts())
	if err != nil {
		return err
	}

	byCall := make(map[string]*CallResult, len(solution.Calls))
	for i := range solution.Calls {
		byCall[solution.Calls[i].CallSite] = &solution.Calls[i]
	}

	for _, call := range trace.Calls {
		result := byCall[call.CallSite]
		if result == nil {
			return &SoundnessFailure{
				CallSite:    call.CallSite,
				Method:      call.Method,
				AllocPoint:  call.AllocPoint,
				MostDerived: call.MostDerived,
				KeyString:   call.KeyString,
				Target:      call.Target,
				Reason:      "call site absent from abstract solution",
			}
		}
		if !targetAllowed(*result, call) {
			return &SoundnessFailure{
				CallSite:    call.CallSite,
				Method:      call.Method,
				AllocPoint:  call.AllocPoint,
				MostDerived: call.MostDerived,
				KeyString:   call.KeyString,
				Target:      call.Target,
				Reason:      "no matching allowlist entry",
			}
		}
	}
	return nil
}

func targetAllowed(result CallResult, call ConcreteCall) bool {
	for _, t := range result.Targets {
		if t.Decl != call.Target {
			continue
		}
		if t.Origin.AllocPoint != call.AllocPoint ||
			t.Origin.MostDerived != call.MostDerived {
			continue
		}
		if t.subKey.String() == call.KeyString {
			return true
		}
	}
	return false
}

// GenerateProgram builds a random but well-formed finite-object program over
// the analyzer's hierarchy. It is the workload generator for CheckSoundness:
// every generated program executes without ambiguity, and each dynamic type
// it produces is reachable through an explicit inheritance path.
func (a *Analyzer) GenerateProgram(rng *rand.Rand, instrCount int) *ConcreteProgram {
	if rng == nil {
		rng = rand.New(rand.NewSource(1))
	}
	if instrCount < 1 {
		instrCount = 1
	}

	program := &ConcreteProgram{}
	world := &genWorld{
		vars:   make(map[string]*genVar),
		cells:  make(map[genCell]*genVar),
		fields: make(map[string]bool),
	}

	nextVar := 0
	allocSeq := 0
	freshVar := func() string {
		name := fmt.Sprintf("v%d", nextVar)
		nextVar++
		return name
	}

	// Every execution starts with at least one allocation so calls exist.
	seed := a.randomConcreteObject(rng, &allocSeq, freshVar)
	program.Instructions = append(program.Instructions, seed.ins)
	world.vars[seed.ins.Dst] = seed.state

	for len(program.Instructions) < instrCount {
		known := knownVars(world.vars)
		if len(known) == 0 {
			obj := a.randomConcreteObject(rng, &allocSeq, freshVar)
			program.Instructions = append(program.Instructions, obj.ins)
			world.vars[obj.ins.Dst] = obj.state
			continue
		}

		switch rng.Intn(6) {
		case 0:
			obj := a.randomConcreteObject(rng, &allocSeq, freshVar)
			program.Instructions = append(program.Instructions, obj.ins)
			world.vars[obj.ins.Dst] = obj.state
		case 1:
			src := pick(rng, known)
			dst := freshVar()
			program.Instructions = append(program.Instructions, ExecInstr{Op: OpCopy, Src: src, Dst: dst})
			world.vars[dst] = cloneState(world.vars[src])
		case 2:
			if cast := a.randomCast(rng, pick(rng, known), world, freshVar); cast != nil {
				program.Instructions = append(program.Instructions, cast.ins)
				world.vars[cast.ins.Dst] = cast.state
			}
		case 3:
			if pair := randomFieldStore(rng, known, world); pair != nil {
				program.Instructions = append(program.Instructions, *pair)
			}
		case 4:
			if pair := randomFieldLoad(rng, known, world, freshVar); pair != nil {
				program.Instructions = append(program.Instructions, pair.ins)
				if pair.state != nil {
					world.vars[pair.ins.Dst] = pair.state
				} else {
					delete(world.vars, pair.ins.Dst)
				}
			}
		case 5:
			if call := a.randomConcreteCall(rng, pick(rng, known), world, len(program.Instructions)); call != nil {
				for len(program.Calls) <= len(program.Instructions) {
					program.Calls = append(program.Calls, nil)
				}
				program.Calls[len(program.Instructions)-1] = append(
					program.Calls[len(program.Instructions)-1], *call)
			}
		}
	}

	// Emit a final round of calls so every live variable is exercised.
	for _, name := range knownVars(world.vars) {
		if call := a.randomConcreteCall(rng, name, world, len(program.Instructions)); call != nil {
			for len(program.Calls) <= len(program.Instructions) {
				program.Calls = append(program.Calls, nil)
			}
			program.Calls[len(program.Instructions)] = append(
				program.Calls[len(program.Instructions)], *call)
		}
	}
	return program
}

type genVar struct {
	allocPoint  string
	mostDerived string
	key         subKey
	class       string
}

func cloneState(s *genVar) *genVar {
	cp := *s
	return &cp
}

type genCell struct {
	allocPoint  string
	mostDerived string
	key         subKey
	fieldName   string
}

type generatedObject struct {
	ins   ExecInstr
	state *genVar
}

type genWorld struct {
	vars   map[string]*genVar
	cells  map[genCell]*genVar
	fields map[string]bool
}

func genCellOf(v *genVar, field string) genCell {
	return genCell{
		allocPoint:  v.allocPoint,
		mostDerived: v.mostDerived,
		key:         v.key,
		fieldName:   field,
	}
}

func (a *Analyzer) randomConcreteObject(rng *rand.Rand, seq *int, freshVar func() string) generatedObject {
	class := a.order[rng.Intn(len(a.order))]
	*seq = *seq + 1
	alloc := fmt.Sprintf("alloc%d", *seq)
	dst := freshVar()
	return generatedObject{
		ins: ExecInstr{
			Op:         OpAlloc,
			AllocPoint: alloc,
			Class:      class,
			Dst:        dst,
		},
		state: &genVar{
			allocPoint:  alloc,
			mostDerived: class,
			key:         rootKey(class),
			class:       class,
		},
	}
}

type generatedCast struct {
	ins   ExecInstr
	state *genVar
}

func (a *Analyzer) randomCast(rng *rand.Rand, srcName string, world *genWorld, freshVar func() string) *generatedCast {
	src := world.vars[srcName]
	c := a.classes[src.class]
	if len(c.DirectBases) == 0 {
		return nil
	}
	base := c.DirectBases[rng.Intn(len(c.DirectBases))]
	step := PathStep{From: src.class, To: base.Name, Virtual: base.Virtual}
	dst := freshVar()
	return &generatedCast{
		ins: ExecInstr{
			Op:   OpCast,
			Src:  srcName,
			Dst:  dst,
			Path: []PathStep{step},
		},
		state: &genVar{
			allocPoint:  src.allocPoint,
			mostDerived: src.mostDerived,
			key:         appendKey(src.key, []PathStep{step}),
			class:       base.Name,
		},
	}
}

func randomFieldStore(rng *rand.Rand, vars []string, world *genWorld) *ExecInstr {
	base := pick(rng, vars)
	src := pick(rng, vars)
	field := fmt.Sprintf("f%d", rng.Intn(4))
	world.fields[field] = true
	ins := &ExecInstr{
		Op:        OpFieldStore,
		Base:      base,
		FieldName: field,
		Src:       src,
	}
	world.cells[genCellOf(world.vars[base], field)] = cloneState(world.vars[src])
	return ins
}

type generatedLoad struct {
	ins   ExecInstr
	state *genVar
}

func randomFieldLoad(rng *rand.Rand, vars []string, world *genWorld, freshVar func() string) *generatedLoad {
	if len(world.fields) == 0 {
		return nil
	}
	base := pick(rng, vars)
	field := pick(rng, fieldsList(world.fields))
	dst := freshVar()
	ins := ExecInstr{Op: OpFieldLoad, Base: base, FieldName: field, Dst: dst}
	if src, ok := world.cells[genCellOf(world.vars[base], field)]; ok {
		return &generatedLoad{ins: ins, state: cloneState(src)}
	}
	return &generatedLoad{ins: ins}
}

func (a *Analyzer) randomConcreteCall(rng *rand.Rand, recvName string, world *genWorld, _ int) *CallFact {
	recv := world.vars[recvName]
	if recv == nil {
		return nil
	}
	table, err := a.OverrideTableFor(recv.mostDerived)
	if err != nil {
		return nil
	}
	methods := table.CallableMethods(recv.key)
	if len(methods) == 0 {
		return nil
	}
	sort.Strings(methods)
	method := methods[rng.Intn(len(methods))]
	return &CallFact{
		CallSite: fmt.Sprintf("call_%s_%s", recvName, method),
		Recv:     recvName,
		Method:   method,
	}
}

func knownVars(state map[string]*genVar) []string {
	out := make([]string, 0, len(state))
	for name, s := range state {
		if s != nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func fieldsList(fields map[string]bool) []string {
	out := make([]string, 0, len(fields))
	for f := range fields {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func pick[T any](rng *rand.Rand, xs []T) T {
	return xs[rng.Intn(len(xs))]
}
