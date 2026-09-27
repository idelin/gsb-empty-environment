package dte

import "strconv"

// Mode selects whether non-conforming traffic is dropped or delayed.
type Mode int

const (
	Policing Mode = iota
	Shaping
)

func (m Mode) String() string {
	switch m {
	case Policing:
		return "policing"
	case Shaping:
		return "shaping"
	default:
		return "unknown"
	}
}

// Rate is an exact token rate: Num tokens per Den ticks.
type Rate struct {
	Num int64
	Den int64
}

func (r Rate) String() string {
	return strconv.FormatInt(r.Num, 10) + "/" + strconv.FormatInt(r.Den, 10)
}

// EventKind enumerates all externally scheduled discrete events.
type EventKind int

const (
	Arrive EventKind = iota
	Cancel
	SetRateEvent
	Pause
	Resume
)

func (k EventKind) String() string {
	switch k {
	case Arrive:
		return "arrive"
	case Cancel:
		return "cancel"
	case SetRateEvent:
		return "set-rate"
	case Pause:
		return "pause"
	case Resume:
		return "resume"
	default:
		return "unknown"
	}
}

// Event is one input command at an integer tick.
type Event struct {
	Tick   int64
	Kind   EventKind
	Flow   string
	Bucket string
	ID     int
	Bytes  int64
	Rate   Rate
}

// Arrival schedules a packet arrival.
func Arrival(tick int64, flow string, id int, bytes int64) Event {
	return Event{Tick: tick, Kind: Arrive, Flow: flow, ID: id, Bytes: bytes}
}

// Cancellation cancels a packet still waiting in a shaping queue.
func Cancellation(tick int64, flow string, id int) Event {
	return Event{Tick: tick, Kind: Cancel, Flow: flow, ID: id}
}

// RateChange atomically changes a bucket weight/token rate at tick.
func RateChange(tick int64, bucket string, rate Rate) Event {
	return Event{Tick: tick, Kind: SetRateEvent, Bucket: bucket, Rate: rate}
}

// PauseBucket stops replenishment at tick after the interval ending there is settled.
func PauseBucket(tick int64, bucket string) Event {
	return Event{Tick: tick, Kind: Pause, Bucket: bucket}
}

// ResumeBucket resumes replenishment at tick without refilling the paused gap.
func ResumeBucket(tick int64, bucket string) Event {
	return Event{Tick: tick, Kind: Resume, Bucket: bucket}
}
