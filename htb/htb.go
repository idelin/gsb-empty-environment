// Package htb implements a hierarchical (tree-shaped) token-bucket rate
// limiter running on a discrete-event model with integer ticks.
//
// Design rules:
//   - Every packet must hold allowance on EVERY bucket along its
//     root-to-leaf path; deduction is atomic (all hops or none).
//   - Refills use exact rational arithmetic (math/big.Rat): rates are
//     rational tokens-per-tick, balances never lose fractions.
//   - Queued packets hold no tokens, so cancelling a queued packet
//     consumes nothing.
//   - Tenants can be re-attached to a new parent at runtime; the unsent
//     queue and the reserved token balance migrate atomically with it.
//   - Refill is lazy (computed from the last settled tick), so there are
//     no timers: a rate change can never make an old timer refill twice,
//     and advancing to a duplicate or past tick never refills again.
//   - A packet larger than a bucket's capacity is explicitly rejected,
//     or, under AllowDebt, sends the bucket into debt (negative balance)
//     that future refills must repay.
//   - Time is integer ticks; no real network traffic is involved.
package htb

import (
	"errors"
	"fmt"
	"math/big"
)

// Rat builds an exact rational num/den.
func Rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

func cloneRat(x *big.Rat) *big.Rat { return new(big.Rat).Set(x) }

// DebtPolicy decides what happens to a packet larger than a bucket's
// capacity.
type DebtPolicy int

const (
	// RejectOversize explicitly rejects any packet exceeding some bucket
	// capacity along its path.
	RejectOversize DebtPolicy = iota
	// AllowDebt lets an oversize packet through; the bucket balance goes
	// negative and the debt is repaid by future refills.
	AllowDebt
)

// Packet is a unit of work waiting in a tenant queue. Size is an exact
// rational token cost.
type Packet struct {
	ID   string
	Size *big.Rat
}

// Bucket is a single token bucket with a rational rate (tokens/tick).
type Bucket struct {
	Capacity *big.Rat
	Rate     *big.Rat
	Tokens   *big.Rat
	lastTick int64
}

func newBucket(capacity, rate *big.Rat, full bool, now int64) *Bucket {
	b := &Bucket{
		Capacity: cloneRat(capacity),
		Rate:     cloneRat(rate),
		Tokens:   new(big.Rat),
		lastTick: now,
	}
	if full {
		b.Tokens.Set(capacity)
	}
	return b
}

// settle applies the refill earned between lastTick and tick. A duplicate
// or stale tick is a no-op, so refills can never be applied twice.
func (b *Bucket) settle(tick int64) {
	if tick <= b.lastTick {
		return
	}
	elapsed := Rat(tick-b.lastTick, 1)
	b.Tokens.Add(b.Tokens, new(big.Rat).Mul(b.Rate, elapsed))
	if b.Tokens.Cmp(b.Capacity) > 0 {
		b.Tokens.Set(b.Capacity)
	}
	b.lastTick = tick
}

// Node is one bucket in the tree. Leaf nodes owned by tenants also hold
// the unsent queue.
type Node struct {
	ID       string
	Bucket   *Bucket
	parent   *Node
	children []*Node
	queue    []*Packet
}

// Deduction is the per-hop evidence that a packet was charged at one
// bucket along its root-to-leaf path.
type Deduction struct {
	NodeID string
	Amount *big.Rat
	Before *big.Rat
	After  *big.Rat
}

// SendResult reports the outcome of a SendNext call. On success Path
// carries the deduction evidence for every hop, root first.
type SendResult struct {
	Sent   bool
	Reason string
	Packet *Packet
	Path   []Deduction
}

// Tree is the limiter: a rooted tree of buckets plus a logical clock.
type Tree struct {
	root  *Node
	nodes map[string]*Node
	now   int64
	debt  DebtPolicy
}

// NewTree creates an empty limiter with the given oversize debt policy.
func NewTree(debt DebtPolicy) *Tree {
	return &Tree{nodes: make(map[string]*Node), debt: debt}
}

// Now returns the current tick.
func (t *Tree) Now() int64 { return t.now }

// AddNode attaches a new bucket under parentID ("" creates the root).
// A full bucket starts with Capacity tokens, otherwise zero.
func (t *Tree) AddNode(id, parentID string, capacity, rate *big.Rat, startFull bool) error {
	if id == "" {
		return errors.New("htb: empty node id")
	}
	if _, dup := t.nodes[id]; dup {
		return fmt.Errorf("htb: duplicate node %q", id)
	}
	if capacity == nil || capacity.Sign() <= 0 {
		return fmt.Errorf("htb: node %q capacity must be positive", id)
	}
	if rate == nil || rate.Sign() < 0 {
		return fmt.Errorf("htb: node %q rate must be non-negative", id)
	}
	n := &Node{ID: id, Bucket: newBucket(capacity, rate, startFull, t.now)}
	if parentID == "" {
		if t.root != nil {
			return errors.New("htb: root already exists")
		}
		t.root = n
	} else {
		p, ok := t.nodes[parentID]
		if !ok {
			return fmt.Errorf("htb: unknown parent %q", parentID)
		}
		n.parent = p
		p.children = append(p.children, n)
	}
	t.nodes[id] = n
	return nil
}

// Advance moves the clock to tick. Ticks are integers; advancing to the
// current or a past tick is a no-op and never triggers another refill.
func (t *Tree) Advance(tick int64) bool {
	if tick <= t.now {
		return false
	}
	t.now = tick
	return true
}

// SettleAll forces every bucket to account refills up to now. Refill is
// lazy, so this only makes balances observable; it never changes the
// outcome of any operation.
func (t *Tree) SettleAll() {
	for _, n := range t.nodes {
		n.Bucket.settle(t.now)
	}
}

// Balance returns the exact token balance of a node at the current tick.
func (t *Tree) Balance(id string) (*big.Rat, error) {
	n, ok := t.nodes[id]
	if !ok {
		return nil, fmt.Errorf("htb: unknown node %q", id)
	}
	n.Bucket.settle(t.now)
	return cloneRat(n.Bucket.Tokens), nil
}

// SetRate changes the refill rate of a node. The bucket is first settled
// at the current tick with the OLD rate; because refill is lazy there is
// no timer that could fire again and refill a second time.
func (t *Tree) SetRate(id string, rate *big.Rat) error {
	n, ok := t.nodes[id]
	if !ok {
		return fmt.Errorf("htb: unknown node %q", id)
	}
	if rate == nil || rate.Sign() < 0 {
		return fmt.Errorf("htb: rate must be non-negative")
	}
	n.Bucket.settle(t.now)
	n.Bucket.Rate = cloneRat(rate)
	return nil
}

// Enqueue appends a packet to a tenant's unsent queue. Queued packets
// hold no tokens.
func (t *Tree) Enqueue(tenantID string, pkt Packet) error {
	n, ok := t.nodes[tenantID]
	if !ok {
		return fmt.Errorf("htb: unknown tenant %q", tenantID)
	}
	if pkt.Size == nil || pkt.Size.Sign() <= 0 {
		return errors.New("htb: packet size must be positive")
	}
	n.queue = append(n.queue, &Packet{ID: pkt.ID, Size: cloneRat(pkt.Size)})
	return nil
}

// Cancel removes a queued packet. Since queued packets hold no tokens,
// cancellation never touches any bucket.
func (t *Tree) Cancel(tenantID, packetID string) bool {
	n, ok := t.nodes[tenantID]
	if !ok {
		return false
	}
	for i, p := range n.queue {
		if p.ID == packetID {
			n.queue = append(n.queue[:i:i], n.queue[i+1:]...)
			return true
		}
	}
	return false
}

// QueueLen reports how many packets wait in a tenant queue.
func (t *Tree) QueueLen(tenantID string) int {
	if n, ok := t.nodes[tenantID]; ok {
		return len(n.queue)
	}
	return 0
}

// HeadID returns the ID of the first queued packet, if any.
func (t *Tree) HeadID(tenantID string) (string, bool) {
	if n, ok := t.nodes[tenantID]; ok && len(n.queue) > 0 {
		return n.queue[0].ID, true
	}
	return "", false
}

// ParentID returns the current parent of a node ("" for the root).
func (t *Tree) ParentID(id string) (string, bool) {
	if n, ok := t.nodes[id]; ok {
		if n.parent == nil {
			return "", true
		}
		return n.parent.ID, true
	}
	return "", false
}

// pathOf returns the chain root..n.
func (t *Tree) pathOf(n *Node) []*Node {
	var rev []*Node
	for cur := n; cur != nil; cur = cur.parent {
		rev = append(rev, cur)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// SendNext tries to send the head packet of a tenant queue. The packet
// needs allowance on EVERY bucket along the root-to-leaf path; deduction
// is atomic: either every hop is charged (and evidence is returned) or
// nothing is charged and the packet stays queued.
func (t *Tree) SendNext(tenantID string) SendResult {
	n, ok := t.nodes[tenantID]
	if !ok {
		return SendResult{Reason: "unknown tenant"}
	}
	if len(n.queue) == 0 {
		return SendResult{Reason: "queue empty"}
	}
	pkt := n.queue[0]
	path := t.pathOf(n)
	for _, nd := range path {
		nd.Bucket.settle(t.now)
	}
	// Oversize policy and atomic affordability pre-check.
	for _, nd := range path {
		oversize := pkt.Size.Cmp(nd.Bucket.Capacity) > 0
		switch {
		case oversize && t.debt == RejectOversize:
			return SendResult{Reason: fmt.Sprintf("packet %s exceeds capacity of bucket %s", pkt.ID, nd.ID), Packet: pkt}
		case oversize && t.debt == AllowDebt:
			// Allowed: this bucket will go into debt.
		case nd.Bucket.Tokens.Cmp(pkt.Size) < 0:
			return SendResult{Reason: fmt.Sprintf("insufficient tokens at bucket %s", nd.ID), Packet: pkt}
		}
	}
	// Atomic deduction with per-hop evidence.
	res := SendResult{Sent: true, Packet: pkt}
	for _, nd := range path {
		before := cloneRat(nd.Bucket.Tokens)
		nd.Bucket.Tokens.Sub(nd.Bucket.Tokens, pkt.Size)
		res.Path = append(res.Path, Deduction{
			NodeID: nd.ID,
			Amount: cloneRat(pkt.Size),
			Before: before,
			After:  cloneRat(nd.Bucket.Tokens),
		})
	}
	n.queue = n.queue[1:]
	return res
}

// Reattach moves a subtree to a new parent atomically: the node's bucket
// (including its reserved token balance) and its unsent queue travel
// with it. Balances along the old and new paths are settled first so the
// move happens at exact, observable values.
func (t *Tree) Reattach(id, newParentID string) error {
	n, ok := t.nodes[id]
	if !ok {
		return fmt.Errorf("htb: unknown node %q", id)
	}
	np, ok := t.nodes[newParentID]
	if !ok {
		return fmt.Errorf("htb: unknown parent %q", newParentID)
	}
	if n.parent == nil {
		return errors.New("htb: cannot reattach the root")
	}
	if n.parent == np {
		return nil
	}
	for cur := np; cur != nil; cur = cur.parent {
		if cur == n {
			return errors.New("htb: reattach would create a cycle")
		}
	}
	for _, nd := range t.pathOf(n) {
		nd.Bucket.settle(t.now)
	}
	for _, nd := range t.pathOf(np) {
		nd.Bucket.settle(t.now)
	}
	old := n.parent
	for i, c := range old.children {
		if c == n {
			old.children = append(old.children[:i:i], old.children[i+1:]...)
			break
		}
	}
	n.parent = np
	np.children = append(np.children, n)
	return nil
}
