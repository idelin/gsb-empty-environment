// Package recur computes instances of a finite weekly recurring meeting rule
// with range revisions and single-occurrence exceptions, using only fixed UTC
// offsets and the time package's civil-date arithmetic (no tz database).
package recur

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// Limits enforced on all inputs.
const (
	MaxMinuteOfDay = 24*60 - 1
	MaxDurationMin = 7 * 24 * 60
	MaxOffsetMin   = 14 * 60
	MaxWeeks       = 5200
)

var (
	ErrOverflow   = errors.New("recur: minutes to seconds conversion overflow")
	ErrNoRevision = errors.New("recur: no range revision covers week index")
)

// Action is the kind of a single-occurrence exception.
type Action string

const (
	ActionOverride Action = "override"
	ActionCancel   Action = "cancel"
	ActionRestore  Action = "restore"
)

// actionRank breaks ties between exceptions sharing the same revision number:
// cancel beats override beats restore.
func actionRank(a Action) int {
	switch a {
	case ActionCancel:
		return 3
	case ActionOverride:
		return 2
	case ActionRestore:
		return 1
	}
	return 0
}

// Source identifies which layer produced an instance.
type Source string

const (
	SourceRange    Source = "range"
	SourceOverride Source = "override"
)

// Params are the time-of-week attributes of an occurrence. Changing any of
// them never changes an instance's identity (rule ID + week index).
type Params struct {
	Weekday     time.Weekday
	Minute      int // minute of day, local wall clock
	DurationMin int
	OffsetMin   int // fixed UTC offset in minutes, east of UTC positive
}

// Validate range-checks all fields.
func (p Params) Validate() error {
	if p.Weekday < time.Sunday || p.Weekday > time.Saturday {
		return fmt.Errorf("recur: invalid weekday %d", p.Weekday)
	}
	if p.Minute < 0 || p.Minute > MaxMinuteOfDay {
		return fmt.Errorf("recur: minute of day %d out of range [0,%d]", p.Minute, MaxMinuteOfDay)
	}
	if p.DurationMin <= 0 || p.DurationMin > MaxDurationMin {
		return fmt.Errorf("recur: duration %d out of range (0,%d]", p.DurationMin, MaxDurationMin)
	}
	if p.OffsetMin < -MaxOffsetMin || p.OffsetMin > MaxOffsetMin {
		return fmt.Errorf("recur: offset %d out of range [-%d,%d]", p.OffsetMin, MaxOffsetMin, MaxOffsetMin)
	}
	return nil
}

// Revision is a range revision: Params effective from week FromSeq onward.
// For equal FromSeq the larger Rev wins.
type Revision struct {
	FromSeq int
	Rev     int
	Params
}

// Exception is a single-occurrence override, cancellation, or restore.
// For equal Seq the larger Rev wins; ties break cancel > override > restore.
type Exception struct {
	Seq    int
	Rev    int
	Action Action
	Params // meaningful only for ActionOverride
}

// Instance is one concrete occurrence.
type Instance struct {
	ID     string // stable identity: "<ruleID>#<seq>"
	Seq    int
	Start  int64 // UTC unix seconds, inclusive
	End    int64 // UTC unix seconds, exclusive
	Rev    int   // revision number actually adopted
	Source Source
}

// Rule is a finite weekly recurrence starting on a Monday.
type Rule struct {
	ID        string
	StartDate time.Time // UTC midnight of the starting Monday
	Weeks     int       // finite number of weekly periods

	revisions  map[int]Revision  // keyed by FromSeq, highest Rev kept
	exceptions map[int]Exception // keyed by Seq, highest Rev (then rank) kept
}

// NewRule validates the rule header. startDate must be a Monday; its wall
// clock components are interpreted as a civil date in UTC.
func NewRule(id string, startDate time.Time, weeks int) (*Rule, error) {
	if id == "" {
		return nil, errors.New("recur: empty rule ID")
	}
	y, m, d := startDate.Date()
	base := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	if base.Weekday() != time.Monday {
		return nil, fmt.Errorf("recur: start date %s is not a Monday", base.Format("2006-01-02"))
	}
	if weeks <= 0 || weeks > MaxWeeks {
		return nil, fmt.Errorf("recur: weeks %d out of range (0,%d]", weeks, MaxWeeks)
	}
	return &Rule{
		ID:         id,
		StartDate:  base,
		Weeks:      weeks,
		revisions:  map[int]Revision{},
		exceptions: map[int]Exception{},
	}, nil
}

// UpsertRevision registers a range revision. It reports whether the rule
// changed: a revision loses to an existing one with the same FromSeq and a
// greater or equal Rev.
func (r *Rule) UpsertRevision(rev Revision) error {
	if rev.FromSeq < 0 || rev.FromSeq >= r.Weeks {
		return fmt.Errorf("recur: revision from_seq %d out of range [0,%d)", rev.FromSeq, r.Weeks)
	}
	if rev.Rev < 0 {
		return fmt.Errorf("recur: negative revision number %d", rev.Rev)
	}
	if err := rev.Params.Validate(); err != nil {
		return err
	}
	if old, ok := r.revisions[rev.FromSeq]; ok && old.Rev >= rev.Rev {
		return nil
	}
	r.revisions[rev.FromSeq] = rev
	return nil
}

// UpsertException registers a single-occurrence exception. It reports whether
// the rule changed.
func (r *Rule) UpsertException(exc Exception) error {
	if exc.Seq < 0 || exc.Seq >= r.Weeks {
		return fmt.Errorf("recur: exception seq %d out of range [0,%d)", exc.Seq, r.Weeks)
	}
	if exc.Rev < 0 {
		return fmt.Errorf("recur: negative revision number %d", exc.Rev)
	}
	switch exc.Action {
	case ActionOverride:
		if err := exc.Params.Validate(); err != nil {
			return err
		}
	case ActionCancel, ActionRestore:
	default:
		return fmt.Errorf("recur: unknown exception action %q", exc.Action)
	}
	if old, ok := r.exceptions[exc.Seq]; ok {
		if old.Rev > exc.Rev {
			return nil
		}
		if old.Rev == exc.Rev && actionRank(old.Action) >= actionRank(exc.Action) {
			return nil
		}
	}
	r.exceptions[exc.Seq] = exc
	return nil
}

// RevisionApplied reports whether rev would win over the current state.
func (r *Rule) RevisionApplied(rev Revision) bool {
	old, ok := r.revisions[rev.FromSeq]
	return !ok || old.Rev < rev.Rev
}

// ExceptionApplied reports whether exc would win over the current state.
func (r *Rule) ExceptionApplied(exc Exception) bool {
	old, ok := r.exceptions[exc.Seq]
	if !ok {
		return true
	}
	if old.Rev != exc.Rev {
		return old.Rev < exc.Rev
	}
	return actionRank(old.Action) < actionRank(exc.Action)
}

// SeqForDate maps a civil date to its week index, counted in whole weeks from
// the rule's original start date. It never depends on any offset, so instance
// identities cannot drift when offsets change.
func (r *Rule) SeqForDate(date time.Time) (int, bool) {
	y, m, d := date.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	days := int(day.Sub(r.StartDate).Hours() / 24)
	if days < 0 {
		return 0, false
	}
	seq := days / 7
	if seq >= r.Weeks {
		return 0, false
	}
	return seq, true
}

// effective resolves the winning layers for week seq.
func (r *Rule) effective(seq int) (Params, int, Source, bool) {
	bestFrom := -1
	var rev Revision
	for from, rv := range r.revisions {
		if from <= seq && from > bestFrom {
			bestFrom, rev = from, rv
		}
	}
	if bestFrom < 0 {
		return Params{}, 0, "", false
	}
	p, revNo, src := rev.Params, rev.Rev, SourceRange
	if exc, ok := r.exceptions[seq]; ok {
		switch exc.Action {
		case ActionCancel:
			return Params{}, 0, "", false
		case ActionOverride:
			p, revNo, src = exc.Params, exc.Rev, SourceOverride
		case ActionRestore:
			// falls back to the range revision
		}
	}
	return p, revNo, src, true
}

// minutesToSeconds converts minutes to seconds with an overflow check.
func minutesToSeconds(m int64) (int64, error) {
	if m > math.MaxInt64/60 || m < math.MinInt64/60 {
		return 0, ErrOverflow
	}
	return m * 60, nil
}

func addSeconds(a, b int64) (int64, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// Instance computes the concrete occurrence for week seq, or ok=false if the
// week is out of range, uncovered by any revision, or canceled.
func (r *Rule) Instance(seq int) (inst Instance, ok bool) {
	if seq < 0 || seq >= r.Weeks {
		return Instance{}, false
	}
	p, revNo, src, ok := r.effective(seq)
	if !ok {
		return Instance{}, false
	}
	dayOffset := (int(p.Weekday) - int(time.Monday) + 7) % 7
	day := r.StartDate.AddDate(0, 0, 7*seq+dayOffset)
	minSec, err := minutesToSeconds(int64(p.Minute))
	if err != nil {
		return Instance{}, false
	}
	offSec, err := minutesToSeconds(int64(p.OffsetMin))
	if err != nil {
		return Instance{}, false
	}
	durSec, err := minutesToSeconds(int64(p.DurationMin))
	if err != nil {
		return Instance{}, false
	}
	start, err := addSeconds(day.Unix(), minSec-offSec)
	if err != nil {
		return Instance{}, false
	}
	end, err := addSeconds(start, durSec)
	if err != nil {
		return Instance{}, false
	}
	return Instance{
		ID:     fmt.Sprintf("%s#%d", r.ID, seq),
		Seq:    seq,
		Start:  start,
		End:    end,
		Rev:    revNo,
		Source: src,
	}, true
}

// AllInstances expands every week of the finite horizon. Intended for diffs
// and for the reference implementation, not for indexed queries.
func (r *Rule) AllInstances() []Instance {
	var out []Instance
	for seq := 0; seq < r.Weeks; seq++ {
		if inst, ok := r.Instance(seq); ok {
			out = append(out, inst)
		}
	}
	return out
}

// QueryReference is the per-week traversal reference implementation: it
// expands every week and keeps those whose [Start,End) intersects [from,to).
func (r *Rule) QueryReference(from, to int64) []Instance {
	var out []Instance
	for seq := 0; seq < r.Weeks; seq++ {
		if inst, ok := r.Instance(seq); ok && inst.Start < to && inst.End > from {
			out = append(out, inst)
		}
	}
	return out
}
