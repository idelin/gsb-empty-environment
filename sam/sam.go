// Package sam implements a rollbackable suffix automaton over a byte string.
//
// The automaton supports appending bytes at the end, undoing appends
// (rollback to any previous version), locating all occurrences of a
// pattern, and reporting the number of distinct substrings.
//
// Empty-pattern convention (public contract):
//   - The empty pattern is considered to occur at every position
//     0..n (inclusive) of the indexed string of length n, i.e. n+1
//     occurrences. Occurrences("") returns [0 1 ... n].
//   - DistinctSubstrings never counts the empty substring.
package sam

// undo kinds recorded on the undo log.
const (
	undoAddTransition = iota // transition did not exist before; undo deletes it
	undoSetTransition        // transition overwrote an old target; undo restores it
	undoSetLink              // suffix link of an existing state changed; undo restores it
)

type state struct {
	next   map[byte]int // transitions
	link   int          // suffix link, -1 for the root
	length int          // length of the longest string in this state
	endPos int          // end index where this state was created as "cur"; -1 for clones
}

type undoEntry struct {
	kind   int
	st     int  // state that was mutated
	ch     byte // transition character (transition entries only)
	oldVal int  // overwritten value (set entries only)
}

// snapshot captures everything needed to undo one Append.
type snapshot struct {
	statesLen int
	last      int
	total     int
	n         int
	logLen    int
}

// Automaton is a rollbackable suffix automaton.
type Automaton struct {
	states []state
	last   int
	total  int // number of distinct non-empty substrings
	n      int // current text length
	log    []undoEntry
	undo   []snapshot // one snapshot per live Append, used as an undo stack
}

// New returns an empty automaton indexing the empty string.
func New() *Automaton {
	return &Automaton{
		states: []state{{next: make(map[byte]int), link: -1, length: 0, endPos: -1}},
		last:   0,
	}
}

// Len returns the length of the currently indexed string.
func (a *Automaton) Len() int { return a.n }

// Version returns the current version: the number of live (not yet
// rolled back) appends. Pass a previously returned version to Rollback
// to restore the automaton to that point.
func (a *Automaton) Version() int { return len(a.undo) }

// Append extends the indexed string by one byte.
func (a *Automaton) Append(c byte) {
	a.undo = append(a.undo, snapshot{
		statesLen: len(a.states),
		last:      a.last,
		total:     a.total,
		n:         a.n,
		logLen:    len(a.log),
	})

	cur := len(a.states)
	a.states = append(a.states, state{
		next:   make(map[byte]int),
		link:   0,
		length: a.states[a.last].length + 1,
		endPos: a.n,
	})

	p := a.last
	for p != -1 {
		if _, ok := a.states[p].next[c]; ok {
			break
		}
		a.log = append(a.log, undoEntry{kind: undoAddTransition, st: p, ch: c})
		a.states[p].next[c] = cur
		p = a.states[p].link
	}

	if p == -1 {
		a.states[cur].link = 0
	} else {
		q := a.states[p].next[c]
		if a.states[p].length+1 == a.states[q].length {
			a.states[cur].link = q
		} else {
			clone := len(a.states)
			next := make(map[byte]int, len(a.states[q].next))
			for k, v := range a.states[q].next {
				next[k] = v
			}
			a.states = append(a.states, state{
				next:   next,
				link:   a.states[q].link,
				length: a.states[p].length + 1,
				endPos: -1,
			})
			for p != -1 {
				if v, ok := a.states[p].next[c]; !ok || v != q {
					break
				}
				a.log = append(a.log, undoEntry{kind: undoSetTransition, st: p, ch: c, oldVal: q})
				a.states[p].next[c] = clone
				p = a.states[p].link
			}
			a.log = append(a.log, undoEntry{kind: undoSetLink, st: q, oldVal: a.states[q].link})
			a.states[q].link = clone
			a.states[cur].link = clone
		}
	}

	// Only the new "cur" state changes the distinct-substring count;
	// a clone contributes exactly what q loses when its link moves.
	a.total += a.states[cur].length - a.states[a.states[cur].link].length
	a.last = cur
	a.n++
}

// AppendString appends a whole string.
func (a *Automaton) AppendString(s string) {
	for i := 0; i < len(s); i++ {
		a.Append(s[i])
	}
}

// Pop undoes the most recent Append. It is a no-op on an empty automaton.
func (a *Automaton) Pop() {
	if len(a.undo) == 0 {
		return
	}
	a.Rollback(len(a.undo) - 1)
}

// Rollback restores the automaton to the given version, undoing all
// appends made after it. Every mutation (added or overwritten
// transitions, changed suffix links) is reverted, so no residual
// transitions or miscounted substrings survive a rollback.
func (a *Automaton) Rollback(version int) {
	if version < 0 {
		version = 0
	}
	for len(a.undo) > version {
		snap := a.undo[len(a.undo)-1]
		a.undo = a.undo[:len(a.undo)-1]
		for len(a.log) > snap.logLen {
			e := a.log[len(a.log)-1]
			a.log = a.log[:len(a.log)-1]
			switch e.kind {
			case undoAddTransition:
				delete(a.states[e.st].next, e.ch)
			case undoSetTransition:
				a.states[e.st].next[e.ch] = e.oldVal
			case undoSetLink:
				a.states[e.st].link = e.oldVal
			}
		}
		a.states = a.states[:snap.statesLen]
		a.last = snap.last
		a.total = snap.total
		a.n = snap.n
	}
}

// DistinctSubstrings returns the number of distinct non-empty
// substrings of the currently indexed string.
func (a *Automaton) DistinctSubstrings() int { return a.total }

// findState follows the transitions for pat from the root.
func (a *Automaton) findState(pat string) (int, bool) {
	v := 0
	for i := 0; i < len(pat); i++ {
		nx, ok := a.states[v].next[pat[i]]
		if !ok {
			return -1, false
		}
		v = nx
	}
	return v, true
}

// Contains reports whether pat occurs in the indexed string.
// The empty pattern always occurs (see package convention).
func (a *Automaton) Contains(pat string) bool {
	if pat == "" {
		return true
	}
	_, ok := a.findState(pat)
	return ok
}

// Count returns the number of occurrences of pat. The empty pattern
// occurs n+1 times (see package convention).
func (a *Automaton) Count(pat string) int {
	return len(a.Occurrences(pat))
}

// Occurrences returns the sorted start positions of every occurrence
// of pat, or nil if pat does not occur. The empty pattern occurs at
// every position 0..n (see package convention).
func (a *Automaton) Occurrences(pat string) []int {
	if pat == "" {
		pos := make([]int, a.n+1)
		for i := range pos {
			pos[i] = i
		}
		return pos
	}
	v, ok := a.findState(pat)
	if !ok {
		return nil
	}
	// endpos(v) = end positions recorded on the "cur" states of the
	// suffix-link subtree rooted at v. Children are derived from the
	// current links, so the result is always consistent with any
	// sequence of rollbacks.
	children := make([][]int, len(a.states))
	for i := 1; i < len(a.states); i++ {
		l := a.states[i].link
		if l >= 0 {
			children[l] = append(children[l], i)
		}
	}
	var ends []int
	stack := []int{v}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if a.states[x].endPos >= 0 {
			ends = append(ends, a.states[x].endPos)
		}
		stack = append(stack, children[x]...)
	}
	pos := make([]int, 0, len(ends))
	for _, e := range ends {
		pos = append(pos, e-len(pat)+1)
	}
	sortInts(pos)
	return pos
}

func sortInts(s []int) {
	// insertion-free small sort via the standard library is fine, but
	// keep this package dependency-light.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
