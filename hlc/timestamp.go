package hlc

import (
	"encoding/binary"
	"fmt"
)

// WireSize is the fixed on-wire size of an encoded Timestamp:
// 8 bytes physical milliseconds, 2 bytes logical counter, 8 bytes node ID.
const WireSize = 8 + 2 + 8

// MaxLogical is the largest value the 16-bit logical counter can take.
const MaxLogical = uint16(1<<16 - 1)

// Timestamp is a hybrid logical clock timestamp.
//
// Physical is a signed 64-bit Unix time in milliseconds (it is signed so
// that epochs before 1970 are representable). Logical is a 16-bit counter
// that disambiguates events sharing the same Physical value. NodeID is the
// identity of the node that minted the timestamp and acts as the final
// tie-breaker, making every Timestamp totally ordered.
type Timestamp struct {
	Physical int64
	Logical  uint16
	NodeID   uint64
}

// Compare returns -1, 0 or 1 as t is strictly before, equal to or after o.
// Ordering is lexicographic over (Physical, Logical, NodeID).
func (t Timestamp) Compare(o Timestamp) int {
	switch {
	case t.Physical < o.Physical:
		return -1
	case t.Physical > o.Physical:
		return 1
	}
	switch {
	case t.Logical < o.Logical:
		return -1
	case t.Logical > o.Logical:
		return 1
	}
	switch {
	case t.NodeID < o.NodeID:
		return -1
	case t.NodeID > o.NodeID:
		return 1
	default:
		return 0
	}
}

// Before reports whether t is strictly before o.
func (t Timestamp) Before(o Timestamp) bool { return t.Compare(o) < 0 }

// After reports whether t is strictly after o.
func (t Timestamp) After(o Timestamp) bool { return t.Compare(o) > 0 }

// IsZero reports whether t is the zero value.
func (t Timestamp) IsZero() bool { return t == Timestamp{} }

// MarshalBinary encodes the timestamp as a fixed-size 18-byte big-endian
// blob: signed physical milliseconds, uint16 logical counter, uint64 node ID.
func (t Timestamp) MarshalBinary() ([]byte, error) {
	buf := make([]byte, WireSize)
	binary.BigEndian.PutUint64(buf[0:8], uint64(t.Physical))
	binary.BigEndian.PutUint16(buf[8:10], t.Logical)
	binary.BigEndian.PutUint64(buf[10:18], t.NodeID)
	return buf, nil
}

// UnmarshalBinary decodes a blob produced by MarshalBinary.
func (t *Timestamp) UnmarshalBinary(data []byte) error {
	if len(data) != WireSize {
		return fmt.Errorf("hlc: encoded timestamp has %d bytes, want %d", len(data), WireSize)
	}
	t.Physical = int64(binary.BigEndian.Uint64(data[0:8]))
	t.Logical = binary.BigEndian.Uint16(data[8:10])
	t.NodeID = binary.BigEndian.Uint64(data[10:18])
	return nil
}

// String renders the timestamp as (physical=<ms>, logical=<n>, node=<id>).
func (t Timestamp) String() string {
	return fmt.Sprintf("(physical=%d, logical=%d, node=%d)", t.Physical, t.Logical, t.NodeID)
}
