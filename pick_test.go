package gridray

import (
	"testing"
	"time"
)

func solidRowChunk(x0, w, y int, solidAt ...int) Chunk {
	states := make([]CellState, w)
	for i := range states {
		states[i] = Empty
	}
	for _, x := range solidAt {
		states[x-x0] = Solid
	}
	return NewChunk(Cell{x0, y}, w, 1, states)
}

func statuses(q *Query) []PickStatus {
	res := q.Result()
	out := make([]PickStatus, len(res.Cells))
	for i, c := range res.Cells {
		out[i] = c.Status
	}
	return out
}

func TestPickPendingThenResolve(t *testing.T) {
	s := NewScene()
	// Ray along y=0.5 from x=0.5 to x=5.5 crosses cells (0..5, 0).
	q := s.NewQuery(R(1, 2, 1, 2), R(11, 2, 1, 2))
	defer q.Close()

	res := q.Result()
	if !res.Pending || res.Hit != nil || len(res.Cells) != 6 {
		t.Fatalf("initial: %+v", res)
	}
	for _, c := range res.Cells {
		if c.Status != PickPending {
			t.Fatalf("initial cell should be pending: %+v", c)
		}
	}

	// First chunk: cells (0..2, 0) all empty. Still pending beyond.
	if !s.DeliverChunk(s.Version(), solidRowChunk(0, 3, 0)) {
		t.Fatal("chunk rejected")
	}
	res = q.Result()
	if !res.Pending || res.Hit != nil {
		t.Fatalf("after first chunk: %+v", res)
	}
	want := []PickStatus{PickEmpty, PickEmpty, PickEmpty, PickPending, PickPending, PickPending}
	for i, st := range statuses(q) {
		if st != want[i] {
			t.Fatalf("status at %d: got %v want %v (%v)", i, st, want[i], statuses(q))
		}
	}

	// Second chunk: cells (3..5, 0), solid at (4, 0). Hit resolves.
	if !s.DeliverChunk(s.Version(), solidRowChunk(3, 3, 0, 4)) {
		t.Fatal("chunk rejected")
	}
	res = q.Result()
	if res.Pending || res.Hit == nil || *res.Hit != (Cell{4, 0}) {
		t.Fatalf("resolved: %+v", res)
	}
	// Evidence: empties (0..3, 0) then the hit; cell (5, 0) is occluded.
	if len(res.Cells) != 5 {
		t.Fatalf("evidence: %+v", res.Cells)
	}
	for i := 0; i < 4; i++ {
		if res.Cells[i].Status != PickEmpty || res.Cells[i].Cell != (Cell{i, 0}) {
			t.Fatalf("evidence cell %d: %+v", i, res.Cells[i])
		}
	}
	if res.Cells[4].Status != PickHit {
		t.Fatalf("hit cell: %+v", res.Cells[4])
	}
}

func TestPickRetractsStaleCandidate(t *testing.T) {
	s := NewScene()
	q := s.NewQuery(R(1, 2, 1, 2), R(11, 2, 1, 2))
	defer q.Close()

	// Cells (0,0),(1,0) unknown; (2,0) solid -> tentative hit, pending.
	if !s.DeliverChunk(s.Version(), solidRowChunk(2, 1, 0, 2)) {
		t.Fatal("chunk rejected")
	}
	res := q.Result()
	if !res.Pending || res.Hit == nil || *res.Hit != (Cell{2, 0}) {
		t.Fatalf("tentative: %+v", res)
	}

	// The missing chunk reveals (1,0) solid: the old candidate at (2,0)
	// is retracted in favor of the earlier hit.
	if !s.DeliverChunk(s.Version(), solidRowChunk(0, 2, 0, 1)) {
		t.Fatal("chunk rejected")
	}
	res = q.Result()
	if res.Pending || res.Hit == nil || *res.Hit != (Cell{1, 0}) {
		t.Fatalf("resolved: %+v", res)
	}
	if res.Retracted == nil || *res.Retracted != (Cell{2, 0}) {
		t.Fatalf("expected retraction of (2,0): %+v", res)
	}
}

func TestPickCandidateTurnsEmpty(t *testing.T) {
	s := NewScene()
	q := s.NewQuery(R(1, 2, 1, 2), R(7, 2, 1, 2))
	defer q.Close()

	// Tentative hit at (1,0) while (0,0) is unknown.
	s.DeliverChunk(s.Version(), solidRowChunk(1, 3, 0, 1))
	if got := q.Result().Hit; got == nil || *got != (Cell{1, 0}) {
		t.Fatalf("tentative: %+v", q.Result())
	}
	// Correction: (1,0) is actually empty, (2,0) is solid.
	s.DeliverChunk(s.Version(), solidRowChunk(0, 4, 0, 2))
	res := q.Result()
	if res.Hit == nil || *res.Hit != (Cell{2, 0}) {
		t.Fatalf("resolved: %+v", res)
	}
	if res.Retracted == nil || *res.Retracted != (Cell{1, 0}) {
		t.Fatalf("expected retraction of (1,0): %+v", res)
	}
}

func TestStaleChunkNeverAffectsNewQuery(t *testing.T) {
	s := NewScene()
	q1 := s.NewQuery(R(1, 2, 1, 2), R(7, 2, 1, 2))
	defer q1.Close()
	v0 := s.Version()

	// World edit bumps the version; in-flight chunk requests for v0
	// become stale.
	s.SetCell(Cell{0, 0}, Empty)
	q2 := s.NewQuery(R(1, 2, 1, 2), R(7, 2, 1, 2))
	defer q2.Close()
	before := q2.Result()

	// A stale v0 response must be dropped and must not change q2.
	if s.DeliverChunk(v0, solidRowChunk(1, 3, 0, 2)) {
		t.Fatal("stale chunk accepted")
	}
	if !equalPick(before, q2.Result()) {
		t.Fatalf("stale chunk changed new query: %+v", q2.Result())
	}
	if q2.Result().Version != s.Version() {
		t.Fatalf("query bound to %d, scene at %d", q2.Result().Version, s.Version())
	}

	// The same chunk re-issued at the current version applies.
	if !s.DeliverChunk(s.Version(), solidRowChunk(1, 3, 0, 2)) {
		t.Fatal("current chunk rejected")
	}
	res := q2.Result()
	if res.Hit == nil || *res.Hit != (Cell{2, 0}) {
		t.Fatalf("after current chunk: %+v", res)
	}
	// q1 is bound to the old version and receives no further updates.
	if q1.Result().Seq != 0 {
		t.Fatalf("old query advanced: %+v", q1.Result())
	}
}

func TestPickAsyncDelivery(t *testing.T) {
	s := NewScene()
	q := s.NewQuery(R(1, 2, 1, 2), R(11, 2, 1, 2))
	defer q.Close()

	go func() {
		time.Sleep(10 * time.Millisecond)
		s.DeliverChunk(s.Version(), solidRowChunk(0, 3, 0))
		time.Sleep(10 * time.Millisecond)
		s.DeliverChunk(s.Version(), solidRowChunk(3, 3, 0, 5))
	}()

	deadline := time.After(2 * time.Second)
	var last PickResult
	for {
		select {
		case r, ok := <-q.Updates():
			if !ok {
				return
			}
			last = r
			if !last.Pending && last.Hit != nil {
				if *last.Hit != (Cell{5, 0}) {
					t.Fatalf("hit: %+v", last)
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out; last=%+v", last)
		}
	}
}

func TestPickEvidenceMatchesTraversal(t *testing.T) {
	s := NewScene()
	// Diagonal ray through vertices; solid cell deep in the grid.
	s.SetCell(Cell{3, 3}, Solid)
	p0, p1 := R(1, 2, 1, 2), R(9, 2, 9, 2)
	q := s.NewQuery(p0, p1)
	defer q.Close()

	order := SegmentCells(p0, p1)
	res := q.Result()
	if res.Hit == nil || *res.Hit != (Cell{3, 3}) {
		t.Fatalf("hit: %+v", res)
	}
	// Evidence must be exactly the traversal prefix up to the hit.
	if len(res.Cells) == 0 || res.Cells[len(res.Cells)-1].Cell != (Cell{3, 3}) {
		t.Fatalf("evidence: %+v", res.Cells)
	}
	for i, pc := range res.Cells {
		if pc.Cell != order[i].Cell || pc.T.Cmp(order[i].T) != 0 {
			t.Fatalf("evidence diverges from traversal at %d: %+v vs %+v", i, pc, order[i])
		}
	}
}

func TestPickZeroLengthRay(t *testing.T) {
	s := NewScene()
	s.SetCell(Cell{0, 0}, Solid)
	// Zero-length ray on the vertex (1,1): all four adjacent cells are
	// candidates; (0,0) sorts first and hits immediately.
	q := s.NewQuery(V(1, 1), V(1, 1))
	defer q.Close()
	res := q.Result()
	if res.Hit == nil || *res.Hit != (Cell{0, 0}) || res.Pending {
		t.Fatalf("zero-length pick: %+v", res)
	}
	if len(res.Cells) != 1 || res.Cells[0].Status != PickHit {
		t.Fatalf("evidence: %+v", res.Cells)
	}
}
