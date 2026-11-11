package netsec

import "fmt"

const (
	phaseForward = iota
	phaseBackward
	phaseFinalize
	phaseDone
)

type stateKey struct {
	cell  Cell
	speed int
	dist  int
}

type move struct {
	to    Cell
	speed int
}

// Solver computes, for every time index, the support positions: cells that
// occur in at least one complete feasible trajectory. A cell that is merely
// allowed locally is not part of the support unless a full feasible trajectory
// passes through it.
//
// Work is split into budgeted units via Advance: one unit expands or tests one
// explicit state (cell, previous speed, accumulated length). The solver is
// resumable; Pending reports the time ranges whose support is not finalized.
type Solver struct {
	prob     Problem
	maxSpeed int
	allowed  []map[Cell]struct{}

	phase int

	// fwd[t] holds every state reachable from time 0 at time t.
	fwd []map[stateKey]struct{}
	// bwd[t] is the subset of fwd[t] from which a feasible completion
	// through the final time exists.
	bwd []map[stateKey]struct{}

	// Forward resume cursor.
	fwdLayer int
	fwdQueue []stateKey
	fwdPos   int

	// Backward resume cursor (layers processed from T-1 down to 0).
	bwdLayer int
	bwdQueue []stateKey
	bwdPos   int

	// Finalize cursor.
	finNext  int
	supports [][]Cell
	exposed  []Range

	moveCache map[Cell][]move
	err       error
}

// NewSolver validates the problem and seeds the forward layer at time 0.
func NewSolver(p Problem) (*Solver, error) {
	if err := validateProblem(p); err != nil {
		return nil, err
	}
	timeCount := len(p.Allowed)
	s := &Solver{
		prob:      p,
		maxSpeed:  p.MaxSpeed,
		allowed:   make([]map[Cell]struct{}, timeCount),
		phase:     phaseForward,
		fwd:       make([]map[stateKey]struct{}, timeCount),
		bwd:       make([]map[stateKey]struct{}, timeCount),
		fwdLayer:  0,
		bwdLayer:  timeCount - 2,
		moveCache: make(map[Cell][]move),
	}
	for t, cells := range p.Allowed {
		set := make(map[Cell]struct{}, len(cells))
		for _, cell := range cells {
			set[cell] = struct{}{}
		}
		s.allowed[t] = set
	}
	// At time 0 every allowed cell starts with speed 0 and travelled length 0.
	s.fwd[0] = make(map[stateKey]struct{})
	for _, cell := range sortedCellSet(s.allowed[0]) {
		s.fwd[0][stateKey{cell: cell, speed: 0, dist: 0}] = struct{}{}
	}
	s.fwdQueue = sortedStateKeys(s.fwd[0])
	return s, nil
}

// Advance performs at most budget work units and returns the time ranges not
// yet finalized together with whether all work is complete. A zero or negative
// budget performs no work.
func (s *Solver) Advance(budget int) ([]Range, bool) {
	if budget < 0 {
		budget = 0
	}
	for budget > 0 && s.phase != phaseDone {
		switch s.phase {
		case phaseForward:
			budget = s.runForward(budget)
		case phaseBackward:
			budget = s.runBackward(budget)
		case phaseFinalize:
			budget = s.runFinalize(budget)
		}
	}
	if s.phase == phaseDone {
		return nil, true
	}
	return s.pending(), false
}

// Solve runs to completion. It returns ErrInfeasible when no complete
// trajectory exists.
func (s *Solver) Solve() ([][]Cell, []Range, error) {
	for {
		_, done := s.Advance(1 << 30)
		if done {
			break
		}
	}
	if s.err != nil {
		return nil, nil, s.err
	}
	return s.supports, s.exposed, nil
}

// Supports returns the support cells per time index after completion.
func (s *Solver) Supports() [][]Cell {
	return s.supports
}

// UniqueRanges returns the maximal consecutive time ranges whose support is a
// single cell. Ranges are half-open [Start, End).
func (s *Solver) UniqueRanges() []Range {
	return s.exposed
}

// Err returns the terminal solver error, if any.
func (s *Solver) Err() error {
	return s.err
}

// Pending returns the time ranges not finalized yet.
func (s *Solver) Pending() []Range {
	if s.phase == phaseDone {
		return nil
	}
	return s.pending()
}

func (s *Solver) pending() []Range {
	timeCount := len(s.prob.Allowed)
	switch s.phase {
	case phaseForward:
		return []Range{{Start: s.fwdLayer + 1, End: timeCount}}
	case phaseBackward:
		return []Range{{Start: 0, End: s.bwdLayer + 1}}
	case phaseFinalize:
		return []Range{{Start: s.finNext, End: timeCount}}
	default:
		return nil
	}
}

func (s *Solver) runForward(budget int) int {
	timeCount := len(s.prob.Allowed)
	for budget > 0 {
		if s.fwdLayer == timeCount-1 {
			s.startBackward()
			return budget
		}
		if s.fwdPos == len(s.fwdQueue) {
			next := s.fwdLayer + 1
			if len(s.fwd[next]) == 0 {
				s.fail(ErrInfeasible)
				return 0
			}
			s.fwdLayer = next
			s.fwdQueue = sortedStateKeys(s.fwd[next])
			s.fwdPos = 0
			continue
		}
		key := s.fwdQueue[s.fwdPos]
		s.fwdPos++
		budget--
		moves, err := s.movesFrom(key.cell)
		if err != nil {
			s.fail(err)
			return 0
		}
		for _, mv := range moves {
			if !s.moveAllowed(key, s.fwdLayer, mv) {
				continue
			}
			nextDist := key.dist + mv.speed
			if nextDist > s.prob.TotalLength.Max {
				continue
			}
			if _, ok := s.allowed[s.fwdLayer+1][mv.to]; !ok {
				continue
			}
			if s.fwd[s.fwdLayer+1] == nil {
				s.fwd[s.fwdLayer+1] = make(map[stateKey]struct{})
			}
			s.fwd[s.fwdLayer+1][stateKey{
				cell:  mv.to,
				speed: mv.speed,
				dist:  nextDist,
			}] = struct{}{}
		}
	}
	return budget
}

func (s *Solver) startBackward() {
	timeCount := len(s.prob.Allowed)
	last := make(map[stateKey]struct{})
	for key := range s.fwd[timeCount-1] {
		if key.dist >= s.prob.TotalLength.Min && key.dist <= s.prob.TotalLength.Max {
			last[key] = struct{}{}
		}
	}
	if len(last) == 0 {
		s.fail(ErrInfeasible)
		return
	}
	s.bwd[timeCount-1] = last
	s.bwdLayer = timeCount - 2
	if s.bwdLayer < 0 {
		s.phase = phaseFinalize
		s.finNext = 0
		return
	}
	s.beginBackwardLayer()
	s.phase = phaseBackward
}

func (s *Solver) beginBackwardLayer() {
	s.bwd[s.bwdLayer] = make(map[stateKey]struct{})
	s.bwdQueue = sortedStateKeys(s.fwd[s.bwdLayer])
	s.bwdPos = 0
}

func (s *Solver) runBackward(budget int) int {
	for budget > 0 {
		if s.bwdPos == len(s.bwdQueue) {
			if len(s.bwd[s.bwdLayer]) == 0 {
				s.fail(ErrInfeasible)
				return 0
			}
			s.bwdLayer--
			if s.bwdLayer < 0 {
				s.phase = phaseFinalize
				s.finNext = 0
				return budget
			}
			s.beginBackwardLayer()
			continue
		}
		key := s.bwdQueue[s.bwdPos]
		s.bwdPos++
		budget--
		ok, err := s.canComplete(key)
		if err != nil {
			s.fail(err)
			return 0
		}
		if ok {
			s.bwd[s.bwdLayer][key] = struct{}{}
		}
	}
	return budget
}

func (s *Solver) runFinalize(budget int) int {
	timeCount := len(s.prob.Allowed)
	if s.supports == nil {
		s.supports = make([][]Cell, timeCount)
	}
	for budget > 0 && s.finNext < timeCount {
		cells := make(map[Cell]struct{})
		for key := range s.bwd[s.finNext] {
			cells[key.cell] = struct{}{}
		}
		s.supports[s.finNext] = sortedCellSet(cells)
		s.finNext++
		budget--
	}
	if s.finNext == timeCount {
		s.exposed = MergeUniqueRanges(s.supports)
		s.phase = phaseDone
	}
	return budget
}

// moveAllowed applies the speed cap and the consecutive speed-change cap.
// The first step (t == 0) is exempt from the change cap.
func (s *Solver) moveAllowed(prev stateKey, t int, mv move) bool {
	if s.maxSpeed > 0 && mv.speed > s.maxSpeed {
		return false
	}
	if t > 0 && absInt(mv.speed-prev.speed) > s.prob.MaxSpeedChange {
		return false
	}
	return true
}

// canComplete reports whether key at bwdLayer has a transition into a state
// already proven feasible at bwdLayer+1.
func (s *Solver) canComplete(key stateKey) (bool, error) {
	t := s.bwdLayer
	moves, err := s.movesFrom(key.cell)
	if err != nil {
		return false, err
	}
	for _, mv := range moves {
		if !s.moveAllowed(key, t, mv) {
			continue
		}
		nextDist := key.dist + mv.speed
		if nextDist > s.prob.TotalLength.Max {
			continue
		}
		if _, ok := s.allowed[t+1][mv.to]; !ok {
			continue
		}
		target := stateKey{cell: mv.to, speed: mv.speed, dist: nextDist}
		if _, ok := s.bwd[t+1][target]; ok {
			return true, nil
		}
	}
	return false, nil
}

// movesFrom returns the implicit zero-length stay plus every graph edge.
func (s *Solver) movesFrom(cell Cell) ([]move, error) {
	if cached, ok := s.moveCache[cell]; ok {
		return cached, nil
	}
	moves := []move{{to: cell, speed: 0}}
	for _, edge := range s.prob.Graph.Edges(cell) {
		if edge.Length < 0 {
			return nil, fmt.Errorf("%w: edge length must be non-negative", ErrInvalidProblem)
		}
		moves = append(moves, move{to: edge.To, speed: edge.Length})
	}
	moves = dedupeMoves(sortedMoves(moves))
	s.moveCache[cell] = moves
	return moves, nil
}

func dedupeMoves(moves []move) []move {
	if len(moves) <= 1 {
		return moves
	}
	out := moves[:1]
	for i := 1; i < len(moves); i++ {
		if moves[i] == out[len(out)-1] {
			continue
		}
		out = append(out, moves[i])
	}
	return out
}

func (s *Solver) fail(err error) {
	s.err = err
	s.phase = phaseDone
}
