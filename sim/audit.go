package sim

import (
	"fmt"
	"sort"
)

// ReplicaView is the auditor's view of one replica, computed from durable
// state so it is valid even while the replica is crashed.
type ReplicaView struct {
	ID       int64
	Down     bool
	Balance  int64 // spendable credit
	Spent    int64 // locally consumed credit
	InFlight int64 // escrowed on un-acked outgoing transfers
}

// Snapshot is a global audit view at one virtual instant.
type Snapshot struct {
	Time     int64
	Quota    int64
	Balance  int64 // sum of spendable balances
	Spent    int64 // sum of local consumption
	InFlight int64 // un-acked outgoing escrow (see Audit)
	Replicas []ReplicaView
}

// TotalAvailable is the credit currently usable by holders.
//
// Escrowed funds are absent from the balance the instant a transfer opens and
// stay absent until the ack; therefore while the sender has deducted but the
// receiver has not been credited (message lost or in flight), total available
// strictly decreases and is never inflated.
func (s *Snapshot) TotalAvailable() int64 { return s.Balance }

// durableView reconstructs the view of one replica purely from its WAL,
// which works whether the process is up or down.
func durableView(id int64, d *Disk, down bool) ReplicaView {
	v := ReplicaView{ID: id, Down: down}
	pending := make(map[TransferID]int64)
	for _, o := range d.Records() {
		switch o.Kind {
		case opGenesis, opDeliver:
			v.Balance += o.Amount
		case opSpend:
			v.Balance -= o.Amount
			v.Spent += o.Amount
		case opOpen:
			v.Balance -= o.Amount
			pending[o.ID] = o.Amount
		case opAck:
			delete(pending, o.ID)
		}
	}
	for _, a := range pending {
		v.InFlight += a
	}
	return v
}

// SnapshotNow reads global state from durable logs.
func (s *Simulator) SnapshotNow() Snapshot {
	snap := Snapshot{Time: s.now, Quota: s.quota}
	ids := make([]int64, 0, len(s.replicas))
	for id := range s.replicas {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		r := s.replicas[id]
		v := durableView(id, r.disk, r.down)
		snap.Replicas = append(snap.Replicas, v)
		snap.Balance += v.Balance
		snap.Spent += v.Spent
		snap.InFlight += v.InFlight
	}
	return snap
}

// Audit checks the safety invariants on durable state:
//
//  1. No component is ever negative (no overdraft, no negative consumption).
//  2. Conservation: Balance + Spent + InFlight == Quota at every instant.
//     InFlight here is "deducted but not yet finalized" escrow. Escrow is
//     only removed at the sender's opAck, so between delivery to the
//     receiver and receipt of the ack the same units appear once in the
//     receiver's balance and once in the sender's escrow: they are still
//     being protected from double spend, and the equation below accounts for
//     them with the delivered-but-unacked term I_open\delivered. The variant
//
//	Balance + Spent + InFlight - DeliveredUnacked == Quota
//
//	is a strict conservation invariant at all times; the equation with raw
//	InFlight (Balance + Spent + InFlight == Quota) additionally holds whenever
//	no transfer is in the delivered-but-unacked window. See README for the
//	full proof.
func (s *Simulator) Audit() error {
	snap := s.SnapshotNow()
	for _, v := range snap.Replicas {
		if v.Balance < 0 {
			return fmt.Errorf("audit: replica %d has negative balance %d", v.ID, v.Balance)
		}
		if v.Spent < 0 {
			return fmt.Errorf("audit: replica %d has negative spent %d", v.ID, v.Spent)
		}
		if v.InFlight < 0 {
			return fmt.Errorf("audit: replica %d has negative inflight %d", v.ID, v.InFlight)
		}
	}
	// DeliveredUnacked: amounts durably credited at the receiver while the
	// sender's escrow is still open.
	var deliveredUnacked int64
	for _, r := range s.replicas {
		for _, o := range r.disk.Records() {
			if o.Kind != opDeliver {
				continue
			}
			sender := s.replicas[o.ID.From]
			if sender == nil {
				continue
			}
			if !sender.disk.Seen(o.ID) { // no durable ack yet at the sender
				deliveredUnacked += o.Amount
			}
		}
	}
	strict := snap.Balance + snap.Spent + snap.InFlight - deliveredUnacked
	if strict != snap.Quota {
		return fmt.Errorf("audit: conservation broken: bal=%d spent=%d inflight=%d dup-delivered=%d sum=%d != quota=%d",
			snap.Balance, snap.Spent, snap.InFlight, deliveredUnacked, strict, snap.Quota)
	}
	// The relaxed equality holds exactly when no transfer is in the
	// deducted-but-not-delivered or delivered-but-not-acked windows.
	return nil
}
