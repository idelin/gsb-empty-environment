package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// Default event costs.  A merged page pays the COW cost on its first write;
// a private page pays the cheaper private-write cost; all other accesses use
// the fixed access cost.
const (
	DefaultCOWCost          int64 = 10
	DefaultPrivateWriteCost int64 = 1
	DefaultAccessCost       int64 = 0
)

// CostInterval is an inclusive interval [Min, Max].  It models a caller
// supplied or noisy timing observation.  Min == Max is an exact cost.
type CostInterval struct {
	Min int64
	Max int64
}

// FixedCost returns an exact interval.
func FixedCost(value int64) CostInterval { return CostInterval{Min: value, Max: value} }

// Interval returns an inclusive interval.  Values are swapped if min > max.
func Interval(min, max int64) CostInterval {
	if min > max {
		min, max = max, min
	}
	return CostInterval{Min: min, Max: max}
}

// Valid reports whether the interval is well formed and non-negative.
func (c CostInterval) Valid() bool { return c.Min >= 0 && c.Max >= c.Min }

// Exact reports whether the interval has one value.
func (c CostInterval) Exact() bool { return c.Min == c.Max }

// Overlaps reports whether the two closed intervals share at least one value.
func (c CostInterval) Overlaps(other CostInterval) bool {
	return c.Min <= other.Max && other.Min <= c.Max
}

// Contains reports whether value is in the closed interval.
func (c CostInterval) Contains(value int64) bool { return c.Min <= value && value <= c.Max }

// Add returns the interval sum.
func (c CostInterval) Add(other CostInterval) CostInterval {
	return CostInterval{Min: c.Min + other.Min, Max: c.Max + other.Max}
}

func (c CostInterval) String() string {
	if c.Min == c.Max {
		return strconv.FormatInt(c.Min, 10)
	}
	return fmt.Sprintf("[%d,%d]", c.Min, c.Max)
}

// EventType describes the kind of memory event observed by a caller.
type EventType string

const (
	EventAccess       EventType = "access"
	EventPrivateWrite EventType = "private-write"
	EventCOW          EventType = "cow"
	EventCOWFailure   EventType = "cow-failure"
)

// Observation is one observable memory event.
type Observation struct {
	Event          EventType
	PageID         PageID
	Generation     Generation
	PhysicalPageID PhysicalPageID
	Cost           CostInterval
}

func (o Observation) String() string {
	return fmt.Sprintf("%s page=%d/%d phys=%d cost=%s", o.Event, o.PageID, o.Generation, o.PhysicalPageID, o.Cost)
}

// Trace is the sequence of observations produced by an attacker sequence.
// Victim page creation is deliberately not included: the attacker can only
// observe its own operations.
type Trace []Observation

// Total returns the summed cost interval of the trace.
func (t Trace) Total() CostInterval {
	var total CostInterval
	for _, obs := range t {
		total = total.Add(obs.Cost)
	}
	return total
}

// Costs returns only the observable cost sequence.  Event kinds are internal
// annotations; an attacker with a timer observes costs, not event names.
func (t Trace) Costs() []CostInterval {
	out := make([]CostInterval, len(t))
	for i, obs := range t {
		out[i] = obs.Cost
	}
	return out
}

// CostKey is a deterministic representation of the observable costs.
func (t Trace) CostKey() string {
	var b strings.Builder
	for i, obs := range t {
		if i != 0 {
			b.WriteByte('|')
		}
		b.WriteString(obs.Cost.String())
	}
	return b.String()
}

// TraceRelation describes whether two observed traces can be told apart.
type TraceRelation string

const (
	TracesEqual    TraceRelation = "equal"
	TracesOverlap  TraceRelation = "overlap"
	TracesDistinct TraceRelation = "distinct"
)

// CompareTraces compares traces using only observable costs.
//
//   - TracesEqual means every cost interval is identical.
//   - TracesDistinct means at least one interval pair is disjoint, so a
//     definite leak exists for the sequence.
//   - TracesOverlap means the traces differ only by overlapping intervals.
//     Some noise assignments make them equal, so only a possible leak can be
//     reported.
func CompareTraces(a, b Trace) TraceRelation {
	if len(a) != len(b) {
		return TracesDistinct
	}
	overlap := false
	for i := range a {
		switch {
		case a[i].Cost == b[i].Cost:
			continue
		case !a[i].Cost.Overlaps(b[i].Cost):
			return TracesDistinct
		default:
			overlap = true
		}
	}
	if overlap {
		return TracesOverlap
	}
	return TracesEqual
}

// CostModel supplies a closed cost interval for each event kind.  The zero
// value is normalized to DefaultCostModel.  Alias fields (for example
// WritePrivate and PrivateWrite) are accepted so callers can use the
// terminology that fits their harness.
type CostModel struct {
	COW          CostInterval
	COWWrite     CostInterval
	WriteCOW     CostInterval
	PrivateWrite CostInterval
	WritePrivate CostInterval
	Access       CostInterval
	Read         CostInterval
	Prime        CostInterval
	Other        CostInterval
	COWFailure   CostInterval
	COWFail      CostInterval
}

// DefaultCostModel returns exact costs 10, 1 and 0.
func DefaultCostModel() CostModel {
	return CostModel{
		COW:          FixedCost(DefaultCOWCost),
		PrivateWrite: FixedCost(DefaultPrivateWriteCost),
		Access:       FixedCost(DefaultAccessCost),
		COWFailure:   FixedCost(DefaultCOWCost),
	}
}

// NewCostModel returns a model with the three primary costs.
func NewCostModel(cow, privateWrite, access CostInterval) CostModel {
	return CostModel{COW: cow, PrivateWrite: privateWrite, Access: access, COWFailure: cow}
}

func firstNonZero(intervals ...CostInterval) CostInterval {
	for _, interval := range intervals {
		if interval != (CostInterval{}) {
			return interval
		}
	}
	return CostInterval{}
}

// Normalize fills unset fields with defaults and reconciles alias fields.
func (m CostModel) Normalize() CostModel {
	d := DefaultCostModel()

	if m.COW == (CostInterval{}) {
		m.COW = firstNonZero(m.COWWrite, m.WriteCOW, d.COW)
	}
	if m.PrivateWrite == (CostInterval{}) {
		m.PrivateWrite = firstNonZero(m.WritePrivate, d.PrivateWrite)
	}
	if m.Access == (CostInterval{}) {
		m.Access = firstNonZero(m.Other, m.Read, m.Prime, d.Access)
	}
	// Access is legitimately fixed at zero.  If no access cost was supplied,
	// firstNonZero returns the zero interval, which is already the default.
	if m.COWFailure == (CostInterval{}) {
		m.COWFailure = firstNonZero(m.COWFail, m.COW, d.COWFailure)
	}
	return m
}

// For returns the cost interval for an event type.
func (m CostModel) For(event EventType) CostInterval {
	m = m.Normalize()
	switch event {
	case EventCOW:
		return m.COW
	case EventPrivateWrite:
		return m.PrivateWrite
	case EventCOWFailure:
		return m.COWFailure
	default:
		return m.Access
	}
}

// WriteResult is returned by page writes and metadata updates.
type WriteResult struct {
	COW            bool
	COWFailed      bool
	Cost           CostInterval
	Observation    Observation
	PhysicalPageID PhysicalPageID
	Generation     Generation
	BytesWritten   int
}

// ReadResult is returned by page reads.
type ReadResult struct {
	Content     []byte
	Cost        CostInterval
	Observation Observation
}
