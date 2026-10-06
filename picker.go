package gridray

import (
	"math/big"
	"sync"
)

// ChunkSize is the edge length, in cells, of a square voxel chunk.
const ChunkSize int64 = 16

// ChunkCoord identifies a chunk in the voxel grid.
type ChunkCoord struct {
	X, Y int64
}

// ChunkOf returns the coordinate of the chunk containing cell c.
func ChunkOf(c Cell) ChunkCoord {
	return ChunkCoord{floorDiv(c.X, ChunkSize), floorDiv(c.Y, ChunkSize)}
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// Chunk holds voxel occupancy for one chunk. Solid maps global cell
// coordinates to occupancy; cells absent from the map are empty.
type Chunk struct {
	Solid map[Cell]bool
}

// Status is the resolution state of a pick query.
type Status int

const (
	// Pending means the ray entered a chunk whose data has not arrived;
	// the result is tentative and may be revised.
	Pending Status = iota
	// Hit means a solid cell was found.
	Hit
	// Miss means the whole segment was proven empty.
	Miss
)

func (s Status) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Hit:
		return "Hit"
	default:
		return "Miss"
	}
}

// Snapshot is an immutable view of a query's current result.
type Snapshot struct {
	Status Status
	// Evidence lists every cell before the hit (in traversal order) that
	// is proven empty. It is incomplete while Status is Pending.
	Evidence []Visit
	// HitCell and HitT describe the first solid cell, when Status is Hit.
	HitCell Cell
	HitT    *big.Rat
	// PendingCell is the unknown cell currently blocking the query.
	// It is a candidate, not evidence, and may be retracted when its
	// chunk arrives.
	PendingCell *Visit
}

// Query is a single voxel pick bound to a scene version.
type Query struct {
	picker  *Picker
	Version uint64
	visits  []Visit
	next    int

	status      Status
	evidence    []Visit
	hitCell     Cell
	hitT        *big.Rat
	pendingCell *Visit
}

// Snapshot returns the query's current result.
func (q *Query) Snapshot() Snapshot {
	q.picker.mu.Lock()
	defer q.picker.mu.Unlock()
	return q.snapshotLocked()
}

func (q *Query) snapshotLocked() Snapshot {
	s := Snapshot{
		Status:   q.status,
		Evidence: append([]Visit(nil), q.evidence...),
		HitCell:  q.hitCell,
	}
	if q.hitT != nil {
		s.HitT = new(big.Rat).Set(q.hitT)
	}
	if q.pendingCell != nil {
		v := *q.pendingCell
		v.T = new(big.Rat).Set(v.T)
		s.PendingCell = &v
	}
	return s
}

// Picker performs voxel picks over a scene whose chunks arrive
// asynchronously. Every query is bound to the scene version at creation;
// chunk deliveries tagged with a different version never affect it.
type Picker struct {
	mu      sync.Mutex
	version uint64
	// store[version][coord] holds the chunk data known for that version.
	store map[uint64]map[ChunkCoord]*Chunk
	// waiting[coord] holds queries blocked on an unknown chunk.
	waiting map[ChunkCoord][]*Query
}

// NewPicker returns an empty picker at scene version 0.
func NewPicker() *Picker {
	return &Picker{
		store:   map[uint64]map[ChunkCoord]*Chunk{0: {}},
		waiting: map[ChunkCoord][]*Query{},
	}
}

// Version returns the current scene version.
func (p *Picker) Version() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.version
}

// UpdateScene bumps the scene version and applies edits to the new
// version's chunk map, which starts as a copy of the previous version's.
// Queries already bound to older versions are unaffected.
func (p *Picker) UpdateScene(edit func(set func(ChunkCoord, *Chunk))) {
	p.mu.Lock()
	defer p.mu.Unlock()
	next := make(map[ChunkCoord]*Chunk, len(p.store[p.version]))
	for c, ch := range p.store[p.version] {
		next[c] = ch
	}
	p.version++
	p.store[p.version] = next
	if edit != nil {
		edit(func(c ChunkCoord, ch *Chunk) {
			if ch == nil {
				delete(next, c)
			} else {
				next[c] = ch
			}
		})
	}
}

// DeliverChunk delivers asynchronously fetched chunk data tagged with the
// scene version it was fetched for. Queries bound to that version and
// blocked on the chunk are re-evaluated; queries bound to other versions
// are unaffected, so stale responses never change new queries.
func (p *Picker) DeliverChunk(coord ChunkCoord, ch *Chunk, version uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.store[version]
	if !ok {
		m = map[ChunkCoord]*Chunk{}
		p.store[version] = m
	}
	m[coord] = ch
	qs := p.waiting[coord]
	var keep []*Query
	for _, q := range qs {
		if q.Version == version {
			q.advance(p)
		} else {
			keep = append(keep, q)
		}
	}
	if len(keep) == 0 {
		delete(p.waiting, coord)
	} else {
		p.waiting[coord] = keep
	}
}

// BeginQuery starts a pick along the segment from p0 to p1, bound to the
// current scene version.
func (p *Picker) BeginQuery(p0, p1 Point) *Query {
	p.mu.Lock()
	defer p.mu.Unlock()
	q := &Query{
		picker:  p,
		Version: p.version,
		visits:  TraverseSegment(p0, p1),
	}
	q.advance(p)
	return q
}

// advance walks the traversal order until it hits a solid cell, runs out
// of cells, or is blocked by an unknown chunk. Unknown cells are never
// skipped: the query pends on them instead.
func (q *Query) advance(p *Picker) {
	q.pendingCell = nil
	for q.next < len(q.visits) {
		v := q.visits[q.next]
		ch, known := p.store[q.Version][ChunkOf(v.Cell)]
		if !known {
			q.status = Pending
			vc := v
			q.pendingCell = &vc
			coord := ChunkOf(v.Cell)
			p.waiting[coord] = append(p.waiting[coord], q)
			return
		}
		if ch.Solid[v.Cell] {
			q.status = Hit
			q.hitCell = v.Cell
			q.hitT = v.T
			q.next++
			return
		}
		q.evidence = append(q.evidence, v)
		q.next++
	}
	q.status = Miss
}
