package logrec

import (
	"container/heap"
	"fmt"
)

// Sim is a single-process discrete-event simulator. Message delivery, disk
// persistence, crashes, removals, rejoins and virtual time are all
// controlled here; nothing happens concurrently or outside the event queue.
type Sim struct {
	Now int // virtual time

	c   *Cluster
	seq int
	ev  evHeap

	lastConfirmed uint64
	lastBase      uint64

	// Violations collects safety-invariant violations (empty means the
	// safety evidence holds for the whole run).
	Violations []string
	// Snapshots records every snapshot install performed at rejoin.
	Snapshots []SnapshotInstall
}

// SnapshotInstall is evidence that a removed replica re-entered via snapshot.
type SnapshotInstall struct {
	ReplicaID string
	Index     uint64
	At        int
}

func NewSim(c *Cluster) *Sim { return &Sim{c: c} }

// Cluster returns the simulated cluster.
func (s *Sim) Cluster() *Cluster { return s.c }

// After schedules f to run at Now+delay (negative delays clamp to 0).
func (s *Sim) After(delay int, name string, f func()) {
	if delay < 0 {
		delay = 0
	}
	heap.Push(&s.ev, event{at: s.Now + delay, seq: s.seq, name: name, run: f})
	s.seq++
}

// Run processes events in (time, sequence) order until the queue is empty,
// checking the safety invariants after every single event.
func (s *Sim) Run() {
	for s.ev.Len() > 0 {
		e := heap.Pop(&s.ev).(event)
		s.Now = e.at
		e.run()
		s.check(e.name)
	}
}

// check verifies the safety invariants after an event:
//  1. the confirmed index never regresses;
//  2. the recycled prefix never regresses;
//  3. the recycled prefix never exceeds any non-removed replica's persisted
//     position (no live replica can lose entries it still needs).
func (s *Sim) check(ctx string) {
	c := s.c
	if c.Confirmed < s.lastConfirmed {
		s.Violations = append(s.Violations, fmt.Sprintf("t=%d %s: confirmed regressed %d -> %d", s.Now, ctx, s.lastConfirmed, c.Confirmed))
	}
	s.lastConfirmed = c.Confirmed
	if c.Log.Base() < s.lastBase {
		s.Violations = append(s.Violations, fmt.Sprintf("t=%d %s: recycled prefix regressed %d -> %d", s.Now, ctx, s.lastBase, c.Log.Base()))
	}
	s.lastBase = c.Log.Base()
	for _, id := range c.IDs() {
		r := c.Replica(id)
		if r.Removed {
			continue
		}
		if c.Log.Base() > r.Persisted {
			s.Violations = append(s.Violations, fmt.Sprintf("t=%d %s: recycled prefix %d exceeds persisted %d of replica %s", s.Now, ctx, c.Log.Base(), r.Persisted, id))
		}
	}
}

// Append appends an entry at the leader and schedules replication of the
// whole log tail to every member with the given network/disk/ack delays.
func (s *Sim) Append(data string, netDelay, diskDelay, ackDelay int) uint64 {
	idx := s.c.Append(data)
	for _, id := range s.c.IDs() {
		s.deliver(id, idx, netDelay, diskDelay, ackDelay)
	}
	return idx
}

// deliver models one replica path: message delivery (volatile receive),
// then disk persistence, then the ack back to the leader. A replica that is
// offline or removed when a step fires simply drops it: an entry received
// only in memory is never persisted and never confirmed.
func (s *Sim) deliver(id string, idx uint64, netDelay, diskDelay, ackDelay int) {
	s.After(netDelay, "deliver->"+id, func() {
		r := s.c.Replica(id)
		if r == nil || r.Removed || !r.Online {
			return // message lost: replica unreachable
		}
		if idx > r.Received {
			r.Received = idx // volatile only
		}
		s.After(diskDelay, "persist "+id, func() {
			r := s.c.Replica(id)
			if r == nil || r.Removed || !r.Online {
				return // crashed before persisting: nothing durable
			}
			durable := r.Received
			s.After(ackDelay, "ack "+id, func() {
				s.c.OnPersistedAck(id, durable)
			})
		})
	})
}

// Crash takes a replica offline.
func (s *Sim) Crash(id string) {
	if r := s.c.Replica(id); r != nil {
		r.Online = false
	}
}

// Recover brings a never-removed replica back online.
func (s *Sim) Recover(id string) {
	if r := s.c.Replica(id); r != nil && !r.Removed {
		r.Online = true
	}
}

// Remove explicitly removes an offline replica (recording the snapshot
// re-entry requirement).
func (s *Sim) Remove(id string) error { return s.c.RemoveReplica(id) }

// Rejoin brings a replica back, installing a snapshot when required, and
// records the snapshot install as evidence.
func (s *Sim) Rejoin(id string) error {
	snap, err := s.c.Rejoin(id)
	if err != nil {
		return err
	}
	if snap > 0 {
		s.Snapshots = append(s.Snapshots, SnapshotInstall{ReplicaID: id, Index: snap, At: s.Now})
	}
	return nil
}

type event struct {
	at   int
	seq  int
	name string
	run  func()
}

type evHeap []event

func (h evHeap) Len() int { return len(h) }
func (h evHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].seq < h[j].seq
}
func (h evHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *evHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *evHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}
