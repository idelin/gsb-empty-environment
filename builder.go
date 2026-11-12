package netsec

// NewProgram creates an empty program with the given entry function.
func NewProgram(entry string) *ProgramBuilder {
	return &ProgramBuilder{program: Program{Entry: entry}}
}

// ProgramBuilder is a small convenience builder for Program values.
type ProgramBuilder struct {
	program Program
}

// Function adds a function definition.
func (b *ProgramBuilder) Function(name string, frameSize int64) *ProgramBuilder {
	b.program.Functions = append(b.program.Functions, Function{Name: name, FrameSize: frameSize})
	return b
}

// Call adds a normal call edge to the most recently added function.
func (b *ProgramBuilder) Call(target string, delta int64) *ProgramBuilder {
	return b.CallWith(target, CallNormal, delta, Guard{})
}

// TailCall adds a tail call edge to the most recently added function.
func (b *ProgramBuilder) TailCall(target string, delta int64) *ProgramBuilder {
	return b.CallWith(target, CallTail, delta, Guard{})
}

// CallWith adds a guarded call edge to the most recently added function.
func (b *ProgramBuilder) CallWith(target string, kind CallKind, delta int64, guard Guard) *ProgramBuilder {
	if len(b.program.Functions) == 0 {
		return b
	}
	last := &b.program.Functions[len(b.program.Functions)-1]
	last.Calls = append(last.Calls, Edge{
		Target: target,
		Kind:   kind,
		Guard:  guard,
		Delta:  delta,
	})
	return b
}

// Build returns the assembled program.
func (b *ProgramBuilder) Build() Program {
	return b.program
}
