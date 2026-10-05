// Package schedule implements a weekly recurring-meeting calendar with
// range revisions, single-instance overrides, cancellations and restores.
//
// Identity: an instance is identified by rule ID + week index. The week
// index is always derived from the rule's original start Monday using
// civil-date arithmetic, so changing the fixed UTC offset, weekday,
// minute-of-day or duration never shifts an instance's identity.
//
// Time zones: all computation uses a fixed UTC offset (minutes east of
// UTC) together with the time package's date arithmetic. No IANA time
// zone database is loaded.
package schedule

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// ErrOverflow is returned when a minutes value cannot be represented as a
// time.Duration (minutes -> seconds/nanoseconds conversion overflow).
var ErrOverflow = errors.New("schedule: minutes conversion overflow")

const (
	maxWeeks     = 5200 // 100 years of weekly instances
	maxOffsetMin = 14 * 60
	maxMinuteDay = 1439
	maxDurMin    = 7 * 24 * 60
)

// minutesToDuration converts minutes to a time.Duration, checking for
// overflow of the underlying int64 nanosecond representation.
func minutesToDuration(min int) (time.Duration, error) {
	m := int64(min)
	if m > math.MaxInt64/int64(time.Minute) || m < math.MinInt64/int64(time.Minute) {
		return 0, ErrOverflow
	}
	return time.Duration(m) * time.Minute, nil
}

// minutesToSeconds converts minutes to seconds with an overflow check.
func minutesToSeconds(min int) (int64, error) {
	m := int64(min)
	if m > math.MaxInt64/60 || m < math.MinInt64/60 {
		return 0, ErrOverflow
	}
	return m * 60, nil
}

// Params describes when the instances of a weekly rule occur. It is used
// both for range revisions (effective from FromWeek on) and for
// single-instance overrides.
type Params struct {
	Rev         int `json:"rev"`          // revision number
	FromWeek    int `json:"from_week"`    // first week index this revision applies to
	Weekday     int `json:"weekday"`      // 0 = Monday .. 6 = Sunday
	MinuteOfDay int `json:"minute"`       // minutes after local midnight, 0..1439
	DurationMin int `json:"duration_min"` // instance duration in minutes, > 0
	OffsetMin   int `json:"offset_min"`   // fixed UTC offset in minutes
}

func (p Params) validate() error {
	if p.Weekday < 0 || p.Weekday > 6 {
		return fmt.Errorf("schedule: weekday %d out of range [0,6]", p.Weekday)
	}
	if p.MinuteOfDay < 0 || p.MinuteOfDay > maxMinuteDay {
		return fmt.Errorf("schedule: minute %d out of range [0,%d]", p.MinuteOfDay, maxMinuteDay)
	}
	if p.DurationMin <= 0 || p.DurationMin > maxDurMin {
		return fmt.Errorf("schedule: duration_min %d out of range (0,%d]", p.DurationMin, maxDurMin)
	}
	if p.OffsetMin < -maxOffsetMin || p.OffsetMin > maxOffsetMin {
		return fmt.Errorf("schedule: offset_min %d out of range [%d,%d]", p.OffsetMin, -maxOffsetMin, maxOffsetMin)
	}
	// Checked conversions: even though the ranges above already keep us
	// far from int64 limits, verify the minutes->duration/seconds math.
	for _, m := range []int{p.MinuteOfDay, p.DurationMin, p.OffsetMin} {
		if _, err := minutesToDuration(m); err != nil {
			return err
		}
		if _, err := minutesToSeconds(m); err != nil {
			return err
		}
	}
	return nil
}

// Instance is one concrete occurrence of a rule.
type Instance struct {
	ID        string    // rule ID + "#" + week index; stable across edits
	Week      int       // week index counted from the rule's start Monday
	Rev       int       // revision number actually applied
	Start     time.Time // actual start instant (UTC)
	End       time.Time // actual end instant (UTC), half-open [Start, End)
	OffsetMin int       // fixed UTC offset used to render local times
}

// Rule is a weekly recurrence starting on a Monday and running for a
// finite number of weeks.
type Rule struct {
	ID    string
	Start time.Time // start Monday as a civil date (UTC midnight)
	Weeks int       // finite number of weekly periods

	revisions []Params       // sorted by (FromWeek, Rev)
	overrides map[int]Params // week index -> replacement params
	canceled  map[int]bool   // week index -> canceled
}

// newRule validates and creates a rule.
func newRule(id string, start time.Time, weeks int) (*Rule, error) {
	if id == "" {
		return nil, errors.New("schedule: empty rule id")
	}
	day := civilDate(start)
	if day.Weekday() != time.Monday {
		return nil, fmt.Errorf("schedule: start %s is not a Monday", day.Format("2006-01-02"))
	}
	if weeks <= 0 || weeks > maxWeeks {
		return nil, fmt.Errorf("schedule: weeks %d out of range (0,%d]", weeks, maxWeeks)
	}
	return &Rule{
		ID:        id,
		Start:     day,
		Weeks:     weeks,
		overrides: make(map[int]Params),
		canceled:  make(map[int]bool),
	}, nil
}

// civilDate truncates t to a civil date at UTC midnight.
func civilDate(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// weekOfDate maps a civil date to its week index, counted from the rule's
// original start Monday. It deliberately ignores any revision's UTC
// offset so instance identities cannot drift when the offset changes.
func (r *Rule) weekOfDate(d time.Time) (int, error) {
	day := civilDate(d)
	days := int(day.Sub(r.Start).Hours() / 24)
	if days < 0 {
		return 0, fmt.Errorf("schedule: date %s before rule start %s",
			day.Format("2006-01-02"), r.Start.Format("2006-01-02"))
	}
	week := days / 7
	if week >= r.Weeks {
		return 0, fmt.Errorf("schedule: date %s beyond %d weeks", day.Format("2006-01-02"), r.Weeks)
	}
	return week, nil
}

// rangeParams returns the range revision in effect for the given week:
// the one with the greatest FromWeek <= week; ties on FromWeek are
// resolved in favour of the greater revision number.
func (r *Rule) rangeParams(week int) (Params, bool) {
	i := sort.Search(len(r.revisions), func(i int) bool {
		return r.revisions[i].FromWeek > week
	}) - 1
	if i < 0 {
		return Params{}, false
	}
	return r.revisions[i], true
}

// paramsFor applies the priority chain: cancellation > override > range.
func (r *Rule) paramsFor(week int) (Params, bool) {
	if r.canceled[week] {
		return Params{}, false
	}
	if p, ok := r.overrides[week]; ok {
		return p, true
	}
	return r.rangeParams(week)
}

// instanceAt computes the concrete instance for a week index.
func (r *Rule) instanceAt(week int) (Instance, bool, error) {
	if week < 0 || week >= r.Weeks || r.canceled[week] {
		return Instance{}, false, nil
	}
	p, ok := r.paramsFor(week)
	if !ok {
		return Instance{}, false, nil
	}
	// Civil-date arithmetic anchored at the original start Monday.
	monday := r.Start.AddDate(0, 0, 7*week)
	day := monday.AddDate(0, 0, p.Weekday)

	offsetSec, err := minutesToSeconds(p.OffsetMin)
	if err != nil {
		return Instance{}, false, err
	}
	minuteDur, err := minutesToDuration(p.MinuteOfDay)
	if err != nil {
		return Instance{}, false, err
	}
	dur, err := minutesToDuration(p.DurationMin)
	if err != nil {
		return Instance{}, false, err
	}
	loc := time.FixedZone("fixed", int(offsetSec))
	local := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc).Add(minuteDur)
	start := local.UTC()
	return Instance{
		ID:        fmt.Sprintf("%s#%d", r.ID, week),
		Week:      week,
		Rev:       p.Rev,
		Start:     start,
		End:       start.Add(dur),
		OffsetMin: p.OffsetMin,
	}, true, nil
}

// expand materialises every live instance of the rule. It is the
// reference implementation used to cross-check the index, and is also
// used to snapshot rule versions for diffs. Queries must NOT use this.
func (r *Rule) expand() ([]Instance, error) {
	out := make([]Instance, 0, r.Weeks)
	for w := 0; w < r.Weeks; w++ {
		inst, ok, err := r.instanceAt(w)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, inst)
		}
	}
	return out, nil
}

// addRevision inserts (or replaces) a range revision, keeping the slice
// sorted by (FromWeek, Rev). It returns the first affected week.
func (r *Rule) addRevision(p Params) int {
	i := sort.Search(len(r.revisions), func(i int) bool {
		rp := r.revisions[i]
		return rp.FromWeek > p.FromWeek || (rp.FromWeek == p.FromWeek && rp.Rev >= p.Rev)
	})
	if i < len(r.revisions) && r.revisions[i].FromWeek == p.FromWeek && r.revisions[i].Rev == p.Rev {
		r.revisions[i] = p
	} else {
		r.revisions = append(r.revisions, Params{})
		copy(r.revisions[i+1:], r.revisions[i:])
		r.revisions[i] = p
	}
	return p.FromWeek
}
