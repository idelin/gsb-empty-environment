package llcache

import (
	"context"
	"sync"
	"time"
)

// Node is an L1 cache attached to a shared L2. It keeps per-key
// entries plus a per-key invalidation watermark rebuilt from L2
// notifications. An L1 entry is served only if its version is newer
// than the key's invalidation watermark; otherwise it is revalidated
// against L2 (or, on L2 failure, degraded per the declared rules).
type Node struct {
	l2     *L2
	loader Loader
	clock  Clock

	mu          sync.Mutex
	entries     map[string]Value
	invalidated map[string]uint64 // per-key invalidation watermark
	applied     int               // notifications that changed state
	inbox       <-chan Invalidation
	closed      chan struct{}
	closeOnce   sync.Once
	wg          sync.WaitGroup
}

// NodeOption configures a Node.
type NodeOption func(*Node)

// WithNodeClock injects the node's clock.
func WithNodeClock(c Clock) NodeOption { return func(n *Node) { n.clock = c } }

// NewNode creates an L1 node subscribed to l2's invalidation stream.
// loader is the injectable data source used on misses.
func NewNode(l2 *L2, loader Loader, opts ...NodeOption) *Node {
	n := &Node{
		l2:          l2,
		loader:      loader,
		clock:       time.Now,
		entries:     make(map[string]Value),
		invalidated: make(map[string]uint64),
		inbox:       l2.Subscribe(64),
		closed:      make(chan struct{}),
	}
	for _, opt := range opts {
		opt(n)
	}
	n.wg.Add(1)
	go n.run()
	return n
}

// Get reads key with the declared consistency level.
func (n *Node) Get(ctx context.Context, key string, c Consistency) (Result, error) {
	if c == Strong {
		return n.getStrong(ctx, key)
	}
	return n.getEventual(ctx, key)
}

// getEventual serves a locally validated L1 hit when possible.
// Degradation rule: if L2 is unreachable the loader is tried; if the
// loader also fails, the unvalidatable L1 entry is returned with
// Stale and Degraded set. It is never presented as strongly
// consistent.
func (n *Node) getEventual(ctx context.Context, key string) (Result, error) {
	n.mu.Lock()
	entry, ok := n.entries[key]
	watermark := n.invalidated[key]
	n.mu.Unlock()

	if ok && entry.Version > watermark {
		return Result{Value: entry, Source: "l1"}, nil
	}

	v, found, l2err := n.l2.Get(key)
	if l2err == nil && found {
		n.store(key, v)
		return Result{Value: v, Source: "l2"}, nil
	}

	lv, lerr := n.loader(ctx, key)
	if lerr == nil {
		n.store(key, lv)
		return Result{Value: lv, Source: "loader", Degraded: l2err != nil}, nil
	}
	if ok {
		return Result{Value: entry, Source: "l1", Stale: true, Degraded: true}, nil
	}
	return Result{}, lerr
}

// getStrong revalidates against L2 on every call, so a delayed
// invalidation notification cannot make it serve a superseded
// version. If L2 is unreachable it fails with ErrUnavailable rather
// than pretending to be consistent.
func (n *Node) getStrong(ctx context.Context, key string) (Result, error) {
	v, found, err := n.l2.Get(key)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	if found {
		n.store(key, v)
		return Result{Value: v, Source: "l2"}, nil
	}
	lv, lerr := n.loader(ctx, key)
	if lerr != nil {
		return Result{}, lerr
	}
	n.store(key, lv)
	return Result{Value: lv, Source: "loader"}, nil
}

// AppliedInvalidations reports how many notifications actually
// changed local state; duplicates are absorbed idempotently and do
// not increment the counter.
func (n *Node) AppliedInvalidations() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.applied
}

// Close stops the notification loop and waits for it to exit,
// releasing the node's goroutine.
func (n *Node) Close() {
	n.closeOnce.Do(func() { close(n.closed) })
	n.wg.Wait()
}

func (n *Node) run() {
	defer n.wg.Done()
	for {
		select {
		case msg := <-n.inbox:
			n.applyInvalidation(msg)
		case <-n.closed:
			return
		}
	}
}

// applyInvalidation is idempotent: a duplicate or older notification
// is a no-op, so redelivery never corrupts state.
func (n *Node) applyInvalidation(msg Invalidation) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if msg.Version <= n.invalidated[msg.Key] {
		return
	}
	n.invalidated[msg.Key] = msg.Version
	n.applied++
	// The superseded entry is kept, not deleted: the per-key watermark
	// blocks it from being served as fresh, while the degraded path can
	// still return it explicitly flagged Stale when L2 and the data
	// source are both unreachable.
}

// store keeps L1 versions monotonic: an older value never overwrites
// a newer one.
func (n *Node) store(key string, v Value) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if cur, ok := n.entries[key]; ok && v.Version < cur.Version {
		return
	}
	n.entries[key] = v
}
