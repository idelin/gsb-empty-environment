package chronos

import "fmt"

// Supported serialization range for instants: years 0001..9999 UTC.
const (
	MinInstantSec int64 = -62135596800 // 0001-01-01T00:00:00Z
	MaxInstantSec int64 = 253402300799 // 9999-12-31T23:59:59Z
)

const nanosPerSec = int64(1_000_000_000)

// Instant is an absolute point in time (Unix seconds + nanoseconds) with a
// precision. It is never produced implicitly from a WallClock.
type Instant struct {
	sec   int64
	nanos uint32
	prec  Precision
}

func NewInstant(sec int64, nanos uint32, p Precision) (Instant, error) {
	if !p.Valid() {
		return Instant{}, fmt.Errorf("chronos: illegal precision label %d", uint8(p))
	}
	if sec < MinInstantSec || sec > MaxInstantSec {
		return Instant{}, fmt.Errorf("chronos: instant seconds %d outside supported range [%d, %d]", sec, MinInstantSec, MaxInstantSec)
	}
	if nanos >= uint32(nanosPerSec) {
		return Instant{}, fmt.Errorf("chronos: nanoseconds %d out of range", nanos)
	}
	if int64(nanos)%p.NanosPerUnit() != 0 {
		return Instant{}, fmt.Errorf("chronos: instant %d ns not aligned to precision %s", nanos, p)
	}
	return Instant{sec: sec, nanos: nanos, prec: p}, nil
}

func (i Instant) Sec() int64           { return i.sec }
func (i Instant) Nanos() uint32        { return i.nanos }
func (i Instant) Precision() Precision { return i.prec }

func (i Instant) Before(o Instant) bool {
	return i.sec < o.sec || (i.sec == o.sec && i.nanos < o.nanos)
}

func (i Instant) Equal(o Instant) bool {
	return i.sec == o.sec && i.nanos == o.nanos && i.prec == o.prec
}

// Add shifts an instant by a duration with overflow and range checking.
func (i Instant) Add(d Duration) (Instant, error) {
	carry := d.nanos / nanosPerSec
	rem := d.nanos % nanosPerSec
	sec, err := addChecked(i.sec, carry)
	if err != nil {
		return Instant{}, err
	}
	n := int64(i.nanos) + rem
	if n < 0 {
		n += nanosPerSec
		if sec, err = addChecked(sec, -1); err != nil {
			return Instant{}, err
		}
	}
	if n >= nanosPerSec {
		n -= nanosPerSec
		if sec, err = addChecked(sec, 1); err != nil {
			return Instant{}, err
		}
	}
	p := i.prec
	if d.prec > p {
		p = d.prec
	}
	return NewInstant(sec, uint32(n), p)
}

func (i Instant) String() string {
	y, mo, d := civilFromDays(divFloor(i.sec, 86400))
	rem := i.sec - divFloor(i.sec, 86400)*86400
	return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%09dZ[%s]", y, mo, d,
		rem/3600, (rem%3600)/60, rem%60, i.nanos, i.prec)
}
