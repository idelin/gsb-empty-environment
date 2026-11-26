// Package trijoin maintains a three-way interval join over changelog streams.
//
// The join key is shared by all three streams. A row from A joins a row from B
// when t_B is in [t_A-d1, t_A+d2], and that B row joins a C row when t_C is in
// [t_B-d3, t_B+d4]. Inputs carry signed weights: +1 inserts a row and -1
// retracts it. A negative weight may arrive before its matching insertion.
package trijoin

import (
	"fmt"
	"math"
)

// Stream identifies one of the three input streams.
type Stream uint8

const (
	StreamA Stream = iota
	StreamB
	StreamC
)

// Short aliases for the three streams.
const (
	A = StreamA
	B = StreamB
	C = StreamC
)

func (s Stream) String() string {
	switch s {
	case StreamA:
		return "A"
	case StreamB:
		return "B"
	case StreamC:
		return "C"
	default:
		return fmt.Sprintf("Stream(%d)", uint8(s))
	}
}

func (s Stream) valid() bool {
	return s <= StreamC
}

// Config contains the four interval-join offsets.
//
// All offsets must be non-negative. They are measured in the same units as
// Record.Time.
type Config struct {
	D1 int64
	D2 int64
	D3 int64
	D4 int64
}

// Record is one signed changelog row.
//
// (Stream, Key, RowID) is the logical row identity. A retraction should use the
// same identity, time, and payload as the insertion it retracts.
type Record struct {
	Stream  Stream
	Key     string
	Time    int64
	RowID   int64
	Payload any
	Weight  int64
}

// EventKind distinguishes changelog rows from watermark advances.
type EventKind uint8

const (
	EventRecord EventKind = iota
	EventWatermark
)

// Event is an input to Join.ApplyBatch.
//
// Record is used when Kind is EventRecord. Watermark is used when Kind is
// EventWatermark.
type Event struct {
	Kind      EventKind
	Record    Record
	Watermark int64
}

// RecordEvent constructs a changelog input event.
func RecordEvent(r Record) Event {
	return Event{Kind: EventRecord, Record: r}
}

// WatermarkEvent constructs a watermark input event.
func WatermarkEvent(w int64) Event {
	return Event{Kind: EventWatermark, Watermark: w}
}

// Component identifies one input row participating in an output triple.
type Component struct {
	Stream  Stream
	RowID   int64
	Time    int64
	Payload any
}

// OutputID is the composite row identity of an A x B x C output triple.
// The join Key is carried separately by Delta.Key.
type OutputID struct {
	ARow int64
	BRow int64
	CRow int64
}

func (id OutputID) String() string {
	return fmt.Sprintf("%d/%d/%d", id.ARow, id.BRow, id.CRow)
}

// Delta is a signed change to one output triple.
//
// A positive Weight inserts that many copies of the triple; a negative Weight
// retracts them. Deltas in one ApplyBatch call are already compressed: a
// triple appears at most once, with a non-zero net weight.
type Delta struct {
	Key    string
	ID     OutputID
	A      Component
	B      Component
	C      Component
	Weight int64
}

// Rows returns the three input row numbers in A, B, C order.
func (d Delta) Rows() [3]int64 {
	return [3]int64{d.ID.ARow, d.ID.BRow, d.ID.CRow}
}

// Stats is a consistent snapshot of the operator's counters.
type Stats struct {
	HasWatermark bool
	Watermark    int64

	// SafeLowerBound is the retained boundary for A, B, and C. It is valid only
	// when HasWatermark is true.
	SafeLowerBound [3]int64

	// ActiveRows counts rows whose net input weight is positive.
	ActiveRows [3]int
	// TrackedRows counts retained state rows, including retraction tombstones.
	TrackedRows [3]int
	// CleanedRows is the cumulative number of input rows removed by watermarks.
	CleanedRows int64
	// LateDrops is the cumulative number of changelog events dropped because
	// their time was below that stream's retained boundary.
	LateDrops int64

	// OutputTriples counts materialized output triples with positive weight.
	OutputTriples int64
	// OutputWeight is the sum of materialized output weights.
	OutputWeight int64
}

// safeAdd and safeSub keep interval endpoint calculations from wrapping at the
// ends of the int64 domain.
func safeAdd(a, b int64) int64 {
	if b > 0 && a > math.MaxInt64-b {
		return math.MaxInt64
	}
	if b < 0 && a < math.MinInt64-b {
		return math.MinInt64
	}
	return a + b
}

func safeSub(a, b int64) int64 {
	if b == math.MinInt64 {
		if a >= 0 {
			return math.MaxInt64
		}
		return a - b
	}
	return safeAdd(a, -b)
}
