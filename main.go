// Command checkpoint replays the lease/ack event stream of one shard and
// reports its safe checkpoint after every event.
//
// stdin is a JSON array of events:
//
//   - ["lease", lo, hi, gen] reassigns the half-open interval [lo, hi).
//     Only the sub-ranges whose current generation is strictly smaller than
//     gen adopt gen; those same sub-ranges lose every uncommitted
//     acknowledgement they held.
//   - ["ack", lo, hi, gen] acknowledges the sub-ranges of [lo, hi) whose
//     current generation equals gen.
//
// After each event the contiguous acknowledged prefix starting at offset 0
// is advanced as far as possible. The committed prefix is frozen forever:
// later leases or acks cannot touch it. The output is a JSON array holding
// one checkpoint (the length of the committed prefix) per input event.
//
// Coordinates may reach 10^12. State lives in two randomized treaps of
// half-open intervals (the current generation per range and the set of
// uncommitted acknowledged ranges), so memory is O(number of events) and
// never proportional to the offset span; no per-offset allocation happens.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
)

// node is one treap entry keyed by the inclusive start of a half-open
// [key, end) interval. Keys are unique within a treap.
type node[V any] struct {
	key         int64
	end         int64
	value       V
	prio        uint64
	left, right *node[V]
}

// treap is a randomized binary search tree keyed by interval starts.
type treap[V any] struct {
	root *node[V]
	rng  *rand.Rand
}

func newTreap[V any]() *treap[V] {
	return &treap[V]{rng: rand.New(rand.NewPCG(1, 2))}
}

// splitNodes partitions root into nodes with key < key and nodes with
// key >= key.
func splitNodes[V any](root *node[V], key int64) (*node[V], *node[V]) {
	if root == nil {
		return nil, nil
	}
	if root.key < key {
		lo, hi := splitNodes(root.right, key)
		root.right = lo
		return root, hi
	}
	lo, hi := splitNodes(root.left, key)
	root.left = hi
	return lo, root
}

// mergeNodes joins two treaps under the precondition that every key in a is
// smaller than every key in b.
func mergeNodes[V any](a, b *node[V]) *node[V] {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		a.right = mergeNodes(a.right, b)
		return a
	}
	b.left = mergeNodes(a, b.left)
	return b
}

func removeNode[V any](root *node[V], key int64) *node[V] {
	if root == nil {
		return nil
	}
	switch {
	case key < root.key:
		root.left = removeNode(root.left, key)
	case key > root.key:
		root.right = removeNode(root.right, key)
	default:
		return mergeNodes(root.left, root.right)
	}
	return root
}

// floorNode returns the node with the greatest key <= key, or nil.
func floorNode[V any](root *node[V], key int64) *node[V] {
	var best *node[V]
	for root != nil {
		if root.key <= key {
			best = root
			root = root.right
		} else {
			root = root.left
		}
	}
	return best
}

// lowerBound returns the node with the smallest key >= key, or nil.
func lowerBound[V any](root *node[V], key int64) *node[V] {
	var best *node[V]
	for root != nil {
		if root.key >= key {
			best = root
			root = root.left
		} else {
			root = root.right
		}
	}
	return best
}

// nextNode returns the treap node whose key directly follows node's key.
func nextNode[V any](root, node *node[V]) *node[V] {
	if node.right != nil {
		cur := node.right
		for cur.left != nil {
			cur = cur.left
		}
		return cur
	}
	// Walk up via search: smallest key strictly greater than node.key.
	return lowerBound(root, node.key+1)
}

func (t *treap[V]) upsert(key, end int64, value V) {
	root := removeNode(t.root, key)
	lo, hi := splitNodes(root, key)
	inserted := &node[V]{key: key, end: end, value: value, prio: t.rng.Uint64()}
	t.root = mergeNodes(mergeNodes(lo, inserted), hi)
}

func (t *treap[V]) remove(key int64) {
	t.root = removeNode(t.root, key)
}

// cutRange removes every interval overlapping [lo, hi); sub-ranges left
// outside [lo, hi) are returned so callers can re-insert them.
func (t *treap[V]) cutRange(lo, hi int64) []node[V] {
	n := floorNode(t.root, lo)
	var keys []int64
	var leftovers []node[V]
	for n != nil && n.key < hi {
		if n.end <= lo {
			n = nextNode(t.root, n)
			continue
		}
		keys = append(keys, n.key)
		if n.key < lo {
			leftovers = append(leftovers, node[V]{key: n.key, end: lo, value: n.value, prio: n.prio})
		}
		if n.end > hi {
			leftovers = append(leftovers, node[V]{key: hi, end: n.end, value: n.value, prio: n.prio})
		}
		n = nextNode(t.root, n)
	}
	for _, key := range keys {
		t.remove(key)
	}
	return leftovers
}

// dropBefore discards every interval fully before cut; an interval spanning
// cut is clipped so it starts at cut. It runs in expected O(log n) time.
func (t *treap[V]) dropBefore(cut int64) {
	left, right := splitNodes(t.root, cut)
	var rightmost *node[V]
	removeRightmost(left, &rightmost)
	if rightmost != nil && rightmost.end > cut {
		rightmost.key = cut
		rightmost.left = nil
		rightmost.right = nil
		right = mergeNodes(rightmost, right)
	}
	t.root = right
}

func removeRightmost[V any](root *node[V], out **node[V]) *node[V] {
	if root == nil {
		return nil
	}
	if root.right == nil {
		*out = root
		return root.left
	}
	root.right = removeRightmost(root.right, out)
	return root
}

type genValue struct{ gen int64 }
type ackValue struct{}

// shard holds the replay state for one shard. gens is a full partition of
// the offset axis into constant-generation pieces (generation 0 before the
// first lease); acks holds disjoint uncommitted acknowledged ranges.
type shard struct {
	committed int64
	gens      *treap[genValue]
	acks      *treap[ackValue]
}

func newShard() *shard {
	s := &shard{
		gens: newTreap[genValue](),
		acks: newTreap[ackValue](),
	}
	// The whole axis starts at generation 0.
	s.gens.upsert(0, math.MaxInt64, genValue{gen: 0})
	return s
}

type piece struct {
	key, end int64
	gen      int64
}

type bounds struct{ lo, hi int64 }

// lease reassigns [lo, hi): strictly older sub-ranges adopt gen and lose
// their uncommitted acks; sub-ranges already at gen or newer are untouched.
func (s *shard) lease(lo, hi, gen int64) {
	if hi <= lo || hi <= s.committed {
		return
	}
	if lo < s.committed {
		lo = s.committed
	}
	root := s.gens.root
	first := floorNode(root, lo)
	if first == nil || first.end <= lo {
		first = lowerBound(root, lo)
	}
	if first == nil || first.key >= hi {
		return
	}

	var touched []int64
	var pieces []piece
	appendPiece := func(key, end, g int64) {
		if end <= key {
			return
		}
		if last := len(pieces) - 1; last >= 0 && pieces[last].end == key && pieces[last].gen == g {
			pieces[last].end = end
			return
		}
		pieces = append(pieces, piece{key: key, end: end, gen: g})
	}

	regionEnd := lo
	var cuts []bounds
	for n := first; n != nil && n.key < hi; n = nextNode(root, n) {
		touched = append(touched, n.key)
		segLo := max(n.key, lo)
		segHi := min(n.end, hi)
		if n.key < segLo {
			appendPiece(n.key, segLo, n.value.gen)
		}
		if n.value.gen < gen {
			appendPiece(segLo, segHi, gen)
			cuts = append(cuts, bounds{lo: segLo, hi: segHi})
		} else {
			appendPiece(segLo, segHi, n.value.gen)
		}
		if n.end > segHi {
			appendPiece(segHi, n.end, n.value.gen)
		}
		regionEnd = n.end
	}
	for _, cut := range cuts {
		for _, leftover := range s.acks.cutRange(cut.lo, cut.hi) {
			s.acks.upsert(leftover.key, leftover.end, leftover.value)
		}
	}

	// Coalesce with unchanged neighbours carrying the same generation.
	if pred := floorNode(root, first.key-1); pred != nil && pred.end == first.key &&
		pred.value.gen == pieces[0].gen {
		s.gens.remove(pred.key)
		pieces[0].key = pred.key
	}
	if succ := lowerBound(root, regionEnd); succ != nil && succ.key == regionEnd &&
		succ.value.gen == pieces[len(pieces)-1].gen {
		s.gens.remove(succ.key)
		pieces[len(pieces)-1].end = succ.end
	}

	for _, key := range touched {
		s.gens.remove(key)
	}
	for _, p := range pieces {
		s.gens.upsert(p.key, p.end, genValue{gen: p.gen})
	}
}

// ack acknowledges the parts of [lo, hi) whose current generation equals gen.
func (s *shard) ack(lo, hi, gen int64) {
	if hi <= lo || hi <= s.committed {
		return
	}
	if lo < s.committed {
		lo = s.committed
	}
	for n := floorNode(s.gens.root, lo); n != nil && n.key < hi; n = nextNode(s.gens.root, n) {
		if n.end <= lo {
			continue
		}
		segLo := max(n.key, lo)
		segHi := min(n.end, hi)
		if n.value.gen == gen {
			// addAck coalesces with neighbouring acknowledged ranges so
			// the contiguous-prefix scan sees one unbroken range.
			s.addAck(segLo, segHi)
		}
	}
	s.advance()
}

// addAck merges [lo, hi) into the set of uncommitted acknowledged ranges.
func (s *shard) addAck(lo, hi int64) {
	if hi <= lo {
		return
	}
	t := s.acks
	start, end := lo, hi
	n := floorNode(t.root, lo)
	var keys []int64
	// Collect every range overlapping [lo, hi) or directly adjacent to the
	// growing union, so neighbouring acknowledgements stay one piece.
	for n != nil && n.key <= end {
		if n.end <= lo {
			n = nextNode(t.root, n)
			continue
		}
		keys = append(keys, n.key)
		if n.key < start {
			start = n.key
		}
		if n.end > end {
			end = n.end
		}
		n = nextNode(t.root, n)
	}
	for _, key := range keys {
		t.remove(key)
	}
	t.upsert(start, end, ackValue{})
}

// advance extends the committed prefix across contiguous acknowledged
// ranges starting at the current checkpoint, then freezes everything behind
// it in both treaps.
func (s *shard) advance() {
	pos := s.committed
	for {
		n := floorNode(s.acks.root, pos)
		if n == nil || n.end <= pos {
			break
		}
		pos = n.end
	}
	if pos == s.committed {
		return
	}
	s.committed = pos
	s.acks.dropBefore(pos)
	s.gens.dropBefore(pos)
}

type rawEvent []json.RawMessage

// Replay consumes the event stream and returns one checkpoint per event.
func Replay(r io.Reader) ([]int64, error) {
	var events []rawEvent
	if err := json.NewDecoder(r).Decode(&events); err != nil {
		return nil, fmt.Errorf("decode events: %w", err)
	}

	s := newShard()
	checkpoints := make([]int64, 0, len(events))
	for i, event := range events {
		if len(event) != 4 {
			return nil, fmt.Errorf("event %d: want [kind, lo, hi, gen], got %d elements", i, len(event))
		}
		var kind string
		if err := json.Unmarshal(event[0], &kind); err != nil {
			return nil, fmt.Errorf("event %d: kind: %w", i, err)
		}
		var lo, hi, gen int64
		if err := json.Unmarshal(event[1], &lo); err != nil {
			return nil, fmt.Errorf("event %d: lo: %w", i, err)
		}
		if err := json.Unmarshal(event[2], &hi); err != nil {
			return nil, fmt.Errorf("event %d: hi: %w", i, err)
		}
		if err := json.Unmarshal(event[3], &gen); err != nil {
			return nil, fmt.Errorf("event %d: gen: %w", i, err)
		}
		if lo < 0 || hi < 0 || gen < 0 {
			return nil, fmt.Errorf("event %d: coordinates and generation must be non-negative", i)
		}
		if hi < lo {
			return nil, fmt.Errorf("event %d: hi %d < lo %d", i, hi, lo)
		}

		switch kind {
		case "lease":
			s.lease(lo, hi, gen)
		case "ack":
			s.ack(lo, hi, gen)
		default:
			return nil, fmt.Errorf("event %d: unknown kind %q", i, kind)
		}
		checkpoints = append(checkpoints, s.committed)
	}
	return checkpoints, nil
}

func main() {
	checkpoints, err := Replay(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "checkpoint:", err)
		os.Exit(1)
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(checkpoints); err != nil {
		fmt.Fprintln(os.Stderr, "checkpoint:", err)
		os.Exit(1)
	}
	if _, err := io.Copy(os.Stdout, &buf); err != nil {
		fmt.Fprintln(os.Stderr, "checkpoint:", err)
		os.Exit(1)
	}
}
