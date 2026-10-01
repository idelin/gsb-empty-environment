package sim

// PacketKind distinguishes data packets from ACK packets.
type PacketKind int

const (
	DataPacket PacketKind = iota
	AckPacket
)

// Packet is the unit exchanged between endpoints. For DataPacket the byte
// range [Start, End) is carried in Payload. For AckPacket, Ack is the
// cumulative acknowledgement and Sack lists selectively acknowledged ranges.
type Packet struct {
	Kind       PacketKind
	Start, End Seq // data range [Start, End)
	Ack        Seq // cumulative ack (AckPacket)
	Sack       []Range
	Rwnd       uint64 // advertised receive window (AckPacket)
	Retransmit bool
	Payload    []byte
}

// Endpoint receives packets. Sender and Receiver both implement it.
type Endpoint interface {
	Receive(p Packet)
}

// Pipe is a unidirectional link inside the discrete-event model: packets are
// delivered to the destination after Delay ticks. No real network traffic is
// generated; everything is an event on the Scheduler. An optional Drop
// policy (keyed by the 1-based ordinal of the packet on this pipe) models
// deterministic loss.
type Pipe struct {
	sched   *Scheduler
	Delay   uint64
	Drop    func(n uint64) bool
	dst     Endpoint
	sent    uint64
	dropped uint64
}

// NewPipe creates a pipe delivering to dst after delay ticks.
func NewPipe(sched *Scheduler, delay uint64, dst Endpoint) *Pipe {
	return &Pipe{sched: sched, Delay: delay, dst: dst}
}

// Send enqueues a packet for delivery. Dropped packets vanish silently,
// like on a real lossy link.
func (p *Pipe) Send(pkt Packet) {
	p.sent++
	if p.Drop != nil && p.Drop(p.sent) {
		p.dropped++
		return
	}
	p.sched.After(p.Delay, func() { p.dst.Receive(pkt) })
}

// Sent returns the number of packets offered to the pipe.
func (p *Pipe) Sent() uint64 { return p.sent }

// Dropped returns the number of packets dropped by the loss policy.
func (p *Pipe) Dropped() uint64 { return p.dropped }
