package chronos

import (
	"errors"
	"fmt"
)

// StepKind identifies a migration step kind.
type StepKind uint8

const (
	StepPrecisionScale StepKind = iota + 1
	StepTZRebind
)

// Step records one migration step with everything needed to invert it.
type Step struct {
	Kind     StepKind
	Lossy    bool
	Detail   string
	FromPrec Precision
	ToPrec   Precision
	// TZ rebind bookkeeping.
	OldVersion string
	NewVersion string
	OldChoice  *OffsetChoice // set when the selected offset had to change
}

// Evidence is the ordered record of migration steps. A migration is
// reversible iff no step is lossy.
type Evidence struct{ Steps []Step }

func (e Evidence) Reversible() bool {
	for _, s := range e.Steps {
		if s.Lossy {
			return false
		}
	}
	return true
}

func (e Evidence) Append(s Step) Evidence {
	e.Steps = append(e.Steps, s)
	return e
}

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

// ScaleInstant changes an instant's precision. Scaling up is lossless;
// scaling down truncates toward the coarser unit and is lossy when the
// discarded fraction is non-zero. Nanoseconds are never routed through an
// intermediate millisecond truncation: scaling is computed directly from
// the stored nanosecond field.
func ScaleInstant(i Instant, p Precision) (Instant, Step, error) {
	if !p.Valid() {
		return Instant{}, Step{}, fmt.Errorf("chronos: illegal precision label %d", uint8(p))
	}
	unit := p.NanosPerUnit()
	discarded := int64(i.nanos) % unit
	out, err := NewInstant(i.sec, uint32(int64(i.nanos)-discarded), p)
	st := Step{
		Kind: StepPrecisionScale, Lossy: discarded != 0,
		FromPrec: i.prec, ToPrec: p,
		Detail: fmt.Sprintf("precision %s -> %s, discarded %d ns", i.prec, p, discarded),
	}
	return out, st, err
}

// ScaleWall changes a wall clock's precision (sub-second field only; civil
// fields and offset choice are untouched).
func ScaleWall(w WallClock, p Precision) (WallClock, Step, error) {
	if !p.Valid() {
		return WallClock{}, Step{}, fmt.Errorf("chronos: illegal precision label %d", uint8(p))
	}
	unit := p.NanosPerUnit()
	discarded := int64(w.nanos) % unit
	out := w
	out.nanos = uint32(int64(w.nanos) - discarded)
	out.prec = p
	st := Step{
		Kind: StepPrecisionScale, Lossy: discarded != 0,
		FromPrec: w.prec, ToPrec: p,
		Detail: fmt.Sprintf("precision %s -> %s, discarded %d ns", w.prec, p, discarded),
	}
	return out, st, nil
}

// RebindWall migrates a wall clock to a new timezone table version. The
// civil fields are never reinterpreted as an instant: they are kept as-is
// and only the offset selection is re-resolved. If the previously selected
// offset is still valid it is preserved (lossless, ambiguity selection
// retained). Otherwise the nearest offset is chosen and the step is
// recorded as lossy with the old choice kept for reversal.
func RebindWall(w WallClock, newTab *TZTable) (WallClock, Step, error) {
	if newTab == nil || newTab.Zone != w.zone {
		return WallClock{}, Step{}, fmt.Errorf("chronos: cannot rebind %s to a different zone", w.zone)
	}
	st := Step{Kind: StepTZRebind, OldVersion: w.tzVersion, NewVersion: newTab.Version}
	cands := newTab.ResolveWall(w.wallSec())
	if len(cands) == 0 {
		return WallClock{}, Step{}, ErrGap
	}
	for _, c := range cands {
		if c.OffsetSec == w.choice.OffsetSec {
			out := w
			out.tzVersion = newTab.Version
			out.choice = c
			st.Detail = fmt.Sprintf("tz %s -> %s: selected offset %+ds preserved", w.tzVersion, newTab.Version, c.OffsetSec)
			return out, st, nil
		}
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if abs32(c.OffsetSec-w.choice.OffsetSec) < abs32(best.OffsetSec-w.choice.OffsetSec) {
			best = c
		}
	}
	oc := w.choice
	st.Lossy = true
	st.OldChoice = &oc
	st.Detail = fmt.Sprintf("tz %s -> %s: offset %+ds no longer valid, rebound to %+ds",
		w.tzVersion, newTab.Version, w.choice.OffsetSec, best.OffsetSec)
	out := w
	out.tzVersion = newTab.Version
	out.choice = best
	return out, st, nil
}

// Interval is a half-open precision interval [Lo, Hi) over instants,
// returned when a lossy precision scale is reversed.
type Interval struct{ Lo, Hi Instant }

// WallInterval is a half-open precision interval over wall clocks.
type WallInterval struct{ Lo, Hi WallClock }

func addNanosToInstant(i Instant, delta int64, p Precision) (Instant, error) {
	n := int64(i.nanos) + delta
	sec := i.sec
	for n >= nanosPerSec {
		n -= nanosPerSec
		sec++
	}
	for n < 0 {
		n += nanosPerSec
		sec--
	}
	return NewInstant(sec, uint32(n), p)
}

// ReverseInstant inverts migration steps on an instant. Lossless steps map
// back to exactly one candidate; a lossy precision scale maps back to the
// half-open interval of all originals that truncate to the current value.
func ReverseInstant(steps []Step, v Instant) ([]Instant, *Interval, error) {
	cur := v
	var iv *Interval
	for k := len(steps) - 1; k >= 0; k-- {
		s := steps[k]
		switch s.Kind {
		case StepPrecisionScale:
			if !s.Lossy {
				continue
			}
			unit := s.ToPrec.NanosPerUnit()
			lo, err := NewInstant(cur.sec, cur.nanos, s.FromPrec)
			if err != nil {
				return nil, nil, err
			}
			hi, err := addNanosToInstant(lo, unit, s.FromPrec)
			if err != nil {
				return nil, nil, err
			}
			iv = &Interval{Lo: lo, Hi: hi}
		case StepTZRebind:
			return nil, nil, errors.New("chronos: a tz rebind step cannot be reversed on an instant")
		default:
			return nil, nil, fmt.Errorf("chronos: illegal step kind %d", uint8(s.Kind))
		}
	}
	if iv != nil {
		return nil, iv, nil
	}
	return []Instant{cur}, nil, nil
}

// ReverseWall inverts migration steps on a wall clock. A tz rebind step
// expands into every candidate offset under the old table version (all
// candidates are returned, including the recorded old choice when it is
// still valid). A lossy precision step yields a precision interval.
func ReverseWall(steps []Step, w WallClock, reg *Registry) ([]WallClock, *WallInterval, error) {
	cur := []WallClock{w}
	var unit int64
	var fromPrec Precision
	for k := len(steps) - 1; k >= 0; k-- {
		s := steps[k]
		switch s.Kind {
		case StepPrecisionScale:
			if s.Lossy {
				unit = s.ToPrec.NanosPerUnit()
				fromPrec = s.FromPrec
			}
		case StepTZRebind:
			if reg == nil {
				return nil, nil, errors.New("chronos: reversing a tz rebind needs a registry")
			}
			var next []WallClock
			for _, c := range cur {
				oldTab, err := reg.Lookup(c.zone, s.OldVersion)
				if err != nil {
					return nil, nil, err
				}
				for _, oc := range oldTab.ResolveWall(c.wallSec()) {
					v := c
					v.tzVersion = s.OldVersion
					v.choice = oc
					next = append(next, v)
				}
			}
			cur = next
		default:
			return nil, nil, fmt.Errorf("chronos: illegal step kind %d", uint8(s.Kind))
		}
	}
	if unit != 0 {
		if len(cur) == 0 {
			return nil, nil, errors.New("chronos: no candidates for precision interval")
		}
		lo := cur[0]
		lo.prec = fromPrec
		total := int64(lo.nanos) + unit
		hi := addWallSeconds(lo, total/nanosPerSec)
		hi.nanos = uint32(total % nanosPerSec)
		return cur, &WallInterval{Lo: lo, Hi: hi}, nil
	}
	return cur, nil, nil
}

// addWallSeconds shifts the civil fields of w by delta seconds (the offset
// choice is kept; civil arithmetic never touches instants).
func addWallSeconds(w WallClock, delta int64) WallClock {
	total := w.wallSec() + delta
	days := divFloor(total, 86400)
	rem := total - days*86400
	y, mo, d := civilFromDays(days)
	w.year, w.month, w.day = y, mo, d
	w.hour = uint8(rem / 3600)
	w.min = uint8((rem % 3600) / 60)
	w.sec = uint8(rem % 60)
	return w
}
