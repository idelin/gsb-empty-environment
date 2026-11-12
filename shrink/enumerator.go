package shrink

import (
	"container/heap"
	"errors"
	"strconv"
	"strings"
)

// Precondition decides whether a candidate is admissible. The host implements
// the library's publicly documented precondition here; candidates returning
// false are never replayed against the failing assertion and do not consume
// the shrink budget.
type Precondition func(*Candidate) bool

// Enumerator lazily emits the schema domain in minimization order while
// discarding candidates that fail the public precondition.
type Enumerator struct {
	schema    *Schema
	pre       Precondition
	order     []int
	masks     [][]int
	tier      int
	streams   *tierHeap
	exhausted bool

	// Rejected counts admissible-order candidates discarded by Precondition.
	Rejected int
}

// NewEnumerator validates its inputs and prepares ordered enumeration.
func NewEnumerator(schema *Schema, pre Precondition) (*Enumerator, error) {
	if schema == nil {
		return nil, errors.Join(ErrInvalidSchema, errors.New("schema is nil"))
	}
	if pre == nil {
		return nil, errors.New("shrink: precondition is nil")
	}

	order := make([]int, 0, schema.optional)
	for i, f := range schema.fields {
		if f.Optional {
			order = append(order, i)
		}
	}
	return &Enumerator{
		schema: schema,
		pre:    pre,
		order:  order,
		masks:  presenceSubsets(len(order)),
	}, nil
}

// Next returns the next precondition-satisfying candidate in minimization
// order. It returns ok == false once the whole domain has been enumerated.
func (e *Enumerator) Next() (*Candidate, bool) {
	if e.exhausted {
		return nil, false
	}
	for {
		if e.streams == nil || e.streams.Len() == 0 {
			if !e.openTier() {
				e.exhausted = true
				return nil, false
			}
		}
		stream := heap.Pop(e.streams).(*stream)
		cand := stream.head(e)
		stream.advance(e)
		if stream.node != nil {
			heap.Push(e.streams, stream)
		}
		if e.pre(cand) {
			return cand, true
		}
		e.Rejected++
	}
}

// Exhausted reports whether Next has consumed the full domain.
func (e *Enumerator) Exhausted() bool { return e.exhausted }

// openTier builds sorted product streams for every presence subset with the
// next number of present optional fields.
func (e *Enumerator) openTier() bool {
	for e.tier <= len(e.order) {
		subsets := e.masks[e.tier]
		e.tier++
		if len(subsets) == 0 {
			continue
		}
		streams := make(tierHeap, 0, len(subsets))
		for _, mask := range subsets {
			st := newStream(e, mask)
			if st.node != nil {
				streams = append(streams, st)
			}
		}
		if len(streams) > 0 {
			heap.Init(&streams)
			e.streams = &streams
			return true
		}
	}
	return false
}

// productNode is one tuple of per-field value indices on the sorted
// Cartesian-product frontier.
type productNode struct {
	idx  []int
	cost int
}

// nodeHeap orders the frontier by total field cost and then lexicographically
// by the tuple of indices.
type nodeHeap []*productNode

func (h nodeHeap) Len() int { return len(h) }
func (h nodeHeap) Less(i, j int) bool {
	return lessNode(h[i], h[j])
}
func (h nodeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x any)   { *h = append(*h, x.(*productNode)) }
func (h *nodeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return item
}

func lessNode(a, b *productNode) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	for k := range a.idx {
		if a.idx[k] != b.idx[k] {
			return a.idx[k] < b.idx[k]
		}
	}
	return false
}

// stream walks the sorted Cartesian product of one fixed presence subset.
type stream struct {
	active []int
	mask   int
	node   *productNode
	front  nodeHeap
	seen   map[string]bool
}

func newStream(e *Enumerator, mask int) *stream {
	st := &stream{mask: mask, seen: map[string]bool{}}
	for i, f := range e.schema.fields {
		if !f.Optional || mask&(1<<optionalBit(e.order, i)) != 0 {
			st.active = append(st.active, i)
		}
	}

	start := &productNode{idx: make([]int, len(st.active))}
	for _, fieldIdx := range st.active {
		start.cost += e.schema.values[fieldIdx][0].cost
	}
	st.front = nodeHeap{start}
	st.seen[nodeKey(start.idx)] = true
	st.node = start
	return st
}

// optionalBit maps a global optional field index to its presence-mask bit.
func optionalBit(order []int, fieldIdx int) int {
	for bit, idx := range order {
		if idx == fieldIdx {
			return bit
		}
	}
	return -1
}

func (st *stream) head(e *Enumerator) *Candidate {
	present := make([]bool, len(e.schema.fields))
	values := make([]Value, len(e.schema.fields))
	for pos, fieldIdx := range st.active {
		present[fieldIdx] = true
		values[fieldIdx] = e.schema.values[fieldIdx][st.node.idx[pos]].toValue()
	}
	return &Candidate{
		schema:  e.schema,
		present: present,
		vals:    values,
		bytes:   e.schema.maskBytes() + st.node.cost,
	}
}

// advance removes the current tuple and expands its unvisited neighbors.
func (st *stream) advance(e *Enumerator) {
	heap.Pop(&st.front)
	current := st.node
	for pos, fieldIdx := range st.active {
		next := append([]int(nil), current.idx...)
		if next[pos]+1 >= len(e.schema.values[fieldIdx]) {
			continue
		}
		next[pos]++
		key := nodeKey(next)
		if st.seen[key] {
			continue
		}
		st.seen[key] = true
		oldCost := e.schema.values[fieldIdx][current.idx[pos]].cost
		newCost := e.schema.values[fieldIdx][next[pos]].cost
		neighbor := &productNode{idx: next, cost: current.cost - oldCost + newCost}
		heap.Push(&st.front, neighbor)
	}
	if st.front.Len() > 0 {
		st.node = st.front[0]
	} else {
		st.node = nil
	}
}

func nodeKey(idx []int) string {
	parts := make([]string, len(idx))
	for i, v := range idx {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

// tierHeap merges streams of one presence-count tier, comparing their current
// heads so the globally shortest candidate is emitted first.
type tierHeap []*stream

func (h tierHeap) Len() int { return len(h) }
func (h tierHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.node.cost != b.node.cost {
		return a.node.cost < b.node.cost
	}
	if a.mask != b.mask {
		return a.mask < b.mask
	}
	for k := range a.node.idx {
		if a.node.idx[k] != b.node.idx[k] {
			return a.node.idx[k] < b.node.idx[k]
		}
	}
	return false
}
func (h tierHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *tierHeap) Push(x any)   { *h = append(*h, x.(*stream)) }
func (h *tierHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return item
}
