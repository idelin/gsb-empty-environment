package wquantile

import (
	"bytes"
	"encoding/gob"
	"errors"
	"sort"
)

// ErrUnknownPartition is returned when publishing to an unregistered partition.
var ErrUnknownPartition = errors.New("wquantile: unknown partition")

// ErrDuplicatePartition is returned when registering an existing partition.
var ErrDuplicatePartition = errors.New("wquantile: duplicate partition")

// ErrPartitionDown is returned when publishing to a disconnected partition.
var ErrPartitionDown = errors.New("wquantile: partition is disconnected")

// ErrNoCompleteCut is returned when no barrier cut has completed yet.
var ErrNoCompleteCut = errors.New("wquantile: no complete barrier cut")

// ErrIncompleteCut is returned when the requested barrier cut has not been
// published by every partition.
var ErrIncompleteCut = errors.New("wquantile: barrier cut is not complete")

// Delta is a sequenced add/remove of a sample published by a partition.
// Weight > 0 adds, Weight < 0 removes. Seq is a per-partition sequence
// number shared with barriers; every sequence number is used exactly once.
type Delta struct {
	Seq    uint64
	Value  int64
	Weight int64
}

// Barrier marks a cut point in a partition's stream. A cut for barrier ID
// b is complete once every registered partition has published barrier b;
// the consistent state at the cut contains exactly the deltas with
// sequence numbers <= each partition's own barrier sequence.
type Barrier struct {
	Seq uint64
	ID  uint64
}

// streamItem is one slot of a partition's sequence space.
type streamItem struct {
	Seq       uint64
	IsBarrier bool
	Value     int64  // delta value
	Weight    int64  // delta weight
	Barrier   uint64 // barrier ID
}

type partition struct {
	connected bool
	nextSeq   uint64                // next expected sequence number
	pending   map[uint64]streamItem // out-of-order buffer, dedup by seq
	staged    []streamItem          // contiguous deltas, seq ascending, uncommitted
	barriers  map[uint64]uint64     // barrier ID -> sequence number (staged)
}

// Coordinator merges weighted samples from multiple disconnectable
// partitions. Deltas are only applied to the queryable state when a
// barrier cut completes on every registered partition, so queries never
// observe a mix of old and new partition data.
type Coordinator struct {
	committed *Multiset
	parts     map[string]*partition
	order     []string // deterministic partition order
	latestCut uint64
	hasCut    bool
}

// NewCoordinator returns an empty Coordinator.
func NewCoordinator() *Coordinator {
	return &Coordinator{
		committed: NewMultiset(),
		parts:     make(map[string]*partition),
	}
}

// AddPartition registers a partition. Its stream is expected to start at
// sequence number 0.
func (c *Coordinator) AddPartition(id string) error {
	if _, ok := c.parts[id]; ok {
		return ErrDuplicatePartition
	}
	c.parts[id] = &partition{
		connected: true,
		pending:   make(map[uint64]streamItem),
		barriers:  make(map[uint64]uint64),
	}
	c.order = append(c.order, id)
	sort.Strings(c.order)
	return nil
}

// Disconnect marks a partition as down. It stays registered, so barrier
// cuts cannot complete without it and queries for newer cuts return
// ErrIncompleteCut instead of mixing stale and fresh data.
func (c *Coordinator) Disconnect(id string) error {
	p, ok := c.parts[id]
	if !ok {
		return ErrUnknownPartition
	}
	p.connected = false
	return nil
}

// Reconnect marks a partition as up again. Its dedup and staging state is
// preserved, so the stream resumes where it left off.
func (c *Coordinator) Reconnect(id string) error {
	p, ok := c.parts[id]
	if !ok {
		return ErrUnknownPartition
	}
	p.connected = true
	return nil
}

// PublishDelta ingests one delta. Out-of-order deltas are buffered and
// duplicates (by sequence number) are ignored.
func (c *Coordinator) PublishDelta(id string, d Delta) error {
	return c.publish(id, streamItem{Seq: d.Seq, Value: d.Value, Weight: d.Weight})
}

// PublishBarrier ingests one barrier. Out-of-order barriers are buffered
// and duplicates are ignored. Completing a cut commits staged deltas.
func (c *Coordinator) PublishBarrier(id string, b Barrier) error {
	return c.publish(id, streamItem{Seq: b.Seq, IsBarrier: true, Barrier: b.ID})
}

func (c *Coordinator) publish(id string, it streamItem) error {
	p, ok := c.parts[id]
	if !ok {
		return ErrUnknownPartition
	}
	if !p.connected {
		return ErrPartitionDown
	}
	if it.Seq < p.nextSeq {
		return nil // already consumed: duplicate
	}
	if _, dup := p.pending[it.Seq]; dup {
		return nil
	}
	p.pending[it.Seq] = it
	// Drain the contiguous prefix into staging.
	for {
		next, ok := p.pending[p.nextSeq]
		if !ok {
			break
		}
		delete(p.pending, p.nextSeq)
		p.nextSeq++
		if next.IsBarrier {
			if _, seen := p.barriers[next.Barrier]; !seen {
				p.barriers[next.Barrier] = next.Seq
			}
		} else {
			p.staged = append(p.staged, next)
		}
	}
	return c.tryCommit()
}

// tryCommit commits every barrier cut that all partitions have reached,
// smallest barrier ID first.
func (c *Coordinator) tryCommit() error {
	for {
		cut, ok := c.smallestCommonBarrier()
		if !ok {
			return nil
		}
		if err := c.commitCut(cut); err != nil {
			return err
		}
	}
}

func (c *Coordinator) smallestCommonBarrier() (uint64, bool) {
	if len(c.order) == 0 {
		return 0, false
	}
	best := uint64(0)
	found := false
	for _, id := range c.order {
		p := c.parts[id]
		// Smallest barrier of this partition above the latest cut.
		var minB uint64
		has := false
		for b := range p.barriers {
			if !c.hasCut || b > c.latestCut {
				if !has || b < minB {
					minB, has = b, true
				}
			}
		}
		if !has {
			return 0, false
		}
		if !found || minB > best {
			best, found = minB, true
		}
	}
	// The cut is the largest "minimum" only if every partition has it.
	for _, id := range c.order {
		if _, ok := c.parts[id].barriers[best]; !ok {
			return 0, false
		}
	}
	return best, true
}

func (c *Coordinator) commitCut(cut uint64) error {
	for _, id := range c.order {
		p := c.parts[id]
		seq := p.barriers[cut]
		// Apply staged deltas with Seq <= seq, in order.
		used := 0
		for used < len(p.staged) && p.staged[used].Seq <= seq {
			d := p.staged[used]
			if d.Weight >= 0 {
				c.committed.Add(d.Value, uint64(d.Weight))
			} else {
				if err := c.committed.Remove(d.Value, uint64(-d.Weight)); err != nil {
					return err
				}
			}
			used++
		}
		p.staged = append([]streamItem(nil), p.staged[used:]...)
		// Drop this and any older barriers.
		for b := range p.barriers {
			if b <= cut {
				delete(p.barriers, b)
			}
		}
	}
	c.latestCut = cut
	c.hasCut = true
	return nil
}

// LatestCut returns the most recently completed barrier cut.
func (c *Coordinator) LatestCut() (uint64, bool) {
	return c.latestCut, c.hasCut
}

// CutComplete reports whether the given barrier cut has completed.
func (c *Coordinator) CutComplete(cut uint64) bool {
	return c.hasCut && cut <= c.latestCut
}

// Quantile evaluates the quantile at the most recent completed cut.
func (c *Coordinator) Quantile(q float64) (int64, error) {
	if !c.hasCut {
		return 0, ErrNoCompleteCut
	}
	return c.committed.Quantile(q)
}

// QuantileAt evaluates the quantile for a specific barrier cut. It
// returns ErrIncompleteCut if the cut has not completed on every
// partition, rather than mixing old and new data. For cuts older than
// the latest, the state of the most recent completed cut is returned.
func (c *Coordinator) QuantileAt(cut uint64, q float64) (int64, error) {
	if !c.hasCut || cut > c.latestCut {
		return 0, ErrIncompleteCut
	}
	return c.committed.Quantile(q)
}

// Total returns the total weight at the most recent completed cut.
func (c *Coordinator) Total() uint64 {
	return c.committed.Total()
}

// --- Snapshot / restore ---

type partitionDisk struct {
	ID        string
	Connected bool
	NextSeq   uint64
	Pending   []streamItem
	Staged    []streamItem
	Barriers  map[uint64]uint64
}

type snapshotDisk struct {
	Entries   []Entry
	LatestCut uint64
	HasCut    bool
	Parts     []partitionDisk
}

// Snapshot serializes the full coordinator state, including per-partition
// dedup (seen sequence numbers) and uncommitted staged deltas, so a
// restored coordinator continues exactly where it left off.
func (c *Coordinator) Snapshot() ([]byte, error) {
	disk := snapshotDisk{
		Entries:   c.committed.Entries(),
		LatestCut: c.latestCut,
		HasCut:    c.hasCut,
	}
	for _, id := range c.order {
		p := c.parts[id]
		pd := partitionDisk{
			ID:        id,
			Connected: p.connected,
			NextSeq:   p.nextSeq,
			Staged:    append([]streamItem(nil), p.staged...),
			Barriers:  make(map[uint64]uint64, len(p.barriers)),
		}
		for _, it := range p.pending {
			pd.Pending = append(pd.Pending, it)
		}
		for b, s := range p.barriers {
			pd.Barriers[b] = s
		}
		disk.Parts = append(disk.Parts, pd)
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(disk); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RestoreCoordinator rebuilds a coordinator from Snapshot output.
func RestoreCoordinator(data []byte) (*Coordinator, error) {
	var disk snapshotDisk
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&disk); err != nil {
		return nil, err
	}
	c := NewCoordinator()
	c.committed.LoadEntries(disk.Entries)
	c.latestCut = disk.LatestCut
	c.hasCut = disk.HasCut
	for _, pd := range disk.Parts {
		p := &partition{
			connected: pd.Connected,
			nextSeq:   pd.NextSeq,
			pending:   make(map[uint64]streamItem, len(pd.Pending)),
			staged:    append([]streamItem(nil), pd.Staged...),
			barriers:  make(map[uint64]uint64, len(pd.Barriers)),
		}
		for _, it := range pd.Pending {
			p.pending[it.Seq] = it
		}
		for b, s := range pd.Barriers {
			p.barriers[b] = s
		}
		c.parts[pd.ID] = p
		c.order = append(c.order, pd.ID)
	}
	sort.Strings(c.order)
	return c, nil
}
