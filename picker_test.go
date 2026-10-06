package gridray

import (
	"math/big"
	"math/rand"
	"testing"
)

func emptyChunk() *Chunk { return &Chunk{Solid: map[Cell]bool{}} }

func chunkWith(cells ...Cell) *Chunk {
	c := emptyChunk()
	for _, cell := range cells {
		c.Solid[cell] = true
	}
	return c
}

func evidenceCells(s Snapshot) []Cell {
	var out []Cell
	for _, v := range s.Evidence {
		out = append(out, v.Cell)
	}
	return out
}

// A ray that crosses into an unknown chunk must pend on the first unknown
// cell instead of skipping it; proven-empty cells before it are evidence.
func TestPickPendingNotSkipped(t *testing.T) {
	p := NewPicker()
	p.DeliverChunk(ChunkCoord{0, 0}, emptyChunk(), p.Version())

	q := p.BeginQuery(PtFrac(1, 2, 1, 2), PtFrac(41, 2, 1, 2))
	s := q.Snapshot()
	if s.Status != Pending {
		t.Fatalf("status = %v, want Pending", s.Status)
	}
	if s.PendingCell == nil || s.PendingCell.Cell != (Cell{16, 0}) {
		t.Fatalf("pending cell = %v, want (16,0)", s.PendingCell)
	}
	ev := evidenceCells(s)
	if len(ev) != 16 {
		t.Fatalf("evidence has %d cells, want 16: %v", len(ev), ev)
	}
	for i, c := range ev {
		if c != (Cell{int64(i), 0}) {
			t.Fatalf("evidence[%d] = %v, want (%d,0)", i, c, i)
		}
	}
	for _, c := range ev {
		if c == (Cell{16, 0}) {
			t.Fatal("unknown cell leaked into evidence")
		}
	}
}

// When the missing chunk arrives, the old candidate is retracted: an empty
// candidate becomes evidence and the query advances to the real hit.
func TestPickCandidateRetractedOnDelivery(t *testing.T) {
	p := NewPicker()
	p.DeliverChunk(ChunkCoord{0, 0}, emptyChunk(), p.Version())

	q := p.BeginQuery(PtFrac(1, 2, 1, 2), PtFrac(41, 2, 1, 2))
	if q.Snapshot().PendingCell.Cell != (Cell{16, 0}) {
		t.Fatal("expected pending candidate (16,0)")
	}

	p.DeliverChunk(ChunkCoord{1, 0}, chunkWith(Cell{18, 0}), 0)
	s := q.Snapshot()
	if s.Status != Hit || s.HitCell != (Cell{18, 0}) {
		t.Fatalf("status=%v hit=%v, want Hit (18,0)", s.Status, s.HitCell)
	}
	if s.PendingCell != nil {
		t.Fatalf("stale candidate %v not retracted", s.PendingCell)
	}
	ev := evidenceCells(s)
	if len(ev) != 18 || ev[16] != (Cell{16, 0}) || ev[17] != (Cell{17, 0}) {
		t.Fatalf("evidence = %v, want cells (0,0)..(17,0)", ev)
	}
	if s.HitT.Cmp(big.NewRat(35, 40)) != 0 { // x=18 at t=(18-.5)/20
		t.Fatalf("hit t = %s, want 35/40", s.HitT)
	}
}

// Queries are bound to the scene version: a stale chunk response must not
// change a newer query, while the older query still resolves.
func TestPickVersionBinding(t *testing.T) {
	p := NewPicker()
	p.DeliverChunk(ChunkCoord{0, 0}, emptyChunk(), 0)

	p.UpdateScene(nil) // version 1
	q1 := p.BeginQuery(PtFrac(1, 2, 1, 2), PtFrac(41, 2, 1, 2))
	if q1.Snapshot().Status != Pending {
		t.Fatal("q1 should pend on unknown chunk (1,0)")
	}

	p.UpdateScene(nil) // version 2
	q2 := p.BeginQuery(PtFrac(1, 2, 1, 2), PtFrac(41, 2, 1, 2))
	if q2.Snapshot().Status != Pending {
		t.Fatal("q2 should pend on unknown chunk (1,0)")
	}

	// Stale response for version 1: resolves q1, must not touch q2.
	p.DeliverChunk(ChunkCoord{1, 0}, chunkWith(Cell{17, 0}), 1)
	if s := q1.Snapshot(); s.Status != Hit || s.HitCell != (Cell{17, 0}) {
		t.Fatalf("q1 status=%v hit=%v, want Hit (17,0)", s.Status, s.HitCell)
	}
	if s := q2.Snapshot(); s.Status != Pending || s.PendingCell.Cell != (Cell{16, 0}) {
		t.Fatalf("q2 changed by stale response: status=%v pending=%v", s.Status, s.PendingCell)
	}

	// The version-2 response shows the chunk empty: q2 walks past it.
	p.DeliverChunk(ChunkCoord{1, 0}, emptyChunk(), 2)
	s := q2.Snapshot()
	if s.Status != Miss {
		t.Fatalf("q2 status = %v, want Miss", s.Status)
	}
	if n := len(s.Evidence); n != 21 {
		t.Fatalf("q2 evidence has %d cells, want 21", n)
	}
}

// Scene edits apply only to the new version.
func TestPickSceneUpdateIsolation(t *testing.T) {
	p := NewPicker()
	p.DeliverChunk(ChunkCoord{0, 0}, chunkWith(Cell{2, 0}), 0)

	q0 := p.BeginQuery(PtFrac(1, 2, 1, 2), PtFrac(9, 2, 1, 2))
	if s := q0.Snapshot(); s.Status != Hit || s.HitCell != (Cell{2, 0}) {
		t.Fatalf("q0 = %v %v, want Hit (2,0)", s.Status, s.HitCell)
	}

	p.UpdateScene(func(set func(ChunkCoord, *Chunk)) {
		set(ChunkCoord{0, 0}, chunkWith(Cell{4, 0}))
	})
	q1 := p.BeginQuery(PtFrac(1, 2, 1, 2), PtFrac(9, 2, 1, 2))
	if s := q1.Snapshot(); s.Status != Hit || s.HitCell != (Cell{4, 0}) {
		t.Fatalf("q1 = %v %v, want Hit (4,0)", s.Status, s.HitCell)
	}
	// The old query keeps its old result.
	if s := q0.Snapshot(); s.HitCell != (Cell{2, 0}) {
		t.Fatalf("q0 mutated: %v", s.HitCell)
	}
}

// Fully loaded scene: hit and empty-cell evidence must match the
// clip-based reference traversal exactly.
func TestPickAgainstClipping(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 300; i++ {
		p := NewPicker()
		solid := map[Cell]bool{}
		for _, c := range []Cell{{int64(rng.Intn(8) - 4), int64(rng.Intn(8) - 4)}, {int64(rng.Intn(8) - 4), int64(rng.Intn(8) - 4)}} {
			solid[c] = true
		}
		for cx := int64(-1); cx <= 0; cx++ {
			for cy := int64(-1); cy <= 0; cy++ {
				ch := emptyChunk()
				for c := range solid {
					if ChunkOf(c) == (ChunkCoord{cx, cy}) {
						ch.Solid[c] = true
					}
				}
				p.DeliverChunk(ChunkCoord{cx, cy}, ch, 0)
			}
		}
		p0 := PtFrac(int64(rng.Intn(13)-6), 2, int64(rng.Intn(13)-6), 2)
		p1 := PtFrac(int64(rng.Intn(13)-6), 2, int64(rng.Intn(13)-6), 2)

		q := p.BeginQuery(p0, p1)
		s := q.Snapshot()

		ref := TraverseByClipping(p0, p1)
		var wantHit *Visit
		var wantEvidence []Visit
		for j := range ref {
			if solid[ref[j].Cell] {
				wantHit = &ref[j]
				break
			}
			wantEvidence = append(wantEvidence, ref[j])
		}
		if wantHit == nil {
			if s.Status != Miss {
				t.Fatalf("iter %d: status=%v, want Miss", i, s.Status)
			}
		} else {
			if s.Status != Hit || s.HitCell != wantHit.Cell || s.HitT.Cmp(wantHit.T) != 0 {
				t.Fatalf("iter %d: hit=(%v,%s), want (%v,%s)", i, s.HitCell, s.HitT, wantHit.Cell, wantHit.T)
			}
		}
		if len(s.Evidence) != len(wantEvidence) {
			t.Fatalf("iter %d: evidence has %d cells, want %d", i, len(s.Evidence), len(wantEvidence))
		}
		for j := range wantEvidence {
			if s.Evidence[j].Cell != wantEvidence[j].Cell || s.Evidence[j].T.Cmp(wantEvidence[j].T) != 0 {
				t.Fatalf("iter %d evidence[%d] = (%v,%s), want (%v,%s)",
					i, j, s.Evidence[j].Cell, s.Evidence[j].T, wantEvidence[j].Cell, wantEvidence[j].T)
			}
		}
	}
}
