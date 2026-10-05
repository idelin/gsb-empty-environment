package blockgc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Phase identifies the collector stage recorded in the journal, so an
// interrupted collection can be resumed safely after a crash.
type Phase int

const (
	// PhaseMark: snapshot taken, mark traversal in progress.
	PhaseMark Phase = iota
	// PhaseSweep: marking complete, quarantine list recorded. Blocks in the
	// quarantine list may already be quarantined in the store, but deletion
	// has NOT been confirmed: on recovery they must be restored.
	PhaseSweep
	// PhaseDelete: deletion confirmed (verification passed). Blocks in the
	// quarantine list may be deleted; on recovery deletion is completed.
	PhaseDelete
)

// State is the crash-recovery record persisted by the collector.
type State struct {
	Phase      Phase
	Snapshot   []BlockID // active heads captured at the start of the cycle
	Marked     []BlockID // blocks proven reachable
	Quarantine []BlockID // blocks selected for deletion
	// Confirmed is the deletion confirmation record: it is written only
	// after the quarantine list has been verified against all active roots.
	Confirmed bool
}

// Journal persists collector state so a crash mid-cycle can be resumed.
type Journal interface {
	// Load returns the last saved state, or (nil, nil) if none exists.
	Load() (*State, error)
	Save(*State) error
	Clear() error
}

// MemJournal is an in-memory Journal. AfterSave, if set, runs after every
// Save; tests use it to inject crashes at exact phase boundaries.
type MemJournal struct {
	mu        sync.Mutex
	state     *State
	AfterSave func(Phase)
}

// NewMemJournal returns an empty in-memory journal.
func NewMemJournal() *MemJournal { return &MemJournal{} }

// Load implements Journal.
func (j *MemJournal) Load() (*State, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state == nil {
		return nil, nil
	}
	cp := *j.state
	return &cp, nil
}

// Save implements Journal.
func (j *MemJournal) Save(s *State) error {
	j.mu.Lock()
	cp := *s
	j.state = &cp
	hook := j.AfterSave
	j.mu.Unlock()
	if hook != nil {
		hook(s.Phase)
	}
	return nil
}

// Clear implements Journal.
func (j *MemJournal) Clear() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.state = nil
	return nil
}

// FileJournal is a Journal backed by a JSON file, written atomically via
// rename so a crash cannot leave a torn record.
type FileJournal struct {
	path string
}

// NewFileJournal returns a Journal persisted at path.
func NewFileJournal(path string) *FileJournal { return &FileJournal{path: path} }

// Load implements Journal.
func (j *FileJournal) Load() (*State, error) {
	data, err := os.ReadFile(j.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save implements Journal.
func (j *FileJournal) Save(s *State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}

// Clear implements Journal.
func (j *FileJournal) Clear() error {
	err := os.Remove(j.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Path returns the journal file path.
func (j *FileJournal) Path() string { return filepath.Clean(j.path) }
