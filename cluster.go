package main

import (
	"fmt"
	"math/rand"
	"sort"
)

// ackRecord is a subscriber's confirmed floor for one source, tagged with
// the subscriber's membership epoch. Acks from a stale epoch (recorded
// before the member left and rejoined) must never be reused.
type ackRecord struct {
	epoch uint64
	floor uint64
}

// Cluster simulates the reliable broadcast group: membership with
// leave/rejoin epochs, lossy message delivery, repair/catch-up, and
// ack-gated compression of dedup history.
type Cluster struct {
	nodes   map[NodeID]*Node
	members map[NodeID]bool // current valid subscribers
	nextSeq map[NodeID]uint64
	acks    map[NodeID]map[NodeID]ackRecord // acks[member][source]
	floors  map[NodeID]uint64               // compressed horizon per source

	rng      *rand.Rand
	dropRate float64
	lost     int
}

func NewCluster(seed int64, dropRate float64) *Cluster {
	return &Cluster{
		nodes:    make(map[NodeID]*Node),
		members:  make(map[NodeID]bool),
		nextSeq:  make(map[NodeID]uint64),
		acks:     make(map[NodeID]map[NodeID]ackRecord),
		floors:   make(map[NodeID]uint64),
		rng:      rand.New(rand.NewSource(seed)),
		dropRate: dropRate,
	}
}

// Join adds a new node at epoch 1.
func (c *Cluster) Join(id NodeID, maxGaps int) *Node {
	n := NewNode(id, maxGaps)
	n.epoch = 1
	c.nodes[id] = n
	c.members[id] = true
	c.acks[id] = make(map[NodeID]ackRecord)
	return n
}

// Leave takes a member offline. It stops being a valid subscriber, so its
// acks no longer gate compression; its dedup state is retained locally
// (it is merely offline, not reset).
func (c *Cluster) Leave(id NodeID) {
	n := c.nodes[id]
	n.online = false
	delete(c.members, id)
	delete(c.acks, id)
}

// Rejoin brings a departed node back with a bumped membership epoch.
// Acks recorded under the previous epoch are discarded and any replay of
// them is rejected by RecordAck.
func (c *Cluster) Rejoin(id NodeID) *Node {
	n := c.nodes[id]
	n.epoch++
	n.online = true
	c.members[id] = true
	c.acks[id] = make(map[NodeID]ackRecord)
	return n
}

// Broadcast assigns the next per-source sequence number and delivers the
// message to all online members. Delivery to members other than the
// source is lossy; offline members miss the message entirely.
func (c *Cluster) Broadcast(source NodeID, body string) (Message, error) {
	n, ok := c.nodes[source]
	if !ok || !c.members[source] || !n.online {
		return Message{}, fmt.Errorf("cluster: %s is not an online member", source)
	}
	c.nextSeq[source]++
	m := Message{Source: source, Seq: c.nextSeq[source], Body: body}
	for _, id := range c.sortedMembers() {
		if id == source {
			if _, err := c.nodes[id].Receive(m); err != nil {
				return m, err
			}
			continue
		}
		if c.rng.Float64() < c.dropRate {
			c.lost++
			continue
		}
		if _, err := c.nodes[id].Receive(m); err != nil {
			return m, err
		}
	}
	return m, nil
}

// RecordAck stores a subscriber's confirmed floor for a source. Acks from
// a non-member or carrying a stale membership epoch (e.g. replayed from
// before a leave/rejoin) are rejected.
func (c *Cluster) RecordAck(member NodeID, epoch uint64, source NodeID, floor uint64) error {
	n, ok := c.nodes[member]
	if !ok || !c.members[member] {
		return fmt.Errorf("cluster: ack from %s rejected: not a valid subscriber", member)
	}
	if epoch != n.epoch {
		return fmt.Errorf("cluster: ack from %s rejected: stale epoch %d (current %d)", member, epoch, n.epoch)
	}
	recs := c.acks[member]
	if cur, ok := recs[source]; !ok || floor > cur.floor {
		recs[source] = ackRecord{epoch: epoch, floor: floor}
	}
	return nil
}

// ReportAcks makes every online member report its current per-source
// watermark, tagged with its current membership epoch.
func (c *Cluster) ReportAcks() {
	for _, id := range c.sortedMembers() {
		n := c.nodes[id]
		for source := range c.nextSeq {
			_ = c.RecordAck(id, n.epoch, source, n.AckFloor(source))
		}
	}
}

// CompressionFloor returns the highest sequence number confirmed by every
// valid subscriber in their current epoch, and whether compression is
// possible at all. A single valid subscriber without a current-epoch ack
// blocks compression.
func (c *Cluster) CompressionFloor(source NodeID) (uint64, bool) {
	if len(c.members) == 0 {
		return 0, false
	}
	floor := ^uint64(0)
	for id := range c.members {
		rec, ok := c.acks[id][source]
		if !ok || rec.epoch != c.nodes[id].epoch {
			return 0, false
		}
		if rec.floor < floor {
			floor = rec.floor
		}
	}
	return floor, true
}

// MaybeCompress advances the compression horizon for a source to the
// highest floor confirmed by all valid subscribers, then brings every
// online member's dedup window up to the established horizon (capped at
// the member's own watermark, so a member that was offline when the
// horizon advanced catches up safely once it has the data). It returns
// the current horizon and whether it advanced.
func (c *Cluster) MaybeCompress(source NodeID) (uint64, bool) {
	advanced := false
	if floor, ok := c.CompressionFloor(source); ok && floor > c.floors[source] {
		c.floors[source] = floor
		advanced = true
	}
	horizon := c.floors[source]
	for id := range c.members {
		n := c.nodes[id]
		local := horizon
		if ack := n.AckFloor(source); ack < local {
			local = ack
		}
		if err := n.Window(source).Compress(local); err != nil {
			// Invariant: local <= the member's own watermark. A
			// violation means the ack gating is broken.
			panic(fmt.Sprintf("cluster: unsafe compress for %s: %v", id, err))
		}
	}
	return horizon, advanced
}

// Floor returns the current compression horizon for a source.
func (c *Cluster) Floor(source NodeID) uint64 { return c.floors[source] }

// Lost returns how many deliveries were dropped by the lossy link.
func (c *Cluster) Lost() int { return c.lost }

// MaxSeq returns the highest sequence number assigned by a source.
func (c *Cluster) MaxSeq(source NodeID) uint64 { return c.nextSeq[source] }

// RepairRound lets every online member pull the messages it is missing
// (per source, everything above its watermark up to the source's max)
// from any online peer that holds them. It returns how many messages were
// newly accepted.
func (c *Cluster) RepairRound() int {
	healed := 0
	for _, id := range c.sortedMembers() {
		got, _ := c.catchUp(id)
		healed += got
	}
	return healed
}

// CatchUp backfills a single (typically rejoined) node from its online
// peers. It requests the full range above the node's watermark; entries
// the node already holds in its sparse gap table are recognized as
// duplicates by its dedup window. It returns newly accepted and
// duplicate counts.
func (c *Cluster) CatchUp(id NodeID) (accepted, duplicates int) {
	return c.catchUp(id)
}

func (c *Cluster) catchUp(id NodeID) (accepted, duplicates int) {
	n := c.nodes[id]
	if !n.online {
		return 0, 0
	}
	for _, source := range c.sortedSources() {
		maxSeq := c.nextSeq[source]
		w := n.Window(source)
		for seq := w.Watermark() + 1; seq <= maxSeq; seq++ {
			m, ok := c.locate(source, seq, id)
			if !ok {
				continue // no online peer holds it yet; stays a gap
			}
			if w.Seen(seq) {
				duplicates++
				continue
			}
			if _, err := n.Receive(m); err != nil {
				continue
			}
			accepted++
		}
	}
	return accepted, duplicates
}

// locate finds an online peer (other than exclude) that holds the message.
func (c *Cluster) locate(source NodeID, seq uint64, exclude NodeID) (Message, bool) {
	for _, id := range c.sortedMembers() {
		if id == exclude {
			continue
		}
		if m, ok := c.nodes[id].MessageAt(source, seq); ok {
			return m, true
		}
	}
	return Message{}, false
}

func (c *Cluster) sortedMembers() []NodeID {
	out := make([]NodeID, 0, len(c.members))
	for id := range c.members {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (c *Cluster) sortedSources() []NodeID {
	out := make([]NodeID, 0, len(c.nextSeq))
	for id := range c.nextSeq {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Node exposes a node for inspection by the simulation driver.
func (c *Cluster) Node(id NodeID) *Node { return c.nodes[id] }

// Members returns the sorted list of valid subscribers.
func (c *Cluster) Members() []NodeID { return c.sortedMembers() }
