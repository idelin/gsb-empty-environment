package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// infinity is the exclusive end of the last half-open segment. Real
// coordinates never exceed 1e12, so 1<<62 is a safe sentinel.
const infinity = int64(1) << 62

// node is one uniform segment [start, end) sharing the same generation and
// acknowledgement state. The treap is keyed by start.
type node struct {
	start int64
	end   int64
	gen   int64
	acked bool
	prio  uint64
	left  *node
	right *node
}

// shard stores a single shard as a treap of disjoint segments covering
// [0, infinity), plus the frozen committed prefix.
type shard struct {
	root      *node
	committed int64
	rng       uint64
}

func newShard() *shard {
	return &shard{
		root:      &node{start: 0, end: infinity, prio: 1},
		committed: 0,
		rng:       0x9e3779b97f4a7c15,
	}
}

func (s *shard) nextPrio() uint64 {
	x := s.rng
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	s.rng = x
	return x | 1
}

// split partitions t into keys < key and keys >= key.
func split(t *node, key int64) (*node, *node) {
	if t == nil {
		return nil, nil
	}
	if t.start < key {
		left, right := split(t.right, key)
		t.right = left
		return t, right
	}
	left, right := split(t.left, key)
	t.left = right
	return left, t
}

// merge joins two treaps where every key in a precedes every key in b.
func merge(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		a.right = merge(a.right, b)
		return a
	}
	b.left = merge(a, b.left)
	return b
}

// findContaining returns the segment whose half-open range contains x, i.e.
// the treap node with the greatest start <= x.
func (t *node) findContaining(x int64) *node {
	var candidate *node
	for t != nil {
		if t.start <= x {
			candidate = t
			t = t.right
		} else {
			t = t.left
		}
	}
	return candidate
}

// ensureCut makes x a segment boundary when it lies inside a segment.
func (s *shard) ensureCut(x int64) {
	if x <= 0 || x >= infinity {
		return
	}
	seg := s.root.findContaining(x)
	if seg == nil || seg.start == x || x >= seg.end {
		return
	}
	right := &node{
		start: x,
		end:   seg.end,
		gen:   seg.gen,
		acked: seg.acked,
		prio:  s.nextPrio(),
	}
	leftTree, rest := split(s.root, x)
	seg.end = x
	// split already detached seg as the rightmost node of leftTree, so only
	// the new right-hand node has to be inserted.
	s.root = merge(merge(leftTree, right), rest)
}

func collect(t *node, out *[]*node) {
	if t == nil {
		return
	}
	collect(t.left, out)
	*out = append(*out, t)
	collect(t.right, out)
}

// buildTreap constructs a treap from in-order segments, choosing the
// highest-priority node as each subtree root. Segment keys stay strictly
// ordered even after adjacent segments were coalesced.
func buildTreap(segs []*node) *node {
	if len(segs) == 0 {
		return nil
	}
	rootIdx := 0
	for i := 1; i < len(segs); i++ {
		if segs[i].prio > segs[rootIdx].prio {
			rootIdx = i
		}
	}
	root := segs[rootIdx]
	root.left = buildTreap(segs[:rootIdx])
	root.right = buildTreap(segs[rootIdx+1:])
	return root
}

// rangeApply isolates [l, r) and applies fn to every segment in it, then
// coalesces adjacent segments with identical state. Anything below the
// committed watermark is frozen and never touched.
func (s *shard) rangeApply(l, r int64, fn func(*node)) {
	if l < s.committed {
		l = s.committed
	}
	if l >= r {
		return
	}
	s.ensureCut(l)
	s.ensureCut(r)

	left, rest := split(s.root, l)
	mid, right := split(rest, r)

	var segs []*node
	collect(mid, &segs)
	for _, seg := range segs {
		fn(seg)
		seg.left, seg.right = nil, nil
	}

	kept := segs[:0]
	for _, seg := range segs {
		if n := len(kept); n > 0 && kept[n-1].gen == seg.gen && kept[n-1].acked == seg.acked {
			kept[n-1].end = seg.end
			continue
		}
		kept = append(kept, seg)
	}
	var midNew *node
	midNew = buildTreap(kept)
	s.root = merge(merge(left, midNew), right)
}

// lease reassigns [l, r) to gen: only offsets whose current generation is
// strictly older take the new generation and lose uncommitted acks.
func (s *shard) lease(l, r, gen int64) {
	s.rangeApply(l, r, func(seg *node) {
		if seg.gen < gen {
			seg.gen = gen
			seg.acked = false
		}
	})
}

// ack confirms [l, r) only where the current generation matches.
func (s *shard) ack(l, r, gen int64) {
	s.rangeApply(l, r, func(seg *node) {
		if seg.gen == gen {
			seg.acked = true
		}
	})
}

// advance extends the frozen committed prefix over consecutive acked
// segments starting at the current watermark and returns the breakpoint.
func (s *shard) advance() int64 {
	t := s.root
	var stack []*node
	for t != nil || len(stack) > 0 {
		for t != nil {
			stack = append(stack, t)
			t = t.left
		}
		t = stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if t.start > s.committed {
			break
		}
		if t.acked && t.end > s.committed {
			s.committed = t.end
		} else if !t.acked {
			break
		}
		t = t.right
	}
	return s.committed
}

// rawEvent is one ["lease"|"ack", start, end, generation] tuple.
type rawEvent []json.RawMessage

func replay(events []rawEvent) ([]int64, error) {
	s := newShard()
	breakpoints := make([]int64, 0, len(events))
	for idx, ev := range events {
		if len(ev) != 4 {
			return nil, fmt.Errorf("event %d: want 4 elements, got %d", idx, len(ev))
		}
		var kind string
		if err := json.Unmarshal(ev[0], &kind); err != nil {
			return nil, fmt.Errorf("event %d: %w", idx, err)
		}
		var l, r, gen int64
		if err := json.Unmarshal(ev[1], &l); err != nil {
			return nil, fmt.Errorf("event %d: %w", idx, err)
		}
		if err := json.Unmarshal(ev[2], &r); err != nil {
			return nil, fmt.Errorf("event %d: %w", idx, err)
		}
		if err := json.Unmarshal(ev[3], &gen); err != nil {
			return nil, fmt.Errorf("event %d: %w", idx, err)
		}
		switch kind {
		case "lease":
			s.lease(l, r, gen)
		case "ack":
			s.ack(l, r, gen)
		default:
			return nil, fmt.Errorf("event %d: unknown kind %q", idx, kind)
		}
		breakpoints = append(breakpoints, s.advance())
	}
	return breakpoints, nil
}

func run(stdin io.Reader, stdout io.Writer) error {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	var events []rawEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return fmt.Errorf("parse events: %w", err)
	}
	breakpoints, err := replay(events)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(breakpoints)
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "replay:", err)
		os.Exit(1)
	}
}
