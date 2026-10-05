package blockgc

import (
	"errors"
	"fmt"
	"sync"
)

// ErrQuarantineConflict is returned when verification finds that blocks
// selected for deletion are still reachable from an active root. The
// quarantined blocks are restored and nothing is deleted.
var ErrQuarantineConflict = errors.New("blockgc: reachable blocks were quarantined, collection aborted")

// Collector runs mark-and-sweep garbage collection against a Store, using a
// Manifest as the root set and a Journal for crash recovery.
//
// Safety model:
//   - Roots are re-read after marking (barrier) until the manifest is
//     stable, so roots published during the scan are also traced.
//   - A write barrier on the store treats blocks allocated during a
//     collection as tentative roots and resurrects quarantined blocks that
//     become referenced again.
//   - Before deletion, an independent traversal from all active roots
//     verifies the quarantine set; any conflict restores everything.
//   - Deletion is two-phase: quarantine first, then a confirmation record,
//     then physical delete. A crash at any point is resumable.
type Collector struct {
	store    *Store
	manifest *Manifest
	journal  Journal

	mu      sync.Mutex
	active  bool
	pending map[BlockID]struct{}
}

// NewCollector wires a collector to the store (installing the write
// barrier), manifest and journal.
func NewCollector(s *Store, m *Manifest, j Journal) *Collector {
	c := &Collector{store: s, manifest: m, journal: j}
	s.SetBarrier(c.onPut)
	return c
}

// Run executes one full collection cycle.
func (c *Collector) Run() error {
	c.mu.Lock()
	if c.active {
		c.mu.Unlock()
		return errors.New("blockgc: collection already in progress")
	}
	c.active = true
	c.pending = make(map[BlockID]struct{})
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.active = false
		c.pending = nil
		c.mu.Unlock()
	}()

	st := &State{Phase: PhaseMark, Snapshot: c.manifest.ActiveHeads()}
	if err := c.journal.Save(st); err != nil {
		return err
	}

	marked := c.mark(st.Snapshot)

	// Barrier: the manifest may have changed while we were marking. Keep
	// tracing newly published roots (and blocks allocated during the scan)
	// until the root set is stable.
	for {
		v1 := c.manifest.Version()
		heads := c.manifest.ActiveHeads()
		fresh := c.drainPending()
		var newRoots []BlockID
		for _, h := range heads {
			if !marked[h] {
				newRoots = append(newRoots, h)
			}
		}
		newRoots = append(newRoots, fresh...)
		if len(newRoots) == 0 && v1 == c.manifest.Version() {
			break
		}
		for id := range c.mark(newRoots) {
			marked[id] = true
		}
	}

	st.Marked = setToSlice(marked)
	if err := c.journal.Save(st); err != nil {
		return err
	}

	// Sweep: everything live that was not marked goes to quarantine. The
	// quarantine list is journaled BEFORE blocks are moved, so a crash can
	// always reconstruct what was selected.
	var quarantine []BlockID
	for _, id := range c.store.LiveIDs() {
		if !marked[id] {
			quarantine = append(quarantine, id)
		}
	}
	st.Phase = PhaseSweep
	st.Quarantine = quarantine
	if err := c.journal.Save(st); err != nil {
		return err
	}
	c.store.Quarantine(quarantine...)

	// Verify: independently re-traverse from all currently active roots.
	// Anything reachable must not be in quarantine.
	if bad := Verify(c.store, c.manifest, sliceToSet(quarantine)); len(bad) > 0 {
		c.restoreReachable(quarantine...)
		_ = c.journal.Clear()
		return fmt.Errorf("%w: %d blocks restored (first: %s)", ErrQuarantineConflict, len(bad), bad[0])
	}

	// Confirm: write the deletion confirmation record, then delete.
	st.Phase = PhaseDelete
	st.Confirmed = true
	if err := c.journal.Save(st); err != nil {
		return err
	}
	c.store.Delete(quarantine...)
	return c.journal.Clear()
}

// Resume inspects the journal and finishes or rolls back an interrupted
// collection. It reports whether there was anything to resume.
//
//   - Deletion confirmed (PhaseDelete): complete the physical deletion.
//   - Anything earlier: deletion was never confirmed, so quarantined blocks
//     are restored and a fresh collection is run.
func (c *Collector) Resume() (bool, error) {
	st, err := c.journal.Load()
	if err != nil {
		return false, err
	}
	if st == nil {
		return false, nil
	}
	if st.Confirmed {
		// Deletion was confirmed before the crash: it is safe (and
		// required) to finish deleting exactly the journaled set.
		c.store.Quarantine(st.Quarantine...)
		c.store.Delete(st.Quarantine...)
		return true, c.journal.Clear()
	}
	// Not confirmed: roll back any quarantine and start over.
	c.restoreReachable(st.Quarantine...)
	if err := c.journal.Clear(); err != nil {
		return true, err
	}
	return true, c.Run()
}

// onPut is the store write barrier. Blocks allocated during a collection are
// tentative roots; references to quarantined blocks resurrect them
// (transitively), so a writer can never resurrect a graph on top of deleted
// storage.
func (c *Collector) onPut(id BlockID, refs []BlockID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active {
		return
	}
	c.pending[id] = struct{}{}
	var revive []BlockID
	if c.store.IsQuarantined(id) {
		revive = append(revive, id)
	}
	for _, r := range refs {
		if c.store.IsQuarantined(r) {
			revive = append(revive, r)
		}
	}
	if len(revive) > 0 {
		c.restoreReachable(revive...)
	}
}

// drainPending returns and clears blocks allocated during this cycle.
func (c *Collector) drainPending() []BlockID {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]BlockID, 0, len(c.pending))
	for id := range c.pending {
		out = append(out, id)
	}
	c.pending = make(map[BlockID]struct{})
	return out
}

// mark traces all blocks reachable from roots. Cycles are handled by the
// visited set; references to missing blocks are tolerated.
func (c *Collector) mark(roots []BlockID) map[BlockID]bool {
	marked := make(map[BlockID]bool)
	stack := append([]BlockID(nil), roots...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if marked[id] {
			continue
		}
		b, ok := c.store.Get(id)
		if !ok {
			continue
		}
		marked[id] = true
		stack = append(stack, b.Refs...)
	}
	return marked
}

// restoreReachable moves ids and everything reachable from them back out of
// quarantine.
func (c *Collector) restoreReachable(ids ...BlockID) {
	seen := make(map[BlockID]bool)
	stack := append([]BlockID(nil), ids...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		c.store.Restore(id)
		if b, ok := c.store.Get(id); ok {
			stack = append(stack, b.Refs...)
		}
	}
}

func setToSlice(s map[BlockID]bool) []BlockID {
	out := make([]BlockID, 0, len(s))
	for id := range s {
		out = append(out, id)
	}
	return out
}

func sliceToSet(ids []BlockID) map[BlockID]bool {
	out := make(map[BlockID]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}
