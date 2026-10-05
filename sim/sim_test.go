package sim

import "testing"

func mustDeduct(t *testing.T, r *Replica, amount int64) {
	t.Helper()
	if err := r.Deduct(amount); err != nil {
		t.Fatalf("deduct %d on %s: %v", amount, r.ID, err)
	}
}

func mustTransfer(t *testing.T, n *Network, from, to string, amount int64) Transfer {
	t.Helper()
	tr, err := n.Transfer(from, to, amount)
	if err != nil {
		t.Fatalf("transfer %s->%s %d: %v", from, to, amount, err)
	}
	return tr
}

func checkTotal(t *testing.T, n *Network, want int64) {
	t.Helper()
	if got := n.Total(); got != want {
		t.Fatalf("total quota = %d, want %d", got, want)
	}
	for id := range n.replicas {
		if n.replicas[id].Available() < 0 {
			t.Fatalf("replica %s has negative quota %d", id, n.replicas[id].Available())
		}
	}
}

// TestPartitionReconnectConservation partitions the network into two
// groups, performs offline deductions and transfers (some crossing the
// partition), then heals everything and checks quota conservation.
func TestPartitionReconnectConservation(t *testing.T) {
	a := NewReplica("A", 40)
	b := NewReplica("B", 30)
	c := NewReplica("C", 20)
	d := NewReplica("D", 10)
	n := NewNetwork(a, b, c, d)
	const total = 100

	// Cut into {A,B} and {C,D}.
	n.Partition("A", "C")
	n.Partition("A", "D")
	n.Partition("B", "C")
	n.Partition("B", "D")
	checkTotal(t, n, total)

	mustDeduct(t, a, 10)             // A: 40 -> 30
	mustTransfer(t, n, "A", "B", 15) // A: 30 -> 15, B: 30 -> 45
	mustTransfer(t, n, "B", "C", 20) // B: 45 -> 25, queued for C
	mustDeduct(t, c, 5)              // C: 20 -> 15
	mustTransfer(t, n, "C", "D", 8)  // C: 15 -> 7, D: 10 -> 18
	mustTransfer(t, n, "D", "A", 5)  // D: 18 -> 13, queued for A
	checkTotal(t, n, total)

	if got := n.InFlight(); got != 25 {
		t.Fatalf("in-flight = %d, want 25", got)
	}
	// B cannot deduct more than it holds while offline.
	if err := b.Deduct(26); err != ErrInsufficient {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
	checkTotal(t, n, total)

	// Heal everything.
	n.Heal("A", "C")
	n.Heal("A", "D")
	n.Heal("B", "C")
	n.Heal("B", "D")
	checkTotal(t, n, total)

	if got := n.InFlight(); got != 0 {
		t.Fatalf("in-flight after heal = %d, want 0", got)
	}
	wantAvail := map[string]int64{"A": 20, "B": 25, "C": 27, "D": 13}
	for id, want := range wantAvail {
		if got := n.Replica(id).Available(); got != want {
			t.Fatalf("replica %s available = %d, want %d", id, got, want)
		}
	}
	if got := a.Deducted() + c.Deducted(); got != 15 {
		t.Fatalf("total deducted = %d, want 15", got)
	}
}

// TestDuplicateDeliveryIsIdempotent delivers the same transfer message
// repeatedly and verifies the quota is granted exactly once.
func TestDuplicateDeliveryIsIdempotent(t *testing.T) {
	a := NewReplica("A", 50)
	b := NewReplica("B", 0)
	n := NewNetwork(a, b)

	tr := mustTransfer(t, n, "A", "B", 20) // delivered immediately
	if got := b.Available(); got != 20 {
		t.Fatalf("B available = %d, want 20", got)
	}
	// Duplicate deliveries: direct, and via gossip merges.
	b.Receive(tr)
	b.Receive(tr)
	n.Heal("A", "B")
	n.Heal("A", "B")
	if got := b.Available(); got != 20 {
		t.Fatalf("B available after duplicates = %d, want 20", got)
	}
	checkTotal(t, n, 50)

	recv := 0
	for _, e := range b.Ledger() {
		if e.Kind == "RECV" {
			recv++
		}
	}
	if recv != 1 {
		t.Fatalf("B ledger has %d RECV entries, want 1", recv)
	}
}

// TestNoPreSpendOfInFlightQuota ensures a receiver cannot spend quota
// whose transfer message has not arrived yet.
func TestNoPreSpendOfInFlightQuota(t *testing.T) {
	a := NewReplica("A", 10)
	b := NewReplica("B", 5)
	n := NewNetwork(a, b)
	n.Partition("A", "B")

	mustTransfer(t, n, "A", "B", 10) // queued, not delivered
	if got := b.Available(); got != 5 {
		t.Fatalf("B available before delivery = %d, want 5", got)
	}
	if err := b.Deduct(6); err != ErrInsufficient {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
	checkTotal(t, n, 15)

	n.Heal("A", "B")
	if got := b.Available(); got != 15 {
		t.Fatalf("B available after heal = %d, want 15", got)
	}
	mustDeduct(t, b, 15)
	checkTotal(t, n, 15)
}

// TestOfflineDeductNeverNegative drives a replica to zero and confirms
// further deductions fail without going negative.
func TestOfflineDeductNeverNegative(t *testing.T) {
	r := NewReplica("R", 7)
	mustDeduct(t, r, 7)
	if err := r.Deduct(1); err != ErrInsufficient {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
	if got := r.Available(); got != 0 {
		t.Fatalf("available = %d, want 0", got)
	}
}

// TestTransferExceedingQuotaFails ensures a replica cannot transfer
// quota it does not hold.
func TestTransferExceedingQuotaFails(t *testing.T) {
	a := NewReplica("A", 5)
	b := NewReplica("B", 5)
	n := NewNetwork(a, b)
	if _, err := n.Transfer("A", "B", 6); err != ErrInsufficient {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
	checkTotal(t, n, 10)
}

// TestGossipMergeDeliversViaThirdParty: A sends to C while A-C is cut.
// B learns of the transfer via gossip from A, then delivers it to C via
// gossip. When A-C later heals, the queued original message is a
// duplicate and must not double-grant.
func TestGossipMergeDeliversViaThirdParty(t *testing.T) {
	a := NewReplica("A", 30)
	b := NewReplica("B", 0)
	c := NewReplica("C", 0)
	n := NewNetwork(a, b, c)
	n.Partition("A", "C")

	mustTransfer(t, n, "A", "C", 12) // queued for C
	checkTotal(t, n, 30)

	n.Heal("A", "B") // B learns of A#1 via gossip (not addressed to B)
	if got := c.Available(); got != 0 {
		t.Fatalf("C available = %d, want 0", got)
	}
	n.Heal("B", "C") // B gossips A#1 to C; C credits it
	if got := c.Available(); got != 12 {
		t.Fatalf("C available after gossip = %d, want 12", got)
	}
	checkTotal(t, n, 30)

	n.Heal("A", "C") // original queued message finally arrives: duplicate
	if got := c.Available(); got != 12 {
		t.Fatalf("C available after duplicate delivery = %d, want 12", got)
	}
	if got := n.InFlight(); got != 0 {
		t.Fatalf("in-flight = %d, want 0", got)
	}
	checkTotal(t, n, 30)
}
