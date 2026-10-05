// Package sim implements a multi-replica inventory simulator with
// transferable deduction quotas.
//
// Design invariants:
//   - A replica may only deduct quota it locally holds, so the global
//     total can never go negative even while replicas are offline.
//   - Quota is moved between replicas via transfer messages. Sent quota
//     is escrowed (in-flight) and cannot be spent by the receiver until
//     the message actually arrives: no pre-spending.
//   - Every transfer carries a unique ID; receivers deduplicate by ID,
//     so duplicated messages never grant quota twice.
//   - On reconnect, replicas gossip their known transfer sets and merge
//     them (union by message ID), which converges ledgers while keeping
//     total quota conserved.
package sim

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrInsufficient is returned when a replica lacks the quota for an
// operation.
var ErrInsufficient = errors.New("insufficient quota")

// Transfer is a quota transfer message between two replicas.
type Transfer struct {
	ID     string
	From   string
	To     string
	Amount int64
}

// LedgerEntry is one event in a replica's local transfer ledger.
type LedgerEntry struct {
	Kind       string // "DEDUCT", "SEND" or "RECV"
	Amount     int64
	Peer       string // counterparty for SEND/RECV
	TransferID string // set for SEND/RECV
}

// Replica is one inventory replica holding a spendable quota.
type Replica struct {
	ID       string
	quota    int64
	deducted int64
	seq      int
	known    map[string]Transfer // every transfer this replica has heard of
	applied  map[string]bool     // incoming transfers already credited
	ledger   []LedgerEntry
}

// NewReplica creates a replica holding the given initial quota.
func NewReplica(id string, quota int64) *Replica {
	return &Replica{
		ID:      id,
		quota:   quota,
		known:   make(map[string]Transfer),
		applied: make(map[string]bool),
	}
}

// Available returns the locally spendable quota.
func (r *Replica) Available() int64 { return r.quota }

// Deducted returns the amount this replica has consumed so far.
func (r *Replica) Deducted() int64 { return r.deducted }

// Ledger returns a copy of the replica's local event log.
func (r *Replica) Ledger() []LedgerEntry {
	out := make([]LedgerEntry, len(r.ledger))
	copy(out, r.ledger)
	return out
}

// Deduct consumes local quota. It never blocks and never goes negative:
// a replica can only spend quota it actually holds.
func (r *Replica) Deduct(amount int64) error {
	if amount <= 0 {
		return errors.New("amount must be positive")
	}
	if amount > r.quota {
		return ErrInsufficient
	}
	r.quota -= amount
	r.deducted += amount
	r.ledger = append(r.ledger, LedgerEntry{Kind: "DEDUCT", Amount: amount})
	return nil
}

// spendForTransfer escrows quota into a new transfer message.
func (r *Replica) spendForTransfer(to string, amount int64) (Transfer, error) {
	if amount <= 0 {
		return Transfer{}, errors.New("amount must be positive")
	}
	if amount > r.quota {
		return Transfer{}, ErrInsufficient
	}
	r.quota -= amount
	r.seq++
	t := Transfer{ID: fmt.Sprintf("%s#%d", r.ID, r.seq), From: r.ID, To: to, Amount: amount}
	r.known[t.ID] = t
	r.ledger = append(r.ledger, LedgerEntry{Kind: "SEND", Amount: amount, Peer: to, TransferID: t.ID})
	return t, nil
}

// Receive applies an incoming transfer. It is idempotent: a duplicated
// message ID is credited only once.
func (r *Replica) Receive(t Transfer) {
	if _, ok := r.known[t.ID]; !ok {
		r.known[t.ID] = t
	}
	if t.To != r.ID || r.applied[t.ID] {
		return
	}
	r.applied[t.ID] = true
	r.quota += t.Amount
	r.ledger = append(r.ledger, LedgerEntry{Kind: "RECV", Amount: t.Amount, Peer: t.From, TransferID: t.ID})
}

// mergeKnown merges the transfer set learned from a peer (anti-entropy
// gossip on reconnect). Any transfer addressed to this replica that has
// not been credited yet is applied; everything commutes by message ID.
func (r *Replica) mergeKnown(peer *Replica) {
	ids := make([]string, 0, len(peer.known))
	for id := range peer.known {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r.Receive(peer.known[id])
	}
}

type pair struct{ a, b string }

func key(a, b string) pair {
	if a > b {
		a, b = b, a
	}
	return pair{a, b}
}

// Network simulates the links between replicas: it routes transfer
// messages, supports partitions, and queues in-flight messages while a
// link is cut.
type Network struct {
	replicas map[string]*Replica
	cut      map[pair]bool
	inflight map[string][]Transfer // queued per destination replica
}

// NewNetwork builds a fully connected network over the given replicas.
func NewNetwork(replicas ...*Replica) *Network {
	n := &Network{
		replicas: make(map[string]*Replica),
		cut:      make(map[pair]bool),
		inflight: make(map[string][]Transfer),
	}
	for _, r := range replicas {
		n.replicas[r.ID] = r
	}
	return n
}

// Replica returns a replica by ID.
func (n *Network) Replica(id string) *Replica { return n.replicas[id] }

// Partition cuts the link between two replicas (both directions).
func (n *Network) Partition(a, b string) { n.cut[key(a, b)] = true }

func (n *Network) connected(a, b string) bool { return !n.cut[key(a, b)] }

// Transfer moves quota from one replica to another. The amount is
// escrowed from the sender immediately; the receiver can only spend it
// once the message is delivered.
func (n *Network) Transfer(from, to string, amount int64) (Transfer, error) {
	s := n.replicas[from]
	t, err := s.spendForTransfer(to, amount)
	if err != nil {
		return Transfer{}, err
	}
	n.route(t)
	return t, nil
}

func (n *Network) route(t Transfer) {
	dst := n.replicas[t.To]
	if dst.applied[t.ID] {
		return // already credited (e.g. via gossip): drop the stale copy
	}
	if n.connected(t.From, t.To) {
		dst.Receive(t)
	} else {
		n.inflight[t.To] = append(n.inflight[t.To], t)
	}
}

// purgeDelivered drops queued messages whose destination has already
// credited them through another path, so stale copies do not inflate
// the in-flight escrow.
func (n *Network) purgeDelivered(id string) {
	queued := n.inflight[id]
	if len(queued) == 0 {
		return
	}
	dst := n.replicas[id]
	kept := queued[:0]
	for _, t := range queued {
		if !dst.applied[t.ID] {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(n.inflight, id)
	} else {
		n.inflight[id] = kept
	}
}

// Heal reconnects two replicas: queued messages for both sides are
// delivered (or re-queued if the sender is still cut off) and both
// replicas gossip-merge their known transfer sets.
func (n *Network) Heal(a, b string) {
	delete(n.cut, key(a, b))
	for _, id := range []string{a, b} {
		queued := n.inflight[id]
		delete(n.inflight, id)
		for _, t := range queued {
			n.route(t)
		}
	}
	n.replicas[a].mergeKnown(n.replicas[b])
	n.replicas[b].mergeKnown(n.replicas[a])
	n.purgeDelivered(a)
	n.purgeDelivered(b)
}

// InFlight returns the total quota currently escrowed in undelivered
// messages.
func (n *Network) InFlight() int64 {
	var total int64
	for _, ts := range n.inflight {
		for _, t := range ts {
			total += t.Amount
		}
	}
	return total
}

// Total returns the conserved quantity: available quota plus deducted
// quota across all replicas, plus quota still in flight.
func (n *Network) Total() int64 {
	var total int64
	for _, r := range n.replicas {
		total += r.quota + r.deducted
	}
	return total + n.InFlight()
}

// Report renders each replica's available quota and transfer ledger.
func (n *Network) Report() string {
	var b strings.Builder
	ids := make([]string, 0, len(n.replicas))
	for id := range n.replicas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := n.replicas[id]
		fmt.Fprintf(&b, "Replica %s: available=%d deducted=%d\n", id, r.quota, r.deducted)
		for _, e := range r.ledger {
			switch e.Kind {
			case "DEDUCT":
				fmt.Fprintf(&b, "  DEDUCT            -%d\n", e.Amount)
			case "SEND":
				fmt.Fprintf(&b, "  SEND  -> %-4s     -%d (%s)\n", e.Peer, e.Amount, e.TransferID)
			case "RECV":
				fmt.Fprintf(&b, "  RECV  <- %-4s     +%d (%s)\n", e.Peer, e.Amount, e.TransferID)
			}
		}
	}
	fmt.Fprintf(&b, "In-flight quota: %d\n", n.InFlight())
	fmt.Fprintf(&b, "Total (available + deducted + in-flight): %d\n", n.Total())
	return b.String()
}
