// Package arbiter provides a multi-node contention and conflict arbiter
// over hexgrid cells. Multiple execution nodes compete for exclusive
// ownership of cell sets; the arbiter guarantees:
//
//   - Atomicity:      a multi-cell acquisition is all-or-nothing.
//   - Linearizability: every state transition happens under a single
//     mutex, giving each operation a well-defined linearization point.
//   - Deadlock freedom: no lock is ever held while blocking; waiters
//     park on a generation broadcast channel and retry.
//   - No leaks:       timed-out or cancelled waiters leave no registered
//     state and spawn no lingering goroutines.
//
// Fencing tokens are issued per cell on every grant so that holders can
// detect stale ownership when talking to downstream stores.
package arbiter

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ID identifies a resource managed by the arbiter (e.g. a hexgrid cell).
type ID uint64

// ErrNoCells is returned when an acquisition requests an empty cell set.
var ErrNoCells = errors.New("arbiter: empty cell set")

// cellState is the mutable per-cell ownership record. An entry with
// token == 0 is free; the fencing counter survives releases so tokens
// are strictly increasing over the cell's lifetime.
type cellState struct {
	token uint64 // grant token of the current owner; 0 means free
	node  string
	fence uint64
}

// Arbiter arbitrates exclusive ownership of cells across nodes.
// The zero value is not usable; construct with New.
type Arbiter struct {
	mu        sync.Mutex
	cells     map[ID]*cellState
	gen       chan struct{} // closed and replaced on every release (broadcast)
	nextToken uint64
	active    int
}

// New returns a ready-to-use Arbiter.
func New() *Arbiter {
	return &Arbiter{
		cells: make(map[ID]*cellState),
		gen:   make(chan struct{}),
	}
}

// Grant is an ownership handle for an acquired cell set. It is safe for
// concurrent use; Release is idempotent and exactly-once.
type Grant struct {
	arb    *Arbiter
	token  uint64
	node   string
	cells  []ID
	fences map[ID]uint64
	done   int32 // atomic: 0 = live, 1 = released
}

// Node returns the owner name recorded at acquisition time.
func (g *Grant) Node() string { return g.node }

// Cells returns a copy of the owned cell set.
func (g *Grant) Cells() []ID {
	out := make([]ID, len(g.cells))
	copy(out, g.cells)
	return out
}

// Fence returns the fencing token issued for cell at grant time.
// The second return value is false if the cell is not part of the grant.
func (g *Grant) Fence(cell ID) (uint64, bool) {
	f, ok := g.fences[cell]
	return f, ok
}

// Release frees all cells of the grant. It is idempotent: the first call
// performs the release and reports true; later calls are no-ops reporting
// false. A stale grant can never free cells re-granted to another node,
// because ownership is validated against the grant token under the mutex.
func (g *Grant) Release() bool {
	if !atomic.CompareAndSwapInt32(&g.done, 0, 1) {
		return false
	}
	a := g.arb
	a.mu.Lock()
	for _, c := range g.cells {
		if st, ok := a.cells[c]; ok && st.token == g.token {
			st.token = 0
			st.node = ""
		}
	}
	a.active--
	close(a.gen) // wake all waiters; they re-check under the mutex
	a.gen = make(chan struct{})
	a.mu.Unlock()
	return true
}

// canonical sorts and dedups the requested cell set.
func canonical(cells []ID) []ID {
	out := make([]ID, len(cells))
	copy(out, cells)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	k := 0
	for _, c := range out {
		if k == 0 || out[k-1] != c {
			out[k] = c
			k++
		}
	}
	return out[:k]
}

// tryGrantLocked attempts the all-or-nothing grant. Caller holds a.mu.
func (a *Arbiter) tryGrantLocked(node string, cells []ID) *Grant {
	for _, c := range cells {
		if st, ok := a.cells[c]; ok && st.token != 0 {
			return nil
		}
	}
	a.nextToken++
	token := a.nextToken
	fences := make(map[ID]uint64, len(cells))
	for _, c := range cells {
		st, ok := a.cells[c]
		if !ok {
			st = &cellState{}
			a.cells[c] = st
		}
		st.fence++
		st.token = token
		st.node = node
		fences[c] = st.fence
	}
	a.active++
	return &Grant{arb: a, token: token, node: node, cells: cells, fences: fences}
}

// Acquire blocks until the whole cell set is granted exclusively to node,
// or until ctx is cancelled/times out. The acquisition is atomic: either
// every requested cell is granted or none is. Waiters never hold locks
// while parked, so concurrent acquisitions cannot deadlock.
func (a *Arbiter) Acquire(ctx context.Context, node string, cells []ID) (*Grant, error) {
	set := canonical(cells)
	if len(set) == 0 {
		return nil, ErrNoCells
	}
	for {
		a.mu.Lock()
		if g := a.tryGrantLocked(node, set); g != nil {
			a.mu.Unlock()
			return g, nil
		}
		ch := a.gen
		a.mu.Unlock()
		select {
		case <-ch:
			// Ownership changed; retry.
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// AcquireTimeout is Acquire bounded by a relative timeout.
func (a *Arbiter) AcquireTimeout(node string, cells []ID, d time.Duration) (*Grant, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return a.Acquire(ctx, node, cells)
}

// TryAcquire performs a single non-blocking acquisition attempt.
func (a *Arbiter) TryAcquire(node string, cells []ID) (*Grant, bool) {
	set := canonical(cells)
	if len(set) == 0 {
		return nil, false
	}
	a.mu.Lock()
	g := a.tryGrantLocked(node, set)
	a.mu.Unlock()
	return g, g != nil
}

// OwnerOf reports the current owner of a cell and its fencing token.
// owned is false when the cell is free.
func (a *Arbiter) OwnerOf(cell ID) (node string, fence uint64, owned bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st, ok := a.cells[cell]; ok && st.token != 0 {
		return st.node, st.fence, true
	}
	return "", 0, false
}

// Active returns the number of live grants.
func (a *Arbiter) Active() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.active
}
