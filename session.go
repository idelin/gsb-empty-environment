package groupopt

import (
	"fmt"
	"sort"
	"time"
)

// Session is a stateful solver across checkpoints.
//
// Executed setup actions are non-refundable: their group fees stay sunk
// and count against every later budget. Committed options are irrevocable
// and must appear in every later solution. Profits and the budget may be
// revised at checkpoints; each revision (and each new commitment) bumps
// the epoch, discards the search tree, and invalidates every certificate
// issued under earlier epochs — stale optimality proofs are never reused.
type Session struct {
	inst      Instance
	c         *compiled
	budget    int64
	committed map[int]bool
	paid      map[int]bool
	epoch     int
	eng       *solver
}

// NewSession validates the instance and opens epoch 1.
func NewSession(inst Instance, budget int64) (*Session, error) {
	if err := inst.Validate(); err != nil {
		return nil, err
	}
	if budget < 0 {
		return nil, fmt.Errorf("groupopt: negative budget %d", budget)
	}
	s := &Session{
		inst:      copyInstance(inst),
		budget:    budget,
		committed: make(map[int]bool),
		paid:      make(map[int]bool),
		epoch:     1,
	}
	s.rebuild()
	return s, nil
}

func copyInstance(in Instance) Instance {
	out := Instance{
		Options:  make([]Option, len(in.Options)),
		SetupFee: make(map[int]int64, len(in.SetupFee)),
	}
	for i, o := range in.Options {
		req := make([]int, len(o.Requires))
		copy(req, o.Requires)
		o.Requires = req
		out.Options[i] = o
	}
	for g, f := range in.SetupFee {
		out.SetupFee[g] = f
	}
	return out
}

// rebuild discards all search state and re-derives it from the committed
// closure. Called on every mutation so no stale bound survives a revision.
func (s *Session) rebuild() {
	s.c = compile(s.inst)
	s.eng = newSolver(s.c, s.budget, s.committed, s.paid)
}

// ExecuteSetup records that a group's setup action has been performed.
// The fee is sunk from now on and counts against the budget even if no
// option of the group is ever selected. No-op if already executed.
func (s *Session) ExecuteSetup(group int) error {
	if _, ok := s.c.groupNum[group]; !ok {
		return fmt.Errorf("groupopt: unknown group %d", group)
	}
	if s.paid[group] {
		return nil
	}
	s.paid[group] = true
	s.epoch++
	s.rebuild()
	return nil
}

// CommitOption marks an option irrevocably selected. Its transitive
// requirements are forced into every later solution as well.
func (s *Session) CommitOption(id int) error {
	if _, ok := s.c.index[id]; !ok {
		return fmt.Errorf("groupopt: unknown option %d", id)
	}
	if s.committed[id] {
		return nil
	}
	s.committed[id] = true
	s.epoch++
	s.rebuild()
	return nil
}

// Revise applies a checkpoint revision: a new budget and new profits for
// the listed option ids. Committed options and paid setups are preserved.
func (s *Session) Revise(budget int64, profits map[int]int64) error {
	if budget < 0 {
		return fmt.Errorf("groupopt: negative budget %d", budget)
	}
	for id, p := range profits {
		i, ok := s.c.index[id]
		if !ok {
			return fmt.Errorf("groupopt: unknown option %d", id)
		}
		s.inst.Options[i].Profit = p
	}
	s.budget = budget
	s.epoch++
	s.rebuild()
	return nil
}

// Solve runs the search for at most limit wall-clock time; limit <= 0
// means no time limit. Within one epoch, successive calls resume the
// search exactly where the previous call stopped.
func (s *Session) Solve(limit time.Duration) Result {
	if s.eng.infeasible {
		return Result{
			Status: Infeasible,
			Epoch:  s.epoch,
			Cert:   &Certificate{Epoch: s.epoch},
		}
	}
	var deadline time.Time
	if limit > 0 {
		deadline = time.Now().Add(limit)
	}
	s.eng.search(deadline)
	return s.eng.result(s.epoch)
}

// State is the committed closure view an independent verifier needs.
type State struct {
	Budget     int64
	Committed  []int // irrevocably selected option ids, sorted
	PaidGroups []int // groups whose setup fee is sunk, sorted
	Epoch      int
}

// State returns the current committed closure and epoch.
func (s *Session) State() State {
	st := State{Budget: s.budget, Epoch: s.epoch}
	for id := range s.committed {
		st.Committed = append(st.Committed, id)
	}
	for g := range s.paid {
		st.PaidGroups = append(st.PaidGroups, g)
	}
	sort.Ints(st.Committed)
	sort.Ints(st.PaidGroups)
	return st
}

// Instance returns a copy of the instance with all revisions applied.
func (s *Session) Instance() Instance {
	return copyInstance(s.inst)
}
