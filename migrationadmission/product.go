package migrationadmission

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// side is the per-input state of one arm of the comparison product.
//
// The joint (old,new) transduction is tracked together: curState/curPending
// describe the new stream that decides login identifiers, oldState/oldOut the
// old subject stream used for alias authorization. oldState < 0 means the
// input has already left the old grammar and has no old subject.
type side struct {
	name       string
	length     int
	curState   int
	curPending []Symbol
	oldState   int
	oldOut     []Symbol
	ended      bool
}

func (s side) clone() side {
	s.curPending = append([]Symbol(nil), s.curPending...)
	s.oldOut = append([]Symbol(nil), s.oldOut...)
	return s
}

type pairNode struct {
	left     side
	right    side
	diverged bool
}

func (s side) key() string {
	buf := make([]byte, 0, len(s.name)+2+len(s.curPending)*2+2+len(s.oldOut)*2+2+1)
	tmp := make([]byte, binary.MaxVarintLen64)
	put := func(v uint64) {
		n := binary.PutUvarint(tmp, v)
		buf = append(buf, tmp[:n]...)
	}
	put(uint64(len(s.name)))
	buf = append(buf, s.name...)
	put(uint64(s.length))
	put(uint64(s.curState))
	put(uint64(len(s.curPending)))
	for _, symbol := range s.curPending {
		put(uint64(symbol))
	}
	put(uint64(s.oldState))
	put(uint64(len(s.oldOut)))
	for _, symbol := range s.oldOut {
		put(uint64(symbol))
	}
	if s.ended {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	return string(buf)
}

func (n pairNode) key() string {
	if n.diverged {
		return "D"
	}
	return n.left.key() + "|" + n.right.key()
}

// productResult reports the exact maximum unmatched-symbol backlog and, when
// the configuration is unsafe, the first collision found.
type productResult struct {
	MaxBacklog  int
	Violation   error
	Pair        [2]string
	OldSubjects [2][]Symbol
	NewID       []Symbol
}

type productConfig struct {
	oldPolicy *compiledPolicy
	newPolicy *compiledPolicy
	aliases   map[string]bool
}

// analyzeProduct explores the asynchronous comparison product of two copies
// of the joint transducer. It never enumerates input names: moves are grammar
// transitions and the explored object is the state/backlog product.
func analyzeProduct(cfg productConfig) productResult {
	start := side{
		curState: cfg.newPolicy.g.startState,
		oldState: cfg.oldPolicy.g.startState,
	}
	root := pairNode{left: start, right: start}

	queue := []pairNode{root}
	visited := map[string]bool{root.key(): true}

	result := productResult{}

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if node.diverged {
			continue
		}
		backlog := len(node.left.curPending) + len(node.right.curPending)
		if backlog > result.MaxBacklog {
			result.MaxBacklog = backlog
		}
		if backlog > MaxLateness {
			result.Violation = ErrLatenessExceeded
			result.Pair = [2]string{node.left.name, node.right.name}
			return result
		}

		// Both arms ended: the surviving buffers decide equality.
		if node.left.ended && node.right.ended {
			if len(node.left.curPending) == 0 && len(node.right.curPending) == 0 {
				if err := authorize(node, cfg.aliases, &result); err != nil {
					result.Violation = err
					return result
				}
			}
			// A non-empty surviving buffer on only one side means the new
			// identifiers differ: a safe, non-colliding pair.
			continue
		}

		moves, _ := enumerateMoves(node, cfg)
		for _, next := range moves {
			if next.diverged {
				continue
			}
			key := next.key()
			if !visited[key] {
				visited[key] = true
				queue = append(queue, next)
			}
		}
	}
	return result
}

func authorize(node pairNode, aliases map[string]bool, result *productResult) error {
	if node.left.name == node.right.name {
		// Determinism guarantees identical inputs have identical subjects.
		return nil
	}
	// Both arms must have remained inside the old grammar; a name that has no
	// old subject cannot be folded onto another name.
	if node.left.oldState < 0 || node.right.oldState < 0 {
		return collision(node, result)
	}
	leftSubject := append([]Symbol(nil), node.left.oldOut...)
	rightSubject := append([]Symbol(nil), node.right.oldOut...)
	if symbolsEqual(leftSubject, rightSubject) {
		// Different spellings of the same old subject are an inherent alias.
		return nil
	}
	key := aliasKey(leftSubject, rightSubject)
	if !aliases[key] {
		return collision(node, result)
	}
	return nil
}

func collision(node pairNode, result *productResult) error {
	result.Pair = [2]string{node.left.name, node.right.name}
	result.OldSubjects = [2][]Symbol{
		append([]Symbol(nil), node.left.oldOut...),
		append([]Symbol(nil), node.right.oldOut...),
	}
	result.NewID = append([]Symbol(nil), node.left.curPending...)
	return ErrUnauthorizedFold
}

// moveKind selects which arms advance in one product step. Synchronous moves
// keep identical-name paths comparable; asynchronous moves cover inputs of
// different lengths and deletion/expansion buffering.
type moveKind int

const (
	moveLeft moveKind = iota
	moveRight
	moveBoth
)

func enumerateMoves(node pairNode, cfg productConfig) ([]pairNode, int) {
	var out []pairNode
	peak := 0
	for _, kind := range []moveKind{moveBoth, moveLeft, moveRight} {
		moves := expand(node, cfg, kind, false)
		out = append(out, moves...)
		out = append(out, expand(node, cfg, kind, true)...)
		for _, move := range moves {
			if backlog := len(move.left.curPending) + len(move.right.curPending); backlog > peak {
				peak = backlog
			}
		}
	}
	return out, peak
}

// expand emits one move. endMove=true consumes the end marker on the selected
// arm(s); otherwise one alphabet character is consumed on each selected arm.
func expand(node pairNode, cfg productConfig, kind moveKind, endMove bool) []pairNode {
	usesLeft := kind == moveLeft || kind == moveBoth
	usesRight := kind == moveRight || kind == moveBoth

	leftOpts := armOptions(node.left, usesLeft, endMove, cfg)
	rightOpts := armOptions(node.right, usesRight, endMove, cfg)
	if len(leftOpts) == 0 || len(rightOpts) == 0 {
		return nil
	}

	var out []pairNode
	for _, left := range leftOpts {
		for _, right := range rightOpts {
			next := pairNode{left: left, right: right}
			applyCancellation(&next)
			out = append(out, next)
		}
	}
	return out
}

// armOptions returns the possible successor states of one arm. When the arm is
// not selected by the move kind, its only option is its current state.
func armOptions(s side, selected bool, endMove bool, cfg productConfig) []side {
	if !selected {
		return []side{s}
	}
	if s.ended {
		return nil
	}
	if endMove {
		if !cfg.newPolicy.g.accept[s.curState] {
			return nil
		}
		next := s.clone()
		next.ended = true
		next.curPending = append(next.curPending, cfg.newPolicy.t.tail...)
		if next.oldState >= 0 && !cfg.oldPolicy.g.accept[next.oldState] {
			next.oldState = -1
			next.oldOut = nil
		}
		return []side{next}
	}
	if s.length == MaxNameLength {
		return nil
	}
	var out []side
	transitions := cfg.newPolicy.g.delta[s.curState]
	bytes := make([]byte, 0, len(transitions))
	for on := range transitions {
		bytes = append(bytes, on)
	}
	sort.Slice(bytes, func(i, j int) bool { return bytes[i] < bytes[j] })
	for _, on := range bytes {
		nextState := transitions[on]
		next := s.clone()
		next.name += string(on)
		next.length++
		next.curState = nextState
		next.curPending = append(next.curPending, cfg.newPolicy.t.emitFor(on)...)
		if next.oldState >= 0 {
			oldNext, ok := cfg.oldPolicy.g.delta[next.oldState][on]
			if !ok {
				next.oldState = -1
				next.oldOut = nil
			} else {
				next.oldState = oldNext
				next.oldOut = append(next.oldOut, cfg.oldPolicy.t.emitFor(on)...)
			}
		}
		out = append(out, next)
	}
	return out
}

// applyCancellation greedily matches the fronts of the two new-identifier
// buffers. A single mismatching symbol proves the streams can never become
// equal, so the node becomes a safe divergent sink.
func applyCancellation(next *pairNode) {
	for len(next.left.curPending) > 0 && len(next.right.curPending) > 0 {
		if next.left.curPending[0] != next.right.curPending[0] {
			next.diverged = true
			return
		}
		next.left.curPending = next.left.curPending[1:]
		next.right.curPending = next.right.curPending[1:]
	}
}

func symbolsEqual(a, b []Symbol) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// aliasKey is the order-independent lookup key for a pair of old subjects.
func aliasKey(a, b []Symbol) string {
	first := encodeSymbols(a)
	second := encodeSymbols(b)
	if string(first) > string(second) {
		first, second = second, first
	}
	return string(first) + "|" + string(second)
}

func encodeSymbols(symbols []Symbol) []byte {
	e := &encoder{}
	e.symbols(symbols)
	return e.buf.Bytes()
}

func (r productResult) String() string {
	if r.Violation == nil {
		return fmt.Sprintf("product analysis clean, max backlog %d", r.MaxBacklog)
	}
	return fmt.Sprintf("%v: %q vs %q", r.Violation, r.Pair[0], r.Pair[1])
}
