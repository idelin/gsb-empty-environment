package hexgrid

import "fmt"

// Reference is a flat, non-partitioned, versioned map. It runs the exact
// same BFS core as the partitioned Store and exists to prove that
// partitioning never changes query results.
type Reference struct {
	version uint64
	history map[uint64]map[Hex]bool
}

// NewReference builds the reference map at version 1.
func NewReference(walkable, obstacles []Hex) *Reference {
	cells := map[Hex]bool{}
	for _, h := range walkable {
		cells[h] = true
	}
	for _, h := range obstacles {
		cells[h] = false
	}
	return &Reference{version: 1, history: map[uint64]map[Hex]bool{1: cells}}
}

// Version returns the current version.
func (r *Reference) Version() uint64 { return r.version }

// Update applies a batch of walkability changes and bumps the version.
func (r *Reference) Update(changes map[Hex]bool) uint64 {
	r.version++
	cells := make(map[Hex]bool, len(r.history[r.version-1]))
	for h, w := range r.history[r.version-1] {
		cells[h] = w
	}
	for h, w := range changes {
		cells[h] = w
	}
	r.history[r.version] = cells
	return r.version
}

// Query runs the same BFS core as Store.Query, without partitions.
func (r *Reference) Query(q Query) (*Result, error) {
	version := q.Version
	if version == 0 {
		version = r.version
	}
	cells, ok := r.history[version]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrUnknownVersion, version)
	}
	return runQuery(q, version, func(h Hex) (bool, error) { return cells[h], nil }, &Stats{})
}
