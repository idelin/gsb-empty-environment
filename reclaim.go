// Package netsec implements reachability-based reclamation of encrypted
// volume block keys across snapshot roots.
//
// Snapshots of an encrypted volume may share parent blocks. Each snapshot
// is published as a root that authorizes retention of every block reachable
// from its tip. Root authorization is tracked separately from the block
// reference graph: a block key is destroyed only when its reachable-root
// count drops to zero, so revoking one snapshot never destroys keys still
// authorized by another. Incremental commits and revocations are serialized
// under a single mutex so a revocation never observes a half-published
// commit.
package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// BlockID identifies an encrypted data block whose key is managed by the
// reclaimer.
type BlockID string

// RootID identifies a snapshot authorization root.
type RootID string

var (
	// ErrRootExists is returned when a commit reuses a live root ID.
	ErrRootExists = errors.New("netsec: root already exists")
	// ErrBlockExists is returned when a commit redefines a known block.
	ErrBlockExists = errors.New("netsec: block already committed")
	// ErrDanglingEdge is returned when a committed edge points at a block
	// that is neither committed in the same batch nor already retained.
	ErrDanglingEdge = errors.New("netsec: edge targets unknown block")
	// ErrUnknownTip is returned when a commit's tip is not resolvable.
	ErrUnknownTip = errors.New("netsec: tip is not a known block")
)

// Reclaimer tracks which snapshot roots authorize retention of which
// encrypted blocks, and destroys block keys once no live root can reach
// them. The zero value is not ready for use; construct with New.
type Reclaimer struct {
	mu sync.Mutex
	// edges holds the block reference graph for retained blocks only.
	edges map[BlockID][]BlockID
	// roots maps each live root to its tip block.
	roots map[RootID]BlockID
	// reach counts, per retained block, how many live roots can reach it.
	// A block is retained iff its count is greater than zero.
	reach map[BlockID]int
}

// New returns an empty Reclaimer.
func New() *Reclaimer {
	return &Reclaimer{
		edges: make(map[BlockID][]BlockID),
		roots: make(map[RootID]BlockID),
		reach: make(map[BlockID]int),
	}
}

// Commit atomically publishes a batch of new blocks and the snapshot root
// that authorizes them. edges maps each new block to its child references;
// children may be new blocks from the same batch or blocks already retained
// by earlier commits (shared parent blocks). tip is the root's entry block.
//
// The whole batch is validated before any state changes: a failed commit
// leaves the reclaimer untouched. Commit and Revoke are mutually exclusive.
func (r *Reclaimer) Commit(root RootID, tip BlockID, edges map[BlockID][]BlockID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.roots[root]; ok {
		return fmt.Errorf("%w: %q", ErrRootExists, root)
	}
	for id := range edges {
		if _, ok := r.edges[id]; ok {
			return fmt.Errorf("%w: %q", ErrBlockExists, id)
		}
	}
	known := func(id BlockID) bool {
		if _, ok := edges[id]; ok {
			return true
		}
		_, ok := r.edges[id]
		return ok
	}
	if !known(tip) {
		return fmt.Errorf("%w: %q", ErrUnknownTip, tip)
	}
	for from, children := range edges {
		for _, to := range children {
			if !known(to) {
				return fmt.Errorf("%w: %q -> %q", ErrDanglingEdge, from, to)
			}
		}
	}

	for id, children := range edges {
		r.edges[id] = append([]BlockID(nil), children...)
	}
	r.roots[root] = tip
	for id := range r.reachableLocked(tip) {
		r.reach[id]++
	}
	return nil
}

// Revoke removes a snapshot root and destroys the keys of exactly those
// blocks that were reachable from it but from no other live root. It
// returns the destroyed block set in sorted order. Revoking an unknown
// root is a no-op and returns nil.
func (r *Reclaimer) Revoke(root RootID) []BlockID {
	r.mu.Lock()
	defer r.mu.Unlock()

	tip, ok := r.roots[root]
	if !ok {
		return nil
	}
	delete(r.roots, root)

	var destroyed []BlockID
	for id := range r.reachableLocked(tip) {
		r.reach[id]--
		if r.reach[id] == 0 {
			destroyed = append(destroyed, id)
		}
	}
	for _, id := range destroyed {
		delete(r.reach, id)
		delete(r.edges, id)
	}
	sort.Slice(destroyed, func(i, j int) bool { return destroyed[i] < destroyed[j] })
	return destroyed
}

// Retained reports whether a block's key is still authorized by at least
// one live root.
func (r *Reclaimer) Retained(id BlockID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reach[id] > 0
}

// Roots returns the live root IDs in sorted order.
func (r *Reclaimer) Roots() []RootID {
	r.mu.Lock()
	defer r.mu.Unlock()
	roots := make([]RootID, 0, len(r.roots))
	for id := range r.roots {
		roots = append(roots, id)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i] < roots[j] })
	return roots
}

// reachableLocked returns the set of blocks reachable from tip, following
// the retained-block graph. Cycles are handled by the visited set.
// Callers must hold r.mu.
func (r *Reclaimer) reachableLocked(tip BlockID) map[BlockID]struct{} {
	seen := make(map[BlockID]struct{})
	stack := []BlockID{tip}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := seen[id]; ok {
			continue
		}
		if _, ok := r.edges[id]; !ok {
			continue
		}
		seen[id] = struct{}{}
		stack = append(stack, r.edges[id]...)
	}
	return seen
}
