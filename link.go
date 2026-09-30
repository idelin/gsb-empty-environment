package streamsim

// Link is a store-and-forward link with a FIFO queue. Each packet costs
// Interval ticks of serialization plus Delay ticks of propagation. When
// MaxQueue > 0, packets arriving at a full queue are dropped.
type Link struct {
	sim      *Simulator
	Delay    uint64
	Interval uint64
	MaxQueue int
	Deliver  func(Packet)

	Dropped int
	queue   []Packet
	busy    bool
}

func NewLink(sim *Simulator, delay, interval uint64, deliver func(Packet)) *Link {
	return &Link{sim: sim, Delay: delay, Interval: interval, Deliver: deliver}
}

// Send enqueues a packet onto the link.
func (l *Link) Send(p Packet) {
	if l.MaxQueue > 0 && len(l.queue) >= l.MaxQueue {
		l.Dropped++
		return
	}
	l.queue = append(l.queue, p)
	if !l.busy {
		l.busy = true
		l.sim.After(l.Interval, l.dequeue)
	}
}

func (l *Link) dequeue() {
	p := l.queue[0]
	l.queue = l.queue[1:]
	l.sim.After(l.Delay, func() { l.Deliver(p) })
	if len(l.queue) > 0 {
		l.sim.After(l.Interval, l.dequeue)
	} else {
		l.busy = false
	}
}
