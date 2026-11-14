package netsec

import "time"

// timeNow is a small indirection so tests can reason about audit timestamps
// while production code uses the wall clock.
var timeNow = func() time.Time {
	return time.Now().UTC()
}
