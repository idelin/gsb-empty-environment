package sim

// Link is a simulated one-way link inside the discrete-event model: packets
// are delivered after a fixed integer delay. If DropEvery > 0, every
// DropEvery-th transmitted packet is deterministically lost, which exercises
// SACK-based gap retransmission.
type Link struct {
	q         *Queue
	Delay     int64
	DropEvery int
	Sent      int
	Dropped   int
}

func NewLink(q *Queue, delay int64, dropEvery int) *Link {
	return &Link{q: q, Delay: delay, DropEvery: dropEvery}
}

// Transmit hands a packet to the link. deliver is invoked after Delay ticks
// unless the packet is dropped. Returns false when dropped.
func (l *Link) Transmit(deliver func()) bool {
	l.Sent++
	if l.DropEvery > 0 && l.Sent%l.DropEvery == 0 {
		l.Dropped++
		return false
	}
	l.q.Schedule(l.Delay, deliver)
	return true
}
