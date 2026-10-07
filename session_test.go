package traj

import (
	"reflect"
	"testing"
)

func newTestGraph(t *testing.T) *Graph {
	g := NewGraph("S")
	batch1 := []Edge{
		{ID: "a1", From: "S", To: "P"},
		{ID: "a2", From: "P", To: "Q"},
		{ID: "a3", From: "Q", To: "S"},
	}
	if err := g.ImportBatch(batch1); err != nil {
		t.Fatalf("import batch1: %v", err)
	}
	return g
}

func TestImportBatchRejectsDuplicates(t *testing.T) {
	g := NewGraph("S")
	if err := g.ImportBatch([]Edge{{ID: "a1", From: "S", To: "P"}}); err != nil {
		t.Fatal(err)
	}
	if err := g.ImportBatch([]Edge{{ID: "a1", From: "S", To: "X"}}); err == nil {
		t.Fatal("duplicate ID across batches accepted")
	}
	if err := g.ImportBatch([]Edge{
		{ID: "b1", From: "S", To: "P"},
		{ID: "b1", From: "P", To: "S"},
	}); err == nil {
		t.Fatal("duplicate ID within batch accepted")
	}
	if v := g.Version(); v != 1 {
		t.Fatalf("version = %d after rejected batches, want 1", v)
	}
}

func TestSessionPrefixAndRemainingRoute(t *testing.T) {
	g := newTestGraph(t)
	s := NewSession(g)
	if err := s.WalkPrefix([]string{"a1", "a2"}); err != nil {
		t.Fatalf("walk prefix: %v", err)
	}
	if s.Current() != "Q" {
		t.Fatalf("current = %s, want Q", s.Current())
	}
	route, diag := s.RemainingRoute()
	if diag != nil {
		t.Fatalf("unexpected diagnosis: %v", diag)
	}
	if want := []string{"a3"}; !reflect.DeepEqual(RouteIDs(route), want) {
		t.Fatalf("remaining route = %v, want %v", RouteIDs(route), want)
	}
}

func TestSessionPrefixValidation(t *testing.T) {
	g := newTestGraph(t)
	s := NewSession(g)
	if err := s.WalkPrefix([]string{"zz"}); !IsKind(err, ErrUnknownEdge) {
		t.Fatalf("unknown edge: %v", err)
	}
	if err := s.WalkPrefix([]string{"a2"}); !IsKind(err, ErrPrefixBroken) {
		t.Fatalf("broken prefix: %v", err)
	}
	if err := s.WalkPrefix([]string{"a1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WalkPrefix([]string{"a1"}); !IsKind(err, ErrEdgeRewalked) {
		t.Fatalf("rewalked edge: %v", err)
	}
}

func TestPublishInvalidatedByNewBatch(t *testing.T) {
	g := newTestGraph(t)
	s := NewSession(g)
	if err := s.WalkPrefix([]string{"a1"}); err != nil {
		t.Fatal(err)
	}
	route, diag := s.PublishRemaining()
	if diag != nil {
		t.Fatalf("unexpected diagnosis: %v", diag)
	}
	if route.Version != 1 || route.From != "P" {
		t.Fatalf("route = %+v", route)
	}
	if err := g.CheckRoute(route); err != nil {
		t.Fatalf("route should be fresh: %v", err)
	}
	// New edges arrive: old route becomes stale.
	if err := g.ImportBatch([]Edge{{ID: "b1", From: "Q", To: "R"}}); err != nil {
		t.Fatal(err)
	}
	if err := g.CheckRoute(route); !IsKind(err, ErrStaleRoute) {
		t.Fatalf("stale route check: %v", err)
	}
}

func TestCheckpointRestore(t *testing.T) {
	g := newTestGraph(t)
	s := NewSession(g)
	if err := s.WalkPrefix([]string{"a1", "a2"}); err != nil {
		t.Fatal(err)
	}
	cp := s.Save()

	restored, err := Restore(g, cp)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Current() != "Q" {
		t.Fatalf("restored current = %s, want Q", restored.Current())
	}
	if got := RouteIDs(restored.Walked()); !reflect.DeepEqual(got, cp.WalkedIDs) {
		t.Fatalf("restored walked = %v, want %v", got, cp.WalkedIDs)
	}
	// Exhaustive permutation check of the prefix-constrained remainder.
	ok, err := restored.VerifyRemainingExhaustive()
	if err != nil || !ok {
		t.Fatalf("exhaustive verify: ok=%v err=%v", ok, err)
	}

	// A checkpoint from an older edge-set version must be rejected.
	if err := g.ImportBatch([]Edge{{ID: "b1", From: "Q", To: "R"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(g, cp); !IsKind(err, ErrVersionMismatch) {
		t.Fatalf("stale checkpoint restore: %v", err)
	}
}

func TestRestoreReplayValidatesPrefix(t *testing.T) {
	g := newTestGraph(t)
	cp := Checkpoint{Version: g.Version(), WalkedIDs: []string{"a2"}}
	if _, err := Restore(g, cp); !IsKind(err, ErrPrefixBroken) {
		t.Fatalf("restore with broken prefix: %v", err)
	}
}

func TestRemainingRouteFailureAfterPrefix(t *testing.T) {
	// After the prefix S->P, the leftover edge Q->R is disconnected
	// from the current point P.
	g := NewGraph("S")
	if err := g.ImportBatch([]Edge{
		{ID: "a1", From: "S", To: "P"},
		{ID: "a2", From: "Q", To: "R"},
		{ID: "a3", From: "R", To: "Q"},
	}); err != nil {
		t.Fatal(err)
	}
	s := NewSession(g)
	if err := s.WalkPrefix([]string{"a1"}); err != nil {
		t.Fatal(err)
	}
	_, diag := s.RemainingRoute()
	if diag == nil || diag.Kind != FailConnectivity {
		t.Fatalf("want connectivity failure, got %v", diag)
	}
	ok, err := s.VerifyRemainingExhaustive()
	if err != nil || !ok {
		t.Fatalf("exhaustive verify of failure: ok=%v err=%v", ok, err)
	}
}
