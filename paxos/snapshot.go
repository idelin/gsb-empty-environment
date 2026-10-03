package paxos

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ErrNoSnapshot is returned by Latest when the archive is empty.
var ErrNoSnapshot = errors.New("snapshot: no checkpoint or incremental found")

// Snapshot is a materialized view of the state machine at Index.
type Snapshot struct {
	Index uint64
	State map[string]string
}

type checkpointFile struct {
	Index uint64            `json:"index"`
	State map[string]string `json:"state"`
}

type incrementalFile struct {
	From uint64 `json:"from"`
	To   uint64 `json:"to"`
	Ops  []Op   `json:"ops"`
}

// Snapshotter archives checkpoints (full state) and incremental snapshots
// (op deltas since the last checkpoint) as JSON files in a directory.
//
// Layout:
//
//	checkpoint-<index:020d>.json   full state at <index>
//	inc-<from:020d>-<to:020d>.json ops covering (from, to]
//
// All writes are atomic (tmp file + rename). Restore chains the newest
// usable checkpoint with every incremental on top of it. A Snapshotter
// spawns no goroutines; callers drive it.
type Snapshotter struct {
	mu     sync.Mutex
	dir    string
	retain int // how many checkpoints to keep; <=0 means keep all
}

func NewSnapshotter(dir string, retain int) (*Snapshotter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Snapshotter{dir: dir, retain: retain}, nil
}

func checkpointName(index uint64) string {
	return fmt.Sprintf("checkpoint-%020d.json", index)
}

func incrementalName(from, to uint64) string {
	return fmt.Sprintf("inc-%020d-%020d.json", from, to)
}

// writeAtomic persists data via a temp file + rename so a crash never
// leaves a half-written snapshot visible.
func (s *Snapshotter) writeAtomic(name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, name))
}

// WriteCheckpoint archives a full-state checkpoint and prunes anything the
// retention policy no longer needs.
func (s *Snapshotter) WriteCheckpoint(index uint64, state map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := checkpointFile{Index: index, State: state}
	if cp.State == nil {
		cp.State = map[string]string{}
	}
	if err := s.writeAtomic(checkpointName(index), cp); err != nil {
		return err
	}
	return s.pruneLocked()
}

// WriteIncremental archives the ops covering (from, to].
func (s *Snapshotter) WriteIncremental(from, to uint64, ops []Op) error {
	if to <= from {
		return fmt.Errorf("snapshot: invalid incremental range (%d, %d]", from, to)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inc := incrementalFile{From: from, To: to, Ops: ops}
	if inc.Ops == nil {
		inc.Ops = []Op{}
	}
	return s.writeAtomic(incrementalName(from, to), inc)
}

func parseCheckpointName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "checkpoint-") || !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	n, err := strconv.ParseUint(name[len("checkpoint-"):len(name)-len(".json")], 10, 64)
	return n, err == nil
}

func parseIncrementalName(name string) (from, to uint64, ok bool) {
	if !strings.HasPrefix(name, "inc-") || !strings.HasSuffix(name, ".json") {
		return 0, 0, false
	}
	parts := strings.Split(name[len("inc-"):len(name)-len(".json")], "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	f, err1 := strconv.ParseUint(parts[0], 10, 64)
	t, err2 := strconv.ParseUint(parts[1], 10, 64)
	return f, t, err1 == nil && err2 == nil
}

func (s *Snapshotter) listLocked() (checkpoints []uint64, incrementals [][2]uint64, err error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if idx, ok := parseCheckpointName(name); ok {
			checkpoints = append(checkpoints, idx)
		} else if from, to, ok := parseIncrementalName(name); ok {
			incrementals = append(incrementals, [2]uint64{from, to})
		}
	}
	sort.Slice(checkpoints, func(i, j int) bool { return checkpoints[i] < checkpoints[j] })
	sort.Slice(incrementals, func(i, j int) bool {
		if incrementals[i][0] != incrementals[j][0] {
			return incrementals[i][0] < incrementals[j][0]
		}
		return incrementals[i][1] < incrementals[j][1]
	})
	return checkpoints, incrementals, nil
}

// pruneLocked enforces the retention policy: keep the newest `retain`
// checkpoints and drop incrementals fully covered by the oldest retained
// checkpoint. Stray temp files from crashed writes are removed too.
func (s *Snapshotter) pruneLocked() error {
	if s.retain <= 0 {
		return nil
	}
	checkpoints, incrementals, err := s.listLocked()
	if err != nil {
		return err
	}
	var floor uint64
	if len(checkpoints) > s.retain {
		for _, idx := range checkpoints[:len(checkpoints)-s.retain] {
			if err := os.Remove(filepath.Join(s.dir, checkpointName(idx))); err != nil {
				return err
			}
		}
		floor = checkpoints[len(checkpoints)-s.retain]
	} else if len(checkpoints) > 0 {
		floor = checkpoints[0]
	}
	for _, inc := range incrementals {
		if inc[1] <= floor {
			if err := os.Remove(filepath.Join(s.dir, incrementalName(inc[0], inc[1]))); err != nil {
				return err
			}
		}
	}
	// Clean up orphaned temp files from interrupted writes.
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".tmp") {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
	return nil
}

// Latest loads the newest checkpoint and replays every incremental chained
// on top of it. With no checkpoint it chains incrementals from index 0.
// Returns ErrNoSnapshot for an empty archive.
func (s *Snapshotter) Latest() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	checkpoints, incrementals, err := s.listLocked()
	if err != nil {
		return Snapshot{}, err
	}
	if len(checkpoints) == 0 && len(incrementals) == 0 {
		return Snapshot{}, ErrNoSnapshot
	}

	snap := Snapshot{State: map[string]string{}}
	if len(checkpoints) > 0 {
		idx := checkpoints[len(checkpoints)-1]
		var cp checkpointFile
		if err := s.readJSON(checkpointName(idx), &cp); err != nil {
			return Snapshot{}, fmt.Errorf("snapshot: corrupt checkpoint %d: %w", idx, err)
		}
		snap.Index = cp.Index
		for k, v := range cp.State {
			snap.State[k] = v
		}
	}

	// Chain incrementals: repeatedly find the one starting at the current
	// index. A missing link just ends the chain (older files may be pruned).
	byFrom := map[uint64][2]uint64{}
	for _, inc := range incrementals {
		byFrom[inc[0]] = inc
	}
	for {
		inc, ok := byFrom[snap.Index]
		if !ok {
			break
		}
		var f incrementalFile
		if err := s.readJSON(incrementalName(inc[0], inc[1]), &f); err != nil {
			return Snapshot{}, fmt.Errorf("snapshot: corrupt incremental (%d,%d]: %w", inc[0], inc[1], err)
		}
		for _, op := range f.Ops {
			switch op.Kind {
			case OpSet:
				snap.State[op.Key] = op.Value
			case OpDel:
				delete(snap.State, op.Key)
			}
		}
		snap.Index = f.To
	}
	return snap, nil
}

func (s *Snapshotter) readJSON(name string, v any) error {
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Files lists the archived snapshot file names (sorted); used by tests and
// the demo to verify retention.
func (s *Snapshotter) Files() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
