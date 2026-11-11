package netsec

import (
	"errors"
	"sort"
	"time"
)

// MaxEvents is the largest input the correlator is designed to solve.
const MaxEvents = 24

// DefaultWorkBudget is used when Request.WorkBudget is zero. It bounds the
// number of internal search steps so a hostile input cannot run unbounded.
const DefaultWorkBudget uint64 = 2_000_000

// ErrInvalidInput is returned, possibly wrapped, when a Request cannot be
// correlated because its contents are malformed.
var ErrInvalidInput = errors.New("netsec: invalid correlation request")

// Event is one time-ordered observation produced by the detection host.
type Event struct {
	ID    string
	Stage string
	Time  int64
}

// ChainTemplate describes the ordered stages that make up one attack chain.
// A stage label may repeat; every stage must be matched by a distinct event.
type ChainTemplate struct {
	Name   string
	Stages []string
}

// Request is the correlator input. Events must be ordered by non-decreasing
// Time (ties keep input order). MaxGap bounds the time between consecutive
// stages of a confirmed chain; MaxSpan, when positive, additionally bounds
// the total time span of a chain.
type Request struct {
	Events    []Event
	Templates []ChainTemplate
	MaxGap    int64
	MaxSpan   int64

	// WorkBudget caps internal enumeration steps. Zero selects
	// DefaultWorkBudget. If the budget is exhausted before optimality can be
	// proven, the best verified feasible assignment is still returned.
	WorkBudget uint64

	// MaxDuration optionally caps wall-clock search time. Zero disables it.
	MaxDuration time.Duration
}

// Chain is one confirmed chain together with the events that consume it.
type Chain struct {
	TemplateName string
	EventIDs     []string
}

// Assignment is the correlator output. Chains are mutually event-disjoint.
// Every returned chain is complete: each template stage matched a distinct
// event that respects MaxGap and MaxSpan.
type Assignment struct {
	Chains        []Chain
	EventCount    int
	TotalSpan     int64
	ProvenOptimal bool
	Complete      bool
	Reason        string
}

// candidate is one fully matched chain instance over event indices.
type candidate struct {
	template int
	events   []int
	mask     uint32
	span     int64
	minIdx   int
}

type solver struct {
	req      *Request
	cands    []candidate
	byMin    [][]int
	minLenEv []int
	memo     []map[uint32]memoEntry

	budget   uint64
	work     uint64
	deadline time.Time
	hasClock bool
	stopped  bool
	reason   string

	// winner is the best complete feasible selection found so far. It is
	// always valid (a greedy result seeds it before search) and therefore
	// remains usable even when the budget is exhausted.
	winner      []int
	winnerCount int
	winnerSpan  int64
}

// Assign selects a maximum-cardinality set of complete, mutually
// event-disjoint chains. Ties are resolved by minimum total span and then by
// the lexicographically smallest ordered event-ID assignment.
//
// With at most MaxEvents input events an exact branch-and-bound search is
// used. When the work or wall-clock budget is exhausted before the search
// completes, Assign returns the best verified feasible assignment it found
// with ProvenOptimal set to false; it never claims optimality it did not
// prove.
func Assign(req Request) (*Assignment, error) {
	if err := validate(req); err != nil {
		return nil, err
	}

	budget := req.WorkBudget
	if budget == 0 {
		budget = DefaultWorkBudget
	}
	s := &solver{
		req:      &req,
		budget:   budget,
		hasClock: req.MaxDuration > 0,
		minLenEv: make([]int, len(req.Events)),
	}
	if s.hasClock {
		s.deadline = timeNow().Add(req.MaxDuration)
	}
	for i := range s.minLenEv {
		s.minLenEv[i] = -1
	}

	s.enumerateCandidates()
	s.indexCandidates()
	s.seedGreedy()
	s.search()

	return s.buildAssignment(), nil
}

func validate(req Request) error {
	if len(req.Events) > MaxEvents {
		return errors.Join(ErrInvalidInput, errors.New("more than MaxEvents events"))
	}
	if req.MaxGap < 0 {
		return errors.Join(ErrInvalidInput, errors.New("MaxGap must be non-negative"))
	}
	if req.MaxSpan < 0 {
		return errors.Join(ErrInvalidInput, errors.New("MaxSpan must be non-negative"))
	}
	seenID := make(map[string]struct{}, len(req.Events))
	var lastTime int64
	for i, ev := range req.Events {
		if ev.ID == "" {
			return errors.Join(ErrInvalidInput, errors.New("event with empty ID"))
		}
		if _, dup := seenID[ev.ID]; dup {
			return errors.Join(ErrInvalidInput, errors.New("duplicate event ID: "+ev.ID))
		}
		seenID[ev.ID] = struct{}{}
		if i > 0 && ev.Time < lastTime {
			return errors.Join(ErrInvalidInput, errors.New("events must be time-ordered"))
		}
		lastTime = ev.Time
	}
	seenTpl := make(map[string]struct{}, len(req.Templates))
	for _, tpl := range req.Templates {
		if tpl.Name == "" {
			return errors.Join(ErrInvalidInput, errors.New("template with empty name"))
		}
		if _, dup := seenTpl[tpl.Name]; dup {
			return errors.Join(ErrInvalidInput, errors.New("duplicate template name: "+tpl.Name))
		}
		seenTpl[tpl.Name] = struct{}{}
		if len(tpl.Stages) == 0 {
			return errors.Join(ErrInvalidInput, errors.New("template with no stages: "+tpl.Name))
		}
		for _, stage := range tpl.Stages {
			if stage == "" {
				return errors.Join(ErrInvalidInput, errors.New("empty stage in template: "+tpl.Name))
			}
		}
	}
	return nil
}

// enumerateCandidates generates every complete chain instance of every
// template via index-ordered depth-first matching.
func (s *solver) enumerateCandidates() {
	events := s.req.Events
	for tplIdx, tpl := range s.req.Templates {
		if len(tpl.Stages) == 0 || len(tpl.Stages) > len(events) {
			continue
		}
		path := make([]int, 0, len(tpl.Stages))
		s.matchTemplate(tplIdx, tpl.Stages, 0, 0, path)
		if s.stopped {
			return
		}
	}
}

func (s *solver) matchTemplate(tplIdx int, stages []string, stageIdx, fromEvent int, path []int) {
	if s.stopped {
		return
	}
	if stageIdx == len(stages) {
		matched := append([]int(nil), path...)
		first := matched[0]
		last := matched[len(matched)-1]
		s.cands = append(s.cands, candidate{
			template: tplIdx,
			events:   matched,
			mask:     indexMask(matched),
			span:     s.req.Events[last].Time - s.req.Events[first].Time,
			minIdx:   first,
		})
		return
	}
	firstTime := int64(0)
	if stageIdx > 0 {
		firstTime = s.req.Events[path[0]].Time
	}
	for evIdx := fromEvent; evIdx < len(s.req.Events); evIdx++ {
		ev := s.req.Events[evIdx]
		if ev.Stage != stages[stageIdx] {
			continue
		}
		if stageIdx > 0 {
			prev := s.req.Events[path[stageIdx-1]]
			gap := ev.Time - prev.Time
			if gap > s.req.MaxGap {
				continue
			}
			if s.req.MaxSpan > 0 && ev.Time-firstTime > s.req.MaxSpan {
				break
			}
		}
		if !s.tick() {
			return
		}
		s.matchTemplate(tplIdx, stages, stageIdx+1, evIdx+1, append(path, evIdx))
		if s.stopped {
			return
		}
	}
}

func indexMask(events []int) uint32 {
	var mask uint32
	for _, idx := range events {
		mask |= 1 << uint(idx)
	}
	return mask
}

// indexCandidates builds, for every event, the candidates that consume it and
// records the shortest consuming candidate length for the upper bound.
func (s *solver) indexCandidates() {
	n := len(s.req.Events)
	s.byMin = make([][]int, n)
	for ci, cand := range s.cands {
		for _, evIdx := range cand.events {
			s.byMin[evIdx] = append(s.byMin[evIdx], ci)
		}
	}
	for evIdx := 0; evIdx < n; evIdx++ {
		best := -1
		for _, ci := range s.byMin[evIdx] {
			length := len(s.cands[ci].events)
			if best == -1 || length < best {
				best = length
			}
		}
		s.minLenEv[evIdx] = best
		list := s.byMin[evIdx]
		sort.Slice(list, func(a, b int) bool {
			return s.candidateLess(list[a], list[b])
		})
	}
}

// candidateLess defines a deterministic canonical order: shorter span, then
// fewer consumed events, then template name, then lexicographic event IDs.
func (s *solver) candidateLess(a, b int) bool {
	ca, cb := s.cands[a], s.cands[b]
	if ca.span != cb.span {
		return ca.span < cb.span
	}
	if len(ca.events) != len(cb.events) {
		return len(ca.events) < len(cb.events)
	}
	na := s.req.Templates[ca.template].Name
	nb := s.req.Templates[cb.template].Name
	if na != nb {
		return na < nb
	}
	for k := 0; k < len(ca.events) && k < len(cb.events); k++ {
		ida := s.req.Events[ca.events[k]].ID
		idb := s.req.Events[cb.events[k]].ID
		if ida != idb {
			return ida < idb
		}
	}
	return false
}

// seedGreedy builds a feasible incumbent so span tie-breaking prunes from the
// first search node even when the budget is very small.
func (s *solver) seedGreedy() {
	var used uint32
	chosen := make([]int, 0)
	var span int64
	for evIdx := range s.req.Events {
		if used&(1<<uint(evIdx)) != 0 {
			continue
		}
		for _, ci := range s.byMin[evIdx] {
			cand := s.cands[ci]
			if cand.mask&used == 0 {
				used |= cand.mask
				span += cand.span
				chosen = append(chosen, ci)
				break
			}
		}
	}
	s.winner = chosen
	s.winnerCount = len(chosen)
	s.winnerSpan = span
}
