package sim

import "testing"

// sink is a test endpoint that records every packet it receives.
type sink struct{ packets []Packet }

func (s *sink) Receive(p Packet) { s.packets = append(s.packets, p) }

func (s *sink) dataPackets() int {
	n := 0
	for _, p := range s.packets {
		if p.Kind == DataPacket {
			n++
		}
	}
	return n
}

func newTestSender(cwnd uint64) (*Scheduler, *Sender, *Pipe, *sink) {
	sched := NewScheduler()
	snd := NewSender(sched, NewSeq(0), 1000, cwnd, 50)
	sk := &sink{}
	pipe := NewPipe(sched, 1, sk)
	snd.Out = pipe
	return sched, snd, pipe, sk
}

func TestRejectAckOfUnsentBytes(t *testing.T) {
	_, snd, _, _ := newTestSender(1 << 20)
	snd.Enqueue(make([]byte, 3000)) // sndNxt = 3000

	// Cumulative ACK beyond everything ever sent.
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(4000)})
	if snd.RejectedAcks != 1 {
		t.Fatalf("RejectedAcks = %d, want 1", snd.RejectedAcks)
	}
	if !snd.SndUna().Eq(NewSeq(0)) {
		t.Fatalf("sndUna moved to %s on rejected ACK", snd.SndUna())
	}

	// SACK block covering unsent bytes.
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(0), Sack: []Range{{NewSeq(2500), NewSeq(3500)}}})
	if snd.RejectedAcks != 2 {
		t.Fatalf("RejectedAcks = %d, want 2", snd.RejectedAcks)
	}

	// A valid ACK is still accepted afterwards.
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(1000)})
	if !snd.SndUna().Eq(NewSeq(1000)) {
		t.Fatalf("sndUna = %s, want 1000", snd.SndUna())
	}
}

func TestStaleAckDoesNotRetransmit(t *testing.T) {
	sched, snd, pipe, _ := newTestSender(1 << 20)
	snd.Enqueue(make([]byte, 2000))
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(2000)}) // fully acked

	sentBefore := pipe.Sent()
	// Old ACKs arrive late; they must not move state backwards nor
	// cause already-acked data to be retransmitted.
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(1000)})
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(500), Sack: []Range{{NewSeq(500), NewSeq(1500)}}})
	if snd.StaleAcks != 2 {
		t.Fatalf("StaleAcks = %d, want 2", snd.StaleAcks)
	}
	if !snd.SndUna().Eq(NewSeq(2000)) {
		t.Fatalf("sndUna regressed to %s", snd.SndUna())
	}

	sched.Run(10000) // let every timer fire
	if pipe.Sent() != sentBefore {
		t.Fatalf("stale ACKs triggered %d extra packets", pipe.Sent()-sentBefore)
	}
	if snd.BytesRetransmitted != 0 {
		t.Fatalf("retransmitted %d bytes although all data was acked", snd.BytesRetransmitted)
	}
}

func TestOutOfOrderAndOverlappingAcks(t *testing.T) {
	sched, snd, _, _ := newTestSender(1 << 20)
	snd.Enqueue(make([]byte, 3000)) // segments [0,1000) [1000,2000) [2000,3000)

	// SACK for the third segment arrives first (out of order).
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(0), Sack: []Range{{NewSeq(2000), NewSeq(3000)}}})
	if !snd.SndUna().Eq(NewSeq(0)) {
		t.Fatalf("sndUna = %s, want 0 (cumulative ack not advanced)", snd.SndUna())
	}
	// Cumulative ACK for the first segment plus a SACK block that
	// overlaps the earlier one: union must count bytes once.
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(1000), Sack: []Range{{NewSeq(1000), NewSeq(2500)}}})
	if !snd.SndUna().Eq(NewSeq(1000)) {
		t.Fatalf("sndUna = %s, want 1000", snd.SndUna())
	}
	total := uint64(0)
	for _, r := range snd.Sacked() {
		total += r.Len().Uint64()
	}
	if total != 3000 {
		t.Fatalf("sacked union = %d, want 3000 (overlap counted once)", total)
	}

	sched.Run(10000) // timers fire, but nothing is left unacked
	if snd.BytesRetransmitted != 0 {
		t.Fatalf("retransmitted %d bytes; everything was acked via SACK", snd.BytesRetransmitted)
	}
}

func TestCongestionWindowShrink(t *testing.T) {
	_, snd, pipe, _ := newTestSender(8000)
	snd.Enqueue(make([]byte, 10000))
	if pipe.Sent() != 8 {
		t.Fatalf("sent = %d, want 8 (window 8000)", pipe.Sent())
	}

	snd.SetCwnd(2000) // shrink below inflight
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(2000), Rwnd: 1 << 20})
	if pipe.Sent() != 8 {
		t.Fatalf("sent = %d after shrink, want 8 (inflight 6000 > cwnd 2000)", pipe.Sent())
	}

	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(8000), Rwnd: 1 << 20}) // drain inflight
	if pipe.Sent() != 10 {
		t.Fatalf("sent = %d, want 10 (window allows exactly 2 more)", pipe.Sent())
	}
	if !snd.SndNxt().Eq(NewSeq(10000)) {
		t.Fatalf("sndNxt = %s, want 10000", snd.SndNxt())
	}
}

func TestAdvertisedWindowShrink(t *testing.T) {
	_, snd, pipe, _ := newTestSender(1 << 20)
	snd.Enqueue(make([]byte, 5000))
	if pipe.Sent() != 5 {
		t.Fatalf("sent = %d, want 5", pipe.Sent())
	}
	// Receiver shrinks its window below one MSS: sender must stop.
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(5000), Rwnd: 500})
	if pipe.Sent() != 5 {
		t.Fatalf("sent = %d, want 5 (rwnd 500 < MSS)", pipe.Sent())
	}
	// Window reopens.
	snd.Receive(Packet{Kind: AckPacket, Ack: NewSeq(5000), Rwnd: 3000})
	if pipe.Sent() != 5 {
		t.Fatalf("sent = %d, want 5 (no unsent data left)", pipe.Sent())
	}
	snd.Enqueue(make([]byte, 2500))
	if pipe.Sent() != 8 {
		t.Fatalf("sent = %d, want 8 (1000 + 1000 + 500 within rwnd 3000)", pipe.Sent())
	}
}
