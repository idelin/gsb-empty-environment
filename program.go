package netsec

import (
	"math/big"
	"sort"
)

func (p *Program) ensureCompiled() (*program, error) {
	if p.compiled != nil {
		return p.compiled, nil
	}
	compiled, err := compile(p.Body)
	if err != nil {
		return nil, err
	}
	p.compiled = compiled
	return compiled, nil
}

// Distribution runs the merging interpreter and returns the exact
// probability of every observable outcome. Equal configurations have their
// mass combined while their originating paths stay individually addressable.
func (p *Program) Distribution() (*Distribution, error) {
	compiled, err := p.ensureCompiled()
	if err != nil {
		return nil, err
	}
	e := &engine{prog: compiled}
	terminals, err := e.run()
	if err != nil {
		return nil, err
	}

	byKey := map[string]*Point{}
	keys := make([]string, 0, len(terminals))
	total := new(big.Rat)
	for _, tn := range terminals {
		total.Add(total, tn.mass)
		key := outcomeKey(tn.outcome)
		pt, ok := byKey[key]
		if !ok {
			pt = &Point{Outcome: tn.outcome, Prob: new(big.Rat)}
			byKey[key] = pt
			keys = append(keys, key)
		}
		pt.Prob.Add(pt.Prob, tn.mass)
	}
	if total.Cmp(big.NewRat(1, 1)) != 0 {
		return nil, &EvalError{Reason: "internal: terminal mass does not sum to 1"}
	}

	sort.Strings(keys)
	dist := &Distribution{Points: make([]Point, 0, len(keys))}
	for _, key := range keys {
		dist.Points = append(dist.Points, *byKey[key])
	}
	return dist, nil
}

// PathOutcome is one enumerated execution: the choices taken, the observable
// result, and the exact path probability.
type PathOutcome struct {
	Path    Path
	Outcome Outcome
	Prob    *big.Rat
}

// enumCfg is the mutable execution configuration threaded through the
// structural path enumerator.
type enumCfg struct {
	state   state
	path    Path
	prob    *big.Rat
	outcome Outcome
	occ     map[string]int
}

// continuation runs the statements following the current one. A nil
// continuation means the program ends normally and emits a terminal.
type continuation func(cfg enumCfg) error

// Enumerate returns every concrete execution path with its exact probability.
// It is an independent structural oracle used to cross-check the merging
// interpreter, including nested loops and early exceptions.
func (p *Program) Enumerate() ([]PathOutcome, error) {
	if _, err := p.ensureCompiled(); err != nil {
		return nil, err
	}
	en := &enumerator{}
	start := enumCfg{
		state: state{},
		path:  Path{},
		prob:  big.NewRat(1, 1),
		occ:   map[string]int{},
	}
	if err := en.seq(p.Body, start, nil); err != nil {
		return nil, err
	}
	sort.Slice(en.paths, func(i, j int) bool {
		return pathKey(en.paths[i].Path) < pathKey(en.paths[j].Path)
	})
	return en.paths, nil
}

type enumerator struct{ paths []PathOutcome }

// emit records a terminal (normal end or exception) for the current path.
func (en *enumerator) emit(cfg enumCfg) {
	en.paths = append(en.paths, PathOutcome{
		Path:    append(Path{}, cfg.path...),
		Outcome: cloneOutcome(cfg.outcome),
		Prob:    new(big.Rat).Set(cfg.prob),
	})
}

// seq runs stmts in order and then invokes k. A nil k emits a normal
// terminal. A Fail emits its exception terminal and abandons the continuation.
func (en *enumerator) seq(stmts []Stmt, cfg enumCfg, k continuation) error {
	if len(stmts) == 0 {
		if k == nil {
			en.emit(cfg)
			return nil
		}
		return k(cfg)
	}
	rest := func(c enumCfg) error { return en.seq(stmts[1:], c, k) }
	return en.stmt(stmts[0], cfg, rest)
}

// stmt runs one statement then k. Flip invokes k once per weighted branch.
func (en *enumerator) stmt(st Stmt, cfg enumCfg, k continuation) error {
	switch x := st.(type) {
	case Assign:
		v, err := evalExpr(x.Expr, cfg.state)
		if err != nil {
			return err
		}
		cfg.state = cfg.state.clone()
		cfg.state[x.Name] = v
		return en.run(k, cfg)
	case Observe:
		v, err := evalExpr(x.Expr, cfg.state)
		if err != nil {
			return err
		}
		cfg.outcome = cloneOutcome(cfg.outcome)
		cfg.outcome.Observations = append(cfg.outcome.Observations, Observation{Label: x.Label, Value: v})
		return en.run(k, cfg)
	case Fail:
		cfg.outcome = cloneOutcome(cfg.outcome)
		cfg.outcome.Exception = x.Name
		en.emit(cfg)
		return nil
	case Seq:
		return en.seq(x.Body, cfg, k)
	case Flip:
		if err := validateFlip(x); err != nil {
			return err
		}
		for i, br := range x.Branches {
			if br.Weight.Sign() == 0 {
				continue
			}
			occ := make(map[string]int, len(cfg.occ))
			for k, v := range cfg.occ {
				occ[k] = v
			}
			occ[x.NodeID]++
			child := enumCfg{
				state:   cfg.state.clone(),
				path:    append(append(Path{}, cfg.path...), TraceStep{NodeID: x.NodeID, Branch: i, Occurrence: occ[x.NodeID]}),
				prob:    new(big.Rat).Mul(cfg.prob, br.Weight),
				outcome: cloneOutcome(cfg.outcome),
				occ:     occ,
			}
			if err := en.seq(br.Body, child, k); err != nil {
				return err
			}
		}
		return nil
	case Repeat:
		if x.Count < 0 {
			return &EvalError{Reason: "repeat count must be non-negative"}
		}
		return en.repeatN(x.Body, x.Count, cfg, k)
	case While:
		if x.Limit < 0 {
			return &EvalError{Reason: "while limit must be non-negative"}
		}
		return en.whileLoop(&x, 0, cfg, k)
	default:
		return &EvalError{Reason: "unknown statement type"}
	}
}

func (en *enumerator) run(k continuation, cfg enumCfg) error {
	if k == nil {
		en.emit(cfg)
		return nil
	}
	return k(cfg)
}

// repeatN runs body count times, threading every probabilistic continuation.
func (en *enumerator) repeatN(body []Stmt, count int, cfg enumCfg, k continuation) error {
	if count == 0 {
		return en.run(k, cfg)
	}
	next := func(c enumCfg) error { return en.repeatN(body, count-1, c, k) }
	return en.seq(body, cfg, next)
}

// whileLoop runs body while cond holds, allowing at most limit iterations.
func (en *enumerator) whileLoop(w *While, iterations int, cfg enumCfg, k continuation) error {
	cond, err := evalExpr(w.Cond, cfg.state)
	if err != nil {
		return err
	}
	if !cond {
		return en.run(k, cfg)
	}
	if iterations >= w.Limit {
		return &LimitError{Limit: w.Limit}
	}
	next := func(c enumCfg) error { return en.whileLoop(w, iterations+1, c, k) }
	return en.seq(w.Body, cfg, next)
}

func pathKey(p Path) string {
	buf := make([]byte, 0, len(p)*8)
	for _, step := range p {
		buf = append(buf, byte(len(step.NodeID)>>24), byte(len(step.NodeID)>>16), byte(len(step.NodeID)>>8), byte(len(step.NodeID)))
		buf = append(buf, step.NodeID...)
		buf = append(buf, byte(step.Branch))
		buf = append(buf, byte(step.Occurrence>>24), byte(step.Occurrence>>16), byte(step.Occurrence>>8), byte(step.Occurrence))
	}
	return string(buf)
}
