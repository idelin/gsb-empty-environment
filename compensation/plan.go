package compensation

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

// EntryStatus describes the state of one compensation plan entry.
type EntryStatus string

const (
	StatusPending EntryStatus = "pending"
	StatusDone    EntryStatus = "done"
	StatusFailed  EntryStatus = "failed"
	StatusManual  EntryStatus = "manual"
)

// PlanEntry is one step's compensation record inside a Plan.
type PlanEntry struct {
	StepID    string      `json:"step_id"`
	Status    EntryStatus `json:"status"`
	Attempts  int         `json:"attempts"`
	LastError string      `json:"last_error,omitempty"`
}

// Plan is an ordered, resumable compensation plan. Entries execute in
// slice order, which the executor fills with reverse dependency order
// (dependents before their dependencies). The plan is persisted after
// every entry transition when a path is configured, so a crashed run
// can be resumed exactly where it stopped.
type Plan struct {
	Entries []*PlanEntry `json:"entries"`

	path string
}

// ManualItem is a step that could not be compensated automatically and
// requires human intervention. Manual items are never reported as
// rollback successes.
type ManualItem struct {
	StepID string
	Reason string
}

// RollbackReport summarizes the outcome of running a Plan.
type RollbackReport struct {
	// Compensated lists steps whose compensation succeeded.
	Compensated []string
	// Failed lists steps whose compensation failed and may be retried.
	Failed []string
	// Manual lists steps that need manual handling.
	Manual []ManualItem
}

// Success reports whether the rollback fully completed. It is false
// whenever any step failed or needs manual handling — those steps must
// never be reported as rolled back.
func (r *RollbackReport) Success() bool {
	return len(r.Failed) == 0 && len(r.Manual) == 0
}

// normalizeAttempts maps maxAttempts to at least 1: the first try of
// every entry always happens, and maxAttempts-1 retries may follow.
func normalizeAttempts(maxAttempts int) int {
	if maxAttempts < 1 {
		return 1
	}
	return maxAttempts
}

// Run executes the plan in order. Pending entries and failed entries
// with remaining attempts are (re)tried; done and manual entries are
// skipped, so a resumed plan never compensates a step twice. An entry
// whose attempts are exhausted becomes a manual item.
func (p *Plan) Run(ctx Context, registry map[string]func(Context) error, maxAttempts int) *RollbackReport {
	maxAttempts = normalizeAttempts(maxAttempts)
	for _, e := range p.Entries {
		if e.Status == StatusDone || e.Status == StatusManual {
			continue
		}
		if e.Status == StatusFailed && e.Attempts >= maxAttempts {
			e.Status = StatusManual
			e.LastError = fmt.Sprintf("compensation attempts exhausted: %s", e.LastError)
			p.persist()
			continue
		}
		handler := registry[e.StepID]
		if handler == nil {
			e.Status = StatusManual
			e.LastError = "step has no compensation handler"
			p.persist()
			continue
		}
		e.Attempts++
		if err := handler(ctx); err != nil {
			e.Status = StatusFailed
			e.LastError = err.Error()
			if e.Attempts >= maxAttempts {
				e.Status = StatusManual
				e.LastError = fmt.Sprintf("compensation attempts exhausted: %s", err.Error())
			}
			p.persist()
			continue
		}
		e.Status = StatusDone
		e.LastError = ""
		p.persist()
	}
	return p.Report()
}

// Report builds a RollbackReport from the current entry states without
// executing anything.
func (p *Plan) Report() *RollbackReport {
	rep := &RollbackReport{}
	for _, e := range p.Entries {
		switch e.Status {
		case StatusDone:
			rep.Compensated = append(rep.Compensated, e.StepID)
		case StatusFailed:
			rep.Failed = append(rep.Failed, e.StepID)
		case StatusManual:
			rep.Manual = append(rep.Manual, ManualItem{StepID: e.StepID, Reason: e.LastError})
		}
	}
	return rep
}

// Pending returns true when the plan still has entries that Run would
// attempt (pending, or failed with attempts remaining).
func (p *Plan) Pending(maxAttempts int) bool {
	maxAttempts = normalizeAttempts(maxAttempts)
	for _, e := range p.Entries {
		if e.Status == StatusPending {
			return true
		}
		if e.Status == StatusFailed && e.Attempts < maxAttempts {
			return true
		}
	}
	return false
}

// Save persists the plan as JSON at the given path, atomically.
func (p *Plan) Save(path string) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadPlan reads a previously saved plan from disk.
func LoadPlan(path string) (*Plan, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("compensation: invalid plan file %s: %v", path, err)
	}
	return &p, nil
}

func (p *Plan) persist() {
	if p.path == "" {
		return
	}
	// Persistence failures are intentionally ignored here: the in-memory
	// plan remains authoritative for this run, and the error surfaces on
	// the next explicit Save or on resume.
	_ = p.Save(p.path)
}
