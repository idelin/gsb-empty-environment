package logtrunc

import (
	"fmt"
	"sort"
	"sync"
)

// Snapshot pins a historical sequence range [From, To]. Any segment
// overlapping the range must be retained until the snapshot is released.
type Snapshot struct {
	ID   string
	From uint64
	To   uint64 // inclusive
}

// Snapshots tracks live snapshots.
type Snapshots struct {
	mu    sync.Mutex
	snaps map[string]Snapshot
}

// NewSnapshots creates an empty snapshot set.
func NewSnapshots() *Snapshots { return &Snapshots{snaps: map[string]Snapshot{}} }

// Pin registers a snapshot over [from, to].
func (s *Snapshots) Pin(id string, from, to uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.snaps[id]; ok {
		return fmt.Errorf("logtrunc: snapshot %q already pinned", id)
	}
	if from == 0 || to < from {
		return fmt.Errorf("logtrunc: invalid snapshot range [%d,%d]", from, to)
	}
	s.snaps[id] = Snapshot{ID: id, From: from, To: to}
	return nil
}

// Release drops a snapshot; unknown IDs are ignored.
func (s *Snapshots) Release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snaps, id)
}

// List returns the live snapshots ordered by ID.
func (s *Snapshots) List() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Snapshot, 0, len(s.snaps))
	for _, snap := range s.snaps {
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
