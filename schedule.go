// Package eventsched provides a forkable, persistent priority queue for
// event scheduling. A Branch is an immutable snapshot of a schedule:
// every mutating operation returns a new Branch and leaves the receiver
// untouched, so forking a branch is free and branches never observe each
// other's updates or cancellations.
//
// Events are ordered by time, then by ID. Re-upserting an ID invalidates
// the previous queue entry (lazy deletion via monotonically increasing
// per-ID revision numbers). Merging two branches keeps, for each ID, the
// entry with the larger revision; equal revisions with different content
// are reported as a conflict.
package eventsched

import "fmt"

// Event is a single scheduled item.
type Event struct {
	ID      string
	Time    int64
	Payload string
}

// ConflictError is returned by Merge when both branches contain the same
// ID at the same revision but with different content.
type ConflictError struct {
	ID string
	A  Event
	B  Event
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("eventsched: merge conflict on %q: same revision, different content (%v vs %v)", e.ID, e.A, e.B)
}

// entry is the per-ID record stored in a branch's dictionary.
type entry struct {
	ev  Event
	rev int
}

// ---------------------------------------------------------------------------
// Persistent AVL tree keyed by event ID (path-copying insert/delete).
// ---------------------------------------------------------------------------

type mapNode struct {
	key         string
	val         entry
	left, right *mapNode
	height      int
}

func nodeHeight(n *mapNode) int {
	if n == nil {
		return 0
	}
	return n.height
}

func makeMapNode(key string, val entry, left, right *mapNode) *mapNode {
	h := nodeHeight(left)
	if nodeHeight(right) > h {
		h = nodeHeight(right)
	}
	return &mapNode{key: key, val: val, left: left, right: right, height: h + 1}
}

func rebalance(n *mapNode) *mapNode {
	bf := nodeHeight(n.left) - nodeHeight(n.right)
	if bf > 1 {
		if nodeHeight(n.left.left) < nodeHeight(n.left.right) {
			lr := n.left.right
			newLeft := makeMapNode(lr.key, lr.val, makeMapNode(n.left.key, n.left.val, n.left.left, lr.left), lr.right)
			return makeMapNode(newLeft.key, newLeft.val, newLeft.left, makeMapNode(n.key, n.val, newLeft.right, n.right))
		}
		return makeMapNode(n.left.key, n.left.val, n.left.left, makeMapNode(n.key, n.val, n.left.right, n.right))
	}
	if bf < -1 {
		if nodeHeight(n.right.right) < nodeHeight(n.right.left) {
			rl := n.right.left
			newRight := makeMapNode(rl.key, rl.val, rl.left, makeMapNode(n.right.key, n.right.val, rl.right, n.right.right))
			return makeMapNode(newRight.key, newRight.val, makeMapNode(n.key, n.val, n.left, newRight.left), newRight.right)
		}
		return makeMapNode(n.right.key, n.right.val, makeMapNode(n.key, n.val, n.left, n.right.left), n.right.right)
	}
	return n
}

func mapInsert(n *mapNode, key string, val entry) (*mapNode, bool) {
	if n == nil {
		return makeMapNode(key, val, nil, nil), true
	}
	switch {
	case key < n.key:
		l, added := mapInsert(n.left, key, val)
		return rebalance(makeMapNode(n.key, n.val, l, n.right)), added
	case key > n.key:
		r, added := mapInsert(n.right, key, val)
		return rebalance(makeMapNode(n.key, n.val, n.left, r)), added
	default:
		return makeMapNode(key, val, n.left, n.right), false
	}
}

func mapDeleteMin(n *mapNode) (*mapNode, *mapNode) {
	if n.left == nil {
		return n.right, n
	}
	l, min := mapDeleteMin(n.left)
	return rebalance(makeMapNode(n.key, n.val, l, n.right)), min
}

func mapDelete(n *mapNode, key string) (*mapNode, bool) {
	if n == nil {
		return nil, false
	}
	switch {
	case key < n.key:
		l, deleted := mapDelete(n.left, key)
		if !deleted {
			return n, false
		}
		return rebalance(makeMapNode(n.key, n.val, l, n.right)), true
	case key > n.key:
		r, deleted := mapDelete(n.right, key)
		if !deleted {
			return n, false
		}
		return rebalance(makeMapNode(n.key, n.val, n.left, r)), true
	default:
		if n.left == nil {
			return n.right, true
		}
		if n.right == nil {
			return n.left, true
		}
		r, min := mapDeleteMin(n.right)
		return rebalance(makeMapNode(min.key, min.val, n.left, r)), true
	}
}

func mapGet(n *mapNode, key string) (entry, bool) {
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n.val, true
		}
	}
	return entry{}, false
}

func mapCount(n *mapNode) int {
	if n == nil {
		return 0
	}
	return 1 + mapCount(n.left) + mapCount(n.right)
}

// mapEach calls f for every key in ascending order; it stops early if f
// returns false.
func mapEach(n *mapNode, f func(string, entry) bool) bool {
	if n == nil {
		return true
	}
	if !mapEach(n.left, f) {
		return false
	}
	if !f(n.key, n.val) {
		return false
	}
	return mapEach(n.right, f)
}

// ---------------------------------------------------------------------------
// Persistent leftist heap ordered by (time, id, revision).
// ---------------------------------------------------------------------------

type heapItem struct {
	time    int64
	id      string
	rev     int
	payload string
}

func lessItem(a, b heapItem) bool {
	if a.time != b.time {
		return a.time < b.time
	}
	if a.id != b.id {
		return a.id < b.id
	}
	return a.rev < b.rev
}

type heapNode struct {
	item        heapItem
	left, right *heapNode
	npl         int
}

func heapRank(n *heapNode) int {
	if n == nil {
		return 0
	}
	return n.npl
}

func heapMerge(a, b *heapNode) *heapNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if lessItem(b.item, a.item) {
		a, b = b, a
	}
	right := heapMerge(a.right, b)
	left := a.left
	if heapRank(left) < heapRank(right) {
		left, right = right, left
	}
	return &heapNode{item: a.item, left: left, right: right, npl: heapRank(right) + 1}
}

func heapPush(h *heapNode, item heapItem) *heapNode {
	return heapMerge(h, &heapNode{item: item, npl: 1})
}

// ---------------------------------------------------------------------------
// Branch: an immutable schedule snapshot.
// ---------------------------------------------------------------------------

// Branch is one version of the event schedule. The zero value is an empty
// schedule ready to use. All operations are pure: they return a new Branch
// and never modify the receiver.
type Branch struct {
	entries *mapNode
	queue   *heapNode
	size    int
}

// New returns an empty branch.
func New() Branch { return Branch{} }

// Fork returns an independent copy of the branch. Because branches are
// persistent, the fork shares structure with b but can never observe later
// changes made through b, nor can b observe changes made through the fork.
func (b Branch) Fork() Branch { return b }

// Len reports the number of live events in the branch.
func (b Branch) Len() int { return b.size }

// Has reports whether id currently has a live event in the branch.
func (b Branch) Has(id string) bool {
	_, ok := mapGet(b.entries, id)
	return ok
}

// Upsert schedules ev, replacing any previous event with the same ID. The
// previous queue entry is invalidated (its revision no longer matches) and
// is discarded lazily. The receiver is unchanged.
func (b Branch) Upsert(ev Event) Branch {
	rev := 1
	size := b.size
	if old, ok := mapGet(b.entries, ev.ID); ok {
		rev = old.rev + 1
	} else {
		size++
	}
	entries, _ := mapInsert(b.entries, ev.ID, entry{ev: ev, rev: rev})
	return Branch{
		entries: entries,
		queue:   heapPush(b.queue, heapItem{time: ev.Time, id: ev.ID, rev: rev, payload: ev.Payload}),
		size:    size,
	}
}

// Cancel removes the event with the given ID from this branch only. Other
// branches forked from the same ancestor keep their copy. Cancel of a
// missing ID is a no-op. The receiver is unchanged.
func (b Branch) Cancel(id string) Branch {
	if _, ok := mapGet(b.entries, id); !ok {
		return b
	}
	entries, _ := mapDelete(b.entries, id)
	return Branch{entries: entries, queue: b.queue, size: b.size - 1}
}

// valid reports whether the heap item still matches the live dictionary
// entry for its ID. Revision alone is not enough: an ID cancelled and later
// re-upserted restarts its revision sequence, so a stale entry could
// collide with a fresh one on (id, rev). Comparing the content too makes
// such a collision harmless — a stale entry can only masquerade as a live
// one when it is identical to it.
func (b Branch) valid(item heapItem) bool {
	e, ok := mapGet(b.entries, item.id)
	return ok && e.rev == item.rev && e.ev.Time == item.time && e.ev.Payload == item.payload
}

// clean discards stale queue entries from the top of the heap.
func (b Branch) clean() Branch {
	for b.queue != nil && !b.valid(b.queue.item) {
		b.queue = heapMerge(b.queue.left, b.queue.right)
	}
	return b
}

// Peek returns the earliest event (smallest time, then smallest ID) without
// removing it. The second return value reports whether the branch is
// non-empty.
func (b Branch) Peek() (Event, bool) {
	c := b.clean()
	if c.queue == nil {
		return Event{}, false
	}
	e, _ := mapGet(c.entries, c.queue.item.id)
	return e.ev, true
}

// Pop removes and returns the earliest event. The returned Branch is the
// schedule without that event; the receiver is unchanged.
func (b Branch) Pop() (Event, Branch, bool) {
	c := b.clean()
	if c.queue == nil {
		return Event{}, b, false
	}
	id := c.queue.item.id
	e, _ := mapGet(c.entries, id)
	rest := c.Cancel(id)
	rest.queue = heapMerge(c.queue.left, c.queue.right)
	return e.ev, rest, true
}

// List returns all live events in pop order (time, then ID).
func (b Branch) List() []Event {
	out := make([]Event, 0, b.size)
	cur := b
	for {
		ev, next, ok := cur.Pop()
		if !ok {
			return out
		}
		out = append(out, ev)
		cur = next
	}
}

// Merge combines two branches. For each ID present in both, the entry with
// the larger revision wins; if the revisions are equal the content must be
// identical, otherwise Merge returns a *ConflictError. IDs present in only
// one branch are kept as-is. Neither input is modified.
//
// The merged branch's queue is rebuilt from the surviving entries rather
// than melded from the two input queues. Branches share heap structure with
// their ancestors, so melding would count shared stale entries with
// exponential path multiplicity and lazy deletion could not discard them in
// reasonable time.
func Merge(a, b Branch) (Branch, error) {
	entries := a.entries
	size := a.size
	var conflict *ConflictError
	mapEach(b.entries, func(id string, eb entry) bool {
		ea, ok := mapGet(entries, id)
		switch {
		case !ok:
			entries, _ = mapInsert(entries, id, eb)
			size++
		case eb.rev > ea.rev:
			entries, _ = mapInsert(entries, id, eb)
		case eb.rev == ea.rev:
			if eb.ev != ea.ev {
				conflict = &ConflictError{ID: id, A: ea.ev, B: eb.ev}
				return false
			}
		}
		return true
	})
	if conflict != nil {
		return Branch{}, conflict
	}
	var queue *heapNode
	mapEach(entries, func(id string, e entry) bool {
		queue = heapPush(queue, heapItem{time: e.ev.Time, id: id, rev: e.rev, payload: e.ev.Payload})
		return true
	})
	return Branch{
		entries: entries,
		queue:   queue,
		size:    size,
	}, nil
}
