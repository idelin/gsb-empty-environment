package material

import (
	"math/big"
	"testing"
)

func mustApply(t *testing.T, s *Simulator, e *Event) *Receipt {
	t.Helper()
	r, err := s.Apply(e)
	if err != nil {
		t.Fatalf("apply %+v: %v", e, err)
	}
	return r
}

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

// buildScenario creates a mixed pipeline:
//   receive A:100, B:60
//   split A -> A1:40, A2:55 (loss 5)
//   merge A1 + B -> M
//   loss 10 on M
//   split M -> F1:60, F2:25 (loss 5)
func buildScenario(t *testing.T) *Simulator {
	t.Helper()
	s := NewSimulator()
	mustApply(t, s, &Event{ID: "e1", Type: EvReceive, LotID: "A", Qty: 100, Source: "srcA"})
	mustApply(t, s, &Event{ID: "e2", Type: EvReceive, LotID: "B", Qty: 60, Source: "srcB"})
	r := mustApply(t, s, &Event{ID: "e3", Type: EvSplit, From: "A",
		Parts: []Part{{LotID: "A1", Qty: 40}, {LotID: "A2", Qty: 55}}})
	if r.LossQty != 5 {
		t.Fatalf("split loss = %d, want 5", r.LossQty)
	}
	if !r.LossVec.Equal(Pure("srcA")) {
		t.Fatalf("split loss vec = %v, want pure srcA", r.LossVec)
	}
	r = mustApply(t, s, &Event{ID: "e4", Type: EvMerge, Ins: []string{"A1", "B"}, Out: "M"})
	if r.LossQty != 0 {
		t.Fatalf("merge loss = %d, want 0 (merge must conserve)", r.LossQty)
	}
	m := s.Lot("M")
	if m.Qty != 100 {
		t.Fatalf("M qty = %d, want 100", m.Qty)
	}
	// M = (40·A + 60·B)/100 = 2/5·srcA + 3/5·srcB
	want := Vector{"srcA": rat(2, 5), "srcB": rat(3, 5)}
	if !m.Vec.Equal(want) {
		t.Fatalf("M vec = %v, want %v", m.Vec, want)
	}
	r = mustApply(t, s, &Event{ID: "e5", Type: EvLoss, LotID: "M", LossQty: 10})
	if !r.LossVec.Equal(want) {
		t.Fatalf("loss vec = %v, want proportional %v", r.LossVec, want)
	}
	mustApply(t, s, &Event{ID: "e6", Type: EvSplit, From: "M",
		Parts: []Part{{LotID: "F1", Qty: 60}, {LotID: "F2", Qty: 25}}})
	return s
}

func TestTraceLinearCombination(t *testing.T) {
	s := buildScenario(t)
	tr, err := s.Trace("F1")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Qty != 60 {
		t.Fatalf("F1 qty = %d, want 60", tr.Qty)
	}
	// F1 = 60 units of (2/5·srcA + 3/5·srcB) = 24·srcA + 36·srcB
	if tr.Amounts["srcA"].Cmp(rat(24, 1)) != 0 || tr.Amounts["srcB"].Cmp(rat(36, 1)) != 0 {
		t.Fatalf("F1 amounts = %v, want srcA=24 srcB=36", tr.Amounts)
	}
	// Shares normalized, amounts sum to qty.
	if !tr.Shares.IsNormalized() {
		t.Fatalf("shares not normalized: %v", tr.Shares)
	}
	total := new(big.Rat)
	for _, a := range tr.Amounts {
		total.Add(total, a)
	}
	if total.Cmp(rat(tr.Qty, 1)) != 0 {
		t.Fatalf("amounts sum %s != qty %d", total, tr.Qty)
	}
}

func TestConservationWithLoss(t *testing.T) {
	s := buildScenario(t)
	// Live inventory + recorded loss must equal received quantity.
	var live int64
	for _, l := range s.Lots() {
		live += l.Qty
	}
	if live+s.TotalLoss() != 160 {
		t.Fatalf("live %d + loss %d != received 160", live, s.TotalLoss())
	}
	if s.TotalLoss() != 20 {
		t.Fatalf("total loss = %d, want 20 (5+10+5)", s.TotalLoss())
	}
	// Source-level conservation: amounts in live lots + loss amounts
	// must equal the received source quantities.
	liveAmt := Vector{}
	for _, l := range s.Lots() {
		liveAmt = liveAmt.Add(l.Vec.Scale(rat(l.Qty, 1)))
	}
	lossAmt := Vector{}
	for _, id := range []string{"e3", "e5", "e6"} {
		r := s.Receipt(id)
		lossAmt = lossAmt.Add(r.LossVec.Scale(rat(r.LossQty, 1)))
	}
	total := liveAmt.Add(lossAmt)
	if total["srcA"].Cmp(rat(100, 1)) != 0 || total["srcB"].Cmp(rat(60, 1)) != 0 {
		t.Fatalf("source conservation violated: %v", total)
	}
}

func TestSplitExceedingQtyRejected(t *testing.T) {
	s := NewSimulator()
	mustApply(t, s, &Event{ID: "e1", Type: EvReceive, LotID: "A", Qty: 10, Source: "srcA"})
	_, err := s.Apply(&Event{ID: "e2", Type: EvSplit, From: "A",
		Parts: []Part{{LotID: "B", Qty: 7}, {LotID: "C", Qty: 4}}})
	if err == nil {
		t.Fatal("expected conservation error for split exceeding qty")
	}
	// Failed event must not mutate state.
	if s.Lot("A").Qty != 10 || s.Lot("B") != nil {
		t.Fatal("failed split mutated state")
	}
}

func TestLossExceedingQtyRejected(t *testing.T) {
	s := NewSimulator()
	mustApply(t, s, &Event{ID: "e1", Type: EvReceive, LotID: "A", Qty: 10, Source: "srcA"})
	if _, err := s.Apply(&Event{ID: "e2", Type: EvLoss, LotID: "A", LossQty: 11}); err == nil {
		t.Fatal("expected error for loss exceeding qty")
	}
}

func TestMergeWeightedRational(t *testing.T) {
	s := NewSimulator()
	mustApply(t, s, &Event{ID: "e1", Type: EvReceive, LotID: "A", Qty: 3, Source: "srcA"})
	mustApply(t, s, &Event{ID: "e2", Type: EvReceive, LotID: "B", Qty: 7, Source: "srcB"})
	mustApply(t, s, &Event{ID: "e3", Type: EvMerge, Ins: []string{"A", "B"}, Out: "M"})
	m := s.Lot("M")
	want := Vector{"srcA": rat(3, 10), "srcB": rat(7, 10)}
	if !m.Vec.Equal(want) {
		t.Fatalf("M vec = %v, want %v", m.Vec, want)
	}
	if !m.Vec.IsNormalized() {
		t.Fatal("merged vector not normalized")
	}
}

func TestReplayConsistency(t *testing.T) {
	s := buildScenario(t)
	if err := s.Replay(); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestDuplicateEventIdempotent(t *testing.T) {
	s := buildScenario(t)
	before := s.Lots()
	lossBefore := s.TotalLoss()

	// Re-apply every event; each must be a no-op returning the same receipt.
	dup := &Event{ID: "e4", Type: EvMerge, Ins: []string{"A1", "B"}, Out: "M"}
	r1, r2 := s.Receipt("e4"), mustApply(t, s, dup)
	if r1 != r2 {
		t.Fatal("duplicate apply did not return the original receipt")
	}
	mustApply(t, s, &Event{ID: "e5", Type: EvLoss, LotID: "M", LossQty: 10})
	mustApply(t, s, &Event{ID: "e1", Type: EvReceive, LotID: "A", Qty: 100, Source: "srcA"})

	after := s.Lots()
	if len(before) != len(after) {
		t.Fatalf("lot count changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if !before[i].Equal(after[i]) {
			t.Fatalf("lot %s changed after duplicate events", before[i].ID)
		}
	}
	if s.TotalLoss() != lossBefore {
		t.Fatalf("total loss changed: %d -> %d", lossBefore, s.TotalLoss())
	}
	if err := s.Replay(); err != nil {
		t.Fatalf("replay after duplicates: %v", err)
	}
}

func TestVectorInvariantAfterEveryEvent(t *testing.T) {
	s := NewSimulator()
	events := []*Event{
		{ID: "e1", Type: EvReceive, LotID: "A", Qty: 90, Source: "srcA"},
		{ID: "e2", Type: EvReceive, LotID: "B", Qty: 30, Source: "srcB"},
		{ID: "e3", Type: EvSplit, From: "A", Parts: []Part{{LotID: "A1", Qty: 50}, {LotID: "A2", Qty: 37}}},
		{ID: "e4", Type: EvMerge, Ins: []string{"A1", "B"}, Out: "M"},
		{ID: "e5", Type: EvLoss, LotID: "M", LossQty: 13},
		{ID: "e6", Type: EvSplit, From: "M", Parts: []Part{{LotID: "F", Qty: 67}}},
	}
	for _, e := range events {
		mustApply(t, s, e)
		for _, l := range s.Lots() {
			if !l.Vec.IsNormalized() {
				t.Fatalf("after %s: lot %s vector not normalized: %v", e.ID, l.ID, l.Vec)
			}
		}
		if err := s.Replay(); err != nil {
			t.Fatalf("after %s: replay: %v", e.ID, err)
		}
	}
	// F = 67 of (50·srcA + 30·srcB)/80 = 5/8·srcA + 3/8·srcB
	f := s.Lot("F")
	want := Vector{"srcA": rat(5, 8), "srcB": rat(3, 8)}
	if !f.Vec.Equal(want) {
		t.Fatalf("F vec = %v, want %v", f.Vec, want)
	}
}
