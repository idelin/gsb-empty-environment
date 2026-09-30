package tcpsim

// HeaderSize is the fixed per-packet protocol overhead in bytes.
const HeaderSize = 40

// PacketKind distinguishes data segments from pure ACKs.
type PacketKind int

const (
	DataPacket PacketKind = iota
	AckPacket
)

// Packet is the unit exchanged between endpoints. For data, Seq is the
// first payload byte and Len the payload length. For ACKs, Seq is the
// cumulative acknowledgment number and Len is zero.
type Packet struct {
	Kind PacketKind
	Seq  int
	Len  int
}

// WireSize returns the on-wire size including header overhead.
func (p Packet) WireSize() int { return p.Len + HeaderSize }

// Endpoint is anything that can receive a packet (hosts, senders).
type Endpoint interface {
	Receive(p Packet)
}

// EndpointFunc adapts a function to the Endpoint interface.
type EndpointFunc func(p Packet)

// Receive calls f(p).
func (f EndpointFunc) Receive(p Packet) { f(p) }

// Link is a FIFO store-and-forward link with a fixed serialization rate
// (bytes per tick) and a fixed propagation delay (ticks). Packets are
// queued: a packet starts serialization only after the previous one has
// fully departed.
type Link struct {
	Name  string
	Rate  int // bytes per tick
	Delay int // propagation delay in ticks
	To    Endpoint

	// Drop, if non-nil, decides whether a packet is discarded.
	Drop func(p Packet) bool
	// OnDrop is a logging hook invoked for each dropped packet.
	OnDrop func(p Packet)

	eng       *Engine
	busyUntil int

	Sent    int
	Dropped int
}

// NewLink creates a link delivering to the given endpoint.
func NewLink(eng *Engine, name string, rate, delay int, to Endpoint) *Link {
	if rate <= 0 {
		rate = 1
	}
	return &Link{eng: eng, Name: name, Rate: rate, Delay: delay, To: to}
}

// Send queues a packet on the link. The arrival tick is
// max(now, busyUntil) + serialization + propagation.
func (l *Link) Send(p Packet) {
	start := l.eng.Now()
	if l.busyUntil > start {
		start = l.busyUntil
	}
	ser := (p.WireSize() + l.Rate - 1) / l.Rate
	depart := start + ser
	l.busyUntil = depart

	if l.Drop != nil && l.Drop(p) {
		l.Dropped++
		if l.OnDrop != nil {
			l.OnDrop(p)
		}
		return
	}
	l.Sent++
	delay := depart - l.eng.Now() + l.Delay
	to := l.To
	l.eng.After(delay, func() { to.Receive(p) })
}
