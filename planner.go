package netsec

import (
	"errors"
	"math/bits"
)

// Status categorizes the outcome of a planning run.
type Status int

const (
	// StatusInvalid means the Model or Budget was malformed.
	StatusInvalid Status = iota
	// StatusSolved means a shortest write sequence was found.
	StatusSolved
	// StatusUnreachable means no legal safe sequence can reach the target;
	// the current configuration is an unrecoverable dead end.
	StatusUnreachable
	// StatusUnfinished means the search budget was exhausted before the
	// question could be settled.
	StatusUnfinished
)

func (s Status) String() string {
	switch s {
	case StatusInvalid:
		return "invalid"
	case StatusSolved:
		return "solved"
	case StatusUnreachable:
		return "unreachable"
	case StatusUnfinished:
		return "unfinished"
	default:
		return "unknown"
	}
}

// Budget bounds the search effort. The zero value selects safe defaults.
type Budget struct {
	// MaxWrites caps the sequence length (BFS depth). Zero defaults to the
	// number of mutable bits.
	MaxWrites int

	// MaxVisited caps how many distinct states may be expanded. Zero defaults
	// to DefaultMaxVisited.
	MaxVisited int

	// DenseLimit switches the search to a compact parent table whenever the
	// full state space fits. Zero defaults to DefaultDenseLimit.
	DenseLimit int
}

const (
	// DefaultMaxVisited bounds sparse searches so a 24-bit field cannot cause
	// unbounded memory growth.
	DefaultMaxVisited = 1 << 20
	// DefaultDenseLimit is the largest state space handled with a parent array
	// plus bitset instead of a hash map. Larger spaces stay sparse so a
	// near-goal 24-bit search does not pay to zero a multi-hundred-MiB table.
	DefaultDenseLimit = 1 << 16
	// DefaultVerifyStates bounds the optional exhaustive dead-end check.
	DefaultVerifyStates = 1 << 16
)

func (b Budget) withDefaults() Budget {
	if b.MaxWrites <= 0 {
		b.MaxWrites = MaxMutableBits
	}
	if b.MaxVisited <= 0 {
		b.MaxVisited = DefaultMaxVisited
	}
	if b.DenseLimit <= 0 {
		b.DenseLimit = DefaultDenseLimit
	}
	return b
}

// Plan is the result of a shortest-path search.
type Plan struct {
	Status Status
	// Writes is the shortest legal sequence when Status == StatusSolved. It
	// is empty (but non-nil) for a zero-write goal match.
	Writes []Write
	// Depth is len(Writes); Depth is zero when Start already satisfies Goal.
	Depth int
	// Visited is the number of distinct states that were expanded.
	Visited int
	// Err carries the validation error behind StatusInvalid.
	Err error
}

// visitedSet records discovered states and their BFS parent plus the write
// that reached them.
type visitedSet interface {
	add(state, parent uint32, w Write) (first bool)
	parentOf(state uint32) (parent uint32, w Write, ok bool)
	size() int
}

type denseVisited struct {
	seen   []uint64
	parent []int32
	mask   []uint32
	value  []uint32
}

func newDenseVisited(space uint32) *denseVisited {
	return &denseVisited{
		seen:   make([]uint64, (space+63)/64),
		parent: make([]int32, space),
		mask:   make([]uint32, space),
		value:  make([]uint32, space),
	}
}

func (d *denseVisited) add(state, parent uint32, w Write) bool {
	word, bit := state/64, uint(state%64)
	if d.seen[word]&(1<<bit) != 0 {
		return false
	}
	d.seen[word] |= 1 << bit
	d.parent[state] = int32(parent)
	d.mask[state] = w.WriteMask
	d.value[state] = w.Value
	return true
}

func (d *denseVisited) parentOf(state uint32) (uint32, Write, bool) {
	word, bit := state/64, uint(state%64)
	if d.seen[word]&(1<<bit) == 0 {
		return 0, Write{}, false
	}
	return uint32(d.parent[state]), Write{WriteMask: d.mask[state], Value: d.value[state]}, true
}

func (d *denseVisited) size() int {
	total := 0
	for _, word := range d.seen {
		total += bits.OnesCount64(word)
	}
	return total
}

type parentInfo struct {
	parent uint32
	write  Write
}

type sparseVisited struct {
	items map[uint32]parentInfo
}

func newSparseVisited() *sparseVisited {
	return &sparseVisited{items: make(map[uint32]parentInfo)}
}

func (s *sparseVisited) add(state, parent uint32, w Write) bool {
	if _, ok := s.items[state]; ok {
		return false
	}
	s.items[state] = parentInfo{parent: parent, write: w}
	return true
}

func (s *sparseVisited) parentOf(state uint32) (uint32, Write, bool) {
	info, ok := s.items[state]
	if !ok {
		return 0, Write{}, false
	}
	return info.parent, info.write, true
}

func (s *sparseVisited) size() int { return len(s.items) }

// PlanModel searches for the shortest safe write sequence.
func PlanModel(m Model, budget Budget) Plan {
	if err := m.Validate(); err != nil {
		return Plan{Status: StatusInvalid, Err: err, Writes: []Write{}}
	}
	budget = budget.withDefaults()

	if !m.Safe(m.Start) {
		return Plan{
			Status: StatusUnreachable,
			Err:    errors.New("netsec: start state already opens a forbidden region"),
			Writes: []Write{},
		}
	}
	if m.AtGoal(m.Start) {
		return Plan{Status: StatusSolved, Writes: []Write{}, Visited: 1}
	}

	space := m.fullMask() + 1
	var visited visitedSet
	dense := int(space) <= budget.DenseLimit
	if dense {
		visited = newDenseVisited(space)
	} else {
		visited = newSparseVisited()
	}
	visited.add(m.Start, m.Start, Write{})

	frontier := []uint32{m.Start}
	depth := 0
	for len(frontier) > 0 {
		if depth >= budget.MaxWrites {
			return unfinished(visited)
		}
		nextFrontier := make([]uint32, 0, len(frontier))
		for _, state := range frontier {
			if w, goal, ok := m.goalWrite(state); ok {
				visited.add(goal, state, w)
				return solved(m, visited, goal)
			}
			var goal uint32
			foundGoal := false
			budgetHit := false
			m.Expand(state, func(next uint32, w Write) bool {
				// Goal checks precede bookkeeping so a one-write target on a
				// wide register is never lost to the visit budget.
				if m.AtGoal(next) {
					goal = next
					foundGoal = true
					_ = visited.add(next, state, w)
					return false
				}
				if visited.size() >= budget.MaxVisited {
					budgetHit = true
					return false
				}
				if !visited.add(next, state, w) {
					return true
				}
				nextFrontier = append(nextFrontier, next)
				return true
			})
			if foundGoal {
				return solved(m, visited, goal)
			}
			if visited.size() >= budget.MaxVisited {
				return unfinished(visited)
			}
			if budgetHit {
				return unfinished(visited)
			}
		}
		frontier = nextFrontier
		depth++
	}

	// Every safe state reachable under the lock/permission dynamics has been
	// enumerated without hitting the goal: the target is provably unreachable.
	return Plan{Status: StatusUnreachable, Visited: visited.size(), Writes: []Write{}}
}

func unfinished(visited visitedSet) Plan {
	return Plan{Status: StatusUnfinished, Visited: visited.size(), Writes: []Write{}}
}

func solved(m Model, visited visitedSet, goal uint32) Plan {
	writes := make([]Write, 0)
	state := goal
	for state != m.Start {
		parent, w, ok := visited.parentOf(state)
		if !ok {
			// Defensive: BFS bookkeeping is internal, so a broken chain is a
			// package bug rather than user error.
			return Plan{
				Status:  StatusInvalid,
				Err:     errors.New("netsec: internal error: missing BFS parent"),
				Writes:  []Write{},
				Visited: visited.size(),
			}
		}
		writes = append(writes, w)
		state = parent
	}
	for left, right := 0, len(writes)-1; left < right; left, right = left+1, right-1 {
		writes[left], writes[right] = writes[right], writes[left]
	}
	return Plan{Status: StatusSolved, Writes: writes, Depth: len(writes), Visited: visited.size()}
}
