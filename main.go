package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// node is one disjoint, contiguous byte interval [start, end) stored in a
// treap keyed by start. maxEnd caches the largest end reachable in the
// subtree, allowing overlap queries in O(log n + k).
type node struct {
	start       int64
	end         int64
	data        []byte
	left, right *node
	pri         uint32
	maxEnd      int64
}

type rngState uint32

func (r *rngState) next() uint32 {
	x := uint32(*r)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*r = rngState(x)
	return x
}

func maxEndOf(n *node) int64 {
	if n == nil {
		return 0
	}
	return n.maxEnd
}

func updateMax(n *node) {
	m := n.end
	if e := maxEndOf(n.left); e > m {
		m = e
	}
	if e := maxEndOf(n.right); e > m {
		m = e
	}
	n.maxEnd = m
}

// split partitions the treap into nodes with start < key (left result) and
// nodes with start >= key (right result).
func split(n *node, key int64) (*node, *node) {
	if n == nil {
		return nil, nil
	}
	if n.start < key {
		left, right := split(n.right, key)
		n.right = left
		updateMax(n)
		return n, right
	}
	left, right := split(n.left, key)
	n.left = right
	updateMax(n)
	return left, n
}

// merge joins two treaps where every key in a is smaller than every key in b.
func merge(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.pri > b.pri {
		a.right = merge(a.right, b)
		updateMax(a)
		return a
	}
	b.left = merge(a, b.left)
	updateMax(b)
	return b
}

func leftmost(n *node) *node {
	for n != nil && n.left != nil {
		n = n.left
	}
	return n
}

func rightmost(n *node) *node {
	for n != nil && n.right != nil {
		n = n.right
	}
	return n
}

func removeMin(n *node) (*node, *node) {
	if n.left == nil {
		return n.right, n
	}
	var removed *node
	n.left, removed = removeMin(n.left)
	updateMax(n)
	return n, removed
}

func removeMax(n *node) (*node, *node) {
	if n.right == nil {
		return n.left, n
	}
	var removed *node
	n.right, removed = removeMax(n.right)
	updateMax(n)
	return n, removed
}

// store holds received fragments sparsely: total retained bytes grow with the
// number of distinct received bytes, never with the declared size.
type store struct {
	root        *node
	rng         rngState
	conflict    int64
	hasConflict bool
}

func newStore() *store {
	return &store{rng: rngState(0x12345678)}
}

// visitOverlaps walks the interval-tree-like treap and invokes fn for every
// stored interval intersecting [lo, hi). Each matching node is visited once.
func (s *store) visitOverlaps(lo, hi int64, fn func(n *node)) {
	var stack []*node
	if s.root != nil {
		stack = append(stack, s.root)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.start >= hi {
			if cur.left != nil {
				stack = append(stack, cur.left)
			}
			continue
		}
		// Prune a subtree whose intervals all end at or before lo.
		if cur.maxEnd <= lo {
			continue
		}
		if maxEndOf(cur.left) > lo {
			if cur.left != nil {
				stack = append(stack, cur.left)
			}
		}
		if cur.end > lo {
			fn(cur)
		}
		if cur.right != nil && cur.right.maxEnd > lo {
			stack = append(stack, cur.right)
		}
	}
}

func (s *store) noteConflict(off int64) {
	if !s.hasConflict || off < s.conflict {
		s.conflict = off
		s.hasConflict = true
	}
}

// insert adds one decoded fragment at absolute offset off. Overlapping bytes
// are compared; disagreements record the smallest conflicting offset, while
// agreeing overlaps and adjacent intervals are coalesced.
func (s *store) insert(off int64, p []byte) {
	if len(p) == 0 {
		return
	}
	lo, hi := off, off+int64(len(p))
	origLo, origHi := lo, hi

	s.visitOverlaps(lo, hi, func(existing *node) {
		// Intersection is [max(lo, existing.start), min(hi, existing.end)).
		from := lo
		if existing.start > from {
			from = existing.start
		}
		to := hi
		if existing.end < to {
			to = existing.end
		}
		fragBase := from - lo
		storedBase := from - existing.start
		length := to - from
		for k := int64(0); k < length; k++ {
			if p[fragBase+k] != existing.data[storedBase+k] {
				s.noteConflict(from + k)
				break
			}
		}
	})

	// Detach everything with start < lo so the predecessor can be found.
	leftTree, rest := split(s.root, lo)
	newLo, newHi := lo, hi
	merged := make([]byte, 0, len(p))

	if pred := rightmost(leftTree); pred != nil && pred.end >= lo {
		var detached *node
		leftTree, detached = removeMax(leftTree)
		newLo = detached.start
		if detached.end >= hi {
			// The predecessor already covers the whole fragment: keep its
			// bytes untouched and reinsert it unchanged.
			nn := &node{
				start: detached.start,
				end:   detached.end,
				data:  detached.data,
				pri:   s.rng.next(),
			}
			updateMax(nn)
			s.root = merge(merge(leftTree, nn), rest)
			return
		}
		merged = append(merged, detached.data...)
		// Overlap: retain the predecessor's copy and resume after its end.
		lo = detached.end
		p = p[lo-origLo:]
	}
	merged = append(merged, p...)
	newHi = origHi

	// Absorb every interval starting at or before the current right edge.
	for {
		first := leftmost(rest)
		if first == nil {
			break
		}
		// Adjacent (start == newHi) and overlapping intervals are absorbed.
		if first.start > newHi {
			break
		}
		var detached *node
		rest, detached = removeMin(rest)
		if detached.end > newHi {
			tail := newHi - detached.start
			merged = append(merged, detached.data[tail:]...)
			newHi = detached.end
		}
	}

	nn := &node{
		start: newLo,
		end:   newHi,
		data:  merged,
		pri:   s.rng.next(),
	}
	updateMax(nn)
	s.root = merge(merge(leftTree, nn), rest)
}

// firstGap returns the smallest uncovered offset in [0, size), or -1 when the
// whole range is covered by disjoint intervals.
func (s *store) firstGap(size int64) int64 {
	var pos int64
	var stack []*node
	cur := s.root
	for cur != nil || len(stack) > 0 {
		for cur != nil {
			stack = append(stack, cur)
			cur = cur.left
		}
		cur = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.start > pos {
			return pos
		}
		if cur.end > pos {
			pos = cur.end
			if pos >= size {
				return -1
			}
		}
		cur = cur.right
	}
	if pos < size {
		return pos
	}
	return -1
}

// flatten appends all stored bytes in offset order.
func flatten(n *node, buf []byte) []byte {
	if n == nil {
		return buf
	}
	buf = flatten(n.left, buf)
	buf = append(buf, n.data...)
	buf = flatten(n.right, buf)
	return buf
}

type csvState int

const (
	csvFieldStart csvState = iota
	csvUnquoted
	csvQuoted
	csvQuoteEnd
)

// parseCSV parses RFC 4180-style CSV: comma separators, double-quote escaping
// via "", CRLF record terminators (the final record may omit CRLF), and CRLF
// inside quoted fields is part of the field. Input is guaranteed valid.
func parseCSV(data []byte) [][]string {
	rows := make([][]string, 0)
	row := make([]string, 0)
	var field []byte
	state := csvFieldStart

	pushField := func() {
		row = append(row, string(field))
		field = nil
	}
	pushRow := func() {
		rows = append(rows, row)
		row = make([]string, 0)
	}

	for i := 0; i < len(data); i++ {
		c := data[i]
		switch state {
		case csvFieldStart:
			switch c {
			case ',':
				pushField()
			case '"':
				state = csvQuoted
			case '\r':
				if i+1 < len(data) && data[i+1] == '\n' {
					i++
				}
				pushField()
				pushRow()
			default:
				field = append(field, c)
				state = csvUnquoted
			}
		case csvUnquoted:
			switch c {
			case ',':
				pushField()
				state = csvFieldStart
			case '\r':
				if i+1 < len(data) && data[i+1] == '\n' {
					i++
				}
				pushField()
				pushRow()
				state = csvFieldStart
			default:
				field = append(field, c)
			}
		case csvQuoted:
			if c == '"' {
				state = csvQuoteEnd
			} else {
				field = append(field, c)
			}
		case csvQuoteEnd:
			switch c {
			case '"':
				field = append(field, '"')
				state = csvQuoted
			case ',':
				pushField()
				state = csvFieldStart
			case '\r':
				if i+1 < len(data) && data[i+1] == '\n' {
					i++
				}
				pushField()
				pushRow()
				state = csvFieldStart
			}
		}
	}

	// A final record without CRLF: anything other than a fresh row boundary
	// means one more field/record is pending.
	if state != csvFieldStart || len(row) > 0 {
		pushField()
		pushRow()
	}
	return rows
}

type inputPayload struct {
	Size  int64               `json:"size"`
	Parts [][]json.RawMessage `json:"parts"`
}

func run(in io.Reader, out io.Writer) error {
	var payload inputPayload
	dec := json.NewDecoder(in)
	if err := dec.Decode(&payload); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}

	st := newStore()
	for idx, rawPart := range payload.Parts {
		if len(rawPart) != 2 {
			return fmt.Errorf("part %d: expected [offset, hexContent]", idx)
		}
		var offset int64
		var hexContent string
		if err := json.Unmarshal(rawPart[0], &offset); err != nil {
			return fmt.Errorf("part %d offset: %w", idx, err)
		}
		if err := json.Unmarshal(rawPart[1], &hexContent); err != nil {
			return fmt.Errorf("part %d content: %w", idx, err)
		}
		data, err := hex.DecodeString(hexContent)
		if err != nil {
			return fmt.Errorf("part %d hex: %w", idx, err)
		}
		st.insert(offset, data)
	}

	enc := json.NewEncoder(out)
	if st.hasConflict {
		return enc.Encode(map[string]int64{"conflict": st.conflict})
	}
	if gap := st.firstGap(payload.Size); gap >= 0 {
		return enc.Encode(map[string]int64{"gap": gap})
	}

	full := flatten(st.root, make([]byte, 0, minInt64(payload.Size, 1<<20)))
	rows := parseCSV(full)
	return enc.Encode(map[string][][]string{"rows": rows})
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
