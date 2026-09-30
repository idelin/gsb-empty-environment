package tcpsim

import "testing"

// blackhole returns a link whose packets are delivered nowhere useful,
// simulating a 100% loss path without using the Drop hook.
func blackhole(eng *Engine) *Link {
	return NewLink(eng, "blackhole", 1000, 1, EndpointFunc(func(Packet) {}))
}

func TestEngineOrdering(t *testing.T) {
	eng := NewEngine()
	var got []int
	eng.After(5, func() { got = append(got, 5) })
	eng.After(1, func() { got = append(got, 1) })
	eng.After(3, func() { got = append(got, 3) })
	eng.After(3, func() { got = append(got, 33) }) // same tick: FIFO
	eng.Run()
	want := []int{1, 3, 33, 5}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if eng.Now() != 5 {
		t.Fatalf("engine clock = %d, want 5", eng.Now())
	}
}

func TestLinkSerializationAndDelay(t *testing.T) {
	eng := NewEngine()
	var arrivals []int
	l := NewLink(eng, "l", 100, 10, EndpointFunc(func(p Packet) {
		arrivals = append(arrivals, eng.Now())
	}))
	// wire size 1000 -> 10 ticks serialization, 10 ticks propagation.
	l.Send(Packet{Kind: DataPacket, Seq: 0, Len: 960})
	// queued behind the first: starts serialization at tick 10.
	l.Send(Packet{Kind: DataPacket, Seq: 960, Len: 960})
	eng.Run()
	want := []int{20, 30}
	if len(arrivals) != 2 || arrivals[0] != want[0] || arrivals[1] != want[1] {
		t.Fatalf("arrivals = %v, want %v", arrivals, want)
	}
}

func TestSlowStartThenCongestionAvoidance(t *testing.T) {
	eng := NewEngine()
	snd := NewSender(eng, 100000, 1000, 8000, 100000)
	snd.Out = blackhole(eng)
	snd.sndNxt = 10000 // pretend 10 segments are outstanding

	// Slow start: +1 MSS per new ACK.
	for ack := 1000; ack <= 7000; ack += 1000 {
		snd.Receive(Packet{Kind: AckPacket, Seq: ack})
		if want := ack + 1000; snd.cwnd != want {
			t.Fatalf("after ack=%d cwnd=%d, want %d", ack, snd.cwnd, want)
		}
	}
	if snd.phase != PhaseCongAvoid {
		t.Fatalf("phase = %s, want %s", snd.phase, PhaseCongAvoid)
	}
	// Congestion avoidance: +MSS*MSS/cwnd per new ACK (additive).
	snd.Receive(Packet{Kind: AckPacket, Seq: 8000})
	if want := 8000 + 1000*1000/8000; snd.cwnd != want {
		t.Fatalf("CA cwnd=%d, want %d", snd.cwnd, want)
	}
	prev := snd.cwnd
	snd.Receive(Packet{Kind: AckPacket, Seq: 9000})
	if snd.cwnd <= prev {
		t.Fatalf("CA window did not grow: %d -> %d", prev, snd.cwnd)
	}
	if inc := snd.cwnd - prev; inc >= snd.MSS {
		t.Fatalf("CA increment %d must be < MSS", inc)
	}
}

func TestTimeoutBackoffRules(t *testing.T) {
	eng := NewEngine()
	snd := NewSender(eng, 10000, 1000, 64000, 100)
	snd.Out = blackhole(eng)
	snd.Start()

	eng.RunUntil(100) // first timeout
	if snd.Timeouts != 1 {
		t.Fatalf("timeouts=%d, want 1", snd.Timeouts)
	}
	if snd.cwnd != snd.MSS {
		t.Fatalf("cwnd=%d, want collapse to MSS=%d", snd.cwnd, snd.MSS)
	}
	if want := maxInt(1000/2, 2*1000); snd.ssthresh != want {
		t.Fatalf("ssthresh=%d, want %d", snd.ssthresh, want)
	}
	if snd.rto != 200 {
		t.Fatalf("rto=%d, want 200", snd.rto)
	}

	eng.RunUntil(300) // second timeout (100 + 200)
	if snd.Timeouts != 2 || snd.rto != 400 {
		t.Fatalf("timeouts=%d rto=%d, want 2 and 400", snd.Timeouts, snd.rto)
	}

	eng.RunUntil(700) // third timeout (300 + 400)
	if snd.Timeouts != 3 || snd.rto != 800 {
		t.Fatalf("timeouts=%d rto=%d, want 3 and 800", snd.Timeouts, snd.rto)
	}
	if snd.Retransmits != 3 {
		t.Fatalf("retransmits=%d, want 3", snd.Retransmits)
	}
}

func TestDupAckNeverGrowsWindow(t *testing.T) {
	eng := NewEngine()
	snd := NewSender(eng, 100000, 1000, 64000, 100000)
	snd.Out = blackhole(eng)
	snd.sndNxt = 5000
	snd.cwnd = 8000

	// Three duplicate ACKs trigger exactly one fast retransmit and
	// halve the window; they must never increase it.
	for i := 0; i < 3; i++ {
		snd.Receive(Packet{Kind: AckPacket, Seq: 0})
	}
	if snd.FastRetransmits != 1 || snd.Retransmits != 1 {
		t.Fatalf("fast=%d retx=%d, want 1 and 1", snd.FastRetransmits, snd.Retransmits)
	}
	if snd.cwnd != 4000 {
		t.Fatalf("cwnd=%d, want 4000 after fast retransmit", snd.cwnd)
	}
	// Further duplicates: no window change, no extra retransmit.
	for i := 0; i < 5; i++ {
		snd.Receive(Packet{Kind: AckPacket, Seq: 0})
	}
	if snd.cwnd != 4000 {
		t.Fatalf("dup ACKs inflated cwnd to %d", snd.cwnd)
	}
	if snd.FastRetransmits != 1 || snd.Retransmits != 1 {
		t.Fatalf("extra retransmit: fast=%d retx=%d", snd.FastRetransmits, snd.Retransmits)
	}
	if snd.sndUna != 0 {
		t.Fatalf("dup ACK moved sndUna to %d", snd.sndUna)
	}
}

func TestStaleAckKeepsWindowNonNegative(t *testing.T) {
	eng := NewEngine()
	snd := NewSender(eng, 9000, 1000, 64000, 100000)
	snd.Out = blackhole(eng)
	snd.sndUna = 5000
	snd.sndNxt = 9000
	snd.cwnd = 2000 // inflight (4000) exceeds cwnd

	if u := snd.Usable(); u != 0 {
		t.Fatalf("usable=%d, want clamped 0", u)
	}
	// Stale ACK below sndUna: must not move anything.
	snd.Receive(Packet{Kind: AckPacket, Seq: 3000})
	if snd.sndUna != 5000 || snd.cwnd != 2000 {
		t.Fatalf("stale ACK mutated state: una=%d cwnd=%d", snd.sndUna, snd.cwnd)
	}
	if snd.StaleAcks != 1 {
		t.Fatalf("stale acks=%d, want 1", snd.StaleAcks)
	}
	if u := snd.Usable(); u < 0 {
		t.Fatalf("usable window went negative: %d", u)
	}
	// Valid ACK to the end, then another stale one.
	snd.Receive(Packet{Kind: AckPacket, Seq: 9000})
	if snd.sndUna != 9000 {
		t.Fatalf("sndUna=%d, want 9000", snd.sndUna)
	}
	cwnd := snd.cwnd
	snd.Receive(Packet{Kind: AckPacket, Seq: 1000})
	if snd.sndUna != 9000 || snd.cwnd != cwnd {
		t.Fatalf("stale ACK after full ack mutated state")
	}
	if u := snd.Usable(); u < 0 {
		t.Fatalf("usable window went negative: %d", u)
	}
}

func TestCumulativeAckClampedToRealBytes(t *testing.T) {
	eng := NewEngine()
	snd := NewSender(eng, 5000, 1000, 64000, 100000)
	snd.Out = blackhole(eng)
	snd.sndNxt = 5000

	// ACK claims far more than was ever sent: only real outstanding
	// bytes may be credited.
	snd.Receive(Packet{Kind: AckPacket, Seq: 999999})
	if snd.sndUna != 5000 {
		t.Fatalf("sndUna=%d, want clamp to 5000", snd.sndUna)
	}
	// Slow-start credit is min(newly, MSS) = 1000, not 999999.
	if snd.cwnd != 2000 {
		t.Fatalf("cwnd=%d, want 2000", snd.cwnd)
	}
	if u := snd.Usable(); u < 0 {
		t.Fatalf("usable window went negative: %d", u)
	}
}

func TestReceiverBufferingAndCumulativeJump(t *testing.T) {
	eng := NewEngine()
	var acks []int
	rcv := NewReceiver()
	rcv.Out = NewLink(eng, "ack", 1000, 0, EndpointFunc(func(p Packet) {
		acks = append(acks, p.Seq)
	}))

	rcv.Receive(Packet{Kind: DataPacket, Seq: 0, Len: 1000})
	rcv.Receive(Packet{Kind: DataPacket, Seq: 1000, Len: 1000})
	// Gap at 2000: buffer 3000, ACK stays at 2000.
	rcv.Receive(Packet{Kind: DataPacket, Seq: 3000, Len: 1000})
	if rcv.RcvNxt != 2000 || rcv.Buffered != 1000 {
		t.Fatalf("rcvNxt=%d buffered=%d", rcv.RcvNxt, rcv.Buffered)
	}
	// Filling the gap drains the buffer: cumulative jump to 4000.
	rcv.Receive(Packet{Kind: DataPacket, Seq: 2000, Len: 1000})
	if rcv.RcvNxt != 4000 || rcv.Received != 4000 || rcv.Buffered != 0 {
		t.Fatalf("rcvNxt=%d received=%d buffered=%d", rcv.RcvNxt, rcv.Received, rcv.Buffered)
	}
	eng.Run()
	want := []int{1000, 2000, 2000, 4000}
	if len(acks) != len(want) {
		t.Fatalf("acks=%v, want %v", acks, want)
	}
	for i := range want {
		if acks[i] != want[i] {
			t.Fatalf("acks=%v, want %v", acks, want)
		}
	}
}

func TestFullTransferWithLoss(t *testing.T) {
	eng := NewEngine()
	const total = 20000
	snd := NewSender(eng, total, 1000, 8000, 400)
	rcv := NewReceiver()
	fwd := NewLink(eng, "data", 260, 40, rcv)
	ack := NewLink(eng, "ack", 2000, 40, snd)
	snd.Out = fwd
	rcv.Out = ack

	drops := map[int]int{}
	fwd.Drop = func(p Packet) bool {
		want := 0
		switch p.Seq {
		case 5000:
			want = 1 // recovered by fast retransmit
		case 12000:
			want = 2 // fast retransmit lost too -> RTO backoff
		}
		if drops[p.Seq] < want {
			drops[p.Seq]++
			return true
		}
		return false
	}

	snd.Start()
	eng.Run()

	if !snd.Done() {
		t.Fatalf("transfer incomplete: una=%d", snd.sndUna)
	}
	if rcv.Received != total {
		t.Fatalf("received=%d, want %d", rcv.Received, total)
	}
	if snd.FastRetransmits < 1 {
		t.Fatalf("expected at least one fast retransmit")
	}
	if snd.Timeouts < 1 {
		t.Fatalf("expected at least one timeout")
	}
	if snd.DupAcks == 0 {
		t.Fatalf("expected duplicate ACKs during loss recovery")
	}
	// After recovery the RTO must fall back to the base value.
	if snd.rto != snd.rtoBase {
		t.Fatalf("rto=%d, want restored base %d", snd.rto, snd.rtoBase)
	}
	if rcv.Buffered != 0 {
		t.Fatalf("receiver buffer not drained: %d", rcv.Buffered)
	}
}
