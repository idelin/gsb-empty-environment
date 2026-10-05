package schedule

import (
	"fmt"
	"time"
)

// Scheduler manages a set of rules, keeps a per-rule updatable index for
// queries, and snapshots every version so two versions can be diffed.
type Scheduler struct {
	rules map[string]*ruleState
}

type ruleState struct {
	rule    *Rule
	index   *Index
	version int
	snaps   map[int][]Instance // version -> instance snapshot
}

func NewScheduler() *Scheduler {
	return &Scheduler{rules: make(map[string]*ruleState)}
}

func (s *Scheduler) state(ruleID string) (*ruleState, error) {
	st, ok := s.rules[ruleID]
	if !ok {
		return nil, fmt.Errorf("schedule: unknown rule %q", ruleID)
	}
	return st, nil
}

// NewRule creates a weekly rule starting on the given Monday. The
// initial version is 0.
func (s *Scheduler) NewRule(id string, start time.Time, weeks int) error {
	if _, exists := s.rules[id]; exists {
		return fmt.Errorf("schedule: rule %q already exists", id)
	}
	r, err := newRule(id, start, weeks)
	if err != nil {
		return err
	}
	s.rules[id] = &ruleState{
		rule:  r,
		index: newIndex(),
		snaps: map[int][]Instance{0: {}},
	}
	return nil
}

// commit bumps the version and snapshots the current instances.
func (st *ruleState) commit() error {
	st.version++
	snap, err := st.rule.expand()
	if err != nil {
		return err
	}
	st.snaps[st.version] = snap
	return nil
}

// refreshWeek recomputes one week in the index.
func (st *ruleState) refreshWeek(week int) error {
	inst, ok, err := st.rule.instanceAt(week)
	if err != nil {
		return err
	}
	if ok {
		st.index.upsert(inst)
	} else {
		st.index.remove(week)
	}
	return nil
}

// AddRevision adds a range revision effective from p.FromWeek and
// returns the new version. Only weeks >= FromWeek are re-indexed.
func (s *Scheduler) AddRevision(ruleID string, p Params) (int, error) {
	st, err := s.state(ruleID)
	if err != nil {
		return 0, err
	}
	if err := p.validate(); err != nil {
		return 0, err
	}
	if p.FromWeek < 0 || p.FromWeek >= st.rule.Weeks {
		return 0, fmt.Errorf("schedule: from_week %d out of range [0,%d)", p.FromWeek, st.rule.Weeks)
	}
	from := st.rule.addRevision(p)
	for w := from; w < st.rule.Weeks; w++ {
		if err := st.refreshWeek(w); err != nil {
			return 0, err
		}
	}
	if err := st.commit(); err != nil {
		return 0, err
	}
	return st.version, nil
}

// SetOverride replaces a single week's instance with the given params.
func (s *Scheduler) SetOverride(ruleID string, week int, p Params) (int, error) {
	st, err := s.state(ruleID)
	if err != nil {
		return 0, err
	}
	if err := p.validate(); err != nil {
		return 0, err
	}
	if week < 0 || week >= st.rule.Weeks {
		return 0, fmt.Errorf("schedule: week %d out of range [0,%d)", week, st.rule.Weeks)
	}
	p.FromWeek = week
	st.rule.overrides[week] = p
	if err := st.refreshWeek(week); err != nil {
		return 0, err
	}
	if err := st.commit(); err != nil {
		return 0, err
	}
	return st.version, nil
}

// Cancel marks one week's instance as canceled. Cancellation wins over
// overrides and range revisions.
func (s *Scheduler) Cancel(ruleID string, week int) (int, error) {
	return s.setCanceled(ruleID, week, true)
}

// Restore removes the cancellation mark from one week.
func (s *Scheduler) Restore(ruleID string, week int) (int, error) {
	return s.setCanceled(ruleID, week, false)
}

func (s *Scheduler) setCanceled(ruleID string, week int, canceled bool) (int, error) {
	st, err := s.state(ruleID)
	if err != nil {
		return 0, err
	}
	if week < 0 || week >= st.rule.Weeks {
		return 0, fmt.Errorf("schedule: week %d out of range [0,%d)", week, st.rule.Weeks)
	}
	if canceled {
		st.rule.canceled[week] = true
	} else {
		delete(st.rule.canceled, week)
	}
	if err := st.refreshWeek(week); err != nil {
		return 0, err
	}
	if err := st.commit(); err != nil {
		return 0, err
	}
	return st.version, nil
}

// Query returns the instances intersecting the UTC half-open interval
// [qs, qe), served from the updatable index (no full expansion).
func (s *Scheduler) Query(ruleID string, qs, qe time.Time) ([]Instance, error) {
	st, err := s.state(ruleID)
	if err != nil {
		return nil, err
	}
	if !qs.Before(qe) {
		return nil, fmt.Errorf("schedule: empty query interval [%s, %s)", qs, qe)
	}
	return st.index.Query(qs, qe), nil
}

// ReferenceQuery is the naive week-by-week reference implementation used
// to cross-check the index in tests.
func (s *Scheduler) ReferenceQuery(ruleID string, qs, qe time.Time) ([]Instance, error) {
	st, err := s.state(ruleID)
	if err != nil {
		return nil, err
	}
	all, err := st.rule.expand()
	if err != nil {
		return nil, err
	}
	var out []Instance
	for _, inst := range all {
		if inst.Start.Before(qe) && qs.Before(inst.End) {
			out = append(out, inst)
		}
	}
	return out, nil
}

// Diff compares two versions of a rule and reports added, removed and
// time-changed instances.
func (s *Scheduler) Diff(ruleID string, fromVersion, toVersion int) ([]Change, error) {
	st, err := s.state(ruleID)
	if err != nil {
		return nil, err
	}
	a, ok := st.snaps[fromVersion]
	if !ok {
		return nil, fmt.Errorf("schedule: unknown version %d", fromVersion)
	}
	b, ok := st.snaps[toVersion]
	if !ok {
		return nil, fmt.Errorf("schedule: unknown version %d", toVersion)
	}
	return DiffInstances(a, b), nil
}

// Version returns the current version of a rule.
func (s *Scheduler) Version(ruleID string) (int, error) {
	st, err := s.state(ruleID)
	if err != nil {
		return 0, err
	}
	return st.version, nil
}

// WeekOfDate converts a civil date to a week index using the rule's
// original start Monday, independent of any revision's UTC offset.
func (s *Scheduler) WeekOfDate(ruleID string, d time.Time) (int, error) {
	st, err := s.state(ruleID)
	if err != nil {
		return 0, err
	}
	return st.rule.weekOfDate(d)
}
