// Package arbiter implements a multi-node contention and conflict
// arbiter over hexgrid cells.
//
// Multiple nodes (goroutines, or distributed nodes fronted by this
// authority) compete to claim disjoint sets of cells. All operations
// are atomic and linearizable: every operation takes the affected lock
// shards in a globally consistent ascending order, so concurrent
// operations on overlapping cells serialize, operations on disjoint
// cells proceed in parallel, and no lock-ordering cycle can exist
// (deadlock-free by construction).
//
// The arbiter spawns no background goroutines; lease expiry is lazy
// (evaluated on access), so nothing can leak.
package arbiter

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"hexgsb/hexgrid"
)

const (
	numShards       = 64
	defaultMaxCells = 1 << 16
)

var (
	// ErrEmptyClaim is returned for operations on an empty cell set.
	ErrEmptyClaim = errors.New("arbiter: empty cell set")
	// ErrTooManyCells is returned when a request exceeds the per-op limit.
	ErrTooManyCells = errors.New("arbiter: cell set exceeds limit")
	// ErrNotHeld is returned when releasing or renewing a cell with no
	// active claim.
	ErrNotHeld = errors.New("arbiter: cell not held")
	// ErrNotOwner is returned when the caller does not own the claim.
	ErrNotOwner = errors.New("arbiter: cell owned by another node")
	// ErrStaleFence is returned when a fencing token does not match the
	// token of the active claim.
	ErrStaleFence = errors.New("arbiter: stale fencing token")
)

// ConflictError reports that an atomic acquire failed because some of
// the requested cells are held by other nodes. No cell was claimed.
type ConflictError struct {
	Node string
	// Held maps each conflicting cell to its current owner.
	Held map[hexgrid.Cell]string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("arbiter: node %q lost conflict on %d cell(s)", e.Node, len(e.Held))
}

// ClaimInfo is a read-only view of an active claim.
type ClaimInfo struct {
	Owner     string
	Fence     uint64
	ExpiresAt time.Time // zero means no expiry
}

type claim struct {
	owner     string
	fence     uint64
	expiresAt time.Time
}

func (c *claim) expired(now time.Time) bool {
	return !c.expiresAt.IsZero() && !now.Before(c.expiresAt)
}

type shard struct {
	mu     sync.Mutex
	claims map[hexgrid.Cell]*claim
}

// Arbiter arbitrates cell ownership between competing nodes. It is
// safe for concurrent use.
type Arbiter struct {
	shards   [numShards]shard
	fence    uint64 // atomic; monotonically increasing fencing token source
	now      func() time.Time
	maxCells int
}

// Option configures an Arbiter.
type Option func(*Arbiter)

// WithClock overrides the time source (mainly for tests).
func WithClock(clk func() time.Time) Option {
	return func(a *Arbiter) { a.now = clk }
}

// WithMaxCells overrides the per-operation cell limit.
func WithMaxCells(n int) Option {
	return func(a *Arbiter) { a.maxCells = n }
}

// New creates an Arbiter.
func New(opts ...Option) *Arbiter {
	a := &Arbiter{now: time.Now, maxCells: defaultMaxCells}
	for i := range a.shards {
		a.shards[i].claims = make(map[hexgrid.Cell]*claim)
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// shardOf hashes a cell to its lock shard (splitmix64 finalizer).
func shardOf(c hexgrid.Cell) int {
	x := uint64(c)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return int(x & (numShards - 1))
}

// normalizeCells deduplicates and validates a request cell set.
func normalizeCells(cells []hexgrid.Cell, maxCells int) ([]hexgrid.Cell, error) {
	if len(cells) == 0 {
		return nil, ErrEmptyClaim
	}
	seen := make(map[hexgrid.Cell]struct{}, len(cells))
	out := make([]hexgrid.Cell, 0, len(cells))
	for _, c := range cells {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	if len(out) > maxCells {
		return nil, ErrTooManyCells
	}
	return out, nil
}

// lockCells locks every shard covering cells in ascending shard order
// and returns the unlock function. The global order guarantees
// deadlock-freedom for multi-shard operations.
func (a *Arbiter) lockCells(cells []hexgrid.Cell) func() {
	var seen [numShards]bool
	idxs := make([]int, 0, 8)
	for _, c := range cells {
		s := shardOf(c)
		if !seen[s] {
			seen[s] = true
			idxs = append(idxs, s)
		}
	}
	sort.Ints(idxs)
	for _, s := range idxs {
		a.shards[s].mu.Lock()
	}
	return func() {
		for i := len(idxs) - 1; i >= 0; i-- {
			a.shards[idxs[i]].mu.Unlock()
		}
	}
}

// Acquire atomically claims all given cells for node. It is
// all-or-nothing: if any cell is actively held by another node, a
// *ConflictError is returned and no claim is made. Cells already held
// by node itself are renewed. A ttl > 0 makes the claims expire lazily.
// On success it returns the fencing token of the claims; tokens are
// strictly increasing across the arbiter's lifetime.
func (a *Arbiter) Acquire(node string, cells []hexgrid.Cell, ttl time.Duration) (uint64, error) {
	cells, err := normalizeCells(cells, a.maxCells)
	if err != nil {
		return 0, err
	}
	unlock := a.lockCells(cells)
	defer unlock()

	now := a.now()
	var conflict *ConflictError
	for _, c := range cells {
		if cl, ok := a.shards[shardOf(c)].claims[c]; ok && !cl.expired(now) && cl.owner != node {
			if conflict == nil {
				conflict = &ConflictError{Node: node, Held: make(map[hexgrid.Cell]string)}
			}
			conflict.Held[c] = cl.owner
		}
	}
	if conflict != nil {
		return 0, conflict
	}

	fence := atomic.AddUint64(&a.fence, 1)
	var exp time.Time
	if ttl > 0 {
		exp = now.Add(ttl)
	}
	for _, c := range cells {
		a.shards[shardOf(c)].claims[c] = &claim{owner: node, fence: fence, expiresAt: exp}
	}
	return fence, nil
}

// checkOwner validates that node actively owns cell c, optionally
// requiring the fencing token to match. Must be called with the shard
// of c locked.
func (a *Arbiter) checkOwner(c hexgrid.Cell, node string, fence uint64, now time.Time) error {
	cl, ok := a.shards[shardOf(c)].claims[c]
	if !ok || cl.expired(now) {
		return fmt.Errorf("%w: cell %s", ErrNotHeld, c)
	}
	if cl.owner != node {
		return fmt.Errorf("%w: cell %s held by %q", ErrNotOwner, c, cl.owner)
	}
	if fence != 0 && cl.fence != fence {
		return fmt.Errorf("%w: cell %s", ErrStaleFence, c)
	}
	return nil
}

// Release atomically releases the given cells. All cells must be
// actively owned by node; if fence is non-zero it must match the claim
// tokens. The operation is all-or-nothing.
func (a *Arbiter) Release(node string, cells []hexgrid.Cell, fence uint64) error {
	cells, err := normalizeCells(cells, a.maxCells)
	if err != nil {
		return err
	}
	unlock := a.lockCells(cells)
	defer unlock()

	now := a.now()
	for _, c := range cells {
		if err := a.checkOwner(c, node, fence, now); err != nil {
			return err
		}
	}
	for _, c := range cells {
		delete(a.shards[shardOf(c)].claims, c)
	}
	return nil
}

// Renew extends the lease of the given cells and rotates their fencing
// token. All cells must be actively owned by node; if fence is non-zero
// it must match. Returns the new fencing token.
func (a *Arbiter) Renew(node string, cells []hexgrid.Cell, fence uint64, ttl time.Duration) (uint64, error) {
	cells, err := normalizeCells(cells, a.maxCells)
	if err != nil {
		return 0, err
	}
	unlock := a.lockCells(cells)
	defer unlock()

	now := a.now()
	for _, c := range cells {
		if err := a.checkOwner(c, node, fence, now); err != nil {
			return 0, err
		}
	}
	newFence := atomic.AddUint64(&a.fence, 1)
	var exp time.Time
	if ttl > 0 {
		exp = now.Add(ttl)
	}
	for _, c := range cells {
		cl := a.shards[shardOf(c)].claims[c]
		cl.fence = newFence
		cl.expiresAt = exp
	}
	return newFence, nil
}

// Snapshot returns a consistent view of the active claims on the given
// cells: the shards are locked together in global order, so the result
// is a linearizable snapshot of exactly the requested cells. Cells with
// no active claim are absent from the map.
func (a *Arbiter) Snapshot(cells []hexgrid.Cell) (map[hexgrid.Cell]ClaimInfo, error) {
	cells, err := normalizeCells(cells, a.maxCells)
	if err != nil {
		return nil, err
	}
	unlock := a.lockCells(cells)
	defer unlock()

	now := a.now()
	out := make(map[hexgrid.Cell]ClaimInfo, len(cells))
	for _, c := range cells {
		if cl, ok := a.shards[shardOf(c)].claims[c]; ok && !cl.expired(now) {
			out[c] = ClaimInfo{Owner: cl.owner, Fence: cl.fence, ExpiresAt: cl.expiresAt}
		}
	}
	return out, nil
}

// OwnerOf returns the active owner of a single cell, if any.
func (a *Arbiter) OwnerOf(c hexgrid.Cell) (string, bool) {
	snap, err := a.Snapshot([]hexgrid.Cell{c})
	if err != nil {
		return "", false
	}
	info, ok := snap[c]
	return info.Owner, ok
}
