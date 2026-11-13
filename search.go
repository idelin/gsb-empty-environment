package netsec

import (
	"fmt"
	"sort"
)

type candidate struct {
	idx     int
	id      PageID
	targets []PhysicalPage
}

type constraints struct {
	must  map[PageID]bool
	stay  map[PageID]bool
	exact map[PageID]PhysicalPage
}

func emptyConstraints() constraints {
	return constraints{
		must:  make(map[PageID]bool),
		stay:  make(map[PageID]bool),
		exact: make(map[PageID]PhysicalPage),
	}
}

type searchStatus uint8

const (
	searchNotFound searchStatus = iota
	searchFound
	searchExhausted
)

type solver struct {
	s          *scenario
	candidates []candidate
	candByID   map[PageID]int
	free       []PhysicalPage

	budget    int
	nodes     int
	exhausted bool

	lastFound    []Move
	hasLastFound bool
}

func newSolver(s *scenario, candidates []candidate, budget int) *solver {
	if budget <= 0 {
		budget = DefaultSearchBudget
	}
	candByID := make(map[PageID]int, len(candidates))
	for i := range candidates {
		candByID[candidates[i].id] = i
	}
	return &solver{
		s:          s,
		candidates: candidates,
		candByID:   candByID,
		free:       append([]PhysicalPage(nil), s.free...),
		budget:     budget,
	}
}

func (sv *solver) tick() bool {
	if sv.nodes >= sv.budget {
		sv.exhausted = true
		return false
	}
	sv.nodes++
	return true
}

func (sv *solver) candidateFor(id PageID) (candidate, bool) {
	i, ok := sv.candByID[id]
	if !ok {
		return candidate{}, false
	}
	return sv.candidates[i], true
}

func (sv *solver) exists(K int, c constraints, requireSequence bool) searchStatus {
	if sv.exhausted || K < 0 {
		return searchExhausted
	}
	if len(c.must) > K {
		return searchNotFound
	}
	for id := range c.must {
		if _, ok := sv.candByID[id]; !ok {
			return searchNotFound
		}
		if c.stay[id] {
			return searchNotFound
		}
	}
	for id, target := range c.exact {
		if _, ok := sv.candByID[id]; !ok {
			return searchNotFound
		}
		if c.stay[id] {
			return searchNotFound
		}
		if !containsPhysical(sv.free, target) {
			return searchNotFound
		}
	}

	e := newEngine(sv.s)
	used := make(map[PhysicalPage]bool, len(c.exact))
	exactIDs := sortedConstraintIDs(c.exact)
	for _, id := range exactIDs {
		target := c.exact[id]
		if !e.usefulTarget(e.s.pageByID[id], target) {
			return searchNotFound
		}
		if used[target] {
			return searchNotFound
		}
		used[target] = true
		e.applyMove(e.s.pageByID[id], target)
	}
	if e.movedCount() > K {
		return searchNotFound
	}

	ctx := &searchContext{
		sv:              sv,
		e:               e,
		K:               K,
		c:               c,
		used:            used,
		requireSequence: requireSequence,
	}
	return ctx.dfs()
}

type searchContext struct {
	sv              *solver
	e               *engine
	K               int
	c               constraints
	used            map[PhysicalPage]bool
	requireSequence bool
}

func (ctx *searchContext) dfs() searchStatus {
	if !ctx.sv.tick() {
		return searchExhausted
	}
	if lower, feasible := ctx.remainingMoveLowerBound(); !feasible || lower > ctx.K {
		return searchNotFound
	}

	if ctx.e.w == 0 {
		pending, ok := ctx.nextPendingMust()
		if !ok {
			if ctx.requireSequence {
				sequenceOK, exhausted := ctx.sv.sequenceExists(ctx.e.currentMoves())
				if exhausted {
					return searchExhausted
				}
				if !sequenceOK {
					return searchNotFound
				}
			}
			ctx.sv.lastFound = ctx.e.currentMoves()
			ctx.sv.hasLastFound = true
			return searchFound
		}
		if ctx.e.movedCount() >= ctx.K {
			return searchNotFound
		}
		cand, _ := ctx.sv.candidateFor(pending)
		for _, target := range cand.targets {
			if ctx.used[target] {
				continue
			}
			ctx.used[target] = true
			u := ctx.e.applyMove(cand.idx, target)
			status := ctx.dfs()
			ctx.e.undo(u)
			delete(ctx.used, target)
			switch status {
			case searchFound:
				return searchFound
			case searchExhausted:
				return searchExhausted
			}
		}
		return searchNotFound
	}

	if ctx.e.movedCount() >= ctx.K {
		return searchNotFound
	}
	w, ok := ctx.e.findWitness()
	if !ok {
		// A positive incremental count with no locatable witness means the
		// index is inconsistent.  Treat it as a closed negative branch rather
		// than continuing with invalid data.
		return searchNotFound
	}

	participants := []PageID{w.Sensitive, w.AttackerBelow, w.AttackerAbove}
	sort.Ints(participants)
	for _, id := range participants {
		if ctx.c.stay[id] {
			continue
		}
		idx, exists := ctx.sv.s.pageByID[id]
		if !exists || ctx.e.isMoved[idx] {
			continue
		}
		cand, movable := ctx.sv.candidateFor(id)
		if !movable {
			continue
		}
		for _, target := range cand.targets {
			if ctx.used[target] {
				continue
			}
			ctx.used[target] = true
			u := ctx.e.applyMove(cand.idx, target)
			status := ctx.dfs()
			ctx.e.undo(u)
			delete(ctx.used, target)
			switch status {
			case searchFound:
				return searchFound
			case searchExhausted:
				return searchExhausted
			}
		}
	}
	return searchNotFound
}

// remainingMoveLowerBound packs page-disjoint initial witnesses that are not
// hit yet and have no fixed-stay participant.  Every packed witness needs a
// distinct future page relocation, giving a safe lower bound for this
// branch.  It also detects an unhit witness with no possible future mover.
func (ctx *searchContext) remainingMoveLowerBound() (int, bool) {
	pendingSet := make(map[PageID]bool)
	for id := range ctx.c.must {
		if !ctx.e.isMoved[ctx.sv.s.pageByID[id]] {
			pendingSet[id] = true
		}
	}

	used := make(map[PageID]bool)
	packing := 0
	coveredByPending := 0
	for _, w := range ctx.sv.s.witnesses {
		ids := []PageID{w.Sensitive, w.AttackerBelow, w.AttackerAbove}
		hit := false
		for _, id := range ids {
			if ctx.e.isMoved[ctx.sv.s.pageByID[id]] {
				hit = true
				break
			}
		}
		if hit {
			continue
		}
		possible := false
		for _, id := range ids {
			if ctx.c.stay[id] {
				continue
			}
			idx := ctx.sv.s.pageByID[id]
			if ctx.e.isMoved[idx] {
				continue
			}
			if _, ok := ctx.sv.candByID[id]; ok {
				possible = true
				break
			}
		}
		if !possible {
			return 0, false
		}
		if used[ids[0]] || used[ids[1]] || used[ids[2]] {
			continue
		}
		packing++
		used[ids[0]] = true
		used[ids[1]] = true
		used[ids[2]] = true
		if pendingSet[ids[0]] || pendingSet[ids[1]] || pendingSet[ids[2]] {
			coveredByPending++
		}
	}

	extra := packing - coveredByPending
	if extra < 0 {
		extra = 0
	}
	lower := ctx.e.movedCount() + len(pendingSet) + extra
	return lower, true
}

func (ctx *searchContext) nextPendingMust() (PageID, bool) {
	var best PageID
	have := false
	for id := range ctx.c.must {
		idx := ctx.sv.s.pageByID[id]
		if ctx.e.isMoved[idx] {
			continue
		}
		if !have || id < best {
			best = id
			have = true
		}
	}
	return best, have
}

func containsPhysical(pages []PhysicalPage, target PhysicalPage) bool {
	for _, p := range pages {
		if p == target {
			return true
		}
	}
	return false
}

func sortedConstraintIDs[T any](m map[PageID]T) []PageID {
	ids := make([]PageID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

// findLexPlan builds the lexicographically smallest assignment among plans
// with at most K moves.  Lexicographic order is (sorted moved page IDs, then
// target physical page IDs in that same page order).
func (sv *solver) findLexPlan(K int, requireSequence bool) ([]Move, searchStatus) {
	if K < 0 || K > len(sv.candidates) || K > len(sv.free) {
		return nil, searchNotFound
	}
	c := emptyConstraints()
	for _, cand := range sv.candidates {
		if len(c.must) == K {
			c.stay[cand.id] = true
			continue
		}
		c.must[cand.id] = true
		status := sv.exists(K, c, requireSequence)
		if status == searchFound {
			continue
		}
		delete(c.must, cand.id)
		c.stay[cand.id] = true
		if status == searchExhausted {
			return nil, searchExhausted
		}
	}
	if len(c.must) != K {
		return nil, searchNotFound
	}
	for _, cand := range sv.candidates {
		if !c.must[cand.id] {
			c.stay[cand.id] = true
		}
	}
	status := sv.exists(K, c, requireSequence)
	if status != searchFound {
		return nil, status
	}

	used := make(map[PhysicalPage]bool, K)
	selected := sortedConstraintIDs(c.must)
	for _, id := range selected {
		cand, _ := sv.candidateFor(id)
		foundTarget := false
		for _, target := range cand.targets {
			if used[target] {
				continue
			}
			c.exact[id] = target
			status = sv.exists(K, c, requireSequence)
			if status == searchFound {
				used[target] = true
				foundTarget = true
				break
			}
			delete(c.exact, id)
			if status == searchExhausted {
				return nil, searchExhausted
			}
		}
		if !foundTarget {
			delete(c.exact, id)
			return nil, searchNotFound
		}
	}

	status = sv.exists(K, c, requireSequence)
	if status != searchFound {
		return nil, status
	}

	moves := make([]Move, 0, K)
	for _, id := range selected {
		idx := sv.s.pageByID[id]
		moves = append(moves, Move{
			Page: id,
			From: sv.s.initial[idx],
			To:   c.exact[id],
		})
	}
	return moves, searchFound
}

func collectCandidates(s *scenario) ([]candidate, error) {
	// A page outside the initial witness set can still be forced to move by a
	// witness created at a destination.  Therefore every movable
	// attacker/sensitive page is a candidate, not only initial participants.
	relevant := make(map[PageID]bool)
	for _, p := range s.pages {
		if p.Movable && (p.Role == RoleAttacker || p.Role == RoleSensitive) {
			relevant[p.ID] = true
		}
	}
	if len(relevant) > MaxMovablePages {
		return nil, fmt.Errorf("%w: got %d, maximum %d",
			ErrTooManyMovablePages, len(relevant), MaxMovablePages)
	}

	ids := make([]PageID, 0, len(relevant))
	for id := range relevant {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	initial := newEngine(s)
	out := make([]candidate, 0, len(ids))
	for _, id := range ids {
		idx := s.pageByID[id]
		targets := make([]PhysicalPage, 0, len(s.free))
		for _, target := range s.free {
			if initial.usefulTarget(idx, target) {
				targets = append(targets, target)
			}
		}
		if len(targets) == 0 {
			continue
		}
		out = append(out, candidate{idx: idx, id: id, targets: targets})
	}
	return out, nil
}

func hasEffectiveMover(w Witness, effective map[PageID]bool) bool {
	for _, id := range []PageID{w.Sensitive, w.AttackerBelow, w.AttackerAbove} {
		if effective[id] {
			return true
		}
	}
	return false
}

func packingLowerBound(s *scenario) int {
	used := make(map[PageID]bool)
	count := 0
	for _, w := range s.witnesses {
		ids := []PageID{w.Sensitive, w.AttackerBelow, w.AttackerAbove}
		if used[ids[0]] || used[ids[1]] || used[ids[2]] {
			continue
		}
		count++
		used[ids[0]] = true
		used[ids[1]] = true
		used[ids[2]] = true
	}
	return count
}
