package archive

import (
	"errors"
	"testing"
)

func TestGrantUnionAndCoverage(t *testing.T) {
	g := NewGrant("t1", Interval{0, 4}, Interval{2, 6}, Interval{8, 9})
	n := g.Normalized()
	if len(n.Intervals) != 2 || n.Intervals[0] != (Interval{0, 6}) || n.Intervals[1] != (Interval{8, 9}) {
		t.Fatalf("normalized = %+v", n.Intervals)
	}
	if !g.Covers(0, 6) || !g.Covers(3, 5) || !g.Covers(8, 9) {
		t.Fatal("covered interval was rejected")
	}
	if g.Covers(5, 9) || g.Covers(6, 8) || g.Covers(7, 9) {
		t.Fatal("gap was accepted")
	}
	if g.Covers(0, 10) {
		t.Fatal("span outside union was accepted")
	}
	if got := g.UnionLength(); got != 7 {
		t.Fatalf("union length = %d", got)
	}
}

func TestPhysicalBlockIdentityDoesNotShareAuthorization(t *testing.T) {
	// root = B1 | B1, so the same physical block appears at [0,8) and [8,16).
	nodes := []Node{
		NewRawNode(0, 1),
		NewConcatNode(1, 0, 0),
	}
	m := &Manifest{
		Version:        1,
		StrategyDigest: strategyDigest("x"),
		Root:           1,
		Nodes:          nodes,
		Blocks:         []Block{{ID: 1, Size: 8}},
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	g := NewGrant("tenant", Interval{0, 8})

	if _, err := PlanBatch(m, g, []Request{{Start: 8, Length: 8}}, DefaultPlanOptions(1)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("second root position should be unauthorized, got %v", err)
	}
	if _, err := PlanBatch(m, g, []Request{{Start: 0, Length: 8}, {Start: 8, Length: 8}}, DefaultPlanOptions(1)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("mixed batch should be rejected as a whole, got %v", err)
	}
	// Same physical block, but the batch still works when both root positions
	// are separately granted.  It costs one decode, not two.
	g2 := NewGrant("tenant", Interval{0, 16})
	p, err := PlanBatch(m, g2, []Request{{Start: 0, Length: 8}, {Start: 8, Length: 8}}, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.DecodeBlocks != 1 {
		t.Fatalf("decode account = %d, want one shared block decode", p.DecodeBlocks)
	}
}

func TestDictionaryAliasesDoNotShareAuthorization(t *testing.T) {
	// Two dict nodes reference the same physical block at different roots.
	nodes := []Node{
		NewDictNode(0, 1, 0, 4),
		NewDictNode(1, 1, 4, 4),
		NewConcatNode(2, 0, 1),
	}
	m := &Manifest{
		Version:        1,
		StrategyDigest: strategyDigest("x"),
		Root:           2,
		Nodes:          nodes,
		Blocks:         []Block{{ID: 1, Size: 8}},
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	g := NewGrant("tenant", Interval{0, 4})
	if _, err := PlanBatch(m, g, []Request{{Start: 4, Length: 4}}, DefaultPlanOptions(1)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("alias at second root should be unauthorized, got %v", err)
	}
	// A request that covers half of each root position is also unauthorized.
	if _, err := PlanBatch(m, g, []Request{{Start: 2, Length: 4}}, DefaultPlanOptions(1)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("half-covered request should be unauthorized, got %v", err)
	}
}

func TestReverseSliceAuthorizationUsesRootCoordinates(t *testing.T) {
	// root = reverse(A..H); grant covers the first half ("HGFE").
	m := &Manifest{
		Version:        1,
		StrategyDigest: strategyDigest("x"),
		Root:           1,
		Nodes: []Node{
			NewRawNode(0, 1),
			NewReverseNode(1, 0),
		},
		Blocks: []Block{{ID: 1, Size: 8}},
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	g := NewGrant("tenant", Interval{0, 4})
	if _, err := PlanBatch(m, g, []Request{{Start: 0, Length: 5}}, DefaultPlanOptions(1)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("request crossing reverse boundary should be unauthorized, got %v", err)
	}
	p, err := PlanBatch(m, g, []Request{{Start: 0, Length: 4}}, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("authorized half should plan: %v", err)
	}
	if p.DecodeBlocks != 1 {
		t.Fatalf("decode = %d", p.DecodeBlocks)
	}
}
