package logrec

import (
	"fmt"
	"sort"
)

// Replica is the leader's view of one follower.
type Replica struct {
	ID        string
	Online    bool
	Removed   bool
	Received  uint64 // highest index received in memory (volatile, never durable)
	Persisted uint64 // highest index durably persisted (the only confirmed position)
}

// SnapshotRequirement records that an explicitly removed replica must
// re-enter through a snapshot, because log entries it missed may already
// have been recycled.
type SnapshotRequirement struct {
	ReplicaID  string
	RemovedAt  uint64 // confirmed index at removal time
	RequireLog string // human-readable re-entry rule
}

// Cluster tracks membership, the monotonic confirmed index, and the
// recyclable log prefix.
type Cluster struct {
	Log       Log
	Confirmed uint64 // monotonic: highest index persisted by a quorum of members

	replicas map[string]*Replica
	order    []string

	// Removals is the durable record of snapshot re-entry requirements.
	Removals []SnapshotRequirement
}

func NewCluster(ids ...string) *Cluster {
	c := &Cluster{replicas: make(map[string]*Replica)}
	for _, id := range ids {
		c.replicas[id] = &Replica{ID: id, Online: true}
		c.order = append(c.order, id)
	}
	return c
}

// IDs returns replica ids in registration order.
func (c *Cluster) IDs() []string { return append([]string(nil), c.order...) }

// Replica returns the leader's view of one replica, or nil.
func (c *Cluster) Replica(id string) *Replica { return c.replicas[id] }

// Members returns the non-removed replicas.
func (c *Cluster) Members() []*Replica {
	var ms []*Replica
	for _, id := range c.order {
		if r := c.replicas[id]; !r.Removed {
			ms = append(ms, r)
		}
	}
	return ms
}

func (c *Cluster) quorum() int { return len(c.Members())/2 + 1 }

// Append appends an entry at the leader and returns its index.
func (c *Cluster) Append(data string) uint64 { return c.Log.Append(data).Index }

// OnPersistedAck records a durable-persistence acknowledgement. Only durable
// positions confirm anything; a replica's volatile Received never does.
// Per-replica positions are applied monotonically (stale acks are ignored).
func (c *Cluster) OnPersistedAck(id string, idx uint64) {
	r, ok := c.replicas[id]
	if !ok || r.Removed {
		return
	}
	if idx > r.Persisted {
		r.Persisted = idx
	}
	c.recompute()
}

// recompute advances Confirmed (monotonic) and recycles the log prefix.
//
// Monotonicity argument: Confirmed only rises because per-replica Persisted
// only rises. The recyclable prefix is min(Confirmed, minPersisted); removal
// only drops members, which cannot lower minPersisted, and rejoin installs a
// snapshot at >= Confirmed, which cannot lower it either. Hence the prefix
// never regresses.
func (c *Cluster) recompute() {
	ms := c.Members()
	if len(ms) == 0 {
		return
	}
	ps := make([]uint64, 0, len(ms))
	for _, r := range ms {
		ps = append(ps, r.Persisted)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i] > ps[j] })
	if cand := ps[c.quorum()-1]; cand > c.Confirmed {
		c.Confirmed = cand
	}
	c.Log.Recycle(c.RecyclablePrefix())
}

// RecyclablePrefix is the highest deletable index: no non-removed replica
// may lose entries it has not yet persisted (a slow replica blocks
// recycling), and entries not yet confirmed by a quorum are kept.
func (c *Cluster) RecyclablePrefix() uint64 {
	ms := c.Members()
	if len(ms) == 0 {
		return c.Confirmed
	}
	minPersisted := ms[0].Persisted
	for _, r := range ms[1:] {
		if r.Persisted < minPersisted {
			minPersisted = r.Persisted
		}
	}
	if c.Confirmed < minPersisted {
		return c.Confirmed
	}
	return minPersisted
}

// RemoveReplica explicitly removes an offline replica from membership and
// records that it must re-enter through a snapshot.
func (c *Cluster) RemoveReplica(id string) error {
	r, ok := c.replicas[id]
	if !ok {
		return fmt.Errorf("unknown replica %q", id)
	}
	if r.Removed {
		return fmt.Errorf("replica %q already removed", id)
	}
	if r.Online {
		return fmt.Errorf("replica %q is online; only an offline replica can be removed", id)
	}
	r.Removed = true
	c.Removals = append(c.Removals, SnapshotRequirement{
		ReplicaID:  id,
		RemovedAt:  c.Confirmed,
		RequireLog: "re-entry requires snapshot install; must not request recycled log entries",
	})
	c.recompute()
	return nil
}

// Rejoin brings a replica back. A removed replica re-enters by installing a
// snapshot at the current confirmed index, so it never requires log entries
// that were already recycled. A never-removed replica is guaranteed to find
// its missing entries still present, because the recyclable prefix never
// exceeded its persisted position. Returns the installed snapshot index
// (0 when no snapshot was needed).
func (c *Cluster) Rejoin(id string) (uint64, error) {
	r, ok := c.replicas[id]
	if !ok {
		return 0, fmt.Errorf("unknown replica %q", id)
	}
	if r.Removed {
		snap := c.Confirmed
		if snap < c.Log.Base() {
			snap = c.Log.Base()
		}
		r.Persisted = snap
		r.Received = snap
		r.Removed = false
		r.Online = true
		c.recompute()
		return snap, nil
	}
	r.Online = true
	if r.Persisted < c.Log.Base() {
		return 0, fmt.Errorf("replica %q requires deleted log entries up to %d", id, c.Log.Base())
	}
	return 0, nil
}
