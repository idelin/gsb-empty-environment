package archive

import (
	"context"
	"errors"
	"testing"
)

func concatRawManifest(t *testing.T, sizes ...uint64) (*Manifest, map[BlockID][]byte) {
	t.Helper()
	nodes := make([]Node, 0, len(sizes)+1)
	blocks := make([]Block, 0, len(sizes))
	pages := make(map[BlockID][]byte, len(sizes))
	children := make([]NodeID, 0, len(sizes))
	for i, size := range sizes {
		id := BlockID(i + 1)
		nodes = append(nodes, NewRawNode(NodeID(i), id))
		blocks = append(blocks, Block{ID: id, Size: size, Codec: "raw"})
		page := make([]byte, size)
		for k := range page {
			page[k] = byte('a' + i)
		}
		pages[id] = page
		children = append(children, NodeID(i))
	}
	nodes = append(nodes, NewConcatNode(NodeID(len(sizes)), children...))
	m := &Manifest{
		Version:        1,
		Strategy:       "raw",
		StrategyDigest: strategyDigest("raw"),
		Root:           NodeID(len(sizes)),
		Nodes:          nodes,
		Blocks:         blocks,
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	return m, pages
}

func TestPlannerReordersPhysicalWork(t *testing.T) {
	m, _ := sharedBlockManifest(t)
	// [0,4)=B1, [4,8)=B2, [8,12)=B1, [12,16)=B3
	batch := []Request{
		{Start: 0, Length: 4},
		{Start: 4, Length: 4},
		{Start: 8, Length: 4},
		{Start: 12, Length: 4},
	}
	g := NewGrant("tenant", Interval{0, m.TotalLength})
	opts := DefaultPlanOptions(1)
	opts.Limits.DecodeBlocks = 3
	p, err := PlanBatch(m, g, batch, opts)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.DecodeBlocks != 3 {
		t.Fatalf("decode = %d, want 3 (two B1 reads share one page)", p.DecodeBlocks)
	}
	var emits []int
	for _, st := range p.Steps {
		if st.Kind == StepEmit {
			emits = append(emits, st.Request)
		}
	}
	if len(emits) != 4 {
		t.Fatalf("emits = %v", emits)
	}
	if emits[0] == 0 && emits[1] == 1 && emits[2] == 2 && emits[3] == 3 {
		t.Fatalf("plan did not reorder physical work: %v", emits)
	}
	if emits[0] != 0 || emits[1] != 2 {
		t.Fatalf("B1 requests should be grouped, emits = %v", emits)
	}

	opts.Limits.DecodeBlocks = 2
	if _, err := PlanBatch(m, g, batch, opts); !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("expected no-feasible-plan, got %v", err)
	}
}

func TestOverlappingRequestsDeduplicateReturnAccount(t *testing.T) {
	m, _ := concatRawManifest(t, 8)
	batch := []Request{{Start: 0, Length: 5}, {Start: 2, Length: 5}}
	g := NewGrant("tenant", Interval{0, m.TotalLength})
	opts := DefaultPlanOptions(1)
	opts.Limits.ReturnBytes = 7
	p, err := PlanBatch(m, g, batch, opts)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.ReturnBytes != 7 {
		t.Fatalf("return account = %d, want 7 (deduplicated union)", p.ReturnBytes)
	}
	if p.DecodeBlocks != 1 {
		t.Fatalf("decode = %d", p.DecodeBlocks)
	}
	opts.Limits.ReturnBytes = 6
	if _, err := PlanBatch(m, g, batch, opts); !errors.Is(err, ErrReturnQuota) {
		t.Fatalf("expected separate return-quota error, got %v", err)
	}
}

func TestDecodeQuotaIsSeparateFromReturnQuota(t *testing.T) {
	m, _ := concatRawManifest(t, 8)
	g := NewGrant("tenant", Interval{0, m.TotalLength})
	opts := DefaultPlanOptions(1)
	opts.Limits.DecodeBlocks = 0
	opts.Limits.ReturnBytes = 100
	if _, err := PlanBatch(m, g, []Request{{Start: 0, Length: 4}}, opts); !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("expected no-feasible-plan, got %v", err)
	}
	opts.Limits.DecodeBlocks = 1
	opts.Limits.ReturnBytes = 3
	if _, err := PlanBatch(m, g, []Request{{Start: 0, Length: 4}}, opts); !errors.Is(err, ErrReturnQuota) {
		t.Fatalf("expected return-quota error, got %v", err)
	}
}

func TestCacheCheckpointStaleness(t *testing.T) {
	m, pages := concatRawManifest(t, 4)
	g := NewGrant("tenant", Interval{0, 4})
	batch := []Request{{Start: 0, Length: 4}}

	old := NewPageCache(1, m.Version-1, m.StrategyDigest)
	old.Put(1, pages[1])
	cp := old.Snapshot()
	opts := DefaultPlanOptions(1)
	opts.Checkpoint = &cp
	opts.Limits.DecodeBlocks = 1
	p, err := PlanBatch(m, g, batch, opts)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !p.CheckpointStale || len(p.InitialCache) != 0 {
		t.Fatalf("old-version cache was trusted: stale=%v initial=%v", p.CheckpointStale, p.InitialCache)
	}
	if p.DecodeBlocks != 1 {
		t.Fatalf("old-version cache saved decode work: %d", p.DecodeBlocks)
	}

	opts.Limits.DecodeBlocks = 0
	if _, err := PlanBatch(m, g, batch, opts); !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("stale cache must not satisfy a zero decode quota, got %v", err)
	}

	oldStrategy := NewPageCache(1, m.Version, strategyDigest("old-strategy"))
	oldStrategy.Put(1, pages[1])
	cp = oldStrategy.Snapshot()
	opts.Checkpoint = &cp
	if _, err := PlanBatch(m, g, batch, opts); !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("old-strategy cache must not satisfy a zero decode quota, got %v", err)
	}

	fresh := NewPageCache(1, m.Version, m.StrategyDigest)
	fresh.Put(1, pages[1])
	cp = fresh.Snapshot()
	opts.Checkpoint = &cp
	p, err = PlanBatch(m, g, batch, opts)
	if err != nil {
		t.Fatalf("fresh checkpoint plan: %v", err)
	}
	if p.CheckpointStale || len(p.InitialCache) != 1 || p.InitialCache[0] != 1 {
		t.Fatalf("fresh cache was not used: stale=%v initial=%v", p.CheckpointStale, p.InitialCache)
	}
	if p.DecodeBlocks != 0 {
		t.Fatalf("fresh cache should save the decode, got %d", p.DecodeBlocks)
	}

	bad := CacheCheckpoint{Version: m.Version, StrategyDigest: m.StrategyDigest, Resident: []BlockID{99}}
	opts.Checkpoint = &bad
	if _, err := PlanBatch(m, g, batch, opts); !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("unknown checkpoint block should be insufficient evidence, got %v", err)
	}
}

func TestInsufficientEvidenceIsDistinctFromInfeasible(t *testing.T) {
	m, pages := concatRawManifest(t, 4)
	g := NewGrant("tenant", Interval{0, 4})
	opts := DefaultPlanOptions(1)
	opts.Limits.DecodeBlocks = 0

	// An unsigned/tampered manifest is insufficient evidence, not a proven
	// cost infeasibility.
	badm := m.Clone()
	badm.Signature[0] ^= 0xff
	if _, err := PlanBatch(badm, g, []Request{{Start: 0, Length: 4}}, opts); !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("bad signature should be insufficient evidence, got %v", err)
	}
	// With evidence, zero decode quota is a definitive no-feasible-plan.
	if _, err := PlanBatch(m, g, []Request{{Start: 0, Length: 4}}, opts); !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("expected no-feasible-plan, got %v", err)
	}
	_ = pages
}

func TestCapacitySmallerThanRequirementIsInfeasible(t *testing.T) {
	m, _ := concatRawManifest(t, 2, 2, 2)
	g := NewGrant("tenant", Interval{0, m.TotalLength})
	opts := DefaultPlanOptions(1)
	opts.ExactLimit = 8
	// This request needs all three pages at once.
	if _, err := PlanBatch(m, g, []Request{{Start: 0, Length: 6}}, opts); !errors.Is(err, ErrNoFeasiblePlan) {
		t.Fatalf("expected no-feasible-plan, got %v", err)
	}
	opts.Limits.CacheCapacity = 3
	if _, err := PlanBatch(m, g, []Request{{Start: 0, Length: 6}}, opts); err != nil {
		t.Fatalf("capacity 3 should be feasible: %v", err)
	}
}

func TestHugeManifestPlansSmallSlices(t *testing.T) {
	const total = uint64(1 << 48)
	m := &Manifest{
		Version:        1,
		Strategy:       "rle",
		StrategyDigest: strategyDigest("rle"),
		Root:           1,
		Nodes: []Node{
			NewRawNode(0, 1),
			NewRepeatNode(1, 0, total),
		},
		Blocks: []Block{{ID: 1, Size: 1, Codec: "rle"}},
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	g := NewGrant("tenant", Interval{total - 20, total})
	batch := []Request{
		{Start: total - 5, Length: 5},
		{Start: total - 20, Length: 5},
	}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if p.DecodeBlocks != 1 || p.ReturnBytes != 10 {
		t.Fatalf("decode=%d return=%d", p.DecodeBlocks, p.ReturnBytes)
	}
	if len(p.Steps) > 6 {
		t.Fatalf("huge batch expanded to %d steps", len(p.Steps))
	}
	var calls int
	res, err := Execute(context.Background(), m, g, batch, p, nil, countingDecoder(map[BlockID][]byte{1: []byte("A")}, &calls))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for i := range res.Data {
		if string(res.Data[i]) != "AAAAA" {
			t.Fatalf("request %d output=%q", i, res.Data[i])
		}
	}
}
