package conntrack

import (
	"sync"
	"testing"
	"time"
)

func TestAccessorsAndSweepStates(t *testing.T) {
	if got := StateEstablished.String(); got != "ESTABLISHED" {
		t.Fatalf("State.String = %q", got)
	}
	if got := VerdictChallengeACK.String(); got != "CHALLENGE_ACK" {
		t.Fatalf("Verdict.String = %q", got)
	}

	tbl := NewTable()
	tbl.HandshakeTimeout = 5 * time.Second

	syn := seg(ipA, 1000, ipB, 80, FlagSYN)
	syn.Seq = 1
	base := time.Unix(0, 0)
	tbl.ProcessAt(syn, base)
	if got := tbl.Lookup(syn); got != StateSynSent {
		t.Fatalf("lookup = %s", got)
	}
	if n := tbl.Sweep(base.Add(6 * time.Second)); n != 1 {
		t.Fatalf("handshake sweep removed %d, want 1", n)
	}
}

func TestTimeWaitSweep(t *testing.T) {
	tbl := NewTable()
	tbl.CloseTimeout = 5 * time.Second
	base := time.Unix(100, 0)

	syn := seg(ipA, 1000, ipB, 80, FlagSYN)
	syn.Seq = 1000
	tbl.ProcessAt(syn, base)
	synack := seg(ipB, 80, ipA, 1000, FlagSYN|FlagACK)
	synack.Seq, synack.Ack = 5000, 1001
	tbl.ProcessAt(synack, base)
	ack := seg(ipA, 1000, ipB, 80, FlagACK)
	ack.Seq, ack.Ack = 1001, 5001
	tbl.ProcessAt(ack, base)

	finA := seg(ipA, 1000, ipB, 80, FlagFIN|FlagACK)
	finA.Seq, finA.Ack = 1001, 5001
	tbl.ProcessAt(finA, base)
	ackB := seg(ipB, 80, ipA, 1000, FlagACK)
	ackB.Seq, ackB.Ack = 5001, 1002
	tbl.ProcessAt(ackB, base)
	finB := seg(ipB, 80, ipA, 1000, FlagFIN|FlagACK)
	finB.Seq, finB.Ack = 5001, 1002
	tbl.ProcessAt(finB, base)
	ackA := seg(ipA, 1000, ipB, 80, FlagACK)
	ackA.Seq, ackA.Ack = 1002, 5002
	tbl.ProcessAt(ackA, base)

	if got := tbl.Lookup(syn); got != StateTimeWait {
		t.Fatalf("state = %s, want TIME_WAIT", got)
	}
	if n := tbl.Sweep(base.Add(4 * time.Second)); n != 0 {
		t.Fatalf("early close sweep removed %d", n)
	}
	if n := tbl.Sweep(base.Add(6 * time.Second)); n != 1 {
		t.Fatalf("TIME_WAIT sweep removed %d, want 1", n)
	}
}

// TestZeroWindow verifies that with a closed receive window only zero-length
// segments at the window edge pass while data is refused.
func TestZeroWindow(t *testing.T) {
	tbl := NewTable()
	syn := seg(ipA, 1000, ipB, 80, FlagSYN)
	syn.Seq, syn.Window = 1, 0
	tbl.Process(syn)
	synack := seg(ipB, 80, ipA, 1000, FlagSYN|FlagACK)
	synack.Seq, synack.Ack, synack.Window = 100, 2, 0
	tbl.Process(synack)
	ack := seg(ipA, 1000, ipB, 80, FlagACK)
	ack.Seq, ack.Ack, ack.Window = 2, 101, 0
	tbl.Process(ack)

	// B advertises a zero window: any data from A must be dropped.
	data := seg(ipA, 1000, ipB, 80, FlagACK|FlagPSH)
	data.Seq, data.Ack, data.Payload, data.Window = 2, 101, 10, 0
	if v := tbl.Process(data); v != VerdictDrop {
		t.Fatalf("data on zero window: %s, want DROP", v)
	}

	// A pure ACK / window probe at rcvNxt is still acceptable.
	probe := seg(ipA, 1000, ipB, 80, FlagACK)
	probe.Seq, probe.Ack, probe.Window = 2, 101, 0
	if v := tbl.Process(probe); v != VerdictAccept {
		t.Fatalf("zero-length probe: %s, want ACCEPT", v)
	}

	// An RST exactly at rcvNxt still resets even with a zero window.
	rst := seg(ipA, 1000, ipB, 80, FlagRST)
	rst.Seq = 2
	if v := tbl.Process(rst); v != VerdictAccept {
		t.Fatalf("exact RST on zero window: %s, want ACCEPT", v)
	}
}

// TestReverseDirection ensures the tracker is symmetric: a connection opened
// from B to A is tracked identically with reversed addresses.
func TestReverseDirection(t *testing.T) {
	tbl := NewTable()
	syn := seg(ipB, 4000, ipA, 22, FlagSYN)
	syn.Seq = 50
	if v := tbl.Process(syn); v != VerdictAccept {
		t.Fatalf("reverse SYN: %s", v)
	}
	synack := seg(ipA, 22, ipB, 4000, FlagSYN|FlagACK)
	synack.Seq, synack.Ack = 500, 51
	if v := tbl.Process(synack); v != VerdictAccept {
		t.Fatalf("reverse SYN-ACK: %s", v)
	}
	ack := seg(ipB, 4000, ipA, 22, FlagACK)
	ack.Seq, ack.Ack = 51, 501
	if v := tbl.Process(ack); v != VerdictAccept {
		t.Fatalf("reverse ACK: %s", v)
	}
	if s := tbl.Lookup(syn); s != StateEstablished {
		t.Fatalf("reverse handshake state: %s", s)
	}

	// RST from the original responder side, exact sequence.
	rst := seg(ipA, 22, ipB, 4000, FlagRST)
	rst.Seq = 501 // B's rcvNxt tracks A's send space (ISN 500)
	if v := tbl.Process(rst); v != VerdictAccept {
		t.Fatalf("reverse exact RST: %s, want ACCEPT", v)
	}
	if tbl.Len() != 0 {
		t.Fatalf("reverse RST did not close connection")
	}
}

// TestIllegalFlags checks malformed flag combinations are always dropped and
// never create state.
func TestIllegalFlags(t *testing.T) {
	tbl := NewTable()
	cases := []uint8{FlagSYN | FlagRST, FlagSYN | FlagFIN, FlagRST | FlagFIN}
	for _, f := range cases {
		p := seg(ipA, 3000, ipB, 80, f)
		if v := tbl.Process(p); v != VerdictDrop {
			t.Fatalf("flags %s: %s, want DROP", flagName(f), v)
		}
	}
	if tbl.Len() != 0 {
		t.Fatalf("illegal segments created state")
	}
}

// TestConcurrent exercises the table under parallel load to catch races.
func TestConcurrent(t *testing.T) {
	tbl := NewTable()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint16(20000 + w*100)
			for i := 0; i < 100; i++ {
				sp := base + uint16(i)
				syn := seg(ipA, sp, ipB, 80, FlagSYN)
				syn.Seq = uint32(w*1000 + i)
				tbl.Process(syn)
				tbl.Lookup(syn)
				tbl.Len()
			}
		}(worker)
	}
	wg.Wait()
	if tbl.Len() != 800 {
		t.Fatalf("concurrent connections = %d, want 800", tbl.Len())
	}
}

// TestCloseWaitFlow covers the passive-close path where the side that
// received a FIN later closes itself (CLOSE_WAIT -> LAST_ACK -> TIME_WAIT).
func TestCloseWaitFlow(t *testing.T) {
	tbl := establish(t, 0, 0)

	// A closes first; B enters CLOSE_WAIT only after it also FINs. From the
	// middlebox perspective B observing A's FIN yields FIN_WAIT_1 on A's
	// side. Drive the passive path explicitly:
	finA := seg(ipA, 1000, ipB, 80, FlagFIN|FlagACK)
	finA.Seq, finA.Ack = 1001, 5001
	tbl.Process(finA)
	ackB := seg(ipB, 80, ipA, 1000, FlagACK)
	ackB.Seq, ackB.Ack = 5001, 1002
	tbl.Process(ackB)
	if s := tbl.Lookup(finA); s != StateFinWait2 {
		t.Fatalf("state = %s, want FIN_WAIT_2", s)
	}

	// B's FIN moves the connection toward TIME_WAIT.
	finB := seg(ipB, 80, ipA, 1000, FlagFIN|FlagACK)
	finB.Seq, finB.Ack = 5001, 1002
	if v := tbl.Process(finB); v != VerdictAccept {
		t.Fatalf("FIN B: %s", v)
	}
	ackA := seg(ipA, 1000, ipB, 80, FlagACK)
	ackA.Seq, ackA.Ack = 1002, 5002
	if v := tbl.Process(ackA); v != VerdictAccept {
		t.Fatalf("final ACK: %s", v)
	}
	if s := tbl.Lookup(finA); s != StateTimeWait {
		t.Fatalf("state = %s, want TIME_WAIT", s)
	}
}

// TestInWindowRSTKeepsConnection hammers the core security property: many
// in-window forged resets must never tear the connection down.
func TestInWindowRSTKeepsConnection(t *testing.T) {
	tbl := establish(t, 1, 1)
	// B's own SYN-ACK advertises raw window 600 with shift 1, i.e. 1200.
	// A's later Window field must not alter B's receive window.
	for off := uint32(1); off < 1200; off += 37 {
		rst := seg(ipA, 1000, ipB, 80, FlagRST)
		rst.Seq = 1001 + off
		if v := tbl.Process(rst); v != VerdictChallengeACK {
			t.Fatalf("forged in-window RST off=%d: %s, want CHALLENGE_ACK", off, v)
		}
	}
	if tbl.Len() != 1 {
		t.Fatalf("connection count = %d, forged RSTs must not close it", tbl.Len())
	}
	if s := tbl.Lookup(seg(ipA, 1000, ipB, 80, 0)); s != StateEstablished {
		t.Fatalf("state after forged RSTs: %s", s)
	}
}

// TestWindowOwnership pins down that a segment's Window field advertises the
// sender's own receive window and must never resize the receiver's window.
func TestWindowOwnership(t *testing.T) {
	tbl := establish(t, 1, 2)
	// B's SYN-ACK: raw 600 << 2 = 2400. A advertises a huge raw window in
	// its ACK and data; that belongs to A and must not enlarge B's window.
	big := seg(ipA, 1000, ipB, 80, FlagACK|FlagPSH)
	big.Seq, big.Ack, big.Payload, big.Window = 1001, 5001, 1, 65535
	if v := tbl.Process(big); v != VerdictAccept {
		t.Fatalf("data: %s", v)
	}

	rst := seg(ipA, 1000, ipB, 80, FlagRST)
	rst.Seq = 1002 + 2401 // beyond B's 2400-byte window despite A's 65535
	if v := tbl.Process(rst); v != VerdictDrop {
		t.Fatalf("RST beyond B window: %s, want DROP", v)
	}

	inside := seg(ipA, 1000, ipB, 80, FlagRST)
	inside.Seq = 1002 + 2399
	if v := tbl.Process(inside); v != VerdictChallengeACK {
		t.Fatalf("RST inside B window: %s, want CHALLENGE_ACK", v)
	}
}
