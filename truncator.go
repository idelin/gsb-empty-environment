package logtrunc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const journalFile = "truncation.journal"

// journalRecord is one line in the truncation journal.
type journalRecord struct {
	Type    string    `json:"type"` // "submit" | "deleted" | "done"
	ID      string    `json:"id"`
	Target  uint64    `json:"target,omitempty"`
	Segment SegmentID `json:"segment,omitempty"`
}

// Truncator executes truncation requests against a SegmentStore. Every
// request is journaled before work begins, so a crash never loses an
// unfinished request: reopening the Truncator on the same directory
// restores all pending requests, and failed deletions are retried until
// they succeed.
type Truncator struct {
	mu      sync.Mutex
	planner *Planner
	journal *os.File
	pending map[string]uint64 // request ID -> target
}

// OpenTruncator opens (or creates) the journal in dir and replays it.
// Requests that were submitted but never completed are restored as
// pending; call RetryPending to drive them to completion.
func OpenTruncator(dir string, planner *Planner) (*Truncator, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, journalFile)
	pending := map[string]uint64{}
	if data, err := os.ReadFile(path); err == nil {
		var rec journalRecord
		for _, line := range splitLines(data) {
			if err := json.Unmarshal(line, &rec); err != nil {
				return nil, fmt.Errorf("logtrunc: corrupt journal: %w", err)
			}
			switch rec.Type {
			case "submit":
				pending[rec.ID] = rec.Target
			case "done":
				delete(pending, rec.ID)
			case "deleted":
				// audit trail only; the plan is recomputed on retry
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Truncator{planner: planner, journal: f, pending: pending}, nil
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// Submit journals a truncation request and immediately attempts it. A
// non-nil error means the request did not finish; it stays pending and
// durable, and RetryPending (or a reopen) will resume it.
func (t *Truncator) Submit(id string, target uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.pending[id]; ok {
		return fmt.Errorf("logtrunc: request %q already pending", id)
	}
	if err := t.append(journalRecord{Type: "submit", ID: id, Target: target}); err != nil {
		return err
	}
	t.pending[id] = target
	return t.runLocked(id, target)
}

// RetryPending retries every unfinished request. It returns a joined
// error for the requests that still fail; those remain pending.
func (t *Truncator) RetryPending() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.pending))
	for id := range t.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		if err := t.runLocked(id, t.pending[id]); err != nil {
			errs = append(errs, fmt.Errorf("request %q: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// Pending returns the IDs of unfinished requests, sorted.
func (t *Truncator) Pending() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.pending))
	for id := range t.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Close flushes and closes the journal. Pending requests remain on disk.
func (t *Truncator) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.journal.Close()
}

// runLocked recomputes the plan and deletes deletable segments one by
// one, journaling each deletion. On any failure the request stays
// pending so a later retry resumes where it stopped.
func (t *Truncator) runLocked(id string, target uint64) error {
	plan := t.planner.Plan(target)
	for _, seg := range plan.Deletable {
		if err := t.planner.Store.Delete(seg.ID); err != nil && !errors.Is(err, ErrSegmentNotFound) {
			return err
		}
		if err := t.append(journalRecord{Type: "deleted", ID: id, Segment: seg.ID}); err != nil {
			return err
		}
	}
	if err := t.append(journalRecord{Type: "done", ID: id}); err != nil {
		return err
	}
	delete(t.pending, id)
	return nil
}

func (t *Truncator) append(rec journalRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := t.journal.Write(append(data, '\n')); err != nil {
		return err
	}
	return t.journal.Sync()
}
