package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

var errUnrecoverable = errors.New("plan contains unrecoverable items; restore affected segments from backup")

// crashFunc is the effect of a triggered fault-injection point. Production
// behaviour is an abrupt exit (simulated crash); tests override it.
var crashFunc = func() { os.Exit(2) }

// failpoint triggers when STORETOOL_FAILPOINT equals "<actionID>:<phase>".
// Phases: before, pre-rename, post-rename, pre-journal.
func failpoint(actionID, phase string) {
	if os.Getenv("STORETOOL_FAILPOINT") == actionID+":"+phase {
		fmt.Fprintf(os.Stderr, "fault injected at %s:%s\n", actionID, phase)
		crashFunc()
	}
}

type Journal struct {
	PlanSummary string   `json:"plan_state_summary"`
	Completed   []string `json:"completed"`
}

// Apply executes plan against dir. Fresh runs require the store to still
// match plan.StateSummary; interrupted runs resume from the journal instead.
func Apply(dir, planPath string, out io.Writer) error {
	planData, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var plan Plan
	if err := json.Unmarshal(planData, &plan); err != nil {
		return fmt.Errorf("parse plan: %w", err)
	}

	jpath := journalPath(dir)
	journal := Journal{PlanSummary: plan.StateSummary}
	completed := map[string]bool{}
	jb, jerr := os.ReadFile(jpath)
	switch {
	case jerr == nil:
		if err := json.Unmarshal(jb, &journal); err != nil {
			return fmt.Errorf("journal %s is corrupt; manual intervention required: %w", jpath, err)
		}
		if journal.PlanSummary != plan.StateSummary {
			return fmt.Errorf("journal belongs to a different plan (journal summary %s, plan summary %s)",
				journal.PlanSummary, plan.StateSummary)
		}
		for _, id := range journal.Completed {
			completed[id] = true
		}
		fmt.Fprintf(out, "resuming interrupted apply: %d step(s) already completed\n", len(completed))
	case os.IsNotExist(jerr):
		summary, err := StateSummary(dir)
		if err != nil {
			return err
		}
		if summary != plan.StateSummary {
			return fmt.Errorf("state summary mismatch: store changed since audit (plan %s, current %s); re-run audit",
				plan.StateSummary, summary)
		}
		data, err := json.MarshalIndent(journal, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFileAtomic(jpath, data, nil); err != nil {
			return err
		}
	default:
		return jerr
	}

	unrecoverable := 0
	for _, action := range plan.Actions {
		if action.Type == ActionUnrecoverable {
			unrecoverable++
			fmt.Fprintf(out, "SKIP %s: unrecoverable %s (%s)\n", action.ID, action.Segment, action.Reason)
			continue
		}
		if completed[action.ID] {
			fmt.Fprintf(out, "SKIP %s: %s already completed\n", action.ID, action.Type)
			continue
		}
		fmt.Fprintf(out, "RUN  %s: %s %s\n", action.ID, action.Type, action.Segment)
		failpoint(action.ID, "before")
		var err error
		switch action.Type {
		case ActionRemoveOrphan:
			err = os.Remove(segmentPath(dir, action.Segment))
			if os.IsNotExist(err) {
				err = nil
			}
		case ActionRebuildIndex:
			err = applyRebuildIndex(dir, action.ID)
		case ActionSyncManifest:
			err = applySyncManifest(dir, action.ID)
		default:
			err = fmt.Errorf("unknown action type %q", action.Type)
		}
		if err != nil {
			return fmt.Errorf("%s (%s): %w", action.ID, action.Type, err)
		}
		failpoint(action.ID, "pre-journal")
		journal.Completed = append(journal.Completed, action.ID)
		data, err := json.MarshalIndent(journal, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFileAtomic(jpath, data, nil); err != nil {
			return err
		}
	}

	if err := os.Remove(jpath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	if unrecoverable > 0 {
		fmt.Fprintf(out, "%d unrecoverable item(s) remain\n", unrecoverable)
		return errUnrecoverable
	}
	fmt.Fprintln(out, "apply complete")
	return nil
}

func applyRebuildIndex(dir, actionID string) error {
	manifest, err := readManifest(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(manifest.Segments))
	for _, sm := range manifest.Segments {
		data, err := os.ReadFile(segmentPath(dir, sm.Name))
		if err != nil {
			return fmt.Errorf("segment %s unreadable during rebuild: %w", sm.Name, err)
		}
		if got := sha256Hex(data); got != sm.SHA256 {
			return fmt.Errorf("segment %s changed since audit; aborting rebuild", sm.Name)
		}
		names = append(names, sm.Name)
	}
	segData, err := loadSegments(dir, names)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(buildIndex(segData, names), "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(indexPath(dir), data, func() { failpoint(actionID, "pre-rename") }); err != nil {
		return err
	}
	failpoint(actionID, "post-rename")
	return nil
}

func applySyncManifest(dir, actionID string) error {
	manifest, err := readManifest(dir)
	if err != nil {
		return err
	}
	idxData, err := os.ReadFile(indexPath(dir))
	if err != nil {
		return fmt.Errorf("cannot sync manifest: %w", err)
	}
	manifest.Index = &IndexMeta{Name: indexName, Size: int64(len(idxData)), SHA256: sha256Hex(idxData)}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(manifestPath(dir), data, func() { failpoint(actionID, "pre-rename") }); err != nil {
		return err
	}
	failpoint(actionID, "post-rename")
	return nil
}
