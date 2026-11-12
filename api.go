package taskpkg

// Interpret runs the reference interpreter: every leaf occurrence executes,
// no check results are shared. The returned trace is the differential-testing
// oracle for the compiled program.
func Interpret(r *Rule, inputs []bool) (*Trace, error) {
	prog, err := Compile(r)
	if err != nil {
		return nil, err
	}
	if len(inputs) != r.Inputs {
		return nil, errf("got %d inputs, rule expects %d", len(inputs), r.Inputs)
	}
	k := newKernel(prog, inputs, modeInterpret)
	k.run()
	t := k.trace
	return &t, nil
}

// Evaluate runs the compiled program. Pure leaf checks are shared through a
// per-evaluation memo table; checks that emit marks or raise are always
// re-executed at every occurrence.
func (p *Program) Evaluate(inputs []bool) (*Trace, error) {
	if len(inputs) != p.rule.Inputs {
		return nil, errf("got %d inputs, rule expects %d", len(inputs), p.rule.Inputs)
	}
	k := newKernel(p, inputs, modeCompiled)
	k.run()
	t := k.trace
	return &t, nil
}

// Rule returns the rule this program was compiled from.
func (p *Program) Rule() *Rule { return p.rule }

// NumOps reports the number of lowered CFG operations.
func (p *Program) NumOps() int { return len(p.ops) }
