package hashring

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// MigrationPlan is the precomputed impact analysis of the pending standby
// topology, produced during Prewarm before the flip is committed.
type MigrationPlan struct {
	FromVersion uint64
	ToVersion   uint64

	AddedNodes   []string
	RemovedNodes []string

	// HashSpaceMoved is the exact fraction of the hash space [0,2^64)
	// whose primary owner changes between the two rings.
	HashSpaceMoved float64

	// Sampled/SampleMoved cross-check HashSpaceMoved with real keys.
	Sampled     int
	SampleMoved int

	// PerNode lists how much hash space each node gains or loses.
	PerNode map[string]NodeDelta

	ComputedAt time.Time
}

// SampleMoveFraction is the moved fraction measured on sampled keys.
func (p *MigrationPlan) SampleMoveFraction() float64 {
	if p.Sampled == 0 {
		return 0
	}
	return float64(p.SampleMoved) / float64(p.Sampled)
}

// NodeDelta is the per-node hash-space change between two rings.
type NodeDelta struct {
	Gained float64
	Lost   float64
}

// Controller is a dual-ring hot-standby routing controller.
//
//   - Active ring: serves 100% of traffic via lock-free atomic reads.
//   - Standby ring: absorbs topology changes in the background and
//     precomputes the key-migration impact (Prewarm).
//   - Flip: a single atomic pointer store switches all traffic to the
//     warmed standby ring in sub-millisecond time. Readers never observe
//     a torn or partially-built ring because rings are immutable.
type Controller struct {
	active atomic.Pointer[Ring] // serving ring, read on every Lookup

	mu      sync.Mutex    // serializes standby staging/prewarm/flip
	standby *Builder      // staged topology changes
	pending *Ring         // warmed standby ring, ready to flip
	plan    *MigrationPlan
	version uint64
	repl    int
}

// NewController builds the initial active ring (version 1) from the builder.
func NewController(b *Builder) (*Controller, error) {
	if b.Len() == 0 {
		return nil, errors.New("hashring: controller requires at least one node")
	}
	c := &Controller{repl: b.replicas, version: 1}
	c.active.Store(b.Build(1))
	return c, nil
}

// Active returns the currently serving ring.
func (c *Controller) Active() *Ring { return c.active.Load() }

// Version returns the version of the currently serving ring.
func (c *Controller) Version() uint64 { return c.active.Load().Version() }

// Lookup routes a key on the active ring. Lock-free.
func (c *Controller) Lookup(key string) (Node, bool) {
	return c.active.Load().Lookup(key)
}

// LookupN routes a key to n distinct, topology-spread nodes on the active
// ring. Lock-free.
func (c *Controller) LookupN(key string, n int) []Node {
	return c.active.Load().LookupN(key, n)
}

// ensureStandby lazily clones the active topology into the standby builder.
// Must be called with c.mu held.
func (c *Controller) ensureStandby() {
	if c.standby == nil {
		c.standby = FromRing(c.active.Load(), c.repl)
	}
}

// SyncAdd stages a node addition/scale-out on the standby ring.
func (c *Controller) SyncAdd(n Node) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureStandby()
	return c.standby.Add(n)
}

// SyncRemove stages a node removal/scale-in on the standby ring.
func (c *Controller) SyncRemove(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureStandby()
	if !c.standby.Has(id) {
		return fmt.Errorf("hashring: node %q not present in standby topology", id)
	}
	c.standby.Remove(id)
	return nil
}

// Prewarm freezes the staged standby topology into an immutable ring and
// precomputes the full key-migration impact versus the active ring.
// samples keys (deterministically generated) are used to cross-check the
// exact hash-space math. After Prewarm returns, Flip can be executed.
func (c *Controller) Prewarm(samples int) (*MigrationPlan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.standby == nil {
		return nil, errors.New("hashring: no staged standby changes; call SyncAdd/SyncRemove first")
	}
	if c.standby.Len() == 0 {
		return nil, errors.New("hashring: standby topology is empty")
	}
	oldRing := c.active.Load()
	newRing := c.standby.Build(oldRing.Version() + 1)
	plan := computeMigrationPlan(oldRing, newRing, samples)
	c.pending = newRing
	c.plan = plan
	return plan, nil
}

// Plan returns the last precomputed migration plan, or nil.
func (c *Controller) Plan() *MigrationPlan {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plan
}

// PendingVersion reports whether a warmed standby ring is awaiting flip,
// and if so its version.
func (c *Controller) PendingVersion() (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		return 0, false
	}
	return c.pending.Version(), true
}

// Flip atomically switches the serving pointer to the warmed standby ring.
// It is a single atomic store: concurrent readers see either the complete
// old ring or the complete new ring, never anything in between.
// Returns the precomputed migration plan for the switch.
func (c *Controller) Flip() (*MigrationPlan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		return nil, errors.New("hashring: no warmed standby ring; call Prewarm first")
	}
	c.active.Store(c.pending)
	c.version = c.pending.Version()
	c.pending = nil
	c.standby = nil
	plan := c.plan
	c.plan = nil
	return plan, nil
}

// computeMigrationPlan diffs two rings exactly (per hash-space segment) and
// verifies the result with sampled keys.
func computeMigrationPlan(oldRing, newRing *Ring, samples int) *MigrationPlan {
	plan := &MigrationPlan{
		FromVersion: oldRing.Version(),
		ToVersion:   newRing.Version(),
		PerNode:     make(map[string]NodeDelta),
		ComputedAt:  time.Now(),
	}
	for id := range newRing.nodes {
		if !oldRing.Has(id) {
			plan.AddedNodes = append(plan.AddedNodes, id)
		}
	}
	for id := range oldRing.nodes {
		if !newRing.Has(id) {
			plan.RemovedNodes = append(plan.RemovedNodes, id)
		}
	}
	sort.Strings(plan.AddedNodes)
	sort.Strings(plan.RemovedNodes)

	// Exact pass: sweep the union of both rings' vnode boundaries; between
	// consecutive boundaries both rings have constant owners.
	moved, gained, lost := exactMovedFraction(oldRing, newRing)
	plan.HashSpaceMoved = moved
	for id, d := range gained {
		cur := plan.PerNode[id]
		cur.Gained = d
		plan.PerNode[id] = cur
	}
	for id, d := range lost {
		cur := plan.PerNode[id]
		cur.Lost = d
		plan.PerNode[id] = cur
	}

	// Sampled cross-check with deterministic keys.
	if samples > 0 {
		plan.Sampled = samples
		for i := 0; i < samples; i++ {
			key := "prewarm-sample-" + fmt.Sprintf("%08d", i)
			o, _ := oldRing.Lookup(key)
			n, ok := newRing.Lookup(key)
			if !ok || o.ID != n.ID {
				plan.SampleMoved++
			}
		}
	}
	return plan
}

// exactMovedFraction computes the exact fraction of the hash space whose
// primary owner differs between two rings, by sweeping the union of both
// rings' vnode boundaries. Also returns per-node gained/lost fractions.
func exactMovedFraction(oldRing, newRing *Ring) (moved float64, gained, lost map[string]float64) {
	gained = make(map[string]float64)
	lost = make(map[string]float64)
	if len(newRing.points) == 0 {
		return 0, gained, lost
	}
	type boundary struct {
		hash uint64
		ring int // 0 = old, 1 = new
	}
	union := make([]boundary, 0, len(oldRing.points)+len(newRing.points))
	for _, p := range oldRing.points {
		union = append(union, boundary{p.hash, 0})
	}
	for _, p := range newRing.points {
		union = append(union, boundary{p.hash, 1})
	}
	sort.Slice(union, func(i, j int) bool {
		if union[i].hash == union[j].hash {
			return union[i].ring < union[j].ring
		}
		return union[i].hash < union[j].hash
	})
	// Between consecutive union boundaries, both rings have constant owners.
	var movedF float64
	prev := union[len(union)-1].hash // wrap-around segment starts at last boundary
	for _, b := range union {
		segLen := b.hash - prev // uint64 wrap handles the wrap-around segment
		if segLen > 0 {
			// probe at prev (any point strictly inside (prev, b.hash])
			oNode := ownerAt(oldRing, prev)
			nNode := ownerAt(newRing, prev)
			if oNode != nNode {
				movedF += float64(segLen)
				gained[nNode] += float64(segLen)
				lost[oNode] += float64(segLen)
			}
		}
		prev = b.hash
	}
	const full = 1.0 / (float64(^uint64(0)) + 1.0) // 2^-64
	moved = movedF * full
	for id := range gained {
		gained[id] *= full
	}
	for id := range lost {
		lost[id] *= full
	}
	return moved, gained, lost
}

// ownerAt returns the node owning the arc immediately AFTER h, i.e. the
// owner of any key whose hash is in (h, nextBoundary].
func ownerAt(r *Ring, h uint64) string {
	if len(r.points) == 0 {
		return ""
	}
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i].hash > h })
	if i == len(r.points) {
		i = 0
	}
	return r.points[i].node
}
