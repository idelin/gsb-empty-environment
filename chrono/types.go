// Package chrono provides strictly separated time types: Instant (a physical
// point in time), WallClock (a civil reading tied to a named timezone table
// version), Duration (a physical span) and OffsetChoice (which occurrence of
// an ambiguous wall reading is selected).
//
// There are no implicit conversions between these types. The only way from
// WallClock to Instant is the explicit Resolve function, which requires a
// versioned timezone table and a recorded OffsetChoice for ambiguous
// readings. Precision and timezone-table version travel as metadata on every
// value.
package chrono

import (
	"errors"
	"fmt"
)

// Supported serialization range, in nanoseconds since the Unix epoch. The
// range is deliberately narrower than int64 ([-2^62, 2^62-1]) so that
// checked arithmetic keeps headroom. Values outside this range are rejected
// before any object is published.
const (
	MinNanos int64 = -(1 << 62)
	MaxNanos int64 = (1 << 62) - 1
)

var (
	ErrOutOfRange     = errors.New("chrono: value outside supported range")
	ErrOverflow       = errors.New("chrono: integer overflow")
	ErrBadPrecision   = errors.New("chrono: unknown precision")
	ErrBadChoice      = errors.New("chrono: unknown offset choice")
	ErrBadField       = errors.New("chrono: invalid wall-clock field")
	ErrUnknownTag     = errors.New("chrono: unknown type tag")
	ErrTruncated      = errors.New("chrono: truncated input")
	ErrTrailing       = errors.New("chrono: trailing bytes after value")
	ErrStringTooLong  = errors.New("chrono: string field exceeds maximum length")
	ErrGap            = errors.New("chrono: wall-clock reading falls in a gap")
	ErrAmbiguous      = errors.New("chrono: ambiguous wall-clock reading without an offset choice")
	ErrUnknownZone    = errors.New("chrono: unknown zone")
	ErrUnknownVersion = errors.New("chrono: unknown timezone table version")
)

// Precision is the grid a value is expressed on. It is metadata: scaling
// between precisions is an explicit, evidence-producing migration.
type Precision uint8

const (
	PrecSecond Precision = iota
	PrecMillisecond
	PrecMicrosecond
	PrecNanosecond
)

func (p Precision) Valid() bool { return p <= PrecNanosecond }

func (p Precision) String() string {
	switch p {
	case PrecSecond:
		return "s"
	case PrecMillisecond:
		return "ms"
	case PrecMicrosecond:
		return "us"
	case PrecNanosecond:
		return "ns"
	}
	return "?"
}

// NanosPerUnit returns how many nanoseconds one unit of p spans, or 0 for an
// invalid precision.
func (p Precision) NanosPerUnit() int64 {
	switch p {
	case PrecSecond:
		return 1000000000
	case PrecMillisecond:
		return 1000000
	case PrecMicrosecond:
		return 1000
	case PrecNanosecond:
		return 1
	}
	return 0
}

// Instant is a physical point in time: nanoseconds since the Unix epoch on a
// declared precision grid. It never carries a timezone.
type Instant struct {
	nanos int64
	prec  Precision
}

// NewInstant validates range and precision before publishing the value.
func NewInstant(nanos int64, p Precision) (Instant, error) {
	if !p.Valid() {
		return Instant{}, ErrBadPrecision
	}
	if nanos < MinNanos || nanos > MaxNanos {
		return Instant{}, ErrOutOfRange
	}
	return Instant{nanos: nanos, prec: p}, nil
}

// InstantFromSeconds builds an Instant from whole seconds plus a nanosecond
// fraction, rejecting overflow.
func InstantFromSeconds(sec, nsec int64, p Precision) (Instant, error) {
	if !p.Valid() {
		return Instant{}, ErrBadPrecision
	}
	if nsec < 0 || nsec >= 1000000000 {
		return Instant{}, ErrBadField
	}
	prod := sec * 1000000000
	if sec != 0 && prod/sec != 1000000000 {
		return Instant{}, ErrOverflow
	}
	sum := prod + nsec
	if sum < prod { // nsec >= 0, so only positive wrap is possible
		return Instant{}, ErrOverflow
	}
	return NewInstant(sum, p)
}

func (i Instant) Nanos() int64         { return i.nanos }
func (i Instant) Precision() Precision { return i.prec }

func (i Instant) String() string {
	return fmt.Sprintf("Instant{%d ns, prec=%s}", i.nanos, i.prec)
}

// Add shifts an Instant by a Duration with overflow and range checks.
func (i Instant) Add(d Duration) (Instant, error) {
	sum := i.nanos + d.nanos
	if (d.nanos > 0 && sum < i.nanos) || (d.nanos < 0 && sum > i.nanos) {
		return Instant{}, ErrOverflow
	}
	return NewInstant(sum, i.prec)
}

// Duration is a physical span in nanoseconds. Arithmetic is checked.
type Duration struct {
	nanos int64
}

func NewDuration(nanos int64) (Duration, error) {
	if nanos < MinNanos || nanos > MaxNanos {
		return Duration{}, ErrOutOfRange
	}
	return Duration{nanos: nanos}, nil
}

func (d Duration) Nanos() int64 { return d.nanos }

func (d Duration) String() string { return fmt.Sprintf("Duration{%d ns}", d.nanos) }

func (d Duration) Add(o Duration) (Duration, error) {
	sum := d.nanos + o.nanos
	if (o.nanos > 0 && sum < d.nanos) || (o.nanos < 0 && sum > d.nanos) {
		return Duration{}, ErrOverflow
	}
	return NewDuration(sum)
}

func (d Duration) Mul(k int64) (Duration, error) {
	if k == 0 || d.nanos == 0 {
		return Duration{}, nil
	}
	r := d.nanos * k
	if r/k != d.nanos {
		return Duration{}, ErrOverflow
	}
	return NewDuration(r)
}

// OffsetChoice records which occurrence of an ambiguous (overlapping) wall
// reading is selected. It is preserved as metadata across migrations even
// when a new table makes the reading unambiguous, so round trips can
// re-apply it.
type OffsetChoice uint8

const (
	ChoiceNone    OffsetChoice = iota // unambiguous, or no selection recorded
	ChoiceEarlier                     // the earlier instant (pre-transition offset)
	ChoiceLater                       // the later instant (post-transition offset)
)

func (c OffsetChoice) Valid() bool { return c <= ChoiceLater }

func (c OffsetChoice) String() string {
	switch c {
	case ChoiceNone:
		return "none"
	case ChoiceEarlier:
		return "earlier"
	case ChoiceLater:
		return "later"
	}
	return "?"
}

// WallClock is a civil reading: calendar fields plus a selected UTC offset,
// qualified by a zone name and the timezone-table version that produced the
// offset. It is not an Instant; resolving it requires Resolve.
type WallClock struct {
	year      int
	month     int
	day       int
	hour      int
	minute    int
	second    int
	nanos     int64 // sub-second fraction, 0..999999999
	offsetSec int32 // selected UTC offset in seconds
	choice    OffsetChoice
	prec      Precision
	zone      string
	tzVersion string
}

const maxNameLen = 256

// NewWallClock validates every field before publishing the value. Illegal
// tags/fields are rejected here, so no invalid WallClock can exist.
func NewWallClock(year, month, day, hour, minute, second int, nanos int64,
	offsetSec int32, choice OffsetChoice, prec Precision, zone, tzVersion string) (WallClock, error) {
	if !prec.Valid() {
		return WallClock{}, ErrBadPrecision
	}
	if !choice.Valid() {
		return WallClock{}, ErrBadChoice
	}
	if year < 1 || year > 9999 {
		return WallClock{}, fmt.Errorf("%w: year %d", ErrBadField, year)
	}
	if month < 1 || month > 12 {
		return WallClock{}, fmt.Errorf("%w: month %d", ErrBadField, month)
	}
	if day < 1 || day > daysIn(year, month) {
		return WallClock{}, fmt.Errorf("%w: day %d", ErrBadField, day)
	}
	if hour < 0 || hour > 23 {
		return WallClock{}, fmt.Errorf("%w: hour %d", ErrBadField, hour)
	}
	if minute < 0 || minute > 59 {
		return WallClock{}, fmt.Errorf("%w: minute %d", ErrBadField, minute)
	}
	if second < 0 || second > 59 {
		return WallClock{}, fmt.Errorf("%w: second %d", ErrBadField, second)
	}
	if nanos < 0 || nanos > 999999999 {
		return WallClock{}, fmt.Errorf("%w: nanos %d", ErrBadField, nanos)
	}
	if offsetSec <= -86400 || offsetSec >= 86400 {
		return WallClock{}, fmt.Errorf("%w: offset %d", ErrBadField, offsetSec)
	}
	if zone == "" || len(zone) > maxNameLen {
		return WallClock{}, fmt.Errorf("%w: zone name", ErrBadField)
	}
	if tzVersion == "" || len(tzVersion) > maxNameLen {
		return WallClock{}, fmt.Errorf("%w: tz version", ErrBadField)
	}
	return WallClock{
		year: year, month: month, day: day,
		hour: hour, minute: minute, second: second,
		nanos: nanos, offsetSec: offsetSec, choice: choice,
		prec: prec, zone: zone, tzVersion: tzVersion,
	}, nil
}

func (w WallClock) Year() int            { return w.year }
func (w WallClock) Month() int           { return w.month }
func (w WallClock) Day() int             { return w.day }
func (w WallClock) Hour() int            { return w.hour }
func (w WallClock) Minute() int          { return w.minute }
func (w WallClock) Second() int          { return w.second }
func (w WallClock) Nanos() int64         { return w.nanos }
func (w WallClock) OffsetSec() int32     { return w.offsetSec }
func (w WallClock) Choice() OffsetChoice { return w.choice }
func (w WallClock) Precision() Precision { return w.prec }
func (w WallClock) Zone() string         { return w.zone }
func (w WallClock) TZVersion() string    { return w.tzVersion }

func (w WallClock) String() string {
	return fmt.Sprintf("WallClock{%04d-%02d-%02d %02d:%02d:%02d.%09d %+ds %s %s@%s prec=%s}",
		w.year, w.month, w.day, w.hour, w.minute, w.second, w.nanos,
		w.offsetSec, w.choice, w.zone, w.tzVersion, w.prec)
}

// wallNanos returns the civil fields as nanoseconds on a zone-less timeline
// (i.e. what the reading would be if the offset were zero). Valid years keep
// this far inside int64.
func (w WallClock) wallNanos() int64 {
	days := daysFromCivil(w.year, w.month, w.day)
	return days*86400000000000 +
		int64(w.hour)*3600000000000 +
		int64(w.minute)*60000000000 +
		int64(w.second)*1000000000 +
		w.nanos
}

func daysIn(year, month int) int {
	switch month {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	}
	if isLeap(year) {
		return 29
	}
	return 28
}

func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// daysFromCivil is Howard Hinnant's algorithm: days since 1970-01-01.
func daysFromCivil(y, m, d int) int64 {
	yy := int64(y)
	if m <= 2 {
		yy--
	}
	var era int64
	if yy >= 0 {
		era = yy / 400
	} else {
		era = (yy - 399) / 400
	}
	yoe := yy - era*400
	mp := int64((m + 9) % 12)
	doy := (153*mp+2)/5 + int64(d) - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}
