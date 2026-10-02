// Package ledger implements a temporal KPI ledger for employee performance
// evaluation. KPI targets and weights are versioned per evaluation period;
// each version is an immutable, signed strategy snapshot whose assignments
// carry half-open day intervals of validity. Scores can be recomputed under
// any historical snapshot, and the score impact of a version change is
// recorded together with the responsible author's version signature.
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Interval is a half-open day interval [From, To) inside an evaluation
// period. Days are 1-based: day 1 is the first day of the period.
type Interval struct {
	From int
	To   int
}

// Contains reports whether day falls inside the interval.
func (iv Interval) Contains(day int) bool { return day >= iv.From && day < iv.To }

// Len returns the number of days covered by the interval.
func (iv Interval) Len() int { return iv.To - iv.From }

// Assignment pins a KPI's target value and weight to a validity interval.
type Assignment struct {
	KPI    string
	Target float64
	Weight float64
	Valid  Interval
}

// Version is an immutable, signed strategy snapshot.
type Version struct {
	ID          int
	Author      string
	Note        string
	RecordedDay int // day of the period on which the version was recorded
	CreatedAt   time.Time
	Assignments []Assignment

	signature string
}

// Signature returns the content-addressed version signature. It binds the
// responsible author to the exact set of assignments and never changes.
func (v Version) Signature() string { return v.signature }

func sign(v Version) string {
	h := sha256.New()
	fmt.Fprintf(h, "v%d|%s|%s|day=%d|at=%d\n", v.ID, v.Author, v.Note, v.RecordedDay, v.CreatedAt.Unix())
	as := append([]Assignment(nil), v.Assignments...)
	sort.Slice(as, func(i, j int) bool {
		if as[i].KPI != as[j].KPI {
			return as[i].KPI < as[j].KPI
		}
		return as[i].Valid.From < as[j].Valid.From
	})
	for _, a := range as {
		fmt.Fprintf(h, "%s|target=%g|weight=%g|[%d,%d)\n", a.KPI, a.Target, a.Weight, a.Valid.From, a.Valid.To)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Ledger is an append-only store of versions for one evaluation period.
type Ledger struct {
	periodDays int
	versions   []Version
}

// New creates a ledger for an evaluation period of periodDays days.
func New(periodDays int) (*Ledger, error) {
	if periodDays <= 0 {
		return nil, errors.New("ledger: periodDays must be positive")
	}
	return &Ledger{periodDays: periodDays}, nil
}

// PeriodDays returns the length of the evaluation period in days.
func (l *Ledger) PeriodDays() int { return l.periodDays }

// Append validates and records a new immutable version, returning it with
// its assigned ID and signature.
func (l *Ledger) Append(author, note string, recordedDay int, at time.Time, as []Assignment) (Version, error) {
	if author == "" {
		return Version{}, errors.New("ledger: author (responsible signer) is required")
	}
	if recordedDay < 1 || recordedDay > l.periodDays {
		return Version{}, fmt.Errorf("ledger: recordedDay %d outside period [1, %d]", recordedDay, l.periodDays)
	}
	if len(as) == 0 {
		return Version{}, errors.New("ledger: version must contain at least one assignment")
	}
	for _, a := range as {
		if a.KPI == "" {
			return Version{}, errors.New("ledger: assignment KPI name is required")
		}
		if a.Target <= 0 {
			return Version{}, fmt.Errorf("ledger: KPI %q target must be positive", a.KPI)
		}
		if a.Weight < 0 || a.Weight > 1 {
			return Version{}, fmt.Errorf("ledger: KPI %q weight %g outside [0, 1]", a.KPI, a.Weight)
		}
		if a.Valid.From < 1 || a.Valid.To > l.periodDays+1 || a.Valid.From >= a.Valid.To {
			return Version{}, fmt.Errorf("ledger: KPI %q invalid interval [%d, %d) for period of %d days",
				a.KPI, a.Valid.From, a.Valid.To, l.periodDays)
		}
	}
	v := Version{
		ID:          len(l.versions) + 1,
		Author:      author,
		Note:        note,
		RecordedDay: recordedDay,
		CreatedAt:   at.UTC(),
		Assignments: append([]Assignment(nil), as...),
	}
	v.signature = sign(v)
	l.versions = append(l.versions, v)
	return v, nil
}

// Version returns the version with the given ID.
func (l *Ledger) Version(id int) (Version, bool) {
	if id < 1 || id > len(l.versions) {
		return Version{}, false
	}
	return l.versions[id-1], true
}

// Latest returns the most recently recorded version.
func (l *Ledger) Latest() (Version, bool) {
	return l.Version(len(l.versions))
}

// Versions returns all recorded versions in order.
func (l *Ledger) Versions() []Version { return append([]Version(nil), l.versions...) }

// ActiveAt returns the assignments of a version that are valid on the given
// day of the period — a point-in-time view of the strategy snapshot.
func (l *Ledger) ActiveAt(versionID, day int) ([]Assignment, error) {
	v, ok := l.Version(versionID)
	if !ok {
		return nil, fmt.Errorf("ledger: version %d not found", versionID)
	}
	if day < 1 || day > l.periodDays {
		return nil, fmt.Errorf("ledger: day %d outside period [1, %d]", day, l.periodDays)
	}
	var out []Assignment
	for _, a := range v.Assignments {
		if a.Valid.Contains(day) {
			out = append(out, a)
		}
	}
	return out, nil
}

// Actuals maps a KPI name to its actual achieved value at period end.
type Actuals map[string]float64

// effectiveWeights averages each KPI's daily weight over the whole period.
func (l *Ledger) effectiveWeights(v Version) map[string]float64 {
	days := float64(l.periodDays)
	w := map[string]float64{}
	for _, a := range v.Assignments {
		w[a.KPI] += a.Weight * float64(a.Valid.Len()) / days
	}
	return w
}

// ScoreBreakdown details a weighted-score computation under one version.
type ScoreBreakdown struct {
	VersionID int
	Weights   map[string]float64 // period-averaged effective weight per KPI
	Ratios    map[string]float64 // actual / target per KPI
	Total     float64            // sum of weight * ratio
}

// Score computes the final weighted score under the strategy snapshot of the
// given version, enabling historical what-if evaluation replays.
func (l *Ledger) Score(versionID int, actuals Actuals) (ScoreBreakdown, error) {
	v, ok := l.Version(versionID)
	if !ok {
		return ScoreBreakdown{}, fmt.Errorf("ledger: version %d not found", versionID)
	}
	targets := map[string]float64{}
	for _, a := range v.Assignments {
		targets[a.KPI] = a.Target
	}
	weights := l.effectiveWeights(v)
	sb := ScoreBreakdown{
		VersionID: versionID,
		Weights:   weights,
		Ratios:    map[string]float64{},
	}
	for kpi, w := range weights {
		actual, ok := actuals[kpi]
		if !ok {
			return ScoreBreakdown{}, fmt.Errorf("ledger: missing actual value for KPI %q", kpi)
		}
		ratio := actual / targets[kpi]
		sb.Ratios[kpi] = ratio
		sb.Total += w * ratio
	}
	return sb, nil
}

// Impact records how replacing one strategy snapshot with another changes
// the final weighted score, keeping the responsible author's signature.
type Impact struct {
	FromVersion int
	ToVersion   int
	Author      string // responsible signer of the new version
	Note        string
	Signature   string // immutable signature of the new version
	RecordedDay int
	ScoreBefore float64
	ScoreAfter  float64
	Delta       float64 // ScoreAfter - ScoreBefore
}

// Impact computes the score difference between two versions and attaches the
// new version's authorship and signature for audit.
func (l *Ledger) Impact(fromID, toID int, actuals Actuals) (Impact, error) {
	before, err := l.Score(fromID, actuals)
	if err != nil {
		return Impact{}, err
	}
	after, err := l.Score(toID, actuals)
	if err != nil {
		return Impact{}, err
	}
	v, _ := l.Version(toID)
	return Impact{
		FromVersion: fromID,
		ToVersion:   toID,
		Author:      v.Author,
		Note:        v.Note,
		Signature:   v.Signature(),
		RecordedDay: v.RecordedDay,
		ScoreBefore: before.Total,
		ScoreAfter:  after.Total,
		Delta:       after.Total - before.Total,
	}, nil
}
