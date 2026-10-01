package htb

import (
	"strings"
	"testing"
)

func build(t *testing.T, tr *Tree, id, parent string, capN, capD, rateN, rateD int64, full bool) {
	t.Helper()
	if err := tr.AddNode(id, parent, Rat(capN, capD), Rat(rateN, rateD), full); err != nil {
		t.Fatalf("AddNode(%s): %v", id, err)
	}
}

func bal(t *testing.T, tr *Tree, id string) string {
	t.Helper()
	b, err := tr.Balance(id)
	if err != nil {
		t.Fatalf("Balance(%s): %v", id, err)
	}
	return b.RatString()
}

func mustBal(t *testing.T, tr *Tree, id, want string) {
	t.Helper()
	if got := bal(t, tr, id); got != want {
		t.Fatalf("balance of %s = %s, want %s", id, got, want)
	}
}

func enq(t *testing.T, tr *Tree, tenant, pkt string, sizeN, sizeD int64) {
	t.Helper()
	if err := tr.Enqueue(tenant, Packet{ID: pkt, Size: Rat(sizeN, sizeD)}); err != nil {
		t.Fatalf("Enqueue(%s,%s): %v", tenant, pkt, err)
	}
}

// Rational refill: rate 1/3 token/tick must accumulate exactly, with no
// rounding loss across ticks.
func TestRationalRefillExact(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 100, 1, 1, 3, false)
	tr.Advance(1)
	mustBal(t, tr, "root", "1/3")
	tr.Advance(2)
	mustBal(t, tr, "root", "2/3")
	tr.Advance(3)
	mustBal(t, tr, "root", "1")
	tr.Advance(10)
	mustBal(t, tr, "root", "10/3")
}

// Refill must never exceed capacity, even with fractional rates.
func TestRefillCappedAtCapacity(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 2, 1, 1, 2, false)
	tr.Advance(100)
	mustBal(t, tr, "root", "2")
}

// Advancing to the same tick (or a past tick) must not refill again.
func TestDuplicateTickNoDoubleRefill(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 1000, 1, 3, 1, false)
	tr.Advance(5)
	mustBal(t, tr, "root", "15")
	if tr.Advance(5) {
		t.Fatal("Advance(5) twice should be a no-op")
	}
	mustBal(t, tr, "root", "15")
	if tr.Advance(2) {
		t.Fatal("Advance into the past should be a no-op")
	}
	mustBal(t, tr, "root", "15")
	tr.Advance(6)
	mustBal(t, tr, "root", "18")
}

// A child with plenty of tokens must not send when the parent is short,
// and the failed attempt must deduct nothing anywhere (atomicity).
func TestChildAllowedParentBlockedAtomic(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 10, 1, 0, 1, true)
	build(t, tr, "leaf", "root", 100, 1, 0, 1, true)
	enq(t, tr, "leaf", "p1", 20, 1)
	res := tr.SendNext("leaf")
	if res.Sent {
		t.Fatal("send should be rejected: parent cannot afford it")
	}
	if !strings.Contains(res.Reason, "root") {
		t.Fatalf("rejection should name the root bucket, got %q", res.Reason)
	}
	mustBal(t, tr, "root", "10")
	mustBal(t, tr, "leaf", "100")
	if tr.QueueLen("leaf") != 1 {
		t.Fatal("rejected packet must stay queued")
	}
}

// A packet larger than a bucket's capacity is explicitly rejected under
// RejectOversize.
func TestOversizeExplicitReject(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 100, 1, 0, 1, true)
	build(t, tr, "leaf", "root", 10, 1, 0, 1, true)
	enq(t, tr, "leaf", "big", 50, 1)
	res := tr.SendNext("leaf")
	if res.Sent {
		t.Fatal("oversize packet must be rejected")
	}
	if !strings.Contains(res.Reason, "exceeds capacity") {
		t.Fatalf("reason should say capacity exceeded, got %q", res.Reason)
	}
	mustBal(t, tr, "root", "100")
	mustBal(t, tr, "leaf", "10")
}

// Under AllowDebt an oversize packet sends the bucket into debt; the
// debt must be repaid by refills before anything else can be sent.
func TestOversizeDebtRepaidByRefill(t *testing.T) {
	tr := NewTree(AllowDebt)
	build(t, tr, "root", "", 1000, 1, 0, 1, true)
	build(t, tr, "leaf", "root", 10, 1, 1, 1, true)
	enq(t, tr, "leaf", "big", 25, 1)
	res := tr.SendNext("leaf")
	if !res.Sent {
		t.Fatalf("debt mode should send oversize packet: %s", res.Reason)
	}
	mustBal(t, tr, "leaf", "-15")
	enq(t, tr, "leaf", "small", 3, 1)
	if res := tr.SendNext("leaf"); res.Sent {
		t.Fatal("indebted bucket must not send")
	}
	tr.Advance(14) // repay 14 of 15
	if res := tr.SendNext("leaf"); res.Sent {
		t.Fatal("debt not yet repaid, must not send")
	}
	tr.Advance(18) // balance now 3
	res = tr.SendNext("leaf")
	if !res.Sent {
		t.Fatalf("debt repaid, send should succeed: %s", res.Reason)
	}
	mustBal(t, tr, "leaf", "0")
}

// Cancelling a queued packet consumes no tokens.
func TestCancelConsumesNoTokens(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 100, 1, 0, 1, true)
	build(t, tr, "leaf", "root", 10, 1, 0, 1, true)
	enq(t, tr, "leaf", "p1", 4, 1)
	enq(t, tr, "leaf", "p2", 5, 1)
	if !tr.Cancel("leaf", "p1") {
		t.Fatal("cancel should find p1")
	}
	mustBal(t, tr, "root", "100")
	mustBal(t, tr, "leaf", "10")
	if tr.QueueLen("leaf") != 1 {
		t.Fatal("one packet should remain")
	}
	if tr.Cancel("leaf", "p1") {
		t.Fatal("p1 already cancelled")
	}
	res := tr.SendNext("leaf")
	if !res.Sent || res.Packet.ID != "p2" {
		t.Fatalf("expected p2 to send, got %+v", res)
	}
	mustBal(t, tr, "leaf", "5")
}

// Reattach migrates the unsent queue and the reserved token balance
// atomically; later sends charge the NEW ancestry, never the old one.
func TestReattachMigratesQueueAndTokens(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 100, 1, 0, 1, true)
	build(t, tr, "eng", "root", 40, 1, 0, 1, true)
	build(t, tr, "sales", "root", 40, 1, 0, 1, true)
	build(t, tr, "alice", "eng", 20, 1, 0, 1, true)
	enq(t, tr, "alice", "p1", 6, 1)
	enq(t, tr, "alice", "p2", 3, 1)
	res := tr.SendNext("alice") // consumes 6 along root->eng->alice
	if !res.Sent {
		t.Fatalf("send p1: %s", res.Reason)
	}
	mustBal(t, tr, "alice", "14")
	if err := tr.Reattach("alice", "sales"); err != nil {
		t.Fatalf("Reattach: %v", err)
	}
	// Reserved tokens and the unsent queue moved with alice.
	mustBal(t, tr, "alice", "14")
	if tr.QueueLen("alice") != 1 {
		t.Fatal("unsent queue must migrate with the tenant")
	}
	res = tr.SendNext("alice")
	if !res.Sent {
		t.Fatalf("send p2 after reattach: %s", res.Reason)
	}
	var path []string
	for _, d := range res.Path {
		path = append(path, d.NodeID)
	}
	if strings.Join(path, ",") != "root,sales,alice" {
		t.Fatalf("p2 must charge the new ancestry, got %v", path)
	}
	mustBal(t, tr, "eng", "34")   // untouched by p2
	mustBal(t, tr, "sales", "37") // charged by p2
	mustBal(t, tr, "alice", "11") // 14 - 3
}

// Reattach must refuse cycles and root moves.
func TestReattachGuards(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 100, 1, 0, 1, true)
	build(t, tr, "a", "root", 10, 1, 0, 1, true)
	build(t, tr, "b", "a", 10, 1, 0, 1, true)
	if err := tr.Reattach("root", "a"); err == nil {
		t.Fatal("cannot reattach the root")
	}
	if err := tr.Reattach("a", "b"); err == nil {
		t.Fatal("reattaching a under its own descendant must fail")
	}
	if err := tr.Reattach("b", "root"); err != nil {
		t.Fatalf("legal reattach failed: %v", err)
	}
}

// A rate change settles with the old rate first; with lazy refill there
// is no old timer that could refill a second time.
func TestRateChangeNoDoubleRefill(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 1000, 1, 2, 1, false)
	tr.Advance(10) // 10 ticks at 2/tick
	mustBal(t, tr, "root", "20")
	if err := tr.SetRate("root", Rat(5, 1)); err != nil {
		t.Fatal(err)
	}
	mustBal(t, tr, "root", "20") // rate change itself adds nothing
	tr.Advance(20)               // 10 ticks at 5/tick
	mustBal(t, tr, "root", "70") // not 90: the old rate must not refill again
	tr.Advance(20)               // duplicate tick
	mustBal(t, tr, "root", "70")
}

// Every successful send returns per-hop deduction evidence that chains
// exactly from root to leaf.
func TestDeductionEvidenceChain(t *testing.T) {
	tr := NewTree(RejectOversize)
	build(t, tr, "root", "", 100, 1, 0, 1, true)
	build(t, tr, "mid", "root", 50, 1, 0, 1, true)
	build(t, tr, "leaf", "mid", 20, 1, 0, 1, true)
	enq(t, tr, "leaf", "p1", 7, 2) // 3.5 tokens: rational cost
	res := tr.SendNext("leaf")
	if !res.Sent {
		t.Fatalf("send: %s", res.Reason)
	}
	if len(res.Path) != 3 {
		t.Fatalf("want 3 hops, got %d", len(res.Path))
	}
	wantIDs := []string{"root", "mid", "leaf"}
	wantBefore := []string{"100", "50", "20"}
	wantAfter := []string{"193/2", "93/2", "33/2"}
	for i, d := range res.Path {
		if d.NodeID != wantIDs[i] {
			t.Fatalf("hop %d = %s, want %s", i, d.NodeID, wantIDs[i])
		}
		if d.Amount.RatString() != "7/2" {
			t.Fatalf("hop %d amount = %s, want 7/2", i, d.Amount)
		}
		if d.Before.RatString() != wantBefore[i] || d.After.RatString() != wantAfter[i] {
			t.Fatalf("hop %d evidence %s->%s, want %s->%s",
				i, d.Before, d.After, wantBefore[i], wantAfter[i])
		}
	}
	mustBal(t, tr, "leaf", "33/2")
}
