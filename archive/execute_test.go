package archive

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func countingDecoder(pages map[BlockID][]byte, calls *int) DecodeFunc {
	return func(ctx context.Context, b Block) ([]byte, error) {
		(*calls)++
		page, ok := pages[b.ID]
		if !ok {
			return nil, fmt.Errorf("no page for block %d", b.ID)
		}
		return page, nil
	}
}

func TestExecuteReturnsResultsInRequestOrder(t *testing.T) {
	m, pages := sharedBlockManifest(t)
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
	var calls int
	res, err := Execute(context.Background(), m, g, batch, p, nil, countingDecoder(pages, &calls))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 3 || res.DecodeBlocks != 3 {
		t.Fatalf("calls=%d decode=%d", calls, res.DecodeBlocks)
	}
	want := [][]byte{
		[]byte("aaaa"),
		[]byte("bbbb"),
		[]byte("aaaa"),
		[]byte("cccc"),
	}
	for i := range want {
		if !bytesEqual(res.Data[i], want[i]) {
			t.Fatalf("request %d data %q want %q; emit order reordering leaked into output", i, res.Data[i], want[i])
		}
	}
}

func TestUnauthorizedBatchRejectedBeforeDecoder(t *testing.T) {
	m, pages := concatRawManifest(t, 8)
	batch := []Request{{Start: 0, Length: 4}, {Start: 4, Length: 4}}
	full := NewGrant("tenant", Interval{0, 8})
	restricted := NewGrant("tenant", Interval{0, 4})
	p, err := PlanBatch(m, full, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan with full grant: %v", err)
	}

	// PlanBatch rejects the mixed batch outright.
	if _, err := PlanBatch(m, restricted, batch, DefaultPlanOptions(1)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized plan rejection, got %v", err)
	}

	// Even a plan that matches the restricted grant digest must be rejected by
	// the checker before any decoder callback.
	cp := *p
	cp.GrantDigest = restricted.Digest()
	var calls int
	_, err = Execute(context.Background(), m, restricted, batch, &cp, nil, countingDecoder(pages, &calls))
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized execute rejection, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("decoder called %d times before whole-batch rejection", calls)
	}
}

func TestCancelThenRetryLeavesCacheUntouched(t *testing.T) {
	m, pages := concatRawManifest(t, 8)
	g := NewGrant("tenant", Interval{0, 8})
	batch := []Request{{Start: 0, Length: 8}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	cache := NewPageCache(1, m.Version, m.StrategyDigest)
	var calls int
	cancelDecode := func(ctx context.Context, b Block) ([]byte, error) {
		calls++
		return nil, context.Canceled
	}
	_, err = Execute(context.Background(), m, g, batch, p, cache, cancelDecode)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("first attempt calls = %d", calls)
	}
	if cache.Len() != 0 {
		t.Fatalf("cancelled attempt committed %d pages", cache.Len())
	}

	calls = 0
	res, err := Execute(context.Background(), m, g, batch, p, cache, countingDecoder(pages, &calls))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if calls != 1 || res.DecodeBlocks != 1 {
		t.Fatalf("retry calls=%d decode=%d", calls, res.DecodeBlocks)
	}
	if !bytesEqual(res.Data[0], pages[1]) {
		t.Fatalf("retry data = %q", res.Data[0])
	}
	if cache.Len() != 1 {
		t.Fatalf("retry did not commit cache")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	if _, err := Execute(ctx, m, g, batch, p, nil, countingDecoder(pages, &calls)); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected pre-cancelled error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("pre-cancelled execute called decoder %d times", calls)
	}
}

func TestDecoderPageMustMatchBlockSize(t *testing.T) {
	m, _ := concatRawManifest(t, 4)
	g := NewGrant("tenant", Interval{0, 4})
	batch := []Request{{Start: 0, Length: 4}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	short := func(ctx context.Context, b Block) ([]byte, error) { return []byte("abc"), nil }
	_, err = Execute(context.Background(), m, g, batch, p, nil, short)
	if !errors.Is(err, ErrInsufficientEvidence) {
		t.Fatalf("short page should be insufficient evidence, got %v", err)
	}
}

func TestNoAdjacentBytesLeakThroughPageOrReverse(t *testing.T) {
	// Araw authorized slice inside a larger page.
	m := &Manifest{
		Version:        1,
		StrategyDigest: strategyDigest("x"),
		Root:           0,
		Nodes:          []Node{NewRawNode(0, 1)},
		Blocks:         []Block{{ID: 1, Size: 8}},
	}
	if err := m.SealFromRoot(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	g := NewGrant("tenant", Interval{2, 6})
	batch := []Request{{Start: 2, Length: 4}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	page := map[BlockID][]byte{1: []byte("ABCDEFGH")}
	var calls int
	res, err := Execute(context.Background(), m, g, batch, p, nil, countingDecoder(page, &calls))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if string(res.Data[0]) != "CDEF" {
		t.Fatalf("output %q leaked adjacent page bytes", res.Data[0])
	}
	if calls != 1 {
		t.Fatalf("decode work should count the physical page once, calls=%d", calls)
	}

	// A reverse node across a repeated boundary; grant covers only [3,5).
	rep := &Manifest{
		Version:        1,
		StrategyDigest: strategyDigest("x"),
		Root:           1,
		Nodes: []Node{
			NewRawNode(0, 1),
			NewRepeatNode(1, 0, 4),
		},
		Blocks: []Block{{ID: 1, Size: 2}},
	}
	if err := rep.SealFromRoot(); err != nil {
		t.Fatalf("seal repeat: %v", err)
	}
	g2 := NewGrant("tenant", Interval{3, 5})
	batch2 := []Request{{Start: 3, Length: 2}}
	p2, err := PlanBatch(rep, g2, batch2, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("repeat plan: %v", err)
	}
	res2, err := Execute(context.Background(), rep, g2, batch2, p2, nil, countingDecoder(map[BlockID][]byte{1: []byte("AB")}, &calls))
	if err != nil {
		t.Fatalf("repeat execute: %v", err)
	}
	if string(res2.Data[0]) != "BA" {
		t.Fatalf("repeat boundary output=%q want BA", res2.Data[0])
	}
}

func TestExecuteUsesFreshCheckpointWithoutDecode(t *testing.T) {
	m, pages := concatRawManifest(t, 4)
	g := NewGrant("tenant", Interval{0, 4})
	batch := []Request{{Start: 0, Length: 4}}
	cache := NewPageCache(1, m.Version, m.StrategyDigest)
	cache.Put(1, pages[1])
	cp := cache.Snapshot()
	opts := DefaultPlanOptions(1)
	opts.Checkpoint = &cp
	opts.Limits.DecodeBlocks = 0
	p, err := PlanBatch(m, g, batch, opts)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	var calls int
	res, err := Execute(context.Background(), m, g, batch, p, cache, countingDecoder(pages, &calls))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 0 || res.DecodeBlocks != 0 {
		t.Fatalf("cache hit did not save decode work: calls=%d", calls)
	}
	if !bytesEqual(res.Data[0], pages[1]) {
		t.Fatalf("data = %q", res.Data[0])
	}
}

func TestStaleCacheIsNotTrustedByExecutor(t *testing.T) {
	m, pages := concatRawManifest(t, 4)
	g := NewGrant("tenant", Interval{0, 4})
	batch := []Request{{Start: 0, Length: 4}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	stale := NewPageCache(1, m.Version-1, m.StrategyDigest)
	stale.Put(1, []byte("ZZZZ")) // old content must never be served
	var calls int
	res, err := Execute(context.Background(), m, g, batch, p, stale, countingDecoder(pages, &calls))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 1 || !bytesEqual(res.Data[0], pages[1]) {
		t.Fatalf("stale cache was used: calls=%d data=%q", calls, res.Data[0])
	}
	if stale.Version != m.Version {
		t.Fatalf("cache version after commit = %d", stale.Version)
	}
}

func TestWarmCacheSavesDecodeOnRepeatExecution(t *testing.T) {
	m, pages := concatRawManifest(t, 8)
	g := NewGrant("tenant", Interval{0, 8})
	batch := []Request{{Start: 0, Length: 8}}
	p, err := PlanBatch(m, g, batch, DefaultPlanOptions(1))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	cache := NewPageCache(1, m.Version, m.StrategyDigest)
	var calls int
	first, err := Execute(context.Background(), m, g, batch, p, cache, countingDecoder(pages, &calls))
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if first.DecodeBlocks != 1 || calls != 1 {
		t.Fatalf("first decode=%d calls=%d", first.DecodeBlocks, calls)
	}

	// The same plan may be reused with a warmed cache.  The plan recorded an
	// upper-bound cost of one decode, but the actual hit costs zero.
	calls = 0
	second, err := Execute(context.Background(), m, g, batch, p, cache, countingDecoder(pages, &calls))
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	if calls != 0 {
		t.Fatalf("warm cache still called decoder %d times", calls)
	}
	if second.DecodeBlocks != 0 || second.Hits != 1 || second.PlannedDecodeBlocks != 1 {
		t.Fatalf("second accounts decode=%d hits=%d planned=%d", second.DecodeBlocks, second.Hits, second.PlannedDecodeBlocks)
	}
	if !bytesEqual(second.Data[0], pages[1]) {
		t.Fatalf("second data = %q", second.Data[0])
	}
}
