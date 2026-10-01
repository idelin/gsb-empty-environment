package sim

import "fmt"

// Receiver is the receiving endpoint. It buffers out-of-order data,
// delivers contiguous bytes in order, and answers every data packet with a
// cumulative ACK plus SACK blocks for buffered ranges above rcvNxt.
type Receiver struct {
	Out  *Pipe
	Rwnd uint64 // advertised receive window (may shrink at runtime)

	rcvNxt   Seq
	pending  []Packet // buffered out-of-order segments
	received SackSet  // union of all byte ranges ever received

	Delivered      []byte
	DuplicateBytes uint64

	Log func(msg string)
}

// NewReceiver creates a receiver expecting data from sequence number base.
func NewReceiver(base Seq, rwnd uint64) *Receiver {
	return &Receiver{rcvNxt: base, Rwnd: rwnd}
}

func (r *Receiver) logf(format string, args ...interface{}) {
	if r.Log != nil {
		r.Log(fmt.Sprintf(format, args...))
	}
}

// RcvNxt returns the next in-order sequence number expected.
func (r *Receiver) RcvNxt() Seq { return r.rcvNxt }

// SetRwnd changes the advertised receive window (takes effect with the
// next ACK sent).
func (r *Receiver) SetRwnd(w uint64) {
	r.Rwnd = w
	r.logf("RWND -> %d", w)
}

// Receive processes an incoming data packet.
func (r *Receiver) Receive(p Packet) {
	if p.Kind != DataPacket {
		return
	}
	if p.End.Cmp(p.Start) <= 0 || p.End.Cmp(r.rcvNxt) <= 0 {
		// Pure duplicate: fully below rcvNxt.
		if p.End.Cmp(p.Start) > 0 {
			r.DuplicateBytes += p.End.Diff(p.Start)
		}
		r.logf("DUP  [%s,%s)", p.Start, p.End)
	} else {
		r.received.Add(Range{Start: p.Start, End: p.End})
		r.pending = append(r.pending, p)
		r.logf("RECV [%s,%s)", p.Start, p.End)
		r.advance()
	}
	r.sendAck()
}

// advance delivers every buffered byte that has become contiguous,
// tolerating partially overlapping retransmissions.
func (r *Receiver) advance() {
	for {
		progressed := false
		for i, seg := range r.pending {
			if seg.Start.Cmp(r.rcvNxt) <= 0 && seg.End.Cmp(r.rcvNxt) > 0 {
				off := r.rcvNxt.Diff(seg.Start)
				r.Delivered = append(r.Delivered, seg.Payload[int(off):]...)
				r.rcvNxt = seg.End
				r.pending = append(r.pending[:i], r.pending[i+1:]...)
				progressed = true
				break
			}
		}
		if !progressed {
			return
		}
	}
}

func (r *Receiver) sendAck() {
	var blocks []Range
	for _, rg := range r.received.Ranges() {
		if rg.End.Cmp(r.rcvNxt) <= 0 {
			continue
		}
		start := rg.Start
		if start.Cmp(r.rcvNxt) < 0 {
			start = r.rcvNxt
		}
		blocks = append(blocks, Range{Start: start, End: rg.End})
	}
	r.Out.Send(Packet{Kind: AckPacket, Ack: r.rcvNxt, Sack: blocks, Rwnd: r.Rwnd})
}
