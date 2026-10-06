package gridray

import (
	"math/big"
	"sync"
)

// CellState describes whether a cell's content is known, and if so,
// whether it is solid.
type CellState int

const (
	Unknown CellState = iota
	Empty
	Solid
)

// Chunk is a rectangular block of known cells delivered asynchronously,
// e.g. by a streaming world loader. Cells is row-major with W*H entries;
// only Empty and Solid entries are applied.
type Chunk struct {
	Origin Cell // minimum corner
	W, H   int
	Cells  []CellState
}

// NewChunk builds a chunk from a row-major state slice of length w*h.
func NewChunk(origin Cell, w, h int, states []CellState) Chunk {
	return Chunk{Origin: origin, W: w, H: h, Cells: states}
}

// Scene is a versioned store of known cell states. The version increments
// on every direct edit; asynchronous chunk responses are tagged with the
// version their request was issued at and are dropped when stale.
type Scene struct {
	mu      sync.Mutex
	version uint64
	cells   map[Cell]CellState
	queries map[*Query]struct{}
}

// NewScene returns an empty scene at version 0.
func NewScene() *Scene {
	return &Scene{
		cells:   map[Cell]CellState{},
		queries: map[*Query]struct{}{},
	}
}

// Version returns the current scene version.
func (s *Scene) Version() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// SetCell records a known cell state and bumps the scene version. Any
// chunk response still in flight for an older version becomes stale.
func (s *Scene) SetCell(c Cell, st CellState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cells[c] = st
	s.version++
}

// DeliverChunk applies an asynchronous chunk response tagged with the
// scene version its request was issued at. Stale responses are dropped
// and reported as false; they never affect the scene or any query.
func (s *Scene) DeliverChunk(reqVersion uint64, ch Chunk) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if reqVersion != s.version {
		return false
	}
	for y := 0; y < ch.H; y++ {
		for x := 0; x < ch.W; x++ {
			st := ch.Cells[y*ch.W+x]
			if st == Unknown {
				continue
			}
			s.cells[Cell{ch.Origin.X + x, ch.Origin.Y + y}] = st
		}
	}
	s.notifyLocked()
	return true
}

// notifyLocked recomputes every live query bound to the current version.
// Callers must hold s.mu.
func (s *Scene) notifyLocked() {
	for q := range s.queries {
		if q.version == s.version {
			q.refreshLocked()
		}
	}
}

// PickStatus is the evidentiary role of a cell in a PickResult.
type PickStatus int

const (
	// PickEmpty is a cell proven empty by the scene.
	PickEmpty PickStatus = iota
	// PickPending is a cell whose chunk has not arrived yet.
	PickPending
	// PickHit is the first solid cell along the ray.
	PickHit
)

// PickCell is one cell of pick evidence, in traversal order.
type PickCell struct {
	Cell   Cell
	T      *big.Rat
	Status PickStatus
}

// PickResult is a snapshot of a pick query: the first hit (if any) and
// every evidence cell preceding it in traversal order. While unknown
// cells precede the hit, the result is Pending and the hit is only a
// candidate; a later snapshot may retract it.
type PickResult struct {
	Version uint64
	Seq     uint64
	Cells   []PickCell
	// Hit is the first solid cell in traversal order, or nil. It is
	// tentative while Pending is true.
	Hit *Cell
	// Pending is true while unknown cells remain within the evidence.
	Pending bool
	// Retracted is the previous snapshot's hit candidate that no longer
	// stands, if any.
	Retracted *Cell
}

// Query is a pick bound to the scene version at creation time. Chunk
// responses tagged with older versions never change it. The traversal
// order is fixed at creation from SegmentCells.
type Query struct {
	scene   *Scene
	version uint64
	order   []CellHit

	mu      sync.Mutex
	seq     uint64
	result  PickResult
	updates chan PickResult
	closed  bool
}

// NewQuery casts the segment p0 -> p1 through the scene and binds the
// query to the current scene version.
func (s *Scene) NewQuery(p0, p1 Vec) *Query {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := &Query{
		scene:   s,
		version: s.version,
		order:   SegmentCells(p0, p1),
		updates: make(chan PickResult, 1),
	}
	q.result = q.computeLocked()
	s.queries[q] = struct{}{}
	return q
}

// Result returns the latest snapshot.
func (q *Query) Result() PickResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.result
}

// Updates yields a snapshot whenever the result changes. The channel
// coalesces: a slow consumer always finds the newest snapshot.
func (q *Query) Updates() <-chan PickResult {
	return q.updates
}

// Close detaches the query from the scene and closes its update channel.
func (q *Query) Close() {
	q.scene.mu.Lock()
	delete(q.scene.queries, q)
	q.scene.mu.Unlock()
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.updates)
	}
	q.mu.Unlock()
}

// refreshLocked recomputes the snapshot and publishes it when changed.
// Callers must hold the scene lock.
func (q *Query) refreshLocked() {
	next := q.computeLocked()
	q.mu.Lock()
	if equalPick(q.result, next) {
		q.mu.Unlock()
		return
	}
	if q.result.Hit != nil && (next.Hit == nil || *next.Hit != *q.result.Hit) {
		c := *q.result.Hit
		next.Retracted = &c
	}
	q.seq++
	next.Seq = q.seq
	q.result = next
	updates, closed := q.updates, q.closed
	q.mu.Unlock()
	if !closed {
		select {
		case updates <- next:
		default:
			<-updates
			updates <- next
		}
	}
}

// computeLocked builds a snapshot from the current scene state.
// Callers must hold the scene lock.
func (q *Query) computeLocked() PickResult {
	cells := q.scene.cells
	hitIdx := -1
	for i, h := range q.order {
		if cells[h.Cell] == Solid {
			hitIdx = i
			break
		}
	}
	n := len(q.order)
	if hitIdx >= 0 {
		n = hitIdx + 1
	}
	res := PickResult{Version: q.version, Cells: make([]PickCell, n)}
	for i := 0; i < n; i++ {
		h := q.order[i]
		pc := PickCell{Cell: h.Cell, T: h.T}
		switch {
		case i == hitIdx:
			pc.Status = PickHit
		case cells[h.Cell] == Unknown:
			pc.Status = PickPending
			res.Pending = true
		default:
			pc.Status = PickEmpty
		}
		res.Cells[i] = pc
	}
	if hitIdx >= 0 {
		c := q.order[hitIdx].Cell
		res.Hit = &c
	}
	return res
}

func equalPick(a, b PickResult) bool {
	if (a.Hit == nil) != (b.Hit == nil) {
		return false
	}
	if a.Hit != nil && *a.Hit != *b.Hit {
		return false
	}
	if a.Pending != b.Pending || len(a.Cells) != len(b.Cells) {
		return false
	}
	for i := range a.Cells {
		if a.Cells[i].Cell != b.Cells[i].Cell || a.Cells[i].Status != b.Cells[i].Status {
			return false
		}
	}
	return true
}
