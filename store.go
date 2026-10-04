package sedt

import "fmt"

// Snapshot is an immutable version of an image plus its distance transform.
// Old snapshots are never mutated, so queries against them keep returning
// the old distances after later edits.
type Snapshot struct {
	ID    int
	W     int
	H     int
	Cells []bool // row-major pixel values
	Field []Cell // row-major transform results
}

// At returns the transform result for pixel (row, col).
func (s *Snapshot) At(row, col int) Cell {
	return s.Field[row*s.W+col]
}

// Change describes one pixel whose distance or nearest point changed.
type Change struct {
	Row int
	Col int
	Old Cell
	New Cell
}

// UpdateResult summarizes an incremental update.
type UpdateResult struct {
	SnapshotID int      // id of the newly created snapshot
	Examined   int      // number of pixels examined during recomputation
	Changed    []Change // pixels whose Dist2 or Src changed (row-major order)
}

// Store keeps every snapshot so old versions remain queryable.
type Store struct {
	snaps []*Snapshot
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{}
}

// Snapshot returns the snapshot with the given id.
func (s *Store) Snapshot(id int) (*Snapshot, error) {
	if id < 0 || id >= len(s.snaps) {
		return nil, fmt.Errorf("no snapshot %d", id)
	}
	return s.snaps[id], nil
}

// Latest returns the most recent snapshot, or nil if none exists.
func (s *Store) Latest() *Snapshot {
	if len(s.snaps) == 0 {
		return nil
	}
	return s.snaps[len(s.snaps)-1]
}

func (s *Store) commit(w, h int, cells []bool) *Snapshot {
	sn := &Snapshot{
		ID:    len(s.snaps),
		W:     w,
		H:     h,
		Cells: cells,
		Field: ComputeField(w, h, cells),
	}
	s.snaps = append(s.snaps, sn)
	return sn
}

// NewImage validates cells (length w*h) and creates snapshot 0 (or the next
// id). The input slice is copied.
func (s *Store) NewImage(w, h int, cells []bool) (*Snapshot, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("invalid dimensions %dx%d", w, h)
	}
	if len(cells) != w*h {
		return nil, fmt.Errorf("cells length %d, want %d", len(cells), w*h)
	}
	cp := make([]bool, len(cells))
	copy(cp, cells)
	return s.commit(w, h, cp), nil
}

// Flip toggles every pixel in the rectangle (x, y, w, h) of snapshot id and
// creates a new snapshot. The update recomputes the transform over the whole
// grid (the documented full-recompute fallback: deleting the unique nearest
// pixel can change every pixel, so the change region cannot be bounded to
// the edit box) and reports Examined = W*H. The returned Change list is the
// exact set of pixels whose distance or nearest point changed.
func (s *Store) Flip(id, x, y, rw, rh int) (*Snapshot, *UpdateResult, error) {
	old, err := s.Snapshot(id)
	if err != nil {
		return nil, nil, err
	}
	if x < 0 || y < 0 || rw < 0 || rh < 0 || x+rw > old.W || y+rh > old.H {
		return nil, nil, fmt.Errorf("rect (%d,%d,%d,%d) outside %dx%d image",
			x, y, rw, rh, old.W, old.H)
	}
	cells := make([]bool, len(old.Cells))
	copy(cells, old.Cells)
	for r := y; r < y+rh; r++ {
		for c := x; c < x+rw; c++ {
			i := r*old.W + c
			cells[i] = !cells[i]
		}
	}
	sn := s.commit(old.W, old.H, cells)

	res := &UpdateResult{SnapshotID: sn.ID, Examined: old.W * old.H}
	for i := range sn.Field {
		if old.Field[i] != sn.Field[i] {
			res.Changed = append(res.Changed, Change{
				Row: i / old.W,
				Col: i % old.W,
				Old: old.Field[i],
				New: sn.Field[i],
			})
		}
	}
	return sn, res, nil
}
