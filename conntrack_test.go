package conntrack

import (
	"net/netip"
	"testing"
	"time"
)

var (
	ipA = netip.MustParseAddr("192.0.2.1")
	ipB = netip.MustParseAddr("192.0.2.2")
)

func seg(srcIP netip.Addr, sp uint16, dstIP netip.Addr, dp uint16, flags uint8) Packet {
	return Packet{SrcIP: srcIP, SrcPort: sp, DstIP: dstIP, DstPort: dp, Flags: flags, Window: 1000}
}

// establish runs a clean three-way handshake with window scaling and returns
// the table holding one ESTABLISHED connection.
func establish(t *testing.T, scaleA, scaleB uint8) *Table {
	t.Helper()
	tbl := NewTable()
	syn := seg(ipA, 1000, ipB, 80, FlagSYN)
	syn.Seq, syn.Window = 1000, 500
	syn.HasWScale, syn.WScale = true, int(scaleA)
	if v := tbl.Process(syn); v != VerdictAccept {
		t.Fatalf("SYN: %s", v)
	}

	synack := seg(ipB, 80, ipA, 1000, FlagSYN|FlagACK)
	synack.Seq, synack.Ack, synack.Window = 5000, 1001, 600
	synack.HasWScale, synack.WScale = true, int(scaleB)
	if v := tbl.Process(synack); v != VerdictAccept {
		t.Fatalf("SYN-ACK: %s", v)
	}

	ack := seg(ipA, 1000, ipB, 80, FlagACK)
	ack.Seq, ack.Ack, ack.Window = 1001, 5001, 700
	if v := tbl.Process(ack); v != VerdictAccept {
		t.Fatalf("ACK: %s", v)
	}
	if s := tbl.Lookup(syn); s != StateEstablished {
		t.Fatalf("state after handshake = %s", s)
	}
	return tbl
}

func TestHandshakeAndUnknownFlow(t *testing.T) {
	tbl := NewTable()

	rogue := seg(ipA, 9999, ipB, 80, FlagACK)
	rogue.Seq = 1
	if v := tbl.Process(rogue); v != VerdictDrop {
		t.Fatalf("unknown ACK: %s", v)
	}
	rst := seg(ipA, 9999, ipB, 80, FlagRST)
	if v := tbl.Process(rst); v != VerdictDrop {
		t.Fatalf("unknown RST: %s", v)
	}

	establish(t, 0, 0)
}

func TestRSTPolicy(t *testing.T) {
	tbl := establish(t, 0, 0)

	// B's scaled receive window for A's traffic is 600, rcvNxt is 1001.
	exact := seg(ipA, 1000, ipB, 80, FlagRST)
	exact.Seq = 1001
	if v := tbl.Process(exact); v != VerdictAccept {
		t.Fatalf("exact RST: %s, want ACCEPT", v)
	}
	if tbl.Len() != 0 {
		t.Fatalf("exact RST did not remove connection, len=%d", tbl.Len())
	}

	tbl = establish(t, 0, 0)
	inWindow := seg(ipA, 1000, ipB, 80, FlagRST)
	inWindow.Seq = 1001 + 100
	if v := tbl.Process(inWindow); v != VerdictChallengeACK {
		t.Fatalf("in-window RST: %s, want CHALLENGE_ACK", v)
	}
	if tbl.Len() != 1 {
		t.Fatalf("challenge RST removed connection")
	}

	outWindow := seg(ipA, 1000, ipB, 80, FlagRST)
	outWindow.Seq = 1001 + 5000
	if v := tbl.Process(outWindow); v != VerdictDrop {
		t.Fatalf("out-of-window RST: %s, want DROP", v)
	}

	edge := seg(ipA, 1000, ipB, 80, FlagRST)
	edge.Seq = 1001 + 600
	if v := tbl.Process(edge); v != VerdictChallengeACK {
		t.Fatalf("window-edge RST: %s, want CHALLENGE_ACK", v)
	}
}

func TestWindowScaling(t *testing.T) {
	// A advertises shift 2, B advertises shift 3. B's raw window 600
	// becomes 4800 for segments arriving at B.
	tbl := establish(t, 2, 3)

	inside := seg(ipA, 1000, ipB, 80, FlagACK|FlagPSH)
	inside.Seq, inside.Ack = 1001+4000, 5001
	inside.Payload = 10
	if v := tbl.Process(inside); v != VerdictAccept {
		t.Fatalf("data inside scaled window: %s", v)
	}

	rst := seg(ipA, 1000, ipB, 80, FlagRST)
	rst.Seq = 1001 + 4799
	if v := tbl.Process(rst); v != VerdictChallengeACK {
		t.Fatalf("RST inside scaled window: %s, want CHALLENGE_ACK", v)
	}

	// B's receive window is determined solely by B's own advertisements
	// (600<<3 = 4800); A's Window field must not change it. A reset beyond
	// 4800 is therefore out of window and dropped.
	far := seg(ipA, 1000, ipB, 80, FlagRST)
	far.Seq = 1001 + 4801
	if v := tbl.Process(far); v != VerdictDrop {
		t.Fatalf("RST beyond scaled window: %s, want DROP", v)
	}
}

func TestDataWindowChecks(t *testing.T) {
	tbl := establish(t, 0, 0)
	// B's receive window for A's traffic is 600 (from the SYN-ACK);
	// A's rcvNxt on B's data is 1001.

	good := seg(ipA, 1000, ipB, 80, FlagACK|FlagPSH)
	good.Seq, good.Ack, good.Payload = 1001, 5001, 100
	if v := tbl.Process(good); v != VerdictAccept {
		t.Fatalf("in-order data: %s", v)
	}

	// After 100 in-order bytes B expects seq 1101; a retransmit starting
	// earlier but still overlapping the window remains acceptable.
	retrans := seg(ipA, 1000, ipB, 80, FlagACK)
	retrans.Seq, retrans.Ack, retrans.Payload, retrans.Window = 1090, 5001, 20, 700
	if v := tbl.Process(retrans); v != VerdictAccept {
		t.Fatalf("overlapping retransmit: %s", v)
	}

	beyond := seg(ipA, 1000, ipB, 80, FlagACK)
	beyond.Seq, beyond.Ack, beyond.Payload = 1001+5000, 5001, 10
	if v := tbl.Process(beyond); v != VerdictDrop {
		t.Fatalf("out-of-window data: %s, want DROP", v)
	}

	badAck := seg(ipA, 1000, ipB, 80, FlagACK)
	badAck.Seq, badAck.Ack = 1101, 1
	if v := tbl.Process(badAck); v != VerdictChallengeACK {
		t.Fatalf("bogus ACK: %s, want CHALLENGE_ACK", v)
	}
}

func TestSequenceWrapAround(t *testing.T) {
	tbl := NewTable()
	const isnA uint32 = 0xFFFFFFF0

	syn := seg(ipA, 1000, ipB, 80, FlagSYN)
	syn.Seq = isnA
	tbl.Process(syn)
	synack := seg(ipB, 80, ipA, 1000, FlagSYN|FlagACK)
	synack.Seq, synack.Ack = 42, isnA+1
	tbl.Process(synack)
	ack := seg(ipA, 1000, ipB, 80, FlagACK)
	ack.Seq, ack.Ack = isnA+1, 43
	tbl.Process(ack)

	// 32 data bytes carry the sequence space across the 32-bit boundary.
	data := seg(ipA, 1000, ipB, 80, FlagACK|FlagPSH)
	// B sent no data beyond its SYN (seq 42), so A's valid ACK number is 43.
	data.Seq, data.Ack, data.Payload, data.Window = isnA+1, 43, 32, 1000
	if v := tbl.Process(data); v != VerdictAccept {
		t.Fatalf("wrapping data: %s", v)
	}

	// rcvNxt is now (0xFFFFFFF1 + 32) mod 2^32 = 17.
	rstExact := seg(ipA, 1000, ipB, 80, FlagRST)
	rstExact.Seq = 17
	if v := tbl.Process(rstExact); v != VerdictAccept {
		t.Fatalf("post-wrap exact RST: %s, want ACCEPT", v)
	}

	tbl = NewTable()
	tbl.Process(syn)
	tbl.Process(synack)
	tbl.Process(ack)
	tbl.Process(data)

	rstBefore := seg(ipA, 1000, ipB, 80, FlagRST)
	var farBefore uint32 = 17
	rstBefore.Seq = farBefore - 2000
	if v := tbl.Process(rstBefore); v != VerdictDrop {
		t.Fatalf("pre-rcvNxt RST after wrap: %s, want DROP", v)
	}

	// An in-window but non-exact reset after wrapping draws a challenge.
	tbl = NewTable()
	tbl.Process(syn)
	tbl.Process(synack)
	tbl.Process(ack)
	tbl.Process(data)
	rstInWindow := seg(ipA, 1000, ipB, 80, FlagRST)
	rstInWindow.Seq = 17 + 500
	if v := tbl.Process(rstInWindow); v != VerdictChallengeACK {
		t.Fatalf("post-wrap in-window RST: %s, want CHALLENGE_ACK", v)
	}
}

func TestSimultaneousOpen(t *testing.T) {
	tbl := NewTable()
	synA := seg(ipA, 1000, ipB, 80, FlagSYN)
	synA.Seq = 100
	synB := seg(ipB, 80, ipA, 1000, FlagSYN)
	synB.Seq = 200

	if v := tbl.Process(synA); v != VerdictAccept {
		t.Fatalf("SYN A: %s", v)
	}
	if v := tbl.Process(synB); v != VerdictAccept {
		t.Fatalf("cross SYN B: %s", v)
	}
	if s := tbl.Lookup(synA); s != StateSynRcvd {
		t.Fatalf("state after cross SYNs: %s", s)
	}

	ackA := seg(ipA, 1000, ipB, 80, FlagACK)
	ackA.Seq, ackA.Ack = 101, 201
	if v := tbl.Process(ackA); v != VerdictAccept {
		t.Fatalf("ACK A: %s", v)
	}
	ackB := seg(ipB, 80, ipA, 1000, FlagACK)
	ackB.Seq, ackB.Ack = 201, 101
	if v := tbl.Process(ackB); v != VerdictAccept {
		t.Fatalf("ACK B: %s", v)
	}
	if s := tbl.Lookup(synA); s != StateEstablished {
		t.Fatalf("state after simultaneous open: %s", s)
	}
}

func TestSynRetransmit(t *testing.T) {
	tbl := NewTable()
	syn := seg(ipA, 1000, ipB, 80, FlagSYN)
	syn.Seq = 7
	syn.HasWScale, syn.WScale = true, 4
	tbl.Process(syn)

	if v := tbl.Process(syn); v != VerdictAccept {
		t.Fatalf("SYN retransmit: %s", v)
	}
	bogus := syn
	bogus.Seq = 8
	if v := tbl.Process(bogus); v != VerdictDrop {
		t.Fatalf("mutated SYN retransmit: %s, want DROP", v)
	}

	synack := seg(ipB, 80, ipA, 1000, FlagSYN|FlagACK)
	synack.Seq, synack.Ack = 99, 8
	tbl.Process(synack)

	if v := tbl.Process(synack); v != VerdictAccept {
		t.Fatalf("SYN-ACK retransmit: %s", v)
	}

	ack := seg(ipA, 1000, ipB, 80, FlagACK)
	ack.Seq, ack.Ack = 8, 100
	tbl.Process(ack)

	late := seg(ipA, 1000, ipB, 80, FlagSYN)
	late.Seq = 8
	if v := tbl.Process(late); v != VerdictChallengeACK {
		t.Fatalf("late SYN: %s, want CHALLENGE_ACK", v)
	}
	if s := tbl.Lookup(syn); s != StateEstablished {
		t.Fatalf("late SYN changed state to %s", s)
	}
}

func TestHalfCloseTransitions(t *testing.T) {
	tbl := establish(t, 0, 0)

	finA := seg(ipA, 1000, ipB, 80, FlagFIN|FlagACK)
	finA.Seq, finA.Ack = 1001, 5001
	if v := tbl.Process(finA); v != VerdictAccept {
		t.Fatalf("FIN A: %s", v)
	}
	if s := tbl.Lookup(finA); s != StateFinWait1 {
		t.Fatalf("after FIN A: %s", s)
	}

	ackB := seg(ipB, 80, ipA, 1000, FlagACK)
	ackB.Seq, ackB.Ack = 5001, 1002
	if v := tbl.Process(ackB); v != VerdictAccept {
		t.Fatalf("ACK of FIN A: %s", v)
	}
	if s := tbl.Lookup(finA); s != StateFinWait2 {
		t.Fatalf("after ACK of FIN A: %s", s)
	}

	// A FIN retransmit before the peer closes is accepted idempotently.
	finARetrans := finA
	finARetrans.Ack = 5001
	if v := tbl.Process(finARetrans); v != VerdictAccept {
		t.Fatalf("FIN retransmit: %s", v)
	}

	finB := seg(ipB, 80, ipA, 1000, FlagFIN|FlagACK)
	finB.Seq, finB.Ack = 5001, 1002
	if v := tbl.Process(finB); v != VerdictAccept {
		t.Fatalf("FIN B: %s", v)
	}

	ackA := seg(ipA, 1000, ipB, 80, FlagACK)
	ackA.Seq, ackA.Ack = 1002, 5002
	if v := tbl.Process(ackA); v != VerdictAccept {
		t.Fatalf("ACK of FIN B: %s", v)
	}
	if s := tbl.Lookup(finA); s != StateTimeWait {
		t.Fatalf("after full close: %s", s)
	}

	// Data in TIME_WAIT is refused, the final-FIN retransmit is accepted.
	data := seg(ipB, 80, ipA, 1000, FlagACK|FlagPSH)
	data.Seq, data.Ack, data.Payload = 5002, 1002, 5
	if v := tbl.Process(data); v != VerdictDrop {
		t.Fatalf("data in TIME_WAIT: %s, want DROP", v)
	}
	if v := tbl.Process(finB); v != VerdictAccept {
		t.Fatalf("FIN B retransmit in TIME_WAIT: %s", v)
	}
}

func TestSimultaneousClose(t *testing.T) {
	tbl := establish(t, 0, 0)

	finA := seg(ipA, 1000, ipB, 80, FlagFIN|FlagACK)
	finA.Seq, finA.Ack = 1001, 5001
	finB := seg(ipB, 80, ipA, 1000, FlagFIN|FlagACK)
	finB.Seq, finB.Ack = 5001, 1001

	tbl.Process(finA)
	if s := tbl.Lookup(finA); s != StateFinWait1 {
		t.Fatalf("after first FIN: %s", s)
	}
	tbl.Process(finB)
	if s := tbl.Lookup(finA); s != StateClosing {
		t.Fatalf("after cross FIN: %s, want CLOSING", s)
	}

	ackA := seg(ipA, 1000, ipB, 80, FlagACK)
	ackA.Seq, ackA.Ack = 1002, 5002
	tbl.Process(ackA)
	ackB := seg(ipB, 80, ipA, 1000, FlagACK)
	ackB.Seq, ackB.Ack = 5002, 1002
	tbl.Process(ackB)

	if s := tbl.Lookup(finA); s != StateTimeWait {
		t.Fatalf("after simultaneous close: %s", s)
	}
}

func TestSweep(t *testing.T) {
	tbl := NewTable()
	tbl.HandshakeTimeout = 10 * time.Second
	syn := seg(ipA, 1000, ipB, 80, FlagSYN)
	syn.Seq = 1
	base := time.Unix(1000, 0)
	tbl.ProcessAt(syn, base)

	if n := tbl.Sweep(base.Add(9 * time.Second)); n != 0 {
		t.Fatalf("early sweep removed %d", n)
	}
	if n := tbl.Sweep(base.Add(11 * time.Second)); n != 1 {
		t.Fatalf("expired sweep removed %d, want 1", n)
	}
	if tbl.Len() != 0 {
		t.Fatalf("connection still present after sweep")
	}
}
