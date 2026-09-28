package desim

import (
	"bytes"
	"container/heap"
	"encoding/gob"
	"fmt"
)

// Snapshot is the full serializable state of a Sim: scheduler (including
// pending events, slot generations and the sequence counter), RNG state,
// topology, queues, generators and statistics. Restoring a snapshot and
// continuing execution yields a trajectory identical to an uninterrupted
// run.
type Snapshot struct {
	Now        uint64
	Seq        uint64
	Live       int
	Slots      []SlotSnap
	Free       []int
	RNGState   uint64
	NextPkt    int
	Nodes      []Node
	Links      []Link
	Gens       []Generator
	Trace      []string
	Delivered  int
	TotalDelay uint64
}

// SlotSnap is the serializable form of one scheduler slot.
type SlotSnap struct {
	Occupied bool
	Gen      uint64
	Ev       Event
}

// Snapshot captures the current simulation state.
func (s *Sim) Snapshot() *Snapshot {
	snap := &Snapshot{
		Now:        s.sch.now,
		Seq:        s.sch.seq,
		Live:       s.sch.live,
		Free:       append([]int(nil), s.sch.free...),
		RNGState:   s.rng.State,
		NextPkt:    s.NextPkt,
		Nodes:      append([]Node(nil), s.Nodes...),
		Links:      append([]Link(nil), s.Links...),
		Gens:       append([]Generator(nil), s.Gens...),
		Trace:      append([]string(nil), s.Trace...),
		Delivered:  s.Delivered,
		TotalDelay: s.TotalDelay,
	}
	snap.Slots = make([]SlotSnap, len(s.sch.slots))
	for i, sl := range s.sch.slots {
		snap.Slots[i] = SlotSnap{Occupied: sl.occupied, Gen: sl.gen, Ev: sl.ev}
	}
	return snap
}

// Checkpoint serializes the simulation state to bytes (gob, stdlib only).
func (s *Sim) Checkpoint() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s.Snapshot()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RestoreSnapshot rebuilds a Sim from a snapshot. The event heap is
// reconstructed from the occupied slots, so slot indices, generations and
// the global sequence counter continue exactly where they left off.
func RestoreSnapshot(snap *Snapshot) *Sim {
	sch := &Scheduler{
		now:   snap.Now,
		seq:   snap.Seq,
		live:  snap.Live,
		free:  append([]int(nil), snap.Free...),
		slots: make([]slot, len(snap.Slots)),
	}
	for i, ss := range snap.Slots {
		sch.slots[i] = slot{occupied: ss.Occupied, gen: ss.Gen, ev: ss.Ev}
		if ss.Occupied {
			heap.Push(&sch.pq, heapEntry{
				slot: i,
				gen:  ss.Gen,
				tick: ss.Ev.Tick,
				cat:  ss.Ev.Cat,
				seq:  ss.Ev.Seq,
			})
		}
	}
	return &Sim{
		sch:        sch,
		rng:        RNG{State: snap.RNGState},
		Nodes:      append([]Node(nil), snap.Nodes...),
		Links:      append([]Link(nil), snap.Links...),
		Gens:       append([]Generator(nil), snap.Gens...),
		NextPkt:    snap.NextPkt,
		Trace:      append([]string(nil), snap.Trace...),
		Delivered:  snap.Delivered,
		TotalDelay: snap.TotalDelay,
	}
}

// Restore deserializes a checkpoint produced by Checkpoint.
func Restore(data []byte) (*Sim, error) {
	var snap Snapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&snap); err != nil {
		return nil, fmt.Errorf("desim: restore: %w", err)
	}
	return RestoreSnapshot(&snap), nil
}
