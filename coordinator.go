package stats

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ErrIncomplete is returned by Query when at least one tracked partition has
// not yet published up to the requested barrier. The coordinator never
// answers from a mix of old and new partition states.
var ErrIncomplete = errors.New("stats: barrier cut incomplete")

// ErrEmpty is returned by Query when the consistent cut contains no samples.
var ErrEmpty = errors.New("stats: empty set")

// Op is a single add or remove of weight occurrences of an integer value.
type Op struct {
	Value  int64  `json:"value"`
	Weight uint64 `json:"weight"`
	Remove bool   `json:"remove"`
}

// Delta is a sequenced batch of operations published by one partition.
// Barrier != 0 marks the delta that completes that barrier for the
// partition; barrier IDs must increase with Seq within a partition.
type Delta struct {
	Seq     uint64 `json:"seq"`
	Ops     []Op   `json:"ops"`
	Barrier uint64 `json:"barrier,omitempty"`
}

// undoEntry is one applied delta kept for rolling the partition state back
// to earlier barrier cuts.
type undoEntry struct {
	Ops     []Op   `json:"ops"`
	Barrier uint64 `json:"barrier,omitempty"`
}

type partition struct {
	counts   map[int64]uint64
	total    uint64
	nextSeq  uint64
	pending  map[uint64]Delta // out-of-order buffer keyed by Seq
	undo     []undoEntry
	complete uint64 // highest completed barrier
}

func newPartition() *partition {
	return &partition{counts: make(map[int64]uint64), pending: make(map[uint64]Delta)}
}

// Coordinator merges sequenced deltas from multiple disconnectable
// partitions and serves quantile queries only at complete barrier cuts.
//
// Deltas may arrive out of order and duplicated; per-partition sequence
// numbers deduplicate and reorder them. A partition that disconnects simply
// stops publishing: queries past its last barrier report ErrIncomplete
// instead of mixing stale and fresh state.
type Coordinator struct {
	parts map[string]*partition
}

// NewCoordinator returns a coordinator with no tracked partitions.
func NewCoordinator() *Coordinator {
	return &Coordinator{parts: make(map[string]*partition)}
}

// Track registers a partition before its first delta so that queries block
// until it publishes the requested barrier.
func (c *Coordinator) Track(id string) {
	if _, ok := c.parts[id]; !ok {
		c.parts[id] = newPartition()
	}
}

// Untrack removes a partition and its state, allowing queries to complete
// without it.
func (c *Coordinator) Untrack(id string) {
	delete(c.parts, id)
}

// Apply ingests one delta from a partition. Duplicate deltas (already
// applied or already buffered) are dropped; out-of-order deltas are
// buffered until the gap closes. An error is returned if a remove
// underflows; the coordinator state is left unchanged in that case.
func (c *Coordinator) Apply(id string, d Delta) error {
	p, ok := c.parts[id]
	if !ok {
		p = newPartition()
		c.parts[id] = p
	}
	if d.Seq < p.nextSeq {
		return nil // already applied
	}
	if _, dup := p.pending[d.Seq]; dup {
		return nil // already buffered
	}
	p.pending[d.Seq] = d
	for {
		next, ok := p.pending[p.nextSeq]
		if !ok {
			return nil
		}
		delete(p.pending, p.nextSeq)
		if err := p.apply(next); err != nil {
			return err
		}
		p.nextSeq++
	}
}

func (p *partition) apply(d Delta) error {
	applied := 0
	for _, op := range d.Ops {
		if err := applyOp(p.counts, &p.total, op); err != nil {
			for i := applied - 1; i >= 0; i-- {
				_ = applyOp(p.counts, &p.total, invert(d.Ops[i]))
			}
			return err
		}
		applied++
	}
	p.undo = append(p.undo, undoEntry{Ops: d.Ops, Barrier: d.Barrier})
	if d.Barrier > p.complete {
		p.complete = d.Barrier
	}
	return nil
}

func applyOp(counts map[int64]uint64, total *uint64, op Op) error {
	if op.Weight == 0 {
		return nil
	}
	if op.Remove {
		if counts[op.Value] < op.Weight {
			return &UnderflowError{Value: op.Value, Have: counts[op.Value], Want: op.Weight}
		}
		counts[op.Value] -= op.Weight
		*total -= op.Weight
		if counts[op.Value] == 0 {
			delete(counts, op.Value)
		}
		return nil
	}
	counts[op.Value] += op.Weight
	*total += op.Weight
	return nil
}

func invert(op Op) Op { return Op{Value: op.Value, Weight: op.Weight, Remove: !op.Remove} }

// Complete reports whether every tracked partition has completed the
// barrier, i.e. whether Query can answer for it.
func (c *Coordinator) Complete(barrier uint64) bool {
	for _, p := range c.parts {
		if p.complete < barrier {
			return false
		}
	}
	return true
}

// Query returns the weighted quantile over the merged state of all tracked
// partitions as of the given barrier cut. It returns ErrIncomplete if any
// partition has not completed the barrier, and ErrEmpty if the cut holds no
// samples.
func (c *Coordinator) Query(barrier uint64, q float64) (int64, error) {
	agg := NewWeightedQuantile()
	for id, p := range c.parts {
		if p.complete < barrier {
			return 0, fmt.Errorf("%w: partition %q completed barrier %d, want %d",
				ErrIncomplete, id, p.complete, barrier)
		}
		for v, w := range p.stateAt(barrier) {
			agg.Add(v, w)
		}
	}
	value, ok := agg.Quantile(q)
	if !ok {
		return 0, ErrEmpty
	}
	return value, nil
}

// stateAt reconstructs the partition counts as of the barrier cut by
// undoing every delta applied after the delta that completed the barrier.
func (p *partition) stateAt(barrier uint64) map[int64]uint64 {
	cut := -1
	for i := len(p.undo) - 1; i >= 0; i-- {
		if b := p.undo[i].Barrier; b != 0 && b <= barrier {
			cut = i
			break
		}
	}
	counts := make(map[int64]uint64, len(p.counts))
	for v, w := range p.counts {
		counts[v] = w
	}
	var total uint64
	for i := len(p.undo) - 1; i > cut; i-- {
		ops := p.undo[i].Ops
		for j := len(ops) - 1; j >= 0; j-- {
			_ = applyOp(counts, &total, invert(ops[j]))
		}
	}
	return counts
}

// snapshot is the JSON-serializable state of the coordinator. It preserves
// the dedup high-water mark (NextSeq), the undo log used to answer older
// barrier cuts, and the out-of-order buffer.
type snapshot struct {
	Parts map[string]partSnapshot `json:"parts"`
}

type partSnapshot struct {
	NextSeq  uint64           `json:"next_seq"`
	Complete uint64           `json:"complete"`
	Counts   map[int64]uint64 `json:"counts"`
	Undo     []undoEntry      `json:"undo"`
	Pending  []Delta          `json:"pending,omitempty"`
}

// Snapshot serializes the full coordinator state, including dedup and undo
// state, so a restored coordinator continues an in-flight stream safely.
func (c *Coordinator) Snapshot() ([]byte, error) {
	snap := snapshot{Parts: make(map[string]partSnapshot, len(c.parts))}
	for id, p := range c.parts {
		pending := make([]Delta, 0, len(p.pending))
		for _, d := range p.pending {
			pending = append(pending, d)
		}
		sort.Slice(pending, func(i, j int) bool { return pending[i].Seq < pending[j].Seq })
		counts := make(map[int64]uint64, len(p.counts))
		for v, w := range p.counts {
			counts[v] = w
		}
		snap.Parts[id] = partSnapshot{
			NextSeq:  p.nextSeq,
			Complete: p.complete,
			Counts:   counts,
			Undo:     append([]undoEntry(nil), p.undo...),
			Pending:  pending,
		}
	}
	return json.Marshal(snap)
}

// Restore rebuilds a coordinator from Snapshot output.
func Restore(data []byte) (*Coordinator, error) {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("stats: restore: %w", err)
	}
	c := NewCoordinator()
	for id, ps := range snap.Parts {
		p := newPartition()
		p.nextSeq = ps.NextSeq
		p.complete = ps.Complete
		p.undo = append([]undoEntry(nil), ps.Undo...)
		for v, w := range ps.Counts {
			p.counts[v] = w
			p.total += w
		}
		for _, d := range ps.Pending {
			p.pending[d.Seq] = d
		}
		c.parts[id] = p
	}
	return c, nil
}
