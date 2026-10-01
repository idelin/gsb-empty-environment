package sim

import "math/big"

// Receiver keeps the set of received byte intervals, advances the cumulative
// ACK over contiguous data, and reports out-of-order bytes as SACK blocks.
type Receiver struct {
	q      *Queue
	toPeer *Link
	log    *Logger

	rcvNxt   *big.Int
	received IntervalSet

	// OnAck is the delivery callback into the peer (set by the caller).
	OnAck func(AckPacket)

	// Stats.
	ReceivedBytes *big.Int // union of newly received bytes (counted once)
	DupBytes      *big.Int // bytes arriving a second time
}

func NewReceiver(q *Queue, link *Link, isn *big.Int, log *Logger) *Receiver {
	return &Receiver{
		q:             q,
		toPeer:        link,
		log:           log,
		rcvNxt:        new(big.Int).Set(isn),
		ReceivedBytes: new(big.Int),
		DupBytes:      new(big.Int),
	}
}

// RcvNxt returns a copy of the cumulative receive pointer.
func (r *Receiver) RcvNxt() *big.Int { return new(big.Int).Set(r.rcvNxt) }

// OnSegment handles an incoming data segment.
func (r *Receiver) OnSegment(seg Segment) {
	lo := new(big.Int).Set(seg.Seq)
	hi := new(big.Int).Add(seg.Seq, seg.Len)
	if hi.Cmp(r.rcvNxt) <= 0 {
		r.DupBytes.Add(r.DupBytes, seg.Len)
		r.log.Log(r.q.Now(), "RX dup [%s,%s)", lo, hi)
		r.sendAck()
		return
	}
	if lo.Cmp(r.rcvNxt) < 0 {
		r.DupBytes.Add(r.DupBytes, new(big.Int).Sub(r.rcvNxt, lo))
		lo.Set(r.rcvNxt)
	}
	r.received.Add(lo, hi)
	r.ReceivedBytes.Add(r.ReceivedBytes, new(big.Int).Sub(hi, lo))
	r.log.Log(r.q.Now(), "RX [%s,%s)", lo, hi)
	for {
		iv, ok := r.received.Find(r.rcvNxt)
		if !ok {
			break
		}
		r.rcvNxt.Set(iv.Hi)
	}
	r.sendAck()
}

// sendAck emits a cumulative ACK plus SACK blocks for everything held above
// the cumulative point.
func (r *Receiver) sendAck() {
	ack := AckPacket{Ack: new(big.Int).Set(r.rcvNxt)}
	for _, iv := range r.received.Intervals() {
		if iv.Hi.Cmp(r.rcvNxt) > 0 {
			ack.SACKs = append(ack.SACKs, NewInterval(iv.Lo, iv.Hi))
		}
	}
	r.log.Log(r.q.Now(), "TX-ACK ack=%s sacks=%v", ack.Ack, ack.SACKs)
	r.toPeer.Transmit(func() {
		if r.OnAck != nil {
			r.OnAck(ack)
		}
	})
}
