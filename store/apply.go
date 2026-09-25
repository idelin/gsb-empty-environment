package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// journal records apply progress so an interrupted run can resume.
// It is written (temp+fsync+rename) once before the first step
// (write-ahead) and again after each completed step.
type journal struct {
	PlanID  string   `json:"plan_id"`
	Summary string   `json:"summary"` // summary verified when the run started
	Done    []string `json:"done"`
}

// Apply executes plan against dir, resuming from the journal if a
// previous run was interrupted. Guarantees:
//
//   - First run verifies the live store still matches plan.Summary;
//     if anything changed since the audit, it aborts without writes.
//   - Resume verifies the journal belongs to this plan, so a stale or
//     foreign plan can never be replayed.
//   - Every step is idempotent and committed atomically (temp+fsync+
//     rename+dir fsync); a crash anywhere leaves either the old or the
//     new state, never a mixture.
func Apply(dir string, plan *Plan) error {
	if !plan.Repairable {
		return fmt.Errorf("plan %s is not repairable: %s", plan.ID, plan.Steps[0].Detail)
	}
	if err := cleanTemps(dir); err != nil {
		return err
	}
	journalPath := filepath.Join(dir, JournalName)
	var j journal
	resuming := false
	if data, err := os.ReadFile(journalPath); err == nil {
		if err := jsonUnmarshal(data, &j); err != nil {
			return fmt.Errorf("journal corrupt: %v", err)
		}
		if j.PlanID != plan.ID {
			return fmt.Errorf("journal belongs to plan %s, refusing to resume with plan %s", j.PlanID, plan.ID)
		}
		resuming = true
	}
	if !resuming {
		// Fresh run: the store must still look exactly like it did at
		// audit time. Recompute the summary read-only.
		rep, _, err := Audit(dir)
		if err != nil {
			return err
		}
		if rep.Summary != plan.Summary {
			return fmt.Errorf("state changed since audit (summary %s != plan %s); re-run audit",
				rep.Summary[:12], plan.Summary[:12])
		}
		j = journal{PlanID: plan.ID, Summary: plan.Summary}
		// Write-ahead: persist the journal BEFORE executing any step.
		// A crash between a step's atomic commit and its journal update
		// then still leaves a journal behind, so the resume path (not
		// the fresh-run summary check) is taken and the idempotent step
		// is simply re-executed.
		if err := writeJournal(dir, journalPath, &j, "init"); err != nil {
			return err
		}
	}

	done := map[string]bool{}
	for _, id := range j.Done {
		done[id] = true
	}
	for _, step := range plan.Steps {
		if done[step.ID] {
			continue
		}
		if err := runFault(FaultBeforeStep, step.ID); err != nil {
			return err
		}
		var err error
		switch step.Op {
		case OpNoop:
			// nothing to do
		case OpRebuildIndex:
			err = rebuildIndex(dir, step.ID)
		default:
			err = fmt.Errorf("step %s: op %q cannot be executed automatically", step.ID, step.Op)
		}
		if err != nil {
			return err
		}
		j.Done = append(j.Done, step.ID)
		if err := writeJournal(dir, journalPath, &j, step.ID); err != nil {
			return err
		}
		if err := runFault(FaultAfterJournal, step.ID); err != nil {
			return err
		}
	}
	// All steps committed; the journal has served its purpose. If the
	// remove itself is interrupted, the next run simply re-observes a
	// fully-completed journal and removes it again.
	return os.Remove(journalPath)
}

func writeJournal(dir, path string, j *journal, stepID string) error {
	data, err := marshalJSON(j)
	if err != nil {
		return err
	}
	return writeFileAtomic(dir, filepath.Base(path), data, stepID)
}

// rebuildIndex regenerates index.json purely from verified segments.
// Later records for the same key shadow earlier ones (append-only log).
func rebuildIndex(dir, stepID string) error {
	var m Manifest
	if err := readJSON(filepath.Join(dir, ManifestName), &m); err != nil {
		return err
	}
	idx := &Index{
		Version:  1,
		Segments: map[string]ManifestSegment{},
		Entries:  map[string]IndexEntry{},
	}
	for _, ms := range m.Segments {
		// Re-verify each segment immediately before trusting it. The
		// summary check at run start covers the whole set; this guards
		// against mutation between steps of a long apply.
		sum, size, err := sha256File(filepath.Join(dir, ms.Name))
		if err != nil {
			return fmt.Errorf("segment %s vanished during apply: %v", ms.Name, err)
		}
		if size != ms.Size || sum != ms.SHA256 {
			return fmt.Errorf("segment %s changed during apply; aborting", ms.Name)
		}
		idx.Segments[ms.Name] = ms
		name := ms.Name
		err = scanSegment(filepath.Join(dir, name), func(off, length int64, key, value []byte) error {
			idx.Entries[string(key)] = IndexEntry{Segment: name, Offset: off, Length: length}
			return nil
		})
		if err != nil {
			return err
		}
	}
	data, err := marshalJSON(idx)
	if err != nil {
		return err
	}
	return writeFileAtomic(dir, IndexName, data, stepID)
}
