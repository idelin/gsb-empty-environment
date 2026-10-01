package chronos

import "fmt"

// WallClock is a civil (local) time together with its metadata: precision,
// zone, timezone table version, and the selected offset. It is a distinct
// type from Instant; conversion is only possible through explicit calls
// that take a TZTable matching the metadata.
type WallClock struct {
	year      int32
	month     uint8
	day       uint8
	hour      uint8
	min       uint8
	sec       uint8
	nanos     uint32
	prec      Precision
	zone      string
	tzVersion string
	choice    OffsetChoice
}

// NewWallClock validates all fields, resolves the civil time in tab, and
// selects candidate `fold`. It fails (publishing no object) on illegal
// labels, invalid calendar fields, gaps, or an out-of-range fold.
func NewWallClock(year int32, month, day, hour, min, sec uint8, nanos uint32, prec Precision, tab *TZTable, fold uint8) (WallClock, error) {
	if !prec.Valid() {
		return WallClock{}, fmt.Errorf("chronos: illegal precision label %d", uint8(prec))
	}
	if year < 1 || year > 9999 {
		return WallClock{}, fmt.Errorf("chronos: year %d outside supported range [1, 9999]", year)
	}
	if month < 1 || month > 12 {
		return WallClock{}, fmt.Errorf("chronos: illegal month %d", month)
	}
	if day < 1 || day > daysInMonth(year, month) {
		return WallClock{}, fmt.Errorf("chronos: illegal day %d for %04d-%02d", day, year, month)
	}
	if hour > 23 || min > 59 || sec > 59 {
		return WallClock{}, fmt.Errorf("chronos: illegal time %02d:%02d:%02d", hour, min, sec)
	}
	if nanos >= uint32(nanosPerSec) {
		return WallClock{}, fmt.Errorf("chronos: nanoseconds %d out of range", nanos)
	}
	if int64(nanos)%prec.NanosPerUnit() != 0 {
		return WallClock{}, fmt.Errorf("chronos: wall clock %d ns not aligned to precision %s", nanos, prec)
	}
	if tab == nil {
		return WallClock{}, fmt.Errorf("chronos: nil timezone table")
	}
	w := WallClock{
		year: year, month: month, day: day,
		hour: hour, min: min, sec: sec, nanos: nanos,
		prec: prec, zone: tab.Zone, tzVersion: tab.Version,
	}
	cands := tab.ResolveWall(w.wallSec())
	if len(cands) == 0 {
		return WallClock{}, ErrGap
	}
	if int(fold) >= len(cands) {
		return WallClock{}, fmt.Errorf("chronos: fold %d out of range (%d candidates)", fold, len(cands))
	}
	w.choice = cands[fold]
	return w, nil
}

func (w WallClock) Year() int32          { return w.year }
func (w WallClock) Month() uint8         { return w.month }
func (w WallClock) Day() uint8           { return w.day }
func (w WallClock) Hour() uint8          { return w.hour }
func (w WallClock) Minute() uint8        { return w.min }
func (w WallClock) Second() uint8        { return w.sec }
func (w WallClock) Nanos() uint32        { return w.nanos }
func (w WallClock) Precision() Precision { return w.prec }
func (w WallClock) Zone() string         { return w.zone }
func (w WallClock) TZVersion() string    { return w.tzVersion }
func (w WallClock) Choice() OffsetChoice { return w.choice }

func (w WallClock) wallSec() int64 {
	return daysFromCivil(w.year, w.month, w.day)*86400 +
		int64(w.hour)*3600 + int64(w.min)*60 + int64(w.sec)
}

func (w WallClock) String() string {
	return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%09d %s%+ds(fold=%d)@%s[%s]",
		w.year, w.month, w.day, w.hour, w.min, w.sec, w.nanos,
		w.zone, w.choice.OffsetSec, w.choice.Fold, w.tzVersion, w.prec)
}

func (w WallClock) civilEqual(o WallClock) bool {
	return w.year == o.year && w.month == o.month && w.day == o.day &&
		w.hour == o.hour && w.min == o.min && w.sec == o.sec && w.nanos == o.nanos
}

// Equal reports full equality including metadata and offset choice.
func (w WallClock) Equal(o WallClock) bool {
	return w.civilEqual(o) && w.prec == o.prec && w.zone == o.zone &&
		w.tzVersion == o.tzVersion && w.choice == o.choice
}

func (w WallClock) checkTable(tab *TZTable) error {
	if tab == nil || tab.Zone != w.zone || tab.Version != w.tzVersion {
		return fmt.Errorf("%w: value is %s@%s", ErrVersionMismatch, w.zone, w.tzVersion)
	}
	return nil
}

// Candidates explicitly resolves the wall time to every possible instant
// under the given table. The table must match the value's metadata.
func (w WallClock) Candidates(tab *TZTable) ([]Instant, error) {
	if err := w.checkTable(tab); err != nil {
		return nil, err
	}
	cands := tab.ResolveWall(w.wallSec())
	out := make([]Instant, 0, len(cands))
	for _, c := range cands {
		in, err := NewInstant(w.wallSec()-int64(c.OffsetSec), w.nanos, w.prec)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

// Instant explicitly resolves the wall time to the single instant matching
// the stored offset choice. It errors if the choice is not valid in tab.
func (w WallClock) Instant(tab *TZTable) (Instant, error) {
	if err := w.checkTable(tab); err != nil {
		return Instant{}, err
	}
	for _, c := range tab.ResolveWall(w.wallSec()) {
		if c.OffsetSec == w.choice.OffsetSec {
			return NewInstant(w.wallSec()-int64(c.OffsetSec), w.nanos, w.prec)
		}
	}
	return Instant{}, fmt.Errorf("chronos: selected offset %+ds not valid in table %s@%s",
		w.choice.OffsetSec, tab.Zone, tab.Version)
}

// Resolvable reports whether the registry knows this value's table version.
func (w WallClock) Resolvable(reg *Registry) bool {
	_, err := reg.Lookup(w.zone, w.tzVersion)
	return err == nil
}

// InstantFrom resolves through the registry; unknown versions are marked
// unresolvable (ErrUnknownTZ) rather than guessed.
func (w WallClock) InstantFrom(reg *Registry) (Instant, error) {
	tab, err := reg.Lookup(w.zone, w.tzVersion)
	if err != nil {
		return Instant{}, err
	}
	return w.Instant(tab)
}

// WallFromInstant is the explicit Instant -> WallClock conversion. It picks
// the offset in effect at the instant (fold 0).
func WallFromInstant(i Instant, tab *TZTable) (WallClock, error) {
	off := tab.OffsetAt(i.sec)
	local := i.sec + int64(off)
	days := divFloor(local, 86400)
	rem := local - days*86400
	y, mo, d := civilFromDays(days)
	w := WallClock{
		year: y, month: mo, day: d,
		hour: uint8(rem / 3600), min: uint8((rem % 3600) / 60), sec: uint8(rem % 60),
		nanos: i.nanos, prec: i.prec,
		zone: tab.Zone, tzVersion: tab.Version,
		choice: OffsetChoice{OffsetSec: off, Fold: 0},
	}
	return w, nil
}
