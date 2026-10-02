package chrono

import "fmt"

// LossKind classifies why a migration is lossy.
type LossKind string

const (
	LossPrecision LossKind = "precision-truncation"
	LossAmbiguity LossKind = "ambiguity-collapsed"
)

// Loss is one piece of evidence that a migration discarded information.
type Loss struct {
	Kind   LossKind
	Detail string
}

// Migration is the evidence record produced by every migration. Reversible
// is true only when no information was discarded; Losses and Notes explain
// the outcome either way. For the lossy reverse direction, callers use
// ReverseScaleInterval (a precision interval) or ReverseWallClock (all
// candidates).
type Migration struct {
	From       string
	To         string
	Reversible bool
	Losses     []Loss
	Notes      []string
}

func (m Migration) String() string {
	s := fmt.Sprintf("migrate %s -> %s: reversible=%v", m.From, m.To, m.Reversible)
	for _, l := range m.Losses {
		s += fmt.Sprintf("\n  loss[%s]: %s", l.Kind, l.Detail)
	}
	for _, n := range m.Notes {
		s += fmt.Sprintf("\n  note: %s", n)
	}
	return s
}

// ScaleInstant moves an Instant onto precision grid p, flooring toward
// negative infinity. Scaling up (or scaling down onto an exact grid point)
// is lossless and reversible; anything else records the dropped sub-grid
// nanoseconds as evidence. Nanoseconds are never silently routed through a
// millisecond truncation: each scaling uses the target grid directly.
func ScaleInstant(i Instant, p Precision) (Instant, Migration, error) {
	m := Migration{
		From:       fmt.Sprintf("instant(prec=%s)", i.Precision()),
		To:         fmt.Sprintf("instant(prec=%s)", p),
		Reversible: true,
	}
	if !p.Valid() {
		return Instant{}, m, ErrBadPrecision
	}
	unit := p.NanosPerUnit()
	q := i.nanos / unit
	r := i.nanos % unit
	if r < 0 {
		q--
		r += unit
	}
	if r != 0 {
		m.Reversible = false
		m.Losses = append(m.Losses, Loss{
			Kind:   LossPrecision,
			Detail: fmt.Sprintf("dropped %d ns below the %s grid", r, p),
		})
	}
	out, err := NewInstant(q*unit, p)
	if err != nil {
		return Instant{}, m, err
	}
	if r == 0 {
		m.Notes = append(m.Notes, "exact on target grid; fully reversible")
	}
	return out, m, nil
}

// ReverseScaleInterval returns the closed interval of nanosecond values that
// could have produced i when scaled onto i's precision grid: every candidate
// for the lossy reverse direction. For PrecNanosecond the interval is a
// single point.
func ReverseScaleInterval(i Instant) (lo, hi int64) {
	unit := i.Precision().NanosPerUnit()
	return i.Nanos(), i.Nanos() + unit - 1
}

// ScaleWallClock moves a WallClock's sub-second fraction onto grid p with
// the same evidence rules as ScaleInstant. Civil fields, offset, choice and
// table version are untouched.
func ScaleWallClock(w WallClock, p Precision) (WallClock, Migration, error) {
	m := Migration{
		From:       fmt.Sprintf("wallclock(prec=%s)", w.Precision()),
		To:         fmt.Sprintf("wallclock(prec=%s)", p),
		Reversible: true,
	}
	if !p.Valid() {
		return WallClock{}, m, ErrBadPrecision
	}
	unit := p.NanosPerUnit()
	q := w.nanos / unit
	r := w.nanos % unit
	if r != 0 {
		m.Reversible = false
		m.Losses = append(m.Losses, Loss{
			Kind:   LossPrecision,
			Detail: fmt.Sprintf("dropped %d ns below the %s grid", r, p),
		})
	}
	out, err := NewWallClock(w.year, w.month, w.day, w.hour, w.minute, w.second,
		q*unit, w.offsetSec, w.choice, p, w.zone, w.tzVersion)
	if err != nil {
		return WallClock{}, m, err
	}
	return out, m, nil
}

// MigrateWallClock re-interprets a WallClock under a newer table version of
// the same zone. The civil fields and the recorded OffsetChoice are carried
// over verbatim; the offset is recomputed from the new table.
//
//   - Still ambiguous: the recorded choice is re-applied (reversible).
//   - Now unambiguous: the single candidate's offset is used, the choice is
//     kept as metadata so a later migration back can re-apply it, and the
//     collapse is recorded as loss evidence.
//   - Now a gap: the migration fails; no value is published.
//
// The result is still a WallClock. Nothing here produces an Instant, so a
// wall reading can never leak into instant semantics silently.
func MigrateWallClock(w WallClock, reg *Registry, toVersion string) (WallClock, Migration, error) {
	m := Migration{
		From:       fmt.Sprintf("wallclock(%s@%s)", w.Zone(), w.TZVersion()),
		To:         fmt.Sprintf("wallclock(%s@%s)", w.Zone(), toVersion),
		Reversible: true,
	}
	to, err := reg.Lookup(w.Zone(), toVersion)
	if err != nil {
		return WallClock{}, m, err
	}
	cands := to.Candidates(w)
	if len(cands) == 0 {
		return WallClock{}, m, fmt.Errorf("%w under table %s", ErrGap, toVersion)
	}
	var offset int32
	if len(cands) == 1 {
		offset = int32((w.wallNanos() - cands[0]) / 1000000000)
		if w.Choice() != ChoiceNone {
			m.Reversible = false
			m.Losses = append(m.Losses, Loss{
				Kind:   LossAmbiguity,
				Detail: "new table has a single candidate; recorded choice retained as metadata only",
			})
		}
	} else {
		switch w.Choice() {
		case ChoiceEarlier:
			offset = int32((w.wallNanos() - cands[0]) / 1000000000)
		case ChoiceLater:
			offset = int32((w.wallNanos() - cands[1]) / 1000000000)
		default:
			return WallClock{}, m, fmt.Errorf("%w under table %s", ErrAmbiguous, toVersion)
		}
		m.Notes = append(m.Notes, "ambiguity preserved under new table; recorded choice re-applied")
	}
	if offset != w.OffsetSec() {
		m.Notes = append(m.Notes, fmt.Sprintf("offset changed %+ds -> %+ds", w.OffsetSec(), offset))
	}
	out, err := NewWallClock(w.year, w.month, w.day, w.hour, w.minute, w.second,
		w.nanos, offset, w.choice, w.prec, w.zone, toVersion)
	if err != nil {
		return WallClock{}, m, err
	}
	return out, m, nil
}

// ReverseWallClock is the lossy reverse of MigrateWallClock: it returns
// every WallClock under fromVersion whose forward migration could have
// produced w, i.e. all candidates for the civil fields under the old table.
// The caller selects among them (typically via the retained OffsetChoice).
func ReverseWallClock(w WallClock, reg *Registry, fromVersion string) ([]WallClock, Migration, error) {
	m := Migration{
		From:       fmt.Sprintf("wallclock(%s@%s)", w.Zone(), w.TZVersion()),
		To:         fmt.Sprintf("wallclock(%s@%s)", w.Zone(), fromVersion),
		Reversible: true,
	}
	from, err := reg.Lookup(w.Zone(), fromVersion)
	if err != nil {
		return nil, m, err
	}
	cands := from.Candidates(w)
	if len(cands) == 0 {
		return nil, m, fmt.Errorf("%w under table %s", ErrGap, fromVersion)
	}
	out := make([]WallClock, 0, len(cands))
	for i, inst := range cands {
		choice := ChoiceNone
		if len(cands) > 1 {
			if i == 0 {
				choice = ChoiceEarlier
			} else {
				choice = ChoiceLater
			}
		}
		offset := int32((w.wallNanos() - inst) / 1000000000)
		wc, err := NewWallClock(w.year, w.month, w.day, w.hour, w.minute, w.second,
			w.nanos, offset, choice, w.prec, w.zone, fromVersion)
		if err != nil {
			return nil, m, err
		}
		out = append(out, wc)
	}
	m.Notes = append(m.Notes, fmt.Sprintf("%d candidate(s) under %s", len(out), fromVersion))
	return out, m, nil
}
