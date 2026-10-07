package plansel

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"time"
)

func errUnknownPlan(id int) error {
	return fmt.Errorf("plansel: unknown plan ID %d", id)
}

// Session tracks committed state across checkpoints.
//
// A preparation, once executed (Prepare, or implicitly by committing a plan
// of that group), is non-refundable: its fee is sunk and the group never
// charges a fee again. A committed plan is irrevocable: every later solution
// keeps it selected, its cost is sunk, and its group is prepared. Profits
// and the budget may be revised at checkpoints via Revise; any revision
// invalidates outstanding resume tokens, so stale optimality proofs and
// bounds can never be carried over.
type Session struct {
	inst      Instance
	committed map[int]bool
	prepared  map[int]bool
	sunk      int64
	epoch     uint64
}

// NewSession validates inst and starts a fresh session.
func NewSession(inst Instance) (*Session, error) {
	if err := inst.Validate(); err != nil {
		return nil, err
	}
	return &Session{
		inst:      inst.clone(),
		committed: make(map[int]bool),
		prepared:  make(map[int]bool),
		epoch:     1,
	}, nil
}

// Instance returns a copy of the current (possibly revised) instance.
func (s *Session) Instance() Instance { return s.inst.clone() }

// View returns the committed selections and executed preparations.
func (s *Session) View() View {
	return View{Committed: s.Committed(), Prepared: s.Prepared()}
}

// Committed returns the sorted IDs of irrevocably selected plans.
func (s *Session) Committed() []int {
	out := make([]int, 0, len(s.committed))
	for id := range s.committed {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

// Prepared returns the sorted IDs of groups whose preparation was executed.
func (s *Session) Prepared() []int {
	out := make([]int, 0, len(s.prepared))
	for g := range s.prepared {
		out = append(out, g)
	}
	sort.Ints(out)
	return out
}

// Sunk returns the total non-refundable spend so far: costs of committed
// plans plus fees of prepared groups.
func (s *Session) Sunk() int64 { return s.sunk }

// Prepare executes the preparation of the given groups. The fee is sunk
// immediately and is never charged again, even if no plan of the group is
// ever selected. Preparing an already prepared group is a no-op.
func (s *Session) Prepare(groups ...int) error {
	changed := false
	for _, g := range groups {
		if s.prepared[g] {
			continue
		}
		s.prepared[g] = true
		s.sunk += s.inst.GroupFee[g]
		changed = true
	}
	if changed {
		s.epoch++
	}
	return nil
}

// Commit marks plans as irrevocably selected. The dependency closure is
// added automatically (a plan cannot be committed without its
// requirements), and the groups of all committed plans become prepared.
// Plan costs and newly prepared group fees are sunk.
func (s *Session) Commit(ids ...int) error {
	byID := make(map[int]Plan, len(s.inst.Plans))
	for _, p := range s.inst.Plans {
		byID[p.ID] = p
	}
	closure := make(map[int]bool)
	queue := append([]int(nil), ids...)
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			return errUnknownPlan(id)
		}
	}
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if closure[id] {
			continue
		}
		closure[id] = true
		queue = append(queue, byID[id].Requires...)
	}
	changed := false
	for id := range closure {
		if s.committed[id] {
			continue
		}
		s.committed[id] = true
		s.sunk += byID[id].Cost
		changed = true
		if g := byID[id].Group; !s.prepared[g] {
			s.prepared[g] = true
			s.sunk += s.inst.GroupFee[g]
		}
	}
	if changed {
		s.epoch++
	}
	return nil
}

// Revise installs a new budget and overrides profits of the given plans at
// a checkpoint. Committed plans and prepared groups are preserved; their
// costs stay sunk and are not charged against the new budget.
func (s *Session) Revise(budget int64, profits map[int]int64) error {
	if budget < 0 {
		return fmt.Errorf("plansel: negative budget %d", budget)
	}
	known := make(map[int]bool, len(s.inst.Plans))
	for _, p := range s.inst.Plans {
		known[p.ID] = true
	}
	for id := range profits {
		if !known[id] {
			return errUnknownPlan(id)
		}
	}
	for i := range s.inst.Plans {
		if p, ok := profits[s.inst.Plans[i].ID]; ok {
			s.inst.Plans[i].Profit = p
		}
	}
	s.inst.Budget = budget
	s.epoch++
	return nil
}

// Solve searches from scratch under the current committed state. A zero
// deadline means no time limit. If the deadline stops the search, the
// result has Status Feasible, Lower/Upper bracket the optimum, and
// ResumeToken continues the same search later.
func (s *Session) Solve(deadline time.Time) (Result, error) {
	sv, err := newSolver(s.inst, s.committed, s.prepared)
	if err != nil {
		return Result{}, err
	}
	sv.run(deadline)
	res := sv.result()
	if res.Status == Feasible {
		res.token = s.marshalToken(sv)
	}
	return res, nil
}

// Resume continues an interrupted search. The token must have been issued
// for the current checkpoint state; any Commit, Prepare, or Revise since
// then makes it stale and Resume returns an error instead of reusing old
// bounds.
func (s *Session) Resume(tok []byte, deadline time.Time) (Result, error) {
	if len(tok) == 0 {
		return Result{}, errors.New("plansel: empty resume token")
	}
	t, err := decodeToken(tok)
	if err != nil {
		return Result{}, fmt.Errorf("plansel: corrupt resume token: %w", err)
	}
	if t.Epoch != s.epoch {
		return Result{}, errors.New("plansel: stale resume token: checkpoint state changed since token was issued")
	}
	if t.Hash != s.fingerprint() {
		return Result{}, errors.New("plansel: stale resume token: instance changed since token was issued")
	}
	sv, err := newSolver(s.inst, s.committed, s.prepared)
	if err != nil {
		return Result{}, err
	}
	sv.stack = append([]frame(nil), t.Stack...)
	sv.bestProfit, sv.bestCost, sv.bestMask = t.BestProfit, t.BestCost, t.BestMask
	sv.run(deadline)
	res := sv.result()
	if res.Status == Feasible {
		res.token = s.marshalToken(sv)
	}
	return res, nil
}

func (s *Session) marshalToken(sv *solver) []byte {
	return token{
		Epoch:      s.epoch,
		Hash:       s.fingerprint(),
		Stack:      append([]frame(nil), sv.stack...),
		BestProfit: sv.bestProfit,
		BestCost:   sv.bestCost,
		BestMask:   sv.bestMask,
	}.encode()
}

// fingerprint hashes everything that determines the search: the revised
// instance plus the committed and prepared sets.
func (s *Session) fingerprint() uint64 {
	h := fnv.New64a()
	var buf [8]byte
	put := func(v int64) {
		binary.LittleEndian.PutUint64(buf[:], uint64(v))
		h.Write(buf[:])
	}
	put(s.inst.Budget)
	for _, p := range s.inst.Plans {
		put(int64(p.ID))
		put(int64(p.Group))
		put(p.Cost)
		put(p.Profit)
		req := append([]int(nil), p.Requires...)
		sort.Ints(req)
		put(int64(len(req)))
		for _, r := range req {
			put(int64(r))
		}
	}
	fees := make([]int, 0, len(s.inst.GroupFee))
	for g := range s.inst.GroupFee {
		fees = append(fees, g)
	}
	sort.Ints(fees)
	put(int64(len(fees)))
	for _, g := range fees {
		put(int64(g))
		put(s.inst.GroupFee[g])
	}
	for _, id := range s.Committed() {
		put(int64(id))
	}
	put(-1)
	for _, g := range s.Prepared() {
		put(int64(g))
	}
	return h.Sum64()
}
