package htb

import (
	"fmt"
	"math/big"
)

// Ledger is an INDEPENDENT per-event bookkeeper used to verify a Tree.
// It replays every event (advance, send, cancel, rate change, reattach)
// with its own deliberately simple arithmetic and, after each event,
// checks that:
//
//   - every bucket balance in the tree equals the ledger's balance;
//   - no bucket (hence no ancestor of any tenant) is overdrawn, unless
//     the tree runs under AllowDebt;
//   - no balance exceeds its capacity;
//   - conservation holds per node: balance == initial + refilled - deducted.
//
// The ledger shares no state with the tree; it only observes the public
// results of operations (including the per-hop deduction evidence).
type Ledger struct {
	debt     DebtPolicy
	now      int64
	parent   map[string]string
	bal      map[string]*big.Rat
	rate     map[string]*big.Rat
	cap_     map[string]*big.Rat
	settled  map[string]int64
	initial  map[string]*big.Rat
	refilled map[string]*big.Rat
	deducted map[string]*big.Rat
	order    []string
}

// NewLedger creates an empty ledger for a tree with the given debt policy.
func NewLedger(debt DebtPolicy) *Ledger {
	return &Ledger{
		debt:     debt,
		parent:   make(map[string]string),
		bal:      make(map[string]*big.Rat),
		rate:     make(map[string]*big.Rat),
		cap_:     make(map[string]*big.Rat),
		settled:  make(map[string]int64),
		initial:  make(map[string]*big.Rat),
		refilled: make(map[string]*big.Rat),
		deducted: make(map[string]*big.Rat),
	}
}

// AddNode registers a node exactly as added to the tree.
func (l *Ledger) AddNode(id, parentID string, capacity, rate *big.Rat, startFull bool) {
	l.parent[id] = parentID
	l.rate[id] = cloneRat(rate)
	l.cap_[id] = cloneRat(capacity)
	l.settled[id] = l.now
	l.refilled[id] = new(big.Rat)
	l.deducted[id] = new(big.Rat)
	l.bal[id] = new(big.Rat)
	if startFull {
		l.bal[id].Set(capacity)
	}
	l.initial[id] = cloneRat(l.bal[id])
	l.order = append(l.order, id)
}

func (l *Ledger) settle(id string, tick int64) {
	if tick <= l.settled[id] {
		return
	}
	elapsed := Rat(tick-l.settled[id], 1)
	add := new(big.Rat).Mul(l.rate[id], elapsed)
	l.bal[id].Add(l.bal[id], add)
	if l.bal[id].Cmp(l.cap_[id]) > 0 {
		// Overflow is lost at the cap; it is not refundable.
		overflow := new(big.Rat).Sub(l.bal[id], l.cap_[id])
		l.bal[id].Set(l.cap_[id])
		add.Sub(add, overflow)
	}
	l.refilled[id].Add(l.refilled[id], add)
	l.settled[id] = tick
}

// Advance mirrors Tree.Advance.
func (l *Ledger) Advance(tick int64) {
	if tick <= l.now {
		return
	}
	l.now = tick
	for _, id := range l.order {
		l.settle(id, tick)
	}
}

// SetRate mirrors Tree.SetRate: settle with the old rate, then switch.
func (l *Ledger) SetRate(id string, rate *big.Rat) {
	l.settle(id, l.now)
	l.rate[id] = cloneRat(rate)
}

// Reattach mirrors Tree.Reattach. Balances do not change; only the
// ancestry used to validate send evidence is updated.
func (l *Ledger) Reattach(id, newParentID string) {
	l.parent[id] = newParentID
}

// Cancel mirrors Tree.Cancel. Queued packets hold no tokens, so the
// ledger changes nothing; Verify afterwards proves the tree agrees.
func (l *Ledger) Cancel() {}

// Send checks a SendResult against the ledger and books the deductions.
// It validates the per-hop evidence: the path must match the ledger's
// own view of the ancestry, amounts must equal the packet size, and
// Before/After must chain exactly.
func (l *Ledger) Send(tenantID string, res SendResult) error {
	if !res.Sent {
		return nil // rejected sends move no tokens; Verify checks the rest
	}
	if res.Packet == nil {
		return fmt.Errorf("ledger: send of %s reported no packet", tenantID)
	}
	// Reconstruct the expected path from the ledger's own parent map.
	var want []string
	for cur := tenantID; ; cur = l.parent[cur] {
		want = append([]string{cur}, want...)
		if l.parent[cur] == "" {
			break
		}
	}
	if len(res.Path) != len(want) {
		return fmt.Errorf("ledger: path length %d, want %d", len(res.Path), len(want))
	}
	for i, d := range res.Path {
		if d.NodeID != want[i] {
			return fmt.Errorf("ledger: hop %d is %s, want %s", i, d.NodeID, want[i])
		}
		if d.Amount.Cmp(res.Packet.Size) != 0 {
			return fmt.Errorf("ledger: hop %s amount %s != packet size %s", d.NodeID, d.Amount, res.Packet.Size)
		}
		l.settle(d.NodeID, l.now)
		if d.Before.Cmp(l.bal[d.NodeID]) != 0 {
			return fmt.Errorf("ledger: hop %s Before=%s, ledger has %s", d.NodeID, d.Before, l.bal[d.NodeID])
		}
		after := new(big.Rat).Sub(d.Before, d.Amount)
		if d.After.Cmp(after) != 0 {
			return fmt.Errorf("ledger: hop %s After=%s, want %s", d.NodeID, d.After, after)
		}
		l.bal[d.NodeID].Set(after)
		l.deducted[d.NodeID].Add(l.deducted[d.NodeID], d.Amount)
	}
	return nil
}

// Verify compares the full tree state against the ledger and enforces
// the global invariants. It must be called after every event.
func (l *Ledger) Verify(t *Tree) error {
	t.SettleAll()
	for _, id := range l.order {
		l.settle(id, l.now)
		got, err := t.Balance(id)
		if err != nil {
			return err
		}
		if got.Cmp(l.bal[id]) != 0 {
			return fmt.Errorf("ledger: node %s balance %s, ledger has %s (tick %d)", id, got, l.bal[id], l.now)
		}
		if l.debt == RejectOversize && l.bal[id].Sign() < 0 {
			return fmt.Errorf("ledger: node %s overdrawn: %s", id, l.bal[id])
		}
		if l.bal[id].Cmp(l.cap_[id]) > 0 {
			return fmt.Errorf("ledger: node %s balance %s exceeds capacity %s", id, l.bal[id], l.cap_[id])
		}
		sum := new(big.Rat).Add(l.initial[id], l.refilled[id])
		sum.Sub(sum, l.deducted[id])
		if sum.Cmp(l.bal[id]) != 0 {
			return fmt.Errorf("ledger: node %s violates conservation: %s != initial %s + refilled %s - deducted %s",
				id, l.bal[id], l.initial[id], l.refilled[id], l.deducted[id])
		}
	}
	return nil
}
