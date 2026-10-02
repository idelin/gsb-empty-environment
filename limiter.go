// Package limiter implements a hierarchical (tree-shaped) token-bucket
// rate limiter driven by a discrete-event clock.
//
// Design guarantees:
//   - A packet may only be sent if every bucket on its root-to-leaf path
//     has enough tokens; deduction across the path is atomic (all or none).
//   - Refills use exact rational arithmetic (math/big.Rat); time is an
//     integer tick. Re-applying the same tick is a no-op (no double refill).
//   - Queued packets hold no tokens; cancelling one consumes nothing.
//   - Tenants can be re-attached to a different parent at runtime; the
//     unsent queue and the reserved (remaining) tokens migrate with the
//     node as one atomic event.
//   - Rate changes settle the bucket at the current tick first, so an old
//     rate can never refill the same interval twice.
//   - An independent ledger (initial + refilled - deducted == tokens, and
//     0 <= tokens <= capacity) is checkable after every single event.
package limiter

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrOversize is returned when a packet can never fit through some bucket
// on its path (size greater than that bucket's capacity). Such packets are
// explicitly rejected instead of being queued or silently going into debt.
var ErrOversize = errors.New("limiter: packet exceeds bucket capacity on path")

// Rat is a convenience constructor for exact rational values.
func Rat(num, den int64) *big.Rat { return big.NewRat(num, den) }

// Packet is a unit of work waiting in a leaf queue. Size is in tokens.
type Packet struct {
	ID   int
	Size int64
}

// Deduction records one bucket's contribution to a sent packet.
type Deduction struct {
	Node      string
	Amount    *big.Rat
	Remaining *big.Rat
}

// SendEvidence is the per-packet proof that every ancestor on the
// root-to-leaf path was deducted exactly the packet size.
type SendEvidence struct {
	PacketID   int
	Tick       int64
	Deductions []Deduction // ordered root -> leaf
}

// Node is one token bucket in the tree. Leaves typically carry queues.
type Node struct {
	Name     string
	Parent   *Node
	Children []*Node

	Capacity *big.Rat
	Tokens   *big.Rat
	Rate     *big.Rat // tokens per tick, exact rational

	lastTick int64
	queue    []*Packet
}

// NewNode creates a bucket that starts full.
func NewNode(name string, capacity, rate *big.Rat) *Node {
	return &Node{
		Name:     name,
		Capacity: new(big.Rat).Set(capacity),
		Tokens:   new(big.Rat).Set(capacity),
		Rate:     new(big.Rat).Set(rate),
	}
}

// Limiter is the discrete-event simulation context for one tree.
type Limiter struct {
	Root *Node
	Now  int64 // current tick, integer time

	nodes    []*Node
	initial  map[*Node]*big.Rat
	refilled map[*Node]*big.Rat
	deducted map[*Node]*big.Rat

	// Sent is the append-only evidence log, one entry per sent packet.
	Sent []SendEvidence
}

// NewLimiter builds a limiter over an already-linked tree rooted at root.
func NewLimiter(root *Node) *Limiter {
	l := &Limiter{
		Root:     root,
		initial:  map[*Node]*big.Rat{},
		refilled: map[*Node]*big.Rat{},
		deducted: map[*Node]*big.Rat{},
	}
	l.register(root)
	return l
}

func (l *Limiter) register(n *Node) {
	if _, ok := l.initial[n]; ok {
		return
	}
	l.nodes = append(l.nodes, n)
	l.initial[n] = new(big.Rat).Set(n.Tokens)
	l.refilled[n] = new(big.Rat)
	l.deducted[n] = new(big.Rat)
	for _, c := range n.Children {
		c.Parent = n
		l.register(c)
	}
}

// Attach links child under parent, registering it if it is new to the tree.
func (l *Limiter) Attach(parent, child *Node) {
	child.Parent = parent
	parent.Children = append(parent.Children, child)
	l.register(child)
}

// Reattach atomically migrates a subtree to a new parent. The node's
// unsent queue and its reserved tokens are fields of the node itself, so
// they move with it in this single event; no packet is lost and no token
// is created, destroyed, or re-accounted.
func (l *Limiter) Reattach(node, newParent *Node) {
	if node.Parent != nil {
		siblings := node.Parent.Children
		for i, c := range siblings {
			if c == node {
				node.Parent.Children = append(siblings[:i], siblings[i+1:]...)
				break
			}
		}
	}
	node.Parent = newParent
	newParent.Children = append(newParent.Children, node)
}

// settle accounts refills for n up to l.Now. It is idempotent per tick:
// a repeated tick yields delta == 0 and changes nothing.
func (l *Limiter) settle(n *Node) {
	delta := l.Now - n.lastTick
	if delta <= 0 {
		return
	}
	add := new(big.Rat).Mul(n.Rate, big.NewRat(delta, 1))
	newTokens := new(big.Rat).Add(n.Tokens, add)
	if newTokens.Cmp(n.Capacity) > 0 {
		newTokens.Set(n.Capacity)
	}
	l.refilled[n].Add(l.refilled[n], new(big.Rat).Sub(newTokens, n.Tokens))
	n.Tokens = newTokens
	n.lastTick = l.Now
}

// AdvanceTo moves the clock to the given tick and refills every bucket.
// Ticks must be monotonic; advancing to the current tick is a safe no-op.
func (l *Limiter) AdvanceTo(tick int64) {
	if tick < l.Now {
		panic(fmt.Sprintf("limiter: clock moved backwards: %d < %d", tick, l.Now))
	}
	l.Now = tick
	for _, n := range l.nodes {
		l.settle(n)
	}
}

// SetRate changes a bucket's refill rate. The bucket is first settled at
// the current tick under the old rate, so no interval is refilled twice.
func (l *Limiter) SetRate(n *Node, rate *big.Rat) {
	l.settle(n)
	n.Rate = new(big.Rat).Set(rate)
}

// path returns the buckets from the root down to n (inclusive).
func (l *Limiter) path(n *Node) []*Node {
	var rev []*Node
	for cur := n; cur != nil; cur = cur.Parent {
		rev = append(rev, cur)
	}
	path := make([]*Node, len(rev))
	for i, node := range rev {
		path[len(rev)-1-i] = node
	}
	return path
}

// Enqueue appends a packet to a leaf queue. A packet larger than any
// bucket capacity on its path can never be sent and is rejected outright
// (ErrOversize); it is not queued and consumes nothing.
func (l *Limiter) Enqueue(leaf *Node, p Packet) error {
	size := big.NewRat(p.Size, 1)
	for _, n := range l.path(leaf) {
		if size.Cmp(n.Capacity) > 0 {
			return fmt.Errorf("%w: packet %d (size %d) vs bucket %q (capacity %s)",
				ErrOversize, p.ID, p.Size, n.Name, n.Capacity.RatString())
		}
	}
	cp := p
	leaf.queue = append(leaf.queue, &cp)
	return nil
}

// Cancel removes a queued packet. Queued packets hold no tokens, so
// cancelling consumes nothing and frees nothing; it only edits the queue.
func (l *Limiter) Cancel(leaf *Node, packetID int) bool {
	for i, p := range leaf.queue {
		if p.ID == packetID {
			leaf.queue = append(leaf.queue[:i], leaf.queue[i+1:]...)
			return true
		}
	}
	return false
}

// SendNext attempts the head packet of leaf's queue. If any bucket on the
// root-to-leaf path lacks tokens, nothing is deducted and the packet stays
// queued. Otherwise every bucket on the path is deducted atomically and
// the per-packet evidence is recorded.
func (l *Limiter) SendNext(leaf *Node) (*SendEvidence, bool) {
	if len(leaf.queue) == 0 {
		return nil, false
	}
	p := leaf.queue[0]
	size := big.NewRat(p.Size, 1)
	path := l.path(leaf)
	for _, n := range path {
		if n.Tokens.Cmp(size) < 0 {
			return nil, false // atomic: all-or-nothing
		}
	}
	ev := SendEvidence{PacketID: p.ID, Tick: l.Now}
	for _, n := range path {
		n.Tokens.Sub(n.Tokens, size)
		l.deducted[n].Add(l.deducted[n], size)
		ev.Deductions = append(ev.Deductions, Deduction{
			Node:      n.Name,
			Amount:    new(big.Rat).Set(size),
			Remaining: new(big.Rat).Set(n.Tokens),
		})
	}
	leaf.queue = leaf.queue[1:]
	l.Sent = append(l.Sent, ev)
	return &ev, true
}

// QueueLen reports how many packets wait in n's queue.
func (l *Limiter) QueueLen(n *Node) int { return len(n.queue) }

// CheckLedger is the independent per-event auditor. For every bucket it
// verifies tokens == initial + refilled - deducted and
// 0 <= tokens <= capacity. Call it after any event, in any interleaving.
func (l *Limiter) CheckLedger() error {
	for _, n := range l.nodes {
		want := new(big.Rat).Add(l.initial[n], l.refilled[n])
		want.Sub(want, l.deducted[n])
		if n.Tokens.Cmp(want) != 0 {
			return fmt.Errorf("ledger mismatch at %q: tokens %s, want initial %s + refilled %s - deducted %s = %s",
				n.Name, n.Tokens.RatString(), l.initial[n].RatString(),
				l.refilled[n].RatString(), l.deducted[n].RatString(), want.RatString())
		}
		if n.Tokens.Sign() < 0 {
			return fmt.Errorf("overdraft at %q: tokens %s", n.Name, n.Tokens.RatString())
		}
		if n.Tokens.Cmp(n.Capacity) > 0 {
			return fmt.Errorf("above capacity at %q: tokens %s > capacity %s",
				n.Name, n.Tokens.RatString(), n.Capacity.RatString())
		}
	}
	return nil
}


// Fmt renders a rational as an integer when possible, else as num/den.
func Fmt(r *big.Rat) string {
	if r.IsInt() {
		return r.Num().String()
	}
	return r.RatString()
}
