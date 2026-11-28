package wireguard

import (
	"encoding/binary"
	"fmt"
	"time"
)

// TimestampSize is the size of a WireGuard TAI64N timestamp.
const TimestampSize = 12

const tai64Base = uint64(1)<<62 + 10

// Timestamp is a 12-byte big-endian TAI64N value.
type Timestamp [TimestampSize]byte

// TAI64N is an alias for Timestamp.
type TAI64N = Timestamp

// TimestampFromTime converts a wall-clock time to TAI64N.
func TimestampFromTime(t time.Time) Timestamp {
	var ts Timestamp
	binary.BigEndian.PutUint64(ts[0:8], tai64Base+uint64(t.Unix()))
	binary.BigEndian.PutUint32(ts[8:12], uint32(t.Nanosecond()))
	return ts
}

// TimestampFromBytes decodes a 12-byte TAI64N value.
func TimestampFromBytes(b []byte) (Timestamp, error) {
	var ts Timestamp
	if len(b) != TimestampSize {
		return ts, fmt.Errorf("wireguard: invalid TAI64N length %d", len(b))
	}
	copy(ts[:], b)
	return ts, nil
}

// Compare returns -1, 0, or 1 depending on whether ts is before, equal to,
// or after other.
func (ts Timestamp) Compare(other Timestamp) int {
	for i := 0; i < TimestampSize; i++ {
		if ts[i] < other[i] {
			return -1
		}
		if ts[i] > other[i] {
			return 1
		}
	}
	return 0
}

// After reports whether ts is strictly later than other.
func (ts Timestamp) After(other Timestamp) bool {
	return ts.Compare(other) > 0
}

// Bytes returns the wire representation of the timestamp.
func (ts Timestamp) Bytes() []byte {
	out := make([]byte, TimestampSize)
	copy(out, ts[:])
	return out
}

func (ts Timestamp) String() string {
	sec := binary.BigEndian.Uint64(ts[0:8])
	nsec := binary.BigEndian.Uint32(ts[8:12])
	return fmt.Sprintf("tai64n(%d:%09d)", sec-tai64Base, nsec)
}

func (ts Timestamp) next() Timestamp {
	nsec := binary.BigEndian.Uint32(ts[8:12])
	if nsec < 999999999 {
		binary.BigEndian.PutUint32(ts[8:12], nsec+1)
		return ts
	}
	sec := binary.BigEndian.Uint64(ts[0:8])
	binary.BigEndian.PutUint64(ts[0:8], sec+1)
	binary.BigEndian.PutUint32(ts[8:12], 0)
	return ts
}

// timestampGenerator guarantees strictly increasing TAI64N values even when
// the wall clock stalls or moves backwards.
type timestampGenerator struct {
	last Timestamp
	have bool
}

func (g *timestampGenerator) Next(now time.Time) Timestamp {
	ts := TimestampFromTime(now)
	if g.have && ts.Compare(g.last) <= 0 {
		ts = g.last.next()
	}
	g.last = ts
	g.have = true
	return ts
}

// timestampChecker implements the per-peer strict monotonic timestamp check.
type timestampChecker struct {
	last Timestamp
	have bool
}

// Accept returns true only for a timestamp strictly greater than every
// previously accepted timestamp. Accepted values update the high-water mark.
func (c *timestampChecker) Accept(ts Timestamp) bool {
	if c.have && ts.Compare(c.last) <= 0 {
		return false
	}
	c.last = ts
	c.have = true
	return true
}
