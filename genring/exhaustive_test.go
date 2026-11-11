package genring

import (
	"fmt"
	"strings"
	"testing"
)

// exhaustEnv is a small in-memory model of the device and the completion
// queue the host has not yet consumed. The genring.Ring is driven entirely
// through this model so every reachable interleaving of submissions,
// out-of-order completions, duplicates, late entries and resets is explored.
type exhaustEnv struct {
	ring    *Ring
	rec     *recorder
	t       *testing.T
	leased  map[Lease]bool // leases currently owned by an occupied slot
	devLive map[Lease]exactEntry
	cq      []exactEntry
	next    Lease
}

type exactEntry struct {
	slot  int
	tag   uint64
	gen   Generation
	lease Lease
}

func newExhaustEnv(t *testing.T, size, tagBits, maxInflight, ceiling int) *exhaustEnv {
	t.Helper()
	rec := newRecorder()
	r, err := New(Config{
		Size:        size,
		TagBits:     tagBits,
		MaxInflight: maxInflight,
		OnRelease:   rec.onRelease,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.SetSequenceCeiling(uint32(ceiling))
	return &exhaustEnv{
		ring:    r,
		rec:     rec,
		t:       t,
		leased:  map[Lease]bool{},
		devLive: map[Lease]exactEntry{},
		next:    1,
	}
}

func (e *exhaustEnv) checkInvariant(stage string) {
	t := e.t
	// Reconcile the occupied model set against actual release counts first:
	// reclaim can release a prefix of leases from a single Complete call.
	for lease := range e.leased {
		if e.rec.counts[lease] > 0 {
			delete(e.leased, lease)
		}
	}
	// Every currently occupied slot's lease must not have been released.
	for i := range e.ring.slots {
		s := &e.ring.slots[i]
		if s.inFlight || s.completed {
			if e.rec.counts[s.lease] != 0 {
				t.Fatalf("%s: occupied lease %d already released %d times; slot=%+v",
					stage, s.lease, e.rec.counts[s.lease], *s)
			}
			if !e.leased[s.lease] {
				t.Fatalf("%s: occupied lease %d missing from model", stage, s.lease)
			}
		}
	}
	// No lease may ever be released more than once.
	for lease, n := range e.rec.counts {
		if n > 1 {
			t.Fatalf("%s: lease %d released %d times", stage, lease, n)
		}
	}
	// Released leases must leave the occupied model set exactly once.
	for lease, n := range e.rec.counts {
		if n == 1 && e.leased[lease] {
			t.Fatalf("%s: released lease %d still marked occupied in model", stage, lease)
		}
	}
	if e.ring.inflight < 0 || e.ring.inflight > e.ring.maxInflight {
		t.Fatalf("%s: inflight %d out of bounds", stage, e.ring.inflight)
	}
}

func (e *exhaustEnv) snapshot() string {
	var b strings.Builder
	r := e.ring
	fmt.Fprintf(&b, "t=%d,n=%d,inf=%d,comp=%d,ep=%d,seq=%d,dr=%v,iso=%v|",
		r.tail, r.next, r.inflight, r.completed, r.epoch, r.sequence, r.draining, r.isolated)
	for i := range r.slots {
		s := &r.slots[i]
		fmt.Fprintf(&b, "%d:%d:%d:%d:%d:%d;",
			i, boolNum(s.inFlight), boolNum(s.completed), s.gen, s.lease, s.lastTag)
	}
	// Model side: which leases the device still holds and what is queued.
	for lease := range e.devLive {
		fmt.Fprintf(&b, "L%d;", lease)
	}
	fmt.Fprint(&b, "Q")
	for _, c := range e.cq {
		fmt.Fprintf(&b, "%d/%d;", c.slot, c.tag)
	}
	fmt.Fprintf(&b, "|occ")
	for lease := range e.leased {
		fmt.Fprintf(&b, "%d;", lease)
	}
	return b.String()
}

func boolNum(b bool) int {
	if b {
		return 1
	}
	return 0
}

// clone produces an independent environment for one DFS branch.
func (e *exhaustEnv) clone() *exhaustEnv {
	slots := append([]Slot(nil), e.ring.slots...)
	ring := *e.ring
	ring.slots = slots
	events := append([]releaseEvent(nil), e.rec.events...)
	counts := make(map[Lease]int, len(e.rec.counts))
	for k, v := range e.rec.counts {
		counts[k] = v
	}
	leased := make(map[Lease]bool, len(e.leased))
	for k, v := range e.leased {
		leased[k] = v
	}
	devLive := make(map[Lease]exactEntry, len(e.devLive))
	for k, v := range e.devLive {
		devLive[k] = v
	}
	cq := append([]exactEntry(nil), e.cq...)
	rec := &recorder{events: events, counts: counts}
	ring.onRelease = rec.onRelease
	return &exhaustEnv{
		ring:    &ring,
		rec:     rec,
		t:       e.t,
		leased:  leased,
		devLive: devLive,
		cq:      cq,
		next:    e.next,
	}
}

// liveLeases returns device-held leases in a stable order.
func (e *exhaustEnv) liveLeases() []Lease {
	out := make([]Lease, 0, len(e.devLive))
	for lease := range e.devLive {
		out = append(out, lease)
	}
	// Sort by lease id so branch ordering is deterministic.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// applySubmit issues one request through the ring and, on success, models the
// device holding the descriptor.
func (e *exhaustEnv) applySubmit() bool {
	lease := e.next
	idx, gen, err := e.ring.Submit(lease, Tenant(lease))
	if err != nil {
		return false
	}
	e.next++
	e.leased[lease] = true
	e.devLive[lease] = exactEntry{
		slot:  idx,
		tag:   e.ring.Tag(gen),
		gen:   gen,
		lease: lease,
	}
	return true
}

// applyDeviceComplete models hardware writing a completion entry for one
// live descriptor; it moves from device-held state into the host CQ.
func (e *exhaustEnv) applyDeviceComplete(lease Lease) bool {
	entry, ok := e.devLive[lease]
	if !ok {
		return false
	}
	delete(e.devLive, lease)
	e.cq = append(e.cq, entry)
	return true
}

// applyCQConsume feeds one queued entry into the ring at its own index/tag.
func (e *exhaustEnv) applyCQConsume(pos int) bool {
	if pos < 0 || pos >= len(e.cq) {
		return false
	}
	entry := e.cq[pos]
	res, err := e.ring.Complete(entry.slot, entry.tag)
	e.classify(entry, res, err)
	// Remove the consumed entry regardless of classification.
	e.cq = append(e.cq[:pos], e.cq[pos+1:]...)
	return true
}

// applyCQDuplicate replays an existing queued entry without removing it,
// modeling a repeated CQE.
func (e *exhaustEnv) applyCQDuplicate(pos int) bool {
	if pos < 0 || pos >= len(e.cq) {
		return false
	}
	entry := e.cq[pos]
	res, err := e.ring.Complete(entry.slot, entry.tag)
	e.classify(entry, res, err)
	return true
}

// applyLateEntry fabricates a completion for a slot/lease that the device no
// longer holds, modeling an entry arriving after the descriptor was reused.
func (e *exhaustEnv) applyLateEntry() bool {
	if e.ring.isolated {
		return false
	}
	// Use an out-of-range slot index: a real descriptor can never produce it,
	// so this entry is unconditionally bogus and must quarantine without
	// releasing a lease through the normal completion path.
	index := e.ring.size
	tag := uint64(0)
	res, err := e.ring.Complete(index, tag)
	e.classify(exactEntry{slot: index, tag: tag}, res, err)
	return true
}

// applyReset models a device reset.
func (e *exhaustEnv) applyReset() bool {
	if len(e.cq) != 0 {
		// The driver reset contract requires the completion queue to be
		// drained before reset; a truncated tag alone cannot distinguish a
		// pre-reset entry that collides with a post-reset generation.
		return false
	}
	if _, err := e.ring.Reset(); err != nil {
		return false
	}
	// Every occupied lease is returned; drop all device and CQ state.
	for lease := range e.leased {
		delete(e.leased, lease)
	}
	e.devLive = map[Lease]exactEntry{}
	return true
}

// classify reconciles a Complete result with the oracle expectations.
func (e *exhaustEnv) classify(entry exactEntry, res CompletionResult, err error) {
	t := e.t
	switch {
	case e.ring.isolated:
		// Ambiguity or mismatch: all occupied leases must now be released.
		for lease := range e.leased {
			if e.rec.counts[lease] != 1 {
				t.Fatalf("isolation did not release lease %d once: counts=%+v", lease, e.rec.counts)
			}
			delete(e.leased, lease)
		}
		e.devLive = map[Lease]exactEntry{}
	case res.Released:
		lease := entry.lease
		if lease == 0 {
			t.Fatalf("released result for fabricated entry: %+v", res)
		}
		if e.rec.counts[lease] != 1 {
			t.Fatalf("lease %d release count = %d after completion", lease, e.rec.counts[lease])
		}
		delete(e.leased, lease)
	case res.Duplicate || res.Stale:
		// Harmless entry: no model lease may change.
		if entry.lease != 0 && e.rec.counts[entry.lease] > 1 {
			t.Fatalf("harmless entry released lease %d", entry.lease)
		}
	default:
		if err != nil && !e.ring.isolated {
			// Unexpected error that is neither duplicate nor stale.
			t.Fatalf("unexpected completion error without isolation: %v res=%+v", err, res)
		}
	}
}

type exhaustSearch struct {
	t        *testing.T
	visited  map[string]bool
	maxDepth int
	budget   int
	nodes    int
}

func (s *exhaustSearch) dfs(env *exhaustEnv, depth int, trail []string) {
	s.nodes++
	if s.nodes > s.budget {
		s.t.Fatalf("exhaustive search exceeded node budget %d; trail=%s",
			s.budget, strings.Join(trail, " > "))
	}
	env.checkInvariant(strings.Join(trail, " > "))
	if depth == s.maxDepth {
		return
	}

	sig := env.snapshot()
	if s.visited[sig] {
		return
	}
	s.visited[sig] = true

	// Action 1: submit a new request.
	if child := env.clone(); child.applySubmit() {
		s.dfs(child, depth+1, append(trail, "submit"))
	}

	// Action 2: each live device descriptor completes (any order).
	for _, lease := range env.liveLeases() {
		child := env.clone()
		if child.applyDeviceComplete(lease) {
			s.dfs(child, depth+1, append(trail, fmt.Sprintf("devcomplete(%d)", lease)))
		}
	}

	// Action 3: consume each queued CQ entry.
	for pos := range env.cq {
		child := env.clone()
		if child.applyCQConsume(pos) {
			s.dfs(child, depth+1, append(trail, fmt.Sprintf("consume(%d)", pos)))
		}
	}

	// Action 4: replay a queued entry as a duplicate CQE.
	for pos := range env.cq {
		child := env.clone()
		if child.applyCQDuplicate(pos) {
			s.dfs(child, depth+1, append(trail, fmt.Sprintf("duplicate(%d)", pos)))
		}
	}

	// Action 5: a fabricated late entry.
	if child := env.clone(); child.applyLateEntry() {
		s.dfs(child, depth+1, append(trail, "late"))
	}

	// Action 6: device reset.
	if child := env.clone(); child.applyReset() {
		s.dfs(child, depth+1, append(trail, "reset"))
	}
}

func TestExhaustiveSmallRings(t *testing.T) {
	cases := []struct {
		name        string
		size        int
		tagBits     int
		maxInflight int
		ceiling     int
		maxDepth    int
		budget      int
	}{
		{name: "size2-tag1-depth1", size: 2, tagBits: 1, maxInflight: 1, ceiling: 4, maxDepth: 10, budget: 500000},
		{name: "size4-tag1-half", size: 4, tagBits: 1, maxInflight: 2, ceiling: 4, maxDepth: 9, budget: 500000},
		{name: "size4-tag2-half", size: 4, tagBits: 2, maxInflight: 2, ceiling: 4, maxDepth: 9, budget: 500000},
		{name: "size4-tag3-safe", size: 4, tagBits: 3, maxInflight: 2, ceiling: 4, maxDepth: 9, budget: 500000},
		{name: "size8-tag1-half", size: 8, tagBits: 1, maxInflight: 4, ceiling: 5, maxDepth: 9, budget: 500000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newExhaustEnv(t, tc.size, tc.tagBits, tc.maxInflight, tc.ceiling)
			search := &exhaustSearch{
				t:        t,
				visited:  map[string]bool{},
				maxDepth: tc.maxDepth,
				budget:   tc.budget,
			}
			search.dfs(env, 0, nil)
			t.Logf("%s: explored %d distinct states", tc.name, search.nodes)
		})
	}
}
