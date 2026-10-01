package chronos

import "fmt"

// Precision is the resolution at which a value is recorded. It is part of
// the metadata of every value and of its serialized form.
type Precision uint8

const (
	PrecSecond Precision = iota + 1
	PrecMillisecond
	PrecMicrosecond
	PrecNanosecond
)

func (p Precision) Valid() bool { return p >= PrecSecond && p <= PrecNanosecond }

// NanosPerUnit returns how many nanoseconds one unit of p spans.
func (p Precision) NanosPerUnit() int64 {
	switch p {
	case PrecSecond:
		return 1_000_000_000
	case PrecMillisecond:
		return 1_000_000
	case PrecMicrosecond:
		return 1_000
	case PrecNanosecond:
		return 1
	}
	return 0
}

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
	return fmt.Sprintf("Precision(%d)", uint8(p))
}

// parsePrecision rejects illegal precision labels before any object is built.
func parsePrecision(tag uint8) (Precision, error) {
	p := Precision(tag)
	if !p.Valid() {
		return 0, fmt.Errorf("chronos: illegal precision label %d", tag)
	}
	return p, nil
}
