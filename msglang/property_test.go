package msglang_test

import (
	"testing"

	"example.org/netsec/q0470/msglang"
)

func TestBoundedWitnessIsAcceptedByBothPhasesAndMinimal(t *testing.T) {
	ack := msglang.AmbiguousAcknowledge()
	grant := msglang.AmbiguousAuthorization()

	// The first witness found while enumerating in increasing node count must
	// be the exact minimum computed independently by the fixpoint engine.
	bounded, err := msglang.BoundedIntersect(ack, grant, msglang.Bounds{
		MaxNodes:   10,
		NodeBudget: 500000,
	})
	if err != nil {
		t.Fatalf("BoundedIntersect: %v", err)
	}
	exact, err := msglang.MinimalIntersection(ack, grant)
	if err != nil {
		t.Fatalf("MinimalIntersection: %v", err)
	}
	if bounded.Verdict != msglang.VerdictNonEmpty {
		t.Fatalf("expected NonEmpty, got %s: %s", bounded.Verdict, bounded.Reason)
	}
	if bounded.NodeCount != exact.NodeCount {
		t.Fatalf("bounded witness size %d must equal exact minimum %d",
			bounded.NodeCount, exact.NodeCount)
	}

	if ok, _, err := ack.Accept(bounded.Tree); err != nil || !ok {
		t.Fatalf("ack does not accept bounded witness: ok=%v err=%v", ok, err)
	}
	if ok, _, err := grant.Accept(bounded.Tree); err != nil || !ok {
		t.Fatalf("grant does not accept bounded witness: ok=%v err=%v", ok, err)
	}
	if bounded.LeftTrace.Final == bounded.RightTrace.Final {
		t.Fatal("the two parsing paths must end in distinct component states")
	}
	if got := bounded.Tree.NodeCount(); got != bounded.NodeCount {
		t.Fatalf("reported node count %d disagrees with tree %d", bounded.NodeCount, got)
	}
}

func TestVerdictStringStable(t *testing.T) {
	cases := map[msglang.Verdict]string{
		msglang.VerdictNonEmpty: "NonEmpty",
		msglang.VerdictEmpty:    "Empty",
		msglang.VerdictUnknown:  "Unknown",
	}
	for verdict, want := range cases {
		if verdict.String() != want {
			t.Fatalf("got %q want %q", verdict.String(), want)
		}
	}
}
