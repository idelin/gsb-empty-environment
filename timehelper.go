package temporal

import "time"

// NewTimeRangeOf is NewTimeRange accepting time.Time endpoints, converted to
// Unix microseconds in UTC.
func NewTimeRangeOf(lower, upper time.Time, lowerInclusive, upperInclusive bool) Range {
	return NewTimeRange(lower.UTC().UnixMicro(), upper.UTC().UnixMicro(), lowerInclusive, upperInclusive)
}

// PointOf returns the continuous single-point interval [t,t] from a time.Time.
func PointOf(t time.Time) Range { return Point(t.UTC().UnixMicro()) }

// AsTime converts a microsecond timestamp endpoint back to time.Time (UTC).
func AsTime(microseconds int64) time.Time {
	return time.UnixMicro(microseconds).UTC()
}

// Micro is a tiny constructor from seconds/nanoseconds-ish inputs used by
// callers that already hold a time.Time; it returns Unix microseconds.
func Micro(t time.Time) int64 { return t.UTC().UnixMicro() }
