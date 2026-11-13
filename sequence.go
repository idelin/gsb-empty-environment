package netsec

import (
	"fmt"
	"sort"
)

func validateMoveSet(s *scenario, moves []Move, requireOrder bool) ([]Move, error) {
	canonical := append([]Move(nil), moves...)
	if !requireOrder {
		sort.Slice(canonical, func(i, j int) bool { return canonical[i].Page < canonical[j].Page })
	}
	seenPages := make(map[PageID]bool, len(canonical))
	seenTargets := make(map[PhysicalPage]bool, len(canonical))
	for _, move := range canonical {
		idx, ok := s.pageByID[move.Page]
		if !ok {
			return nil, fmt.Errorf("%w: unknown moved page %d", ErrInvalidLayout, move.Page)
		}
		if !s.pages[idx].Movable {
			return nil, fmt.Errorf("%w: page %d is not movable", ErrInvalidLayout, move.Page)
		}
		if move.From != s.initial[idx] {
			return nil, fmt.Errorf("%w: page %d From=%d, initial frame is %d",
				ErrInvalidLayout, move.Page, move.From, s.initial[idx])
		}
		if !containsPhysical(s.free, move.To) {
			return nil, fmt.Errorf("%w: target %d for page %d is not a supplied free page",
				ErrInvalidLayout, move.To, move.Page)
		}
		if seenPages[move.Page] {
			return nil, fmt.Errorf("%w: page %d moved more than once", ErrInvalidLayout, move.Page)
		}
		if seenTargets[move.To] {
			return nil, fmt.Errorf("%w: target %d used more than once", ErrInvalidLayout, move.To)
		}
		seenPages[move.Page] = true
		seenTargets[move.To] = true
	}
	return canonical, nil
}

type sequenceSearch struct {
	sv     *solver
	e      *engine
	moves  []Move
	idxs   []int
	dead   map[uint32]bool
	order  []Move
	counts []int64
}

func (sv *solver) sequenceExists(moves []Move) (bool, bool) {
	_, _, status := sv.findSequence(moves)
	return status == searchFound, status == searchExhausted
}

func (sv *solver) findSequence(moves []Move) ([]Move, []int64, searchStatus) {
	canonical, err := validateMoveSet(sv.s, moves, false)
	if err != nil {
		return nil, nil, searchNotFound
	}
	if len(canonical) > 20 {
		return nil, nil, searchNotFound
	}
	ss := &sequenceSearch{
		sv:    sv,
		e:     newEngine(sv.s),
		moves: canonical,
		dead:  make(map[uint32]bool),
	}
	ss.counts = []int64{ss.e.w}
	ss.idxs = make([]int, len(canonical))
	for i, move := range canonical {
		ss.idxs[i] = sv.s.pageByID[move.Page]
	}
	status := ss.dfs(0)
	if status != searchFound {
		return nil, nil, status
	}
	return append([]Move(nil), ss.order...), append([]int64(nil), ss.counts...), searchFound
}

func (ss *sequenceSearch) dfs(mask uint32) searchStatus {
	if ss.dead[mask] {
		return searchNotFound
	}
	if !ss.sv.tick() {
		return searchExhausted
	}
	if len(ss.order) == len(ss.moves) {
		if ss.e.w == 0 {
			return searchFound
		}
		return searchNotFound
	}

	for i, move := range ss.moves {
		bit := uint32(1) << uint(i)
		if mask&bit != 0 {
			continue
		}
		u := ss.e.applyMove(ss.idxs[i], move.To)
		// A relocation to a different geometry may only add witness triples
		// that contain the moved page.  Thus no-new-witnesses is exactly the
		// local condition that the page is not a participant at its target.
		allowed := u.sameGeom || !ss.e.participates(ss.idxs[i])
		if !allowed {
			ss.e.undo(u)
			continue
		}

		ss.order = append(ss.order, move)
		ss.counts = append(ss.counts, ss.e.w)
		status := ss.dfs(mask | bit)
		ss.e.undo(u)
		switch status {
		case searchFound:
			return searchFound
		case searchExhausted:
			return searchExhausted
		}
		ss.order = ss.order[:len(ss.order)-1]
		ss.counts = ss.counts[:len(ss.counts)-1]
	}
	ss.dead[mask] = true
	return searchNotFound
}

// FindOnlineOrder interprets moves as a final one-to-one assignment and
// returns an order in which no migration step adds a witness triple.  It
// returns ErrNoOnlineOrder when the final layout is safe but no such order
// exists.
func FindOnlineOrder(layout Layout, moves []Move) ([]Move, error) {
	return FindOnlineOrderWithBudget(layout, moves, DefaultSearchBudget)
}

// FindOnlineOrderWithBudget is FindOnlineOrder with an explicit state budget.
func FindOnlineOrderWithBudget(layout Layout, moves []Move, budget int) ([]Move, error) {
	if budget < 0 {
		return nil, fmt.Errorf("%w: negative search budget %d", ErrInvalidLayout, budget)
	}
	s, err := buildScenario(layout, true)
	if err != nil {
		return nil, err
	}
	canonical, err := validateMoveSet(s, moves, false)
	if err != nil {
		return nil, err
	}
	if len(canonical) > MaxMovablePages {
		return nil, fmt.Errorf("%w: got %d moves, maximum %d",
			ErrTooManyMovablePages, len(canonical), MaxMovablePages)
	}
	sv := newSolver(s, nil, budget)
	order, _, status := sv.findSequence(canonical)
	switch status {
	case searchFound:
		return order, nil
	case searchExhausted:
		return nil, ErrBudgetExhausted
	default:
		return nil, ErrNoOnlineOrder
	}
}

// ValidateOnlineOrder checks a supplied order literally.  Every prefix must
// keep the witness-triple set non-expanding and the final layout must have no
// witnesses.
func ValidateOnlineOrder(layout Layout, sequence []Move) error {
	s, err := buildScenario(layout, true)
	if err != nil {
		return err
	}
	canonical, err := validateMoveSet(s, sequence, true)
	if err != nil {
		return err
	}
	if len(canonical) > MaxMovablePages {
		return fmt.Errorf("%w: got %d moves, maximum %d",
			ErrTooManyMovablePages, len(canonical), MaxMovablePages)
	}
	e := newEngine(s)
	for i, move := range sequence {
		idx := s.pageByID[move.Page]
		u := e.applyMove(idx, move.To)
		// Recompute participation at the destination before it is rolled
		// forward; same-geometry moves cannot introduce a new triple.
		if !u.sameGeom && e.participates(idx) {
			e.undo(u)
			return fmt.Errorf("%w at step %d (page %d -> %d)",
				ErrNoOnlineOrder, i, move.Page, move.To)
		}
	}
	if e.w != 0 {
		return fmt.Errorf("%w: final layout still has %d witnesses", ErrNoOnlineOrder, e.w)
	}
	return nil
}
