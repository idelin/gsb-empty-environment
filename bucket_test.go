package tbsim

import (
	"math/big"
	"testing"
)

func TestIdleTokensNeverExceedCap(t *testing.T) {
	b := NewBucket("b", Rat(3, 2), 10, 0)
	b.Advance(1000) // long idle
	if got := b.Avail(1000); got.Cmp(big.NewRat(10, 1)) != 0 {
		t.Fatalf("idle tokens exceed cap: got %v want 10", got)
	}
}

func TestBurstBoundedByCapacity(t *testing.T) {
	b := NewBucket("b", Rat(1, 1), 12, 0)
	p := NewPolicer([]*Flow{NewFlow("f", &Node{Name: "leaf", Bucket: b}, 0, 1, 100, 0)})
	// Full bucket allows exactly a Cap-sized burst back to back.
	p.Arrive(0, Packet{ID: 1, Flow: "f", Size: 7})
	p.Arrive(0, Packet{ID: 2, Flow: "f", Size: 5})
	p.Arrive(0, Packet{ID: 3, Flow: "f", Size: 1}) // 12 used, 0 left, 1 token/tick not yet accrued
	if len(p.Passed) != 2 || len(p.Dropped) != 1 {
		t.Fatalf("burst boundary: passed=%d dropped=%d, want 2/1", len(p.Passed), len(p.Dropped))
	}
	if p.Dropped[0].Pkt.ID != 3 {
		t.Fatalf("wrong packet dropped: %d", p.Dropped[0].Pkt.ID)
	}
}

func TestWaitTicksExactRational(t *testing.T) {
	b := NewBucket("b", Rat(2, 3), 10, 0) // 2/3 token per tick
	b.Consume(0, 10)                      // empty the bucket
	if w := b.WaitTicks(0, 1); w != 2 {   // ceil(1 / (2/3)) = ceil(3/2) = 2
		t.Fatalf("wait = %d, want 2", w)
	}
	if b.Has(1, 1) {
		t.Fatalf("tokens available too early at tick 1")
	}
	if !b.Has(2, 1) {
		t.Fatalf("tokens not available at exact tick 2")
	}
	// Larger need: ceil((10-4/3*0 ... )) check another exact boundary.
	b2 := NewBucket("b2", Rat(3, 4), 9, 0)
	b2.Consume(0, 9)
	if w := b2.WaitTicks(0, 3); w != 4 { // ceil(3 / (3/4)) = 4 exactly
		t.Fatalf("wait = %d, want 4", w)
	}
	if b2.Has(3, 3) || !b2.Has(4, 3) { // monotone clock: query in time order
		t.Fatalf("exact boundary mismatch")
	}
}

func TestConservationUnderChurn(t *testing.T) {
	b := NewBucket("b", Rat(5, 3), 17, 0)
	// Deterministic pseudo-random consumption pattern.
	need := int64(1)
	for now := int64(1); now <= 500; now++ {
		need = need*7%13 + 1
		if b.Has(now, need) && need <= b.Cap {
			b.Consume(now, need)
		}
	}
	a := b.Audit(500)
	if !a.Conserved {
		t.Fatalf("conservation violated: consumed=%v remaining=%v overflow=%v generated=%v",
			a.Consumed, a.Remaining, a.Overflow, a.Generated)
	}
}

func TestClockRewindAndRecoveryNoDoubleRefill(t *testing.T) {
	b := NewBucket("b", Rat(1, 1), 100, 0)
	b.Consume(0, 100) // empty
	b.Advance(50)
	half := b.Avail(50) // 50 tokens
	// Clock jumps backwards, then recovers to 50: must not refill again.
	b.Advance(10)
	b.Advance(49)
	b.Advance(50)
	if got := b.Avail(50); got.Cmp(half) != 0 {
		t.Fatalf("double refill after clock recovery: got %v want %v", got, half)
	}
	b.Advance(60)
	if got := b.Avail(60); got.Cmp(big.NewRat(60, 1)) != 0 {
		t.Fatalf("refill after recovery wrong: got %v want 60", got)
	}
}

func TestSetRateHotSwapNoDoubleRefill(t *testing.T) {
	b := NewBucket("b", Rat(1, 1), 100, 0)
	b.Consume(0, 100)
	b.Advance(30) // 30 tokens
	b.SetRate(30, Rat(2, 1))
	b.Advance(40) // 30 + 10*2 = 50
	if got := b.Avail(40); got.Cmp(big.NewRat(50, 1)) != 0 {
		t.Fatalf("after hot swap: got %v want 50", got)
	}
	if a := b.Audit(40); !a.Conserved {
		t.Fatalf("conservation violated across rate swap")
	}
}

func TestZeroRateNeverPasses(t *testing.T) {
	b := NewBucket("b", Rat(0, 1), 5, 0)
	b.Consume(0, 5)
	if w := b.WaitTicks(10, 1); w != -1 {
		t.Fatalf("zero-rate wait = %d, want -1", w)
	}
}
