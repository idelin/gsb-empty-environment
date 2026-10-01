package sim

import (
	"math/big"
	"testing"
)

func bi(v int64) *big.Int { return big.NewInt(v) }

func newTestSender(stream, mss, window int64) (*Queue, *Sender) {
	q := &Queue{}
	link := NewLink(q, 1, 0)
	s := NewSender(q, link, bi(0), bi(stream), bi(mss), bi(window), 0, nil)
	return q, s
}

// Overlapping SACK blocks merge into a union counted exactly once.
func TestIntervalUnionCountedOnce(t *testing.T) {
	var s IntervalSet
	s.Add(bi(0), bi(10))
	s.Add(bi(5), bi(15))
	s.Add(bi(8), bi(12))
	s.Add(bi(20), bi(25))
	if got := s.Measure(); got.Cmp(bi(20)) != 0 {
		t.Fatalf("union measure = %s, want 20", got)
	}
	ivs := s.Intervals()
	if len(ivs) != 2 || ivs[0].Lo.Cmp(bi(0)) != 0 || ivs[0].Hi.Cmp(bi(15)) != 0 {
		t.Fatalf("intervals = %v, want [[0,15) [20,25)]", ivs)
	}
}

func TestGaps(t *testing.T) {
	var s IntervalSet
	s.Add(bi(0), bi(10))
	s.Add(bi(20), bi(30))
	gaps := s.Gaps(bi(0), bi(40))
	if len(gaps) != 2 || gaps[0].String() != "[10,20)" || gaps[1].String() != "[30,40)" {
		t.Fatalf("gaps = %v, want [[10,20) [30,40)]", gaps)
	}
}

// An ACK for bytes that were never sent must be rejected.
func TestRejectAckOfUnsentBytes(t *testing.T) {
	q, s := newTestSender(10000, 1000, 4000)
	s.Start()
	q.Run(100)
	if s.SndNxt().Cmp(bi(4000)) != 0 {
		t.Fatalf("sndNxt = %s, want 4000", s.SndNxt())
	}
	s.OnAck(AckPacket{Ack: bi(4001)})
	if s.RejectedAcks != 1 {
		t.Fatalf("RejectedAcks = %d, want 1", s.RejectedAcks)
	}
	if s.SndUna().Cmp(bi(0)) != 0 {
		t.Fatalf("sndUna moved to %s after rejected ACK", s.SndUna())
	}
	if s.Retransmitted.Sign() != 0 {
		t.Fatalf("rejected ACK triggered retransmission of %s bytes", s.Retransmitted)
	}
}

// A stale cumulative ACK must not cause already-acked data to be resent.
func TestOldAckDoesNotRetransmit(t *testing.T) {
	q, s := newTestSender(5000, 1000, 5000)
	s.Start()
	q.Run(10)
	s.OnAck(AckPacket{Ack: bi(3000)})
	if s.Retransmitted.Sign() != 0 {
		t.Fatalf("plain cumulative ACK retransmitted %s bytes", s.Retransmitted)
	}
	sent := len(s.SentSegs)
	s.OnAck(AckPacket{Ack: bi(1000)}) // stale
	s.OnAck(AckPacket{Ack: bi(3000)}) // duplicate
	if s.SndUna().Cmp(bi(3000)) != 0 {
		t.Fatalf("sndUna = %s, want 3000", s.SndUna())
	}
	if len(s.SentSegs) != sent {
		t.Fatalf("stale/dup ACKs emitted %d new segments", len(s.SentSegs)-sent)
	}
}

// With SACK blocks present, only the gap between them is retransmitted.
func TestGapOnlyRetransmit(t *testing.T) {
	q, s := newTestSender(5000, 1000, 5000)
	s.Start()
	q.Run(10)
	s.OnAck(AckPacket{Ack: bi(1000), SACKs: []Interval{
		NewInterval(bi(3000), bi(4000)),
		NewInterval(bi(4000), bi(5000)),
	}})
	if s.Retransmitted.Cmp(bi(2000)) != 0 {
		t.Fatalf("retransmitted = %s, want 2000 (only the gap)", s.Retransmitted)
	}
	retx := s.SentSegs[5:]
	if len(retx) != 2 ||
		retx[0].Seg.Seq.Cmp(bi(1000)) != 0 || retx[0].Seg.Len.Cmp(bi(1000)) != 0 ||
		retx[1].Seg.Seq.Cmp(bi(2000)) != 0 || retx[1].Seg.Len.Cmp(bi(1000)) != 0 {
		t.Fatalf("retx segments = %v, want [1000,2000) and [2000,3000)", retx)
	}
	for _, r := range retx {
		if !r.Retx {
			t.Fatalf("segment %v not tagged as retransmission", r.Seg)
		}
	}
}

// Overlapping SACK blocks on the sender are merged; the union is the basis
// for gap computation and is counted once.
func TestSenderSackUnionOverlap(t *testing.T) {
	q, s := newTestSender(5000, 1000, 5000)
	s.Start()
	q.Run(10)
	s.OnAck(AckPacket{Ack: bi(0), SACKs: []Interval{
		NewInterval(bi(2000), bi(3500)),
		NewInterval(bi(3000), bi(5000)), // overlaps previous block
	}})
	if got := s.SackedBytes(); got.Cmp(bi(3000)) != 0 {
		t.Fatalf("sacked union = %s, want 3000 (overlap counted once)", got)
	}
	// Gap is [0,2000): only that may be retransmitted.
	if s.Retransmitted.Cmp(bi(2000)) != 0 {
		t.Fatalf("retransmitted = %s, want 2000", s.Retransmitted)
	}
}

// ACKs arriving out of order: a later (smaller) cumulative ACK is ignored,
// and SACK blocks below sndUna are clipped away.
func TestOutOfOrderAcks(t *testing.T) {
	q, s := newTestSender(5000, 1000, 5000)
	s.Start()
	q.Run(10)
	s.OnAck(AckPacket{Ack: bi(3000), SACKs: []Interval{NewInterval(bi(3500), bi(4000))}})
	if s.Retransmitted.Cmp(bi(500)) != 0 {
		t.Fatalf("retransmitted = %s, want 500 (gap [3000,3500))", s.Retransmitted)
	}
	s.OnAck(AckPacket{Ack: bi(4000), SACKs: []Interval{NewInterval(bi(3000), bi(4000))}})
	s.OnAck(AckPacket{Ack: bi(2000)}) // stale, arrives late
	if s.SndUna().Cmp(bi(4000)) != 0 {
		t.Fatalf("sndUna = %s, want 4000", s.SndUna())
	}
	if s.Retransmitted.Cmp(bi(500)) != 0 {
		t.Fatalf("retransmitted = %s after out-of-order ACKs, want 500", s.Retransmitted)
	}
}

// Shrinking the window clamps the retransmission range.
func TestWindowShrinkClampsRetransmission(t *testing.T) {
	q, s := newTestSender(10000, 1000, 10000)
	s.Start()
	q.Run(10)
	s.SetWindow(bi(1500))
	s.OnAck(AckPacket{Ack: bi(0), SACKs: []Interval{NewInterval(bi(2000), bi(5000))}})
	if s.Retransmitted.Cmp(bi(1500)) != 0 {
		t.Fatalf("retransmitted = %s, want 1500 (clamped to shrunk window)", s.Retransmitted)
	}
	for _, r := range s.SentSegs[10:] {
		end := new(big.Int).Add(r.Seg.Seq, r.Seg.Len)
		if end.Cmp(bi(1500)) > 0 {
			t.Fatalf("segment %v exceeds shrunk window", r.Seg)
		}
	}
}

// End-to-end: lossy link, SACK recovery, extended (2^70) sequence numbers.
func TestFullTransferWithLossAndExtendedSeq(t *testing.T) {
	q := &Queue{}
	data := NewLink(q, 3, 7) // every 7th data packet is lost
	ack := NewLink(q, 3, 0)
	isn := new(big.Int).Lsh(big.NewInt(1), 70) // extended-integer ISN
	sender := NewSender(q, data, isn, bi(10000), bi(1000), bi(4000), 60, nil)
	receiver := NewReceiver(q, ack, isn, nil)
	sender.OnData = receiver.OnSegment
	receiver.OnAck = sender.OnAck

	sender.Start()
	q.Run(100000)

	end := new(big.Int).Add(isn, bi(10000))
	if sender.SndUna().Cmp(end) != 0 {
		t.Fatalf("sndUna = %s, want %s (all acked)", sender.SndUna(), end)
	}
	if receiver.RcvNxt().Cmp(end) != 0 {
		t.Fatalf("rcvNxt = %s, want %s (contiguous)", receiver.RcvNxt(), end)
	}
	if receiver.ReceivedBytes.Cmp(bi(10000)) != 0 {
		t.Fatalf("received = %s, want 10000 (union counted once)", receiver.ReceivedBytes)
	}
	if data.Dropped == 0 {
		t.Fatal("expected packet loss in this scenario")
	}
	if sender.Retransmitted.Sign() <= 0 {
		t.Fatal("expected retransmissions after loss")
	}
	want := new(big.Int).Add(bi(10000), sender.Retransmitted)
	if sender.SentTotal.Cmp(want) != 0 {
		t.Fatalf("sent total = %s, want %s (stream + retx only)", sender.SentTotal, want)
	}
}

// A retransmission that is also lost is recovered by the RTO path.
func TestRTORecoversLostRetransmission(t *testing.T) {
	q := &Queue{}
	data := NewLink(q, 2, 5) // aggressive loss
	ack := NewLink(q, 2, 0)
	sender := NewSender(q, data, bi(0), bi(4000), bi(1000), bi(4000), 40, nil)
	receiver := NewReceiver(q, ack, bi(0), nil)
	sender.OnData = receiver.OnSegment
	receiver.OnAck = sender.OnAck

	sender.Start()
	q.Run(100000)

	if sender.SndUna().Cmp(bi(4000)) != 0 {
		t.Fatalf("sndUna = %s, want 4000", sender.SndUna())
	}
	if receiver.ReceivedBytes.Cmp(bi(4000)) != 0 {
		t.Fatalf("received = %s, want 4000", receiver.ReceivedBytes)
	}
}
