package compensation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrIncomplete is returned by Plan.Run when one or more compensations
// could not be completed. The plan stays resumable: call Run again to
// retry the failed entries.
var ErrIncomplete = errors.New("compensation: plan has unfinished entries")

// Sleeper waits between compensation retries. It must honour ctx.
type Sleeper func(ctx context.Context, d time.Duration) error

// DefaultSleeper sleeps for d or until ctx is cancelled.
func DefaultSleeper(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RetryPolicy controls how many times a failing compensation is
// attempted within a single Plan.Run call.
type RetryPolicy struct {
	// MaxAttempts is the number of attempts per Run call. <= 0 means 1.
	MaxAttempts int
	// Backoff is the delay between attempts.
	Backoff time.Duration
}

func (p RetryPolicy) attempts() int {
	if p.MaxAttempts <= 0 {
		return 1
	}
	return p.MaxAttempts
}

// EntryStatus is the state of a single compensation entry.
type EntryStatus string

const (
	EntryPending   EntryStatus = "pending"
	EntrySucceeded EntryStatus = "succeeded"
	EntryFailed    EntryStatus = "failed"
	EntryManual    EntryStatus = "manual_required"
)

// PlanStatus is the aggregate state of a compensation plan.
type PlanStatus string

const (
	PlanPending        PlanStatus = "pending"
	PlanInProgress     PlanStatus = "in_progress"
	PlanRolledBack     PlanStatus = "rolled_back"
	PlanManualRequired PlanStatus = "manual_required"
	PlanFailed         PlanStatus = "failed"
)

// Entry is one step's compensation record inside a plan.
type Entry struct {
	StepID        string      `json:"step_id"`
	Compensatable bool        `json:"compensatable"`
	Status        EntryStatus `json:"status"`
	Attempts      int         `json:"attempts"`
	LastError     string      `json:"last_error,omitempty"`
}

// ManualItem is a step that cannot be compensated automatically and
// must be resolved by a human. Manual items are never reported as
// rolled back.
type ManualItem struct {
	StepID string `json:"step_id"`
	Reason string `json:"reason"`
}

// HandlerRegistry maps step IDs to compensation handlers. It is used
// to re-attach behaviour when a plan is restored from a snapshot.
type HandlerRegistry map[string]CompensateFunc

// Plan is a resumable compensation plan. Entries execute in reverse
// dependency order. A Plan can be snapshotted and restored to resume
// compensation across process restarts.
type Plan struct {
	entries  []*Entry
	registry HandlerRegistry
	retry    RetryPolicy
	sleep    Sleeper
	manual   []ManualItem
}

func newPlan(entries []*Entry, registry HandlerRegistry, retry RetryPolicy, sleep Sleeper) *Plan {
	if sleep == nil {
		sleep = DefaultSleeper
	}
	if registry == nil {
		registry = HandlerRegistry{}
	}
	return &Plan{entries: entries, registry: registry, retry: retry, sleep: sleep}
}

// Entries returns a copy of the plan entries in execution order.
func (p *Plan) Entries() []Entry {
	out := make([]Entry, len(p.entries))
	for i, e := range p.entries {
		out[i] = *e
	}
	return out
}

// ManualItems returns the steps that require manual handling.
func (p *Plan) ManualItems() []ManualItem {
	out := make([]ManualItem, len(p.manual))
	copy(out, p.manual)
	return out
}

// Status aggregates the entry states. A plan is only RolledBack when
// every entry succeeded; plans containing manual items report
// ManualRequired instead.
func (p *Plan) Status() PlanStatus {
	failed, manual, pending := 0, 0, 0
	for _, e := range p.entries {
		switch e.Status {
		case EntryFailed:
			failed++
		case EntryManual:
			manual++
		case EntryPending:
			pending++
		}
	}
	switch {
	case failed > 0:
		return PlanFailed
	case pending == len(p.entries) && len(p.entries) > 0:
		return PlanPending
	case pending > 0:
		return PlanInProgress
	case manual > 0:
		return PlanManualRequired
	default:
		return PlanRolledBack
	}
}

// Run executes all unfinished entries in reverse dependency order.
// Failing compensations are retried according to the retry policy and
// otherwise left in EntryFailed so a later Run call can resume them.
// Non-compensatable steps are moved to the manual items list and never
// reported as rolled back. Run returns ErrIncomplete (wrapped with the
// failing step IDs) if any entry remains failed.
func (p *Plan) Run(ctx context.Context) error {
	var failed []string
	for _, e := range p.entries {
		if e.Status == EntrySucceeded || e.Status == EntryManual {
			continue
		}
		if !e.Compensatable {
			p.markManual(e, "step is not compensatable")
			continue
		}
		handler := p.registry[e.StepID]
		if handler == nil {
			p.markManual(e, "no compensation handler registered")
			continue
		}
		if err := p.compensate(ctx, e, handler); err != nil {
			if ctx.Err() != nil {
				// Stop early; everything unfinished stays resumable.
				return ctx.Err()
			}
			failed = append(failed, e.StepID)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%v: %s", ErrIncomplete, strings.Join(failed, ", "))
	}
	return nil
}

func (p *Plan) compensate(ctx context.Context, e *Entry, handler CompensateFunc) error {
	var err error
	for i := 0; i < p.retry.attempts(); i++ {
		if i > 0 && p.retry.Backoff > 0 {
			if serr := p.sleep(ctx, p.retry.Backoff); serr != nil {
				e.Status = EntryFailed
				return serr
			}
		}
		e.Attempts++
		if err = handler(ctx); err == nil {
			e.Status = EntrySucceeded
			e.LastError = ""
			return nil
		}
		e.LastError = err.Error()
	}
	e.Status = EntryFailed
	return err
}

func (p *Plan) markManual(e *Entry, reason string) {
	e.Status = EntryManual
	p.manual = append(p.manual, ManualItem{StepID: e.StepID, Reason: reason})
}

// MarkManual moves a failed entry to the manual items list. Use it
// when an operator decides a repeatedly failing compensation must be
// resolved by hand. It returns an error if the step is unknown or the
// entry is not in a failed state.
func (p *Plan) MarkManual(stepID, reason string) error {
	for _, e := range p.entries {
		if e.StepID == stepID {
			if e.Status != EntryFailed {
				return fmt.Errorf("compensation: step %q is %s, only failed entries can be marked manual", stepID, e.Status)
			}
			p.markManual(e, reason)
			return nil
		}
	}
	return fmt.Errorf("compensation: unknown step %q", stepID)
}

type planSnapshot struct {
	Entries []*Entry     `json:"entries"`
	Manual  []ManualItem `json:"manual_items,omitempty"`
}

// Snapshot serialises the plan state (entry order, statuses, attempt
// counters and manual items) so compensation can resume later, e.g.
// after a process restart.
func (p *Plan) Snapshot() ([]byte, error) {
	return json.Marshal(planSnapshot{Entries: p.entries, Manual: p.manual})
}

// RestorePlan rebuilds a plan from a snapshot. Compensation behaviour
// is re-attached through registry; steps without a registered handler
// become manual items when the plan runs.
func RestorePlan(data []byte, registry HandlerRegistry, retry RetryPolicy, sleep Sleeper) (*Plan, error) {
	var snap planSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("compensation: invalid plan snapshot: %v", err)
	}
	if len(snap.Entries) == 0 {
		return nil, fmt.Errorf("compensation: snapshot contains no entries")
	}
	plan := newPlan(snap.Entries, registry, retry, sleep)
	plan.manual = snap.Manual
	return plan, nil
}
