package clocksync

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func replay(t *testing.T, name string) (Result, []Message) {
	t.Helper()
	f, err := os.Open(filepath.Join("fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fx, err := LoadFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := PairMessages(fx.Events)
	if err != nil {
		t.Fatal(err)
	}
	return Solve(Nodes(fx.Nodes, msgs), DeriveConstraints(msgs)), msgs
}

func TestReplayConsistentFixture(t *testing.T) {
	res, msgs := replay(t, "consistent.json")
	if res.Conflict != nil {
		t.Fatalf("unexpected conflict: %v", res.Conflict)
	}
	// Ground truth used to synthesize the fixture: theta[B]=+50, theta[C]=-30.
	if iv := res.Intervals["B"]; iv.Lo > 50 || iv.Hi < 50 {
		t.Fatalf("interval B = %v does not contain true offset 50", iv)
	}
	if iv := res.Intervals["C"]; iv.Lo > -30 || iv.Hi < -30 {
		t.Fatalf("interval C = %v does not contain true offset -30", iv)
	}
	if v := CheckCausal(msgs, res.Offsets); len(v) != 0 {
		t.Fatalf("reversed causal edges: %v", v)
	}
}

func TestReplayConflictFixture(t *testing.T) {
	res, _ := replay(t, "conflict.json")
	if res.Conflict == nil {
		t.Fatal("expected conflict chain, got none")
	}
	if len(res.Conflict.Chain) != 2 || res.Conflict.Total >= 0 {
		t.Fatalf("bad conflict: %v", res.Conflict)
	}
}

func TestReplaySparseFixture(t *testing.T) {
	res, _ := replay(t, "sparse.json")
	if res.Conflict != nil {
		t.Fatalf("unexpected conflict: %v", res.Conflict)
	}
	if hi := res.Intervals["B"].Hi; hi != 40 {
		t.Fatalf("hi(B) = %v, want 40", hi)
	}
	if lo := res.Intervals["B"].Lo; !math.IsInf(lo, -1) {
		t.Fatalf("lo(B) = %v, want -inf (no reverse-path constraint)", lo)
	}
	if iv := res.Intervals["C"]; iv != Wide() {
		t.Fatalf("interval C = %v, want wide", iv)
	}
}
