package shard

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"geocell/grid"
)

func newTestEngine(t *testing.T, nodes int, shardRes int) *Engine {
	t.Helper()
	ids := make([]NodeID, nodes)
	for i := range ids {
		ids[i] = NodeID(fmt.Sprintf("node-%d", i))
	}
	e, err := NewEngine(ids, shardRes, 128, 512)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	e.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	})
	return e
}

func randomCells(t *testing.T, rng *rand.Rand, n, res int) []grid.Cell {
	t.Helper()
	out := make([]grid.Cell, 0, n)
	for len(out) < n {
		ll := grid.LatLng{Lat: rng.Float64()*170 - 85, Lng: rng.Float64()*360 - 180}
		c, err := grid.LatLngToCell(ll, res)
		if err != nil {
			t.Fatalf("LatLngToCell: %v", err)
		}
		out = append(out, c)
	}
	return out
}

func TestEngineIngestQuery(t *testing.T) {
	e := newTestEngine(t, 4, 3)
	center := mustCell(t, beijing, 8)
	ring, err := grid.KRing(center, 3)
	if err != nil {
		t.Fatalf("KRing: %v", err)
	}
	// ingest a subset of the ring plus decoys far away
	rng := rand.New(rand.NewSource(5))
	subset := ring[:20]
	decoys := randomCells(t, rng, 500, 8)
	if err := e.Ingest(append(append([]grid.Cell{}, subset...), decoys...)); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	res, err := e.QueryKRing(center, 3)
	if err != nil {
		t.Fatalf("QueryKRing: %v", err)
	}
	if res.Total != grid.KRingSize(3) {
		t.Fatalf("Total = %d, want %d", res.Total, grid.KRingSize(3))
	}
	if len(res.Present) != len(subset) {
		t.Fatalf("Present = %d, want %d", len(res.Present), len(subset))
	}
	got := map[grid.Cell]bool{}
	for _, c := range res.Present {
		got[c] = true
	}
	for _, c := range subset {
		if !got[c] {
			t.Fatalf("ingested cell %s missing from query result", c)
		}
	}
	// locality: the whole 37-cell ring must be served by very few RPCs
	if res.RPCs > 7 {
		t.Fatalf("RPCs = %d, locality broken (naive would be %d)", res.RPCs, res.NaiveRPCs)
	}
	if res.RPCs >= res.NaiveRPCs {
		t.Fatalf("no communication reduction: %d vs naive %d", res.RPCs, res.NaiveRPCs)
	}
	if res.ShardsTouched < 1 || res.NodesTouched < 1 {
		t.Fatal("expected at least one shard and node touched")
	}
	m := e.Metrics()
	if m.Queries != 1 || m.Ingests != 1 {
		t.Fatalf("bad metrics: %+v", m)
	}
	if m.CellsHit != int64(len(subset)) {
		t.Fatalf("CellsHit = %d, want %d", m.CellsHit, len(subset))
	}
}

func TestEngineSingleNodeNoCrossTalk(t *testing.T) {
	e := newTestEngine(t, 1, 3)
	center := mustCell(t, beijing, 8)
	if err := e.Ingest([]grid.Cell{center}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	res, err := e.QueryKRing(center, 2)
	if err != nil {
		t.Fatalf("QueryKRing: %v", err)
	}
	if res.NodesTouched != 1 {
		t.Fatalf("Nodes = %d nodes, want 1", res.NodesTouched)
	}
	if len(res.Present) != 1 || res.Present[0] != center {
		t.Fatalf("Present = %v, want [center]", res.Present)
	}
}

// TestEngineConcurrent hammers the engine from many goroutines with
// interleaved ingests and queries. Run with -race.
func TestEngineConcurrent(t *testing.T) {
	e := newTestEngine(t, 8, 3)
	rng := rand.New(rand.NewSource(3))
	centers := randomCells(t, rng, 64, 8)

	const workers = 32
	const queriesPerWorker = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers*queriesPerWorker)

	// concurrent ingests
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(100 + g)))
			for i := 0; i < 25; i++ {
				cells := make([]grid.Cell, 0, 40)
				for j := 0; j < 40; j++ {
					ll := grid.LatLng{Lat: r.Float64()*170 - 85, Lng: r.Float64()*360 - 180}
					c, err := grid.LatLngToCell(ll, 8)
					if err != nil {
						errs <- err
						return
					}
					cells = append(cells, c)
				}
				if err := e.Ingest(cells); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	// concurrent queries
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < queriesPerWorker; i++ {
				c := centers[(g*queriesPerWorker+i)%len(centers)]
				res, err := e.QueryKRing(c, 3)
				if err != nil {
					errs <- err
					return
				}
				if res.Total != grid.KRingSize(3) {
					errs <- fmt.Errorf("bad ring size %d", res.Total)
					return
				}
				if res.RPCs > res.Total {
					errs <- fmt.Errorf("RPCs %d > cells %d", res.RPCs, res.Total)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent op failed: %v", err)
	}
	m := e.Metrics()
	if m.Queries != workers*queriesPerWorker {
		t.Fatalf("Queries = %d, want %d", m.Queries, workers*queriesPerWorker)
	}
	if m.Ingests != 8*25 {
		t.Fatalf("Ingests = %d, want %d", m.Ingests, 8*25)
	}
	if m.CellsRouted <= 0 || m.CrossNodeRPC <= 0 {
		t.Fatalf("metrics not advancing: %+v", m)
	}
}

// TestEngineQueryCorrectnessUnderConcurrency verifies query results are
// exact while ingests run concurrently.
func TestEngineQueryCorrectnessUnderConcurrency(t *testing.T) {
	e := newTestEngine(t, 6, 3)
	center := mustCell(t, beijing, 8)
	ring, err := grid.KRing(center, 3)
	if err != nil {
		t.Fatalf("KRing: %v", err)
	}
	// pre-ingest the full ring through the engine
	if err := e.Ingest(ring); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	rng := rand.New(rand.NewSource(11))
	decoys := randomCells(t, rng, 2000, 8)
	// background ingest churn
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; ; i += 4 {
				select {
				case <-stop:
					return
				default:
				}
				if err := e.Ingest(decoys[i%len(decoys) : i%len(decoys)+1]); err != nil {
					return
				}
			}
		}(g)
	}
	// queries must always see the full pre-ingested ring
	for i := 0; i < 200; i++ {
		res, err := e.QueryKRing(center, 3)
		if err != nil {
			t.Fatalf("QueryKRing: %v", err)
		}
		if len(res.Present) != len(ring) {
			t.Fatalf("Present = %d, want full ring %d", len(res.Present), len(ring))
		}
	}
	close(stop)
	wg.Wait()
}

func TestEngineShutdownIdempotent(t *testing.T) {
	e := newTestEngine(t, 3, 3)
	ctx := context.Background()
	if err := e.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := e.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
	if _, err := e.QueryKRing(mustCell(t, beijing, 8), 1); !errors.Is(err, ErrShutdown) {
		t.Fatalf("query after shutdown: got %v, want ErrShutdown", err)
	}
	if err := e.Ingest([]grid.Cell{mustCell(t, beijing, 8)}); !errors.Is(err, ErrShutdown) {
		t.Fatalf("ingest after shutdown: got %v, want ErrShutdown", err)
	}
}

// TestEngineNoGoroutineLeak asserts strictly that no goroutine survives a
// full start/query/shutdown cycle.
func TestEngineNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for cycle := 0; cycle < 3; cycle++ {
		ids := []NodeID{"a", "b", "c", "d"}
		e, err := NewEngine(ids, 3, 64, 128)
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		e.Start()
		center := mustCell(t, beijing, 8)
		ring, err := grid.KRing(center, 3)
		if err != nil {
			t.Fatalf("KRing: %v", err)
		}
		if err := e.Ingest(ring); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
		if _, err := e.QueryKRing(center, 3); err != nil {
			t.Fatalf("QueryKRing: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := e.Shutdown(ctx); err != nil {
			cancel()
			t.Fatalf("Shutdown: %v", err)
		}
		cancel()
	}
	// poll: goroutines must return to the baseline
	deadline := time.Now().Add(3 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: before=%d after=%d", before, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNewEngineErrors(t *testing.T) {
	if _, err := NewEngine(nil, 3, 16, 16); !errors.Is(err, ErrNoNodes) {
		t.Fatalf("expected ErrNoNodes, got %v", err)
	}
	if _, err := NewEngine([]NodeID{"a"}, -1, 16, 16); err == nil {
		t.Fatal("expected error for bad shard resolution")
	}
	if _, err := NewEngine([]NodeID{"a"}, grid.MaxRes+1, 16, 16); err == nil {
		t.Fatal("expected error for bad shard resolution")
	}
}
