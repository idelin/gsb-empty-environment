package reachability

import "fmt"

// Engine maintains the set of public object classes and their shortest
// proofs incrementally as boolean condition events arrive. It is the
// entry point a Go policy host should hold on to: build it once with
// NewEngine, then feed it Event batches via Apply.
//
// The engine keeps, per class, the best proof contributed by every
// statement, so an event only forces recomputation of the statements it
// touches (all statements of a toggled entry, or the single statement of
// a toggled tag rule) rather than the whole model.
type Engine struct {
	m *Model
	// byStmt[class][stmt] is the best proof for class entering through
	// stmt, or absent when stmt currently contributes no complete path.
	byStmt map[ClassID]map[StmtID]Proof
	// public[class] is the current shortest proof for class.
	public map[ClassID]Proof
}

// NewEngine builds an Engine and performs the initial full evaluation.
func NewEngine(m *Model) *Engine {
	e := &Engine{
		m:      m,
		byStmt: make(map[ClassID]map[StmtID]Proof, len(m.classes)),
		public: make(map[ClassID]Proof, len(m.classes)),
	}
	for sid := range m.stmts {
		e.recomputeStmt(sid)
	}
	return e
}

// Apply applies a batch of events and incrementally updates the public
// set. Events are applied in order; later events in the same batch see
// the state left by earlier ones.
func (e *Engine) Apply(events ...Event) error {
	affected := make(map[StmtID]struct{})
	for _, ev := range events {
		stmts, err := e.m.apply(ev)
		if err != nil {
			return err
		}
		for _, sid := range stmts {
			affected[sid] = struct{}{}
		}
	}
	for sid := range affected {
		e.recomputeStmt(sid)
	}
	return nil
}

// Public returns the current public classes with their shortest proofs.
// The returned map is a copy and safe for the caller to keep.
func (e *Engine) Public() map[ClassID]Proof {
	out := make(map[ClassID]Proof, len(e.public))
	for c, p := range e.public {
		out[c] = p
	}
	return out
}

// Proof returns the shortest proof for one class and whether the class
// is currently public.
func (e *Engine) Proof(class ClassID) (Proof, bool) {
	p, ok := e.public[class]
	return p, ok
}

// recomputeStmt re-evaluates every class granted by one statement and
// refreshes the per-class minimum. This is the only place where the
// three layers are joined, and it always joins them along a single
// entry path: the statement's own entry, the statement itself and its
// own tag rules.
func (e *Engine) recomputeStmt(sid StmtID) {
	m := e.m
	s := m.stmts[sid]
	entryOpen := m.entries[s.Entry].Open
	active := entryOpen && s.Allows

	touched := make(map[ClassID]struct{}, len(s.Grants))
	for _, class := range s.Grants {
		touched[class] = struct{}{}
		perStmt := e.perStmt(class)
		delete(perStmt, sid)
		if !active {
			continue
		}
		if !m.tagGated(sid, class) {
			perStmt[sid] = Proof{Class: class, Entry: s.Entry, Stmt: sid, Hops: 3}
			continue
		}
		var best *Proof
		for _, rid := range m.rulesByStmt[sid] {
			r := m.rules[rid]
			if r.Class != class || !r.Match {
				continue
			}
			p := Proof{Class: class, Entry: s.Entry, Stmt: sid, Rule: rid, Hops: 4}
			if best == nil || p.less(*best) {
				cp := p
				best = &cp
			}
		}
		if best != nil {
			perStmt[sid] = *best
		}
	}
	for class := range touched {
		e.refreshClass(class)
	}
}

func (e *Engine) perStmt(class ClassID) map[StmtID]Proof {
	perStmt, ok := e.byStmt[class]
	if !ok {
		perStmt = make(map[StmtID]Proof)
		e.byStmt[class] = perStmt
	}
	return perStmt
}

// refreshClass folds the per-statement best proofs of one class into the
// class's current shortest proof, or unpublishes the class when no
// complete entry path remains.
func (e *Engine) refreshClass(class ClassID) {
	perStmt := e.byStmt[class]
	var best *Proof
	for _, p := range perStmt {
		if best == nil || p.less(*best) {
			cp := p
			best = &cp
		}
	}
	if best == nil {
		delete(e.public, class)
		return
	}
	e.public[class] = *best
}

// Verify recomputes the public set and shortest proofs from scratch with
// FullRecompute and reports any divergence from the incrementally
// maintained state. A nil result means the incremental state is exactly
// the full-recompute state.
func (e *Engine) Verify() error {
	want := FullRecompute(e.m)
	return diff(e.public, want)
}

func diff(got, want map[ClassID]Proof) error {
	for class, wp := range want {
		gp, ok := got[class]
		if !ok {
			return fmt.Errorf("reachability: class %q public in full recompute (%s) but missing incrementally", class, wp)
		}
		if gp != wp {
			return fmt.Errorf("reachability: class %q proof mismatch: incremental %s, full recompute %s", class, gp, wp)
		}
	}
	for class, gp := range got {
		if _, ok := want[class]; !ok {
			return fmt.Errorf("reachability: class %q public incrementally (%s) but not in full recompute", class, gp)
		}
	}
	return nil
}

// CheckEvents is a convenience verifier for hosts and tests: it clones
// the model, applies the events incrementally on one copy and as a pure
// full recompute on another, and requires both to agree on the public
// classes and the shortest proofs.
func CheckEvents(m *Model, events ...Event) (map[ClassID]Proof, error) {
	inc := m.Clone()
	engine := NewEngine(inc)
	if err := engine.Apply(events...); err != nil {
		return nil, err
	}
	if err := engine.Verify(); err != nil {
		return nil, err
	}
	return engine.Public(), nil
}
