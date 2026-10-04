package splitter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snapshot pins the committed pointer of every key at creation time, so GC
// keeps the referenced record copies alive until the snapshot is released.
// Snapshots are persisted and survive restarts.
type Snapshot struct {
	ID       string             `json:"id"`
	Pointers map[string]Pointer `json:"pointers"`
}

func (s *Store) snapshotDir() string { return filepath.Join(s.Dir, "snapshots") }

// BeginSnapshot captures the current committed pointers and returns its ID.
func (s *Store) BeginSnapshot() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), s.seq)
	snap := Snapshot{ID: id, Pointers: map[string]Pointer{}}
	for k, p := range s.pointers {
		snap.Pointers[k] = p
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(filepath.Join(s.snapshotDir(), id+".json"), data); err != nil {
		return "", err
	}
	return id, nil
}

// ReleaseSnapshot removes a persisted snapshot; unknown IDs are ignored.
func (s *Store) ReleaseSnapshot(id string) error {
	err := os.Remove(filepath.Join(s.snapshotDir(), id+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Snapshots lists active snapshot IDs.
func (s *Store) Snapshots() ([]string, error) {
	snaps, err := s.loadSnapshots()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(snaps))
	for id := range snaps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) loadSnapshots() (map[string]Snapshot, error) {
	files, err := os.ReadDir(s.snapshotDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]Snapshot{}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.snapshotDir(), f.Name()))
		if err != nil {
			return nil, err
		}
		var snap Snapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			return nil, fmt.Errorf("corrupt snapshot %s: %v", f.Name(), err)
		}
		out[snap.ID] = snap
	}
	return out, nil
}
