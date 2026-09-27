// Package reordersim implements a discrete-event simulation of a reorder
// buffer that releases out-of-order packets by (EventTime, Seq), with an
// explicit wait limit, an explicit cache limit, a separate late channel,
// and epoch resets that never leak old packets into the new stream.
//
// All time is expressed in integer ticks. No real network traffic is used.
package reordersim

// Packet is the unit of traffic in the simulation.
type Packet struct {
	Epoch     uint64 // stream epoch; only the current epoch is releasable
	Seq       uint64 // sequence number inside the epoch
	EventTime int64  // logical event time in ticks
	Payload   string
}

// key is the release ordering key: event time first, sequence number as
// tie-breaker for packets that share the same event time.
type key struct {
	eventTime int64
	seq       uint64
}

func keyOf(p Packet) key { return key{eventTime: p.EventTime, seq: p.Seq} }

// less reports whether a sorts strictly before b.
func (a key) less(b key) bool {
	if a.eventTime != b.eventTime {
		return a.eventTime < b.eventTime
	}
	return a.seq < b.seq
}

// beforeOrEqual reports whether a sorts before or equal to b. A packet whose
// key is beforeOrEqual the last released key can never be released again
// without modifying the already released sequence, so it is late.
func (a key) beforeOrEqual(b key) bool { return !b.less(a) }
