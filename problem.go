// Package gatetable compiles periodic multicast gate tables for a switch
// fabric with one shared multicast buffer slot per frame instance.
//
// A problem describes one hyperperiod of T ticks (T <= 96), at most four
// egress ports and at most ten frame types.  Frame type f is released at
// Release[f] and has one instance in every period.  Every egress copy of f
// starts at a fixed relative offset from its release; the offset may differ
// between ports, but is the same for every period instance of f.
//
// For each frame instance the chip holds exactly one shared buffer slot from
// the release tick through the completion of the last egress copy.  Slots are
// counted per instance, even when consecutive instances of the same frame
// overlap.  Port copies are non-preemptive, cannot overlap other copies on
// the same port, and cannot use a port's forbidden ticks.
//
// The compiled hardware table is a circular sequence of T port masks.  A
// physical entry represents a maximal circular run of equal masks.  A
// constant sequence therefore occupies exactly one entry.
package gatetable

import (
	"errors"
	"fmt"
)

const (
	// MaxPorts is the largest supported number of egress ports.
	MaxPorts = 4
	// MaxFrameTypes is the largest supported number of multicast frame types.
	MaxFrameTypes = 10
	// MaxPeriod is the largest supported hyperperiod, in ticks.
	MaxPeriod = 96
	// MaxDeadlinePeriods is the largest permitted relative deadline, in periods.
	MaxDeadlinePeriods = 2
)

// Unlimited may be used for either budget when no finite limit is desired.
const Unlimited = -1

// ErrUnschedulable is returned when no schedule satisfies the deadlines,
// port constraints, slot limit and table-entry limit.
var ErrUnschedulable = errors.New("gatetable: no feasible gate table")

// PortSpec describes one egress port.
type PortSpec struct {
	// Forbidden is indexed by tick in [0, Period).  A true entry means that
	// no copy may occupy that tick on this port.
	Forbidden []bool
}

// FrameSpec describes one multicast frame type.  There is exactly one
// instance of the frame in each hyperperiod.
type FrameSpec struct {
	// Name is optional and is copied to results for diagnostics.
	Name string

	// Release is the phase, in ticks, of the instance in a hyperperiod.
	Release int

	// Ports lists the egress ports of the frame, without duplicates.
	// Durations is parallel to Ports.
	Ports     []int
	Durations []int

	// Deadline is the maximum permitted completion latency relative to the
	// release tick.  It must be positive and at most 2*Period.
	Deadline int
}

// Problem is a complete gate-table compilation request.
type Problem struct {
	// Period is the hyperperiod in ticks, 1 <= Period <= MaxPeriod.
	Period int

	// Ports has one entry per physical egress port.  Its length is the
	// number of ports, at most MaxPorts.
	Ports []PortSpec

	// Frames has at most MaxFrameTypes entries, in input order.  That order
	// defines the final lexicographic offset tie-break.
	Frames []FrameSpec

	// MaxSlots is the hard limit on simultaneously held shared slots.
	// Unlimited disables the limit.
	MaxSlots int

	// MaxTableEntries is the hard limit on physical mask-table entries.
	// Unlimited disables the limit.
	MaxTableEntries int
}

// Validate checks the problem shape and all obvious unary bounds.  It does not
// attempt to find a schedule.
func (p Problem) Validate() error {
	if p.Period < 1 || p.Period > MaxPeriod {
		return fmt.Errorf("period %d outside [1,%d]", p.Period, MaxPeriod)
	}
	if len(p.Ports) < 1 || len(p.Ports) > MaxPorts {
		return fmt.Errorf("port count %d outside [1,%d]", len(p.Ports), MaxPorts)
	}
	if len(p.Frames) < 1 || len(p.Frames) > MaxFrameTypes {
		return fmt.Errorf("frame count %d outside [1,%d]", len(p.Frames), MaxFrameTypes)
	}
	if p.MaxSlots == 0 || p.MaxSlots < Unlimited {
		return fmt.Errorf("invalid slot limit %d", p.MaxSlots)
	}
	if p.MaxTableEntries == 0 || p.MaxTableEntries < Unlimited {
		return fmt.Errorf("invalid table-entry limit %d", p.MaxTableEntries)
	}
	for pi, port := range p.Ports {
		if len(port.Forbidden) != p.Period {
			return fmt.Errorf("port %d: forbidden bitmap length %d, want %d",
				pi, len(port.Forbidden), p.Period)
		}
	}
	for fi, frame := range p.Frames {
		if frame.Release < 0 || frame.Release >= p.Period {
			return fmt.Errorf("frame %d: release %d outside [0,%d)",
				fi, frame.Release, p.Period)
		}
		if len(frame.Ports) == 0 {
			return fmt.Errorf("frame %d: empty egress set", fi)
		}
		if len(frame.Ports) != len(frame.Durations) {
			return fmt.Errorf("frame %d: %d ports but %d durations",
				fi, len(frame.Ports), len(frame.Durations))
		}
		if frame.Deadline < 1 || frame.Deadline > MaxDeadlinePeriods*p.Period {
			return fmt.Errorf("frame %d: deadline %d outside [1,%d]",
				fi, frame.Deadline, MaxDeadlinePeriods*p.Period)
		}
		seen := make([]bool, len(p.Ports))
		for i, port := range frame.Ports {
			if port < 0 || port >= len(p.Ports) {
				return fmt.Errorf("frame %d: port %d outside [0,%d)",
					fi, port, len(p.Ports))
			}
			if seen[port] {
				return fmt.Errorf("frame %d: duplicate egress port %d", fi, port)
			}
			seen[port] = true
			d := frame.Durations[i]
			if d < 1 || d > p.Period {
				return fmt.Errorf("frame %d port %d: duration %d outside [1,%d]",
					fi, port, d, p.Period)
			}
		}
	}
	return nil
}

func (p Problem) clone() Problem {
	out := p
	out.Ports = make([]PortSpec, len(p.Ports))
	for i, port := range p.Ports {
		out.Ports[i].Forbidden = append([]bool(nil), port.Forbidden...)
	}
	out.Frames = make([]FrameSpec, len(p.Frames))
	for i, frame := range p.Frames {
		out.Frames[i] = frame
		out.Frames[i].Ports = append([]int(nil), frame.Ports...)
		out.Frames[i].Durations = append([]int(nil), frame.Durations...)
	}
	return out
}
