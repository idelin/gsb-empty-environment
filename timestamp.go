package rowconv

import (
	"fmt"
	"time"
)

// ParseTimestamp parses an RFC3339 timestamp with an explicit numeric
// offset (or "Z") and returns UTC microseconds since the Unix epoch.
//
// Input carrying sub-microsecond precision is rejected in Reject mode and
// rounded ties-to-even to the nearest microsecond in TiesToEven mode.
func ParseTimestamp(s string, mode RoundingMode) (int64, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp %q (need RFC3339 with explicit offset): %v", s, err)
	}
	// time.Time.Unix is the floor of seconds and Nanosecond is always
	// non-negative, so this composition is exact even before 1970.
	nano := int64(t.Nanosecond())
	micros := t.Unix()*1000000 + nano/1000
	rem := nano % 1000
	if rem == 0 {
		return micros, nil
	}
	if mode != TiesToEven {
		return 0, fmt.Errorf("timestamp %q has sub-microsecond precision (%d ns remainder)", s, rem)
	}
	// rem is in [1, 999]; rounding up always means micros+1.
	if rem > 500 || (rem == 500 && micros%2 != 0) {
		micros++
	}
	return micros, nil
}
