package ownership

import (
	"context"
	"errors"
)

// Sentinel errors returned by Tracker.Apply and the host adapter. They wrap
// cleanly with errors.Is.
var (
	// ErrGenerationSealed is returned when a Spawned event targets a generation
	// that has already been sealed by cleanup.
	ErrGenerationSealed = errors.New("ownership: tenant generation is sealed")

	// ErrPIDInUse is returned when Spawned reuses a PID that still belongs to a
	// live tracked process.
	ErrPIDInUse = errors.New("ownership: pid already belongs to a live process")

	// ErrUnknownProcess is returned for events referencing an untracked PID.
	ErrUnknownProcess = errors.New("ownership: unknown process")

	// ErrGenerationMismatch is returned when a child claims a generation that
	// differs from the generation inherited from its tracked parent.
	ErrGenerationMismatch = errors.New("ownership: child generation does not match parent generation")

	// ErrInvalidGeneration is returned for events carrying an invalid Gen.
	ErrInvalidGeneration = errors.New("ownership: invalid tenant generation")
)

// node is the internal record for one observed process. Dead nodes are
// retained so that parent/child edges survive parent-first exits and so PID
// reuse can be detected; Prune reclaims them once they can no longer be
// reached from any live descendant.
type node struct {
	pid      int
	parent   int
	gen      Gen
	pgid     int
	sid      int
	alive    bool
	children []int
}

// Tracker is the concurrency-safe, event-sourced ownership registry.
//
// The zero value is not usable; construct one with NewTracker.
type Tracker struct {
	mu     chan struct{}
	nodes  map[int]*node
	byGen  map[Gen]map[int]*node
	sealed map[Gen]struct{}
	wake   chan struct{}
}

// NewTracker returns an empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{
		mu:     make(chan struct{}, 1),
		nodes:  make(map[int]*node),
		byGen:  make(map[Gen]map[int]*node),
		sealed: make(map[Gen]struct{}),
		wake:   make(chan struct{}, 1),
	}
}

// Apply validates and commits one ownership event. Validation failures leave
// the tracker unchanged.
func (t *Tracker) Apply(ev Event) error {
	t.lock()
	defer t.unlock()

	switch e := ev.(type) {
	case Spawned:
		return t.applySpawned(e)
	case Exited:
		return t.applyExited(e)
	case GroupChanged:
		return t.applyGroupChanged(e)
	case Sealed:
		return t.applySealed(e)
	default:
		return errors.New("ownership: unknown event type")
	}
}

func (t *Tracker) applySpawned(e Spawned) error {
	if !e.Generation.Valid() {
		return ErrInvalidGeneration
	}
	if e.PID <= 0 {
		return errors.New("ownership: spawn event requires a positive pid")
	}
	if _, isSealed := t.sealed[e.Generation]; isSealed {
		return ErrGenerationSealed
	}
	if existing, ok := t.nodes[e.PID]; ok && existing.alive {
		return ErrPIDInUse
	}

	if e.ParentPID > 0 {
		if parent, ok := t.nodes[e.ParentPID]; ok && parent.gen != e.Generation {
			return ErrGenerationMismatch
		}
	}

	// Reusing the PID of a fully exited process replaces the historical node.
	// The old node stays referenced through its parent's child list for
	// ancestry diagnostics; identity from now on belongs to the new node.
	if old, ok := t.nodes[e.PID]; ok && !old.alive {
		t.detachFromGeneration(old)
		if siblings, ok := t.nodes[old.parent]; ok {
			siblings.children = removePID(siblings.children, old.pid)
		}
	}

	n := &node{
		pid:    e.PID,
		parent: e.ParentPID,
		gen:    e.Generation,
		pgid:   e.PGID,
		alive:  true,
	}
	t.nodes[e.PID] = n

	members, ok := t.byGen[e.Generation]
	if !ok {
		members = make(map[int]*node)
		t.byGen[e.Generation] = members
	}
	members[e.PID] = n

	if parent, ok := t.nodes[e.ParentPID]; ok {
		parent.children = append(parent.children, e.PID)
	}

	t.signal()
	return nil
}

func (t *Tracker) applyExited(e Exited) error {
	n, ok := t.nodes[e.PID]
	if !ok {
		return ErrUnknownProcess
	}
	if !n.alive {
		// Repeated exit notifications (e.g. duplicate wait events) are a no-op.
		return nil
	}
	n.alive = false
	t.signal()
	return nil
}

func (t *Tracker) applyGroupChanged(e GroupChanged) error {
	n, ok := t.nodes[e.PID]
	if !ok || !n.alive {
		return ErrUnknownProcess
	}
	n.pgid = e.PGID
	n.sid = e.SID
	// No signal: group/session changes do not affect ownership or cleanup
	// membership, so waiters do not need to wake.
	return nil
}

func (t *Tracker) applySealed(e Sealed) error {
	if !e.Generation.Valid() {
		return ErrInvalidGeneration
	}
	if _, already := t.sealed[e.Generation]; already {
		return nil
	}
	t.sealed[e.Generation] = struct{}{}
	t.signal()
	return nil
}

func (t *Tracker) detachFromGeneration(n *node) {
	if members, ok := t.byGen[n.gen]; ok {
		delete(members, n.pid)
		if len(members) == 0 {
			delete(t.byGen, n.gen)
		}
	}
}

// IsSealed reports whether generation has been closed against new spawns.
func (t *Tracker) IsSealed(g Gen) bool {
	t.lock()
	defer t.unlock()
	_, ok := t.sealed[g]
	return ok
}

// CanSpawn reports whether a process may still be created within generation.
func (t *Tracker) CanSpawn(g Gen) bool {
	t.lock()
	defer t.unlock()
	_, sealed := t.sealed[g]
	return !sealed
}

// GenerationOf returns the immutable generation of a live process.
func (t *Tracker) GenerationOf(pid int) (Gen, bool) {
	t.lock()
	defer t.unlock()
	n, ok := t.nodes[pid]
	if !ok || !n.alive {
		return Gen{}, false
	}
	return n.gen, true
}

// Lookup returns a point-in-time snapshot of a tracked process.
func (t *Tracker) Lookup(pid int) (ProcInfo, bool) {
	t.lock()
	defer t.unlock()
	n, ok := t.nodes[pid]
	if !ok {
		return ProcInfo{}, false
	}
	return n.snapshot(), true
}

// ProcInfo is an immutable snapshot of a tracked process.
type ProcInfo struct {
	PID        int
	ParentPID  int
	Generation Gen
	PGID       int
	SID        int
	Alive      bool
}

func (n *node) snapshot() ProcInfo {
	return ProcInfo{
		PID:        n.pid,
		ParentPID:  n.parent,
		Generation: n.gen,
		PGID:       n.pgid,
		SID:        n.sid,
		Alive:      n.alive,
	}
}

// Members returns the PIDs of every process that currently carries generation,
// including processes whose parents have exited. Dead but not yet pruned
// processes are included so callers can distinguish "never existed" from
// "already terminated".
func (t *Tracker) Members(g Gen) []int {
	t.lock()
	defer t.unlock()
	members := t.byGen[g]
	out := make([]int, 0, len(members))
	for pid := range members {
		out = append(out, pid)
	}
	return out
}

// LiveMembers returns the PIDs of live processes carrying generation. This is
// the reachable descendant set cleanup must terminate.
func (t *Tracker) LiveMembers(g Gen) []int {
	t.lock()
	defer t.unlock()
	members := t.byGen[g]
	out := make([]int, 0, len(members))
	for pid, n := range members {
		if n.alive {
			out = append(out, pid)
		}
	}
	return out
}

// ReachableFrom returns the live descendant closure of pid along recorded
// parent/child edges, following generations implicitly because edges only
// connect processes of the same generation.
func (t *Tracker) ReachableFrom(pid int) []int {
	t.lock()
	defer t.unlock()
	root, ok := t.nodes[pid]
	if !ok {
		return nil
	}
	seen := make(map[int]struct{})
	var walk func(n *node)
	walk = func(n *node) {
		if _, done := seen[n.pid]; done {
			return
		}
		seen[n.pid] = struct{}{}
		for _, childPID := range n.children {
			if child, ok := t.nodes[childPID]; ok && child.alive {
				walk(child)
			}
		}
	}
	if root.alive {
		walk(root)
	} else {
		// A dead root can still anchor orphaned live descendants.
		for _, childPID := range root.children {
			if child, ok := t.nodes[childPID]; ok && child.alive {
				walk(child)
			}
		}
	}
	out := make([]int, 0, len(seen))
	for pid := range seen {
		out = append(out, pid)
	}
	return out
}

// PruneGeneration reclaims dead bookkeeping for generation. Live processes and
// edges still needed by live descendants of other generations are retained. It
// is normally called after Shutdown has confirmed the generation is empty.
func (t *Tracker) PruneGeneration(g Gen) int {
	t.lock()
	defer t.unlock()

	members := t.byGen[g]
	removed := 0
	for pid, n := range members {
		if n.alive || t.hasLiveDescendant(n) {
			continue
		}
		if parent, ok := t.nodes[n.parent]; ok {
			parent.children = removePID(parent.children, pid)
		}
		delete(t.nodes, pid)
		delete(members, pid)
		removed++
	}
	if len(members) == 0 {
		delete(t.byGen, g)
	}
	return removed
}

func (t *Tracker) hasLiveDescendant(n *node) bool {
	for _, childPID := range n.children {
		child, ok := t.nodes[childPID]
		if !ok {
			continue
		}
		if child.alive || t.hasLiveDescendant(child) {
			return true
		}
	}
	return false
}

// Wait blocks until the tracker observes an event or ctx is canceled. It is
// the wakeup primitive used by the cleanup loop; events may coalesce, so
// callers must re-query the state they care about after waking.
func (t *Tracker) Wait(ctx context.Context) error {
	t.lock()
	ch := t.wake
	t.unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *Tracker) signal() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *Tracker) lock()   { t.mu <- struct{}{} }
func (t *Tracker) unlock() { <-t.mu }

func removePID(pids []int, pid int) []int {
	for i, candidate := range pids {
		if candidate == pid {
			return append(pids[:i], pids[i+1:]...)
		}
	}
	return pids
}
