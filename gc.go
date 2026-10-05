package blockgc

import "context"

// Collector implements mark-and-sweep garbage collection over a Store,
// driven by a Manifest. Safety invariants:
//
//  1. Marking is restartable within a run: roots published while a mark
//     pass is in flight are picked up by a follow-up pass, so a block can
//     never be swept just because its root appeared mid-collection.
//  2. Sweeping never deletes directly: unreachable blocks are quarantined
//     and journaled, then independently re-verified against a fresh
//     traversal from all active roots before deletion is confirmed.
//  3. Every destructive step is preceded by a synced journal record, so a
//     crash at any point can be resumed by Recover.
type Collector struct {
	store    *Store
	manifest *Manifest

	// Hooks are optional test/observability callbacks fired between phases.
	Hooks Hooks
}

// Hooks allow tests (or operators) to inject behavior between GC phases,
// e.g. publishing a root after the scan or simulating a crash.
type Hooks struct {
	// AfterMarkPass runs after each mark pass, before the collector checks
	// whether new roots appeared.
	AfterMarkPass func(pass int)
	// AfterQuarantine runs after blocks are quarantined and journaled but
	// before the independent verification traversal.
	AfterQuarantine func(ids []string)
}

// Stats summarizes one Collect or Recover run.
type Stats struct {
	Marked      int // live blocks found reachable
	Quarantined int // blocks moved to quarantine this run
	Restored    int // quarantined blocks found reachable again
	Deleted     int // blocks permanently deleted
}

func NewCollector(store *Store, manifest *Manifest) *Collector {
	return &Collector{store: store, manifest: manifest}
}

// Collect runs one full mark-sweep cycle.
func (c *Collector) Collect(ctx context.Context) (Stats, error) {
	var st Stats
	epoch, err := c.store.nextEpoch()
	if err != nil {
		return st, err
	}
	j, err := c.store.openJournal(epoch)
	if err != nil {
		return st, err
	}
	defer j.close()

	// ---- Mark ---------------------------------------------------------
	// Loop until the active root set is stable: any root published while we
	// were marking triggers another pass, so late arrivals are still marked.
	marked := map[string]bool{}
	seenRoots := map[string]bool{}
	for pass := 0; ; pass++ {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		var fresh []string
		for _, r := range c.manifest.ActiveRoots() {
			if !seenRoots[r] {
				seenRoots[r] = true
				fresh = append(fresh, r)
			}
		}
		for id := range c.reachableFrom(fresh) {
			marked[id] = true
		}
		if c.Hooks.AfterMarkPass != nil {
			c.Hooks.AfterMarkPass(pass)
		}
		// Stop only when the root set stayed stable across this pass; a
		// root published during the pass (including via the hook) forces
		// another one.
		stable := true
		for _, r := range c.manifest.ActiveRoots() {
			if !seenRoots[r] {
				stable = false
				break
			}
		}
		if stable {
			break
		}
	}

	// ---- Sweep into quarantine ---------------------------------------
	// Unmarked blocks are renamed into quarantine and journaled. Nothing is
	// deleted here: a crash leaves quarantined blocks recoverable.
	ids, err := c.store.List()
	if err != nil {
		return st, err
	}
	var quarantined []string
	for _, id := range ids {
		if marked[id] {
			continue
		}
		if err := c.store.Quarantine(id); err != nil {
			return st, err
		}
		if err := j.append(epoch, id, "quarantine"); err != nil {
			return st, err
		}
		quarantined = append(quarantined, id)
	}
	st.Marked = len(ids) - len(quarantined)
	st.Quarantined = len(quarantined)

	if c.Hooks.AfterQuarantine != nil {
		c.Hooks.AfterQuarantine(append([]string(nil), quarantined...))
	}

	// ---- Verify, then delete ------------------------------------------
	// Independent re-traversal from all currently active roots. Anything
	// that became reachable since the scan (new roots, revived references)
	// is restored instead of deleted.
	restored, deleted, err := c.resolveQuarantine(ctx, j, epoch, quarantined)
	st.Restored += restored
	st.Deleted += deleted
	return st, err
}

// Recover resumes interrupted GC work after a crash. It replays the
// journals, then reconciles every block found in quarantine:
//
//   - reachable from the current active roots  -> restored
//   - deletion already confirmed before crash  -> deleted
//   - quarantined but never confirmed          -> re-verified, then
//     restored or confirmed+deleted
//
// Recover is idempotent and safe to run repeatedly.
func (c *Collector) Recover(ctx context.Context) (Stats, error) {
	var st Stats
	lastOp, err := c.store.replayJournals()
	if err != nil {
		return st, err
	}
	quarantined, err := c.store.ListQuarantined()
	if err != nil {
		return st, err
	}
	if len(quarantined) == 0 {
		return st, nil
	}
	epoch, err := c.store.nextEpoch()
	if err != nil {
		return st, err
	}
	j, err := c.store.openJournal(epoch)
	if err != nil {
		return st, err
	}
	defer j.close()

	var pending []string
	for _, id := range quarantined {
		if lastOp[id] == "confirm" {
			// Deletion was confirmed but the crash hit before unlinking.
			if err := c.store.DeleteQuarantined(id); err != nil {
				return st, err
			}
			if err := j.append(epoch, id, "delete"); err != nil {
				return st, err
			}
			st.Deleted++
			continue
		}
		// Never confirmed (or no record at all): must be re-verified
		// against the live root set before any destructive action.
		pending = append(pending, id)
	}
	restored, deleted, err := c.resolveQuarantine(ctx, j, epoch, pending)
	st.Restored += restored
	st.Deleted += deleted
	return st, err
}

// resolveQuarantine independently re-traverses from all active roots and
// either restores or confirms+deletes each quarantined block.
func (c *Collector) resolveQuarantine(ctx context.Context, j *journal, epoch int, ids []string) (restored, deleted int, err error) {
	if len(ids) == 0 {
		return 0, 0, nil
	}
	reachable := c.reachableFrom(c.manifest.ActiveRoots())
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return restored, deleted, err
		}
		if reachable[id] {
			if err := c.store.Restore(id); err != nil {
				return restored, deleted, err
			}
			if err := j.append(epoch, id, "restore"); err != nil {
				return restored, deleted, err
			}
			restored++
			continue
		}
		// Confirmation record first, deletion second: a crash in between is
		// resumed by Recover without a second verification.
		if err := j.append(epoch, id, "confirm"); err != nil {
			return restored, deleted, err
		}
		if err := c.store.DeleteQuarantined(id); err != nil {
			return restored, deleted, err
		}
		if err := j.append(epoch, id, "delete"); err != nil {
			return restored, deleted, err
		}
		deleted++
	}
	return restored, deleted, nil
}

// reachableFrom performs a breadth-first traversal from roots. Cycles are
// handled by the visited set; dangling references and corrupt blocks are
// tolerated (the block ID itself still counts as visited, so a corrupt
// block is conservatively kept).
func (c *Collector) reachableFrom(roots []string) map[string]bool {
	visited := map[string]bool{}
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		b, err := c.store.getAny(id)
		if err != nil {
			continue // missing or corrupt: no refs to follow
		}
		queue = append(queue, b.Refs...)
	}
	return visited
}
