package segstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// State is the persisted state of the commit state machine.
type State string

const (
	StateIdle       State = "idle"
	StateBuilding   State = "building"
	StateValidating State = "validating"
	StatePublishing State = "publishing"
	StateReclaiming State = "reclaiming"
)

// journal is the write-ahead record of one in-flight commit. It is written
// before the first mutation of a phase and is only removed once reclaim of
// the old segments has completed.
type journal struct {
	State       State    `json:"state"`
	Txn         uint64   `json:"txn"`
	NewSegment  string   `json:"new_segment"`
	Size        int64    `json:"size"`
	SHA256      string   `json:"sha256"`
	OldSegments []string `json:"old_segments,omitempty"`
}

const journalFile = "journal.json"

func (s *Store) readJournal() (*journal, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, journalFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("%w: unreadable journal: %v", ErrCorrupt, err)
	}
	return &j, nil
}

func (s *Store) writeJournal(j *journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.dir, journalFile, data)
}

func (s *Store) clearJournal() error {
	err := os.Remove(filepath.Join(s.dir, journalFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return syncDir(s.dir)
}
