package freq

import "testing"

func TestCondRetractMissingRejected(t *testing.T) {
	cm := NewCondModel(256)
	if err := cm.Retract(1, 'a'); err != ErrNotPresent {
		t.Fatalf("retract in empty context: %v, want ErrNotPresent", err)
	}
	cm.Add(1, 'a')
	if err := cm.Retract(1, 'b'); err != ErrNotPresent {
		t.Fatalf("retract of absent symbol: %v, want ErrNotPresent", err)
	}
}

func TestCondConservation(t *testing.T) {
	cm := NewCondModel(256)
	records := 0
	for i := 0; i < 300; i++ {
		cm.Add(byte(i%5), byte('a'+i%3))
		records++
	}
	for i := 0; i < 50; i++ {
		if err := cm.Retract(byte(i%5), byte('a'+i%3)); err == nil {
			records--
		}
	}
	if got := cm.Total(); got != uint64(records) {
		t.Fatalf("total = %d, want %d", got, records)
	}
}

func TestMergeSharesCountsWithoutPollution(t *testing.T) {
	cm := NewCondModel(256)
	cm.Add(1, 'a')
	cm.Add(1, 'a')
	cm.Add(2, 'b')
	cm.MergeContexts(1, 2)

	// After the merge both contexts see the summed distribution.
	if got := cm.Count(1, 'b'); got != 1 {
		t.Fatalf("merged count ctx1['b'] = %d, want 1", got)
	}
	if got := cm.Count(2, 'a'); got != 2 {
		t.Fatalf("merged count ctx2['a'] = %d, want 2", got)
	}
	if got := cm.Total(); got != 3 {
		t.Fatalf("total after merge = %d, want 3 (shared table counted once)", got)
	}

	// Retracting through ctx 1 must not pollute ctx 2.
	if err := cm.Retract(1, 'a'); err != nil {
		t.Fatalf("retract: %v", err)
	}
	if got := cm.Count(2, 'a'); got != 2 {
		t.Fatalf("ctx2['a'] = %d after retract via ctx1, want 2 (no pollution)", got)
	}
	if got := cm.Count(1, 'a'); got != 1 {
		t.Fatalf("ctx1['a'] = %d after retract, want 1", got)
	}

	// Deleting ctx 1 entirely must leave ctx 2 intact.
	cm.DeleteContext(1)
	if got := cm.Count(2, 'a'); got != 2 {
		t.Fatalf("ctx2['a'] = %d after DeleteContext(1), want 2", got)
	}
	if got := cm.Count(2, 'b'); got != 1 {
		t.Fatalf("ctx2['b'] = %d after DeleteContext(1), want 1", got)
	}
	if got := cm.Total(); got != 3 {
		t.Fatalf("total after delete = %d, want 3", got)
	}
}

func TestSplitAfterMergeRestoresIndependence(t *testing.T) {
	cm := NewCondModel(256)
	cm.Add(7, 'x')
	cm.Add(8, 'y')
	cm.MergeContexts(7, 8)
	cm.SplitContext(7)
	// Now ctx 7 owns a private copy: adding through it leaves ctx 8.
	cm.Add(7, 'x')
	if got := cm.Count(8, 'x'); got != 1 {
		t.Fatalf("ctx8['x'] = %d, want 1", got)
	}
	if got := cm.Count(7, 'x'); got != 2 {
		t.Fatalf("ctx7['x'] = %d, want 2", got)
	}
}

func TestCondDeterministicEntropyZero(t *testing.T) {
	cm := NewCondModel(256)
	for i := 0; i < 100; i++ {
		cm.Add(3, 'q')
		cm.Add(4, 'r')
	}
	if got := cm.Entropy(); got != 0 {
		t.Fatalf("conditional entropy of deterministic contexts = %v, want 0", got)
	}
}

func TestCondIncrementalMatchesRecompute(t *testing.T) {
	rng := uint64(999)
	next := func(n uint64) uint64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return (rng >> 33) % n
	}
	type rec struct{ ctx, sym byte }
	cm := NewCondModel(256)
	var live []rec
	for step := 0; step < 4000; step++ {
		if len(live) == 0 || next(3) != 0 {
			r := rec{byte(next(4)), byte(next(6))}
			cm.Add(r.ctx, r.sym)
			live = append(live, r)
		} else {
			i := int(next(uint64(len(live))))
			if err := cm.Retract(live[i].ctx, live[i].sym); err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			live = append(live[:i], live[i+1:]...)
		}
	}
	re := NewCondModel(256)
	for _, r := range live {
		re.Add(r.ctx, r.sym)
	}
	if cm.Total() != re.Total() || cm.Total() != uint64(len(live)) {
		t.Fatalf("totals: incremental %d, recompute %d, records %d", cm.Total(), re.Total(), len(live))
	}
	for ctx := 0; ctx < 4; ctx++ {
		for sym := 0; sym < 6; sym++ {
			a, b := cm.Count(byte(ctx), byte(sym)), re.Count(byte(ctx), byte(sym))
			if a != b {
				t.Fatalf("count[%d][%d]: incremental %d != recompute %d", ctx, sym, a, b)
			}
		}
	}
	if cm.Entropy() != re.Entropy() {
		t.Fatalf("entropy: incremental %v != recompute %v", cm.Entropy(), re.Entropy())
	}
}
