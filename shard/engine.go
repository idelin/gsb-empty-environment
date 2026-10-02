package shard

import (
	"context"
	"sync"
	"sync/atomic"

	"geocell/grid"
)

// Engine is a locality-aware scheduling engine. Each physical node is an
// actor (one goroutine owning its local cell store, no shared mutable
// state and no locks on the hot path). Requests are routed as per-shard
// batches, so a k-ring query costs O(shards touched) cross-node messages
// instead of O(cells).
type Engine struct {
	router *Router
	actors map[NodeID]*actor

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	metrics engineMetrics
}

type engineMetrics struct {
	queries      atomic.Int64
	ingests      atomic.Int64
	cellsRouted  atomic.Int64
	crossNodeRPC atomic.Int64
	cellsHit     atomic.Int64
}

// MetricsSnapshot is a point-in-time copy of the engine metrics.
type MetricsSnapshot struct {
	Queries      int64
	Ingests      int64
	CellsRouted  int64
	CrossNodeRPC int64
	CellsHit     int64
}

type request struct {
	cells  []grid.Cell
	ingest bool
	resp   chan []grid.Cell // buffered (cap 1); nil slice = ack for ingest
}

type actor struct {
	id    NodeID
	inbox chan request
	store map[grid.Cell]struct{}
}

// NewEngine builds an engine over the given nodes. shardRes is the virtual
// shard (ancestor) resolution; vnodes is the number of virtual nodes per
// physical node on the hash ring; queueSize is each node's inbox capacity.
func NewEngine(nodes []NodeID, shardRes, vnodes, queueSize int) (*Engine, error) {
	sh, err := NewSharder(shardRes)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, ErrNoNodes
	}
	if queueSize <= 0 {
		queueSize = 256
	}
	ring := NewRing(vnodes)
	e := &Engine{
		actors: make(map[NodeID]*actor, len(nodes)),
		done:   make(chan struct{}),
	}
	for _, n := range nodes {
		if _, dup := e.actors[n]; dup {
			continue
		}
		ring.AddNode(n)
		e.actors[n] = &actor{
			id:    n,
			inbox: make(chan request, queueSize),
			store: make(map[grid.Cell]struct{}),
		}
	}
	e.router = NewRouter(sh, ring)
	return e, nil
}

// Router exposes the engine's router.
func (e *Engine) Router() *Router { return e.router }

// Start launches the node actors.
func (e *Engine) Start() {
	for _, a := range e.actors {
		e.wg.Add(1)
		go e.run(a)
	}
}

func (e *Engine) run(a *actor) {
	defer e.wg.Done()
	for {
		select {
		case req := <-a.inbox:
			if req.ingest {
				for _, c := range req.cells {
					a.store[c] = struct{}{}
				}
				req.resp <- nil
			} else {
				present := make([]grid.Cell, 0, len(req.cells))
				for _, c := range req.cells {
					if _, ok := a.store[c]; ok {
						present = append(present, c)
					}
				}
				req.resp <- present
			}
		case <-e.done:
			return
		}
	}
}

func (e *Engine) isClosed() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// Shutdown stops all node actors and waits for them to exit. It is
// idempotent and safe to call concurrently with in-flight requests:
// in-flight callers observe ErrShutdown instead of hanging.
func (e *Engine) Shutdown(ctx context.Context) error {
	e.closeOnce.Do(func() { close(e.done) })
	wait := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(wait)
	}()
	select {
	case <-wait:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dispatch routes cells in per-shard batches to their owner nodes and
// collects the per-batch responses.
func (e *Engine) dispatch(cells []grid.Cell, ingest bool) ([][]grid.Cell, int, error) {
	batches, err := e.router.RouteBatch(cells)
	if err != nil {
		return nil, 0, err
	}
	nBatches := 0
	for _, bs := range batches {
		nBatches += len(bs)
	}
	resps := make([]chan []grid.Cell, 0, nBatches)
	sent := 0
	for node, bs := range batches {
		a := e.actors[node]
		for _, b := range bs {
			resp := make(chan []grid.Cell, 1)
			req := request{cells: b.Cells, ingest: ingest, resp: resp}
			select {
			case a.inbox <- req:
				resps = append(resps, resp)
				sent++
			case <-e.done:
				return nil, sent, ErrShutdown
			}
		}
	}
	out := make([][]grid.Cell, 0, len(resps))
	for _, resp := range resps {
		select {
		case r := <-resp:
			out = append(out, r)
		case <-e.done:
			return nil, sent, ErrShutdown
		}
	}
	return out, sent, nil
}

// Ingest stores cells on their owner nodes. It is synchronous: it returns
// after every node has acknowledged its batch.
func (e *Engine) Ingest(cells []grid.Cell) error {
	if e.isClosed() {
		return ErrShutdown
	}
	_, rpcs, err := e.dispatch(cells, true)
	if err != nil {
		return err
	}
	e.metrics.ingests.Add(1)
	e.metrics.cellsRouted.Add(int64(len(cells)))
	e.metrics.crossNodeRPC.Add(int64(rpcs))
	return nil
}

// QueryResult summarizes a k-ring query execution.
type QueryResult struct {
	Center        grid.Cell
	K             int
	Total         int         // cells in the ring
	Present       []grid.Cell // ring cells found in the cluster
	ShardsTouched int
	NodesTouched  int
	RPCs          int // cross-node messages actually sent
	NaiveRPCs     int // messages a cell-at-a-time router would send
}

// QueryKRing computes the k-ring locally (pure integer traversal), routes
// per-shard batches to the owning nodes, and merges which of the ring cells
// are present in the cluster.
func (e *Engine) QueryKRing(center grid.Cell, k int) (QueryResult, error) {
	if e.isClosed() {
		return QueryResult{}, ErrShutdown
	}
	cells, err := grid.KRing(center, k)
	if err != nil {
		return QueryResult{}, err
	}
	res := QueryResult{Center: center, K: k, Total: len(cells), NaiveRPCs: len(cells)}
	if e.isClosed() {
		return QueryResult{}, ErrShutdown
	}
	batches, err := e.router.RouteBatch(cells)
	if err != nil {
		return QueryResult{}, err
	}
	nodes := make(map[NodeID]struct{})
	shards := 0
	for n, bs := range batches {
		nodes[n] = struct{}{}
		shards += len(bs)
	}
	res.NodesTouched = len(nodes)
	res.ShardsTouched = shards

	parts, rpcs, err := e.dispatch(cells, false)
	if err != nil {
		return QueryResult{}, err
	}
	res.RPCs = rpcs
	for _, p := range parts {
		res.Present = append(res.Present, p...)
	}
	e.metrics.queries.Add(1)
	e.metrics.cellsRouted.Add(int64(len(cells)))
	e.metrics.crossNodeRPC.Add(int64(rpcs))
	e.metrics.cellsHit.Add(int64(len(res.Present)))
	return res, nil
}

// Metrics returns a snapshot of the engine metrics.
func (e *Engine) Metrics() MetricsSnapshot {
	return MetricsSnapshot{
		Queries:      e.metrics.queries.Load(),
		Ingests:      e.metrics.ingests.Load(),
		CellsRouted:  e.metrics.cellsRouted.Load(),
		CrossNodeRPC: e.metrics.crossNodeRPC.Load(),
		CellsHit:     e.metrics.cellsHit.Load(),
	}
}
