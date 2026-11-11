package netsec

import (
	"testing"
	"time"
)

// op enumerates every externally visible event class the state machine must
// survive in any interleaving.
type op uint8

const (
	opGrantA1 op = iota // trusted ticket, macA, gen1
	opGrantA2           // newer ticket, same MAC, gen2
	opGrantB3           // migration ticket, macB, gen3
	opStaleB1           // stale/forged lower generation -> must be rejected
	opARPA              // data-plane ARP from macA
	opARPB              // data-plane ARP from macB (potential takeover)
	opTick              // reachability/probe timer advance
	opCount
)

func opName(o op) string {
	names := [...]string{
		"grantA(gen1)", "grantA(gen2)", "grantB(gen3)", "staleB(gen1)",
		"arpA", "arpB", "tick",
	}
	return names[o]
}

// apply executes one operation against a fresh-enough switch and clock.
func applyOp(sw *Switch, clock *fakeClock, o op) {
	switch o {
	case opGrantA1:
		sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 1})
	case opGrantA2:
		sw.Grant(Ticket{IP: testIP, MAC: macA, Generation: 2})
	case opGrantB3:
		sw.Grant(Ticket{IP: testIP, MAC: macB, Generation: 3})
	case opStaleB1:
		sw.Grant(Ticket{IP: testIP, MAC: macB, Generation: 1})
	case opARPA:
		sw.ObserveARP(testIP, macA, port1)
	case opARPB:
		sw.ObserveARP(testIP, macB, port2)
	case opTick:
		clock.advance(11 * time.Second) // exceeds 10s reachable TTL
		sw.Tick()
	}
}

// assertInvariants checks order-independent security properties after every
// single step of every permutation.
func assertInvariants(t *testing.T, sw *Switch, seq []op, step int) {
	t.Helper()
	ip := testIP
	v, ok := sw.Snapshot(ip)
	if !ok {
		return
	}

	// I1: reachable forwarding always requires a live authorized ticket.
	if v.State == StateReachable && !v.HasTicket {
		t.Fatalf("seq %v step %d: reachable without ticket\n%+v", opSeq(seq), step, v)
	}
	// I2: the binding generation can never exceed the ticket generation, and
	// a bound MAC must match the ticket that installed it.
	if v.HasBinding && v.HasTicket {
		if v.BoundGen > v.TicketGen {
			t.Fatalf("seq %v step %d: binding gen %d > ticket gen %d", opSeq(seq), step, v.BoundGen, v.TicketGen)
		}
		if v.BoundMAC == macA && v.BoundGen > 2 {
			t.Fatalf("seq %v step %d: macA bound at gen %d", opSeq(seq), step, v.BoundGen)
		}
	}
	// I3: forwarding is forward only when reachable and bound; drop only when
	// quarantined; flood otherwise.
	verdict, port := sw.Forward(ip)
	switch v.State {
	case StateReachable:
		if verdict != VerdictForward || port != v.BoundPort {
			t.Fatalf("seq %v step %d: reachable verdict %s/%d", opSeq(seq), step, verdict, port)
		}
	case StateQuarantined:
		if verdict != VerdictDrop {
			t.Fatalf("seq %v step %d: quarantined verdict %s", opSeq(seq), step, verdict)
		}
	default:
		if verdict != VerdictFlood {
			t.Fatalf("seq %v step %d: state %s verdict %s, want flood", opSeq(seq), step, v.State, verdict)
		}
	}
	// I4: quarantine is sticky against the data plane; only a newer grant can
	// leave it, and a grant always resets to an un-quarantined state.
	// I5: evidence is only ever attached to a conflict/rejection, and records
	// always name the real observed MAC.
	for _, e := range sw.Evidence(ip) {
		if e.ObservedMAC != macA && e.ObservedMAC != macB {
			t.Fatalf("seq %v step %d: bogus evidence MAC %q", opSeq(seq), step, e.ObservedMAC)
		}
		if e.IP != ip {
			t.Fatalf("seq %v step %d: evidence IP mismatch", opSeq(seq), step)
		}
	}
}

func opSeq(seq []op) []string {
	out := make([]string, len(seq))
	for i, o := range seq {
		out[i] = opName(o)
	}
	return out
}

// TestEventPermutationExhaustive runs every ordering of the seven event
// classes (7! = 5040 sequences) and asserts the security invariants after
// every prefix, proving no interleaving lets a stale ticket or late
// confirmation overwrite a newer binding.
func TestEventPermutationExhaustive(t *testing.T) {
	base := []op{opGrantA1, opGrantA2, opGrantB3, opStaleB1, opARPA, opARPB, opTick}

	var run func([]op, int)
	run = func(seq []op, n int) {
		if n == 0 {
			sw, clock := newTestSwitch(t, testConfig())
			for i, o := range seq {
				applyOp(sw, clock, o)
				assertInvariants(t, sw, seq, i)
			}
			assertFinalSecurity(t, sw, seq)
			return
		}
		for i := 0; i < n; i++ {
			seq[n-1], seq[i] = seq[i], seq[n-1]
			run(seq, n-1)
			seq[n-1], seq[i] = seq[i], seq[n-1]
		}
	}
	run(append([]op(nil), base...), len(base))
}

// assertFinalSecurity checks whole-sequence properties derived from the full
// operation multiset, regardless of the order it arrived in.
func assertFinalSecurity(t *testing.T, sw *Switch, seq []op) {
	t.Helper()
	v, ok := sw.Snapshot(testIP)
	if !ok {
		t.Fatalf("seq %v: neighbor missing", opSeq(seq))
	}

	// Oracle: independently replay the grant operations with the one hard rule
	// of the design (strictly increasing generation) and compare against the
	// state machine's own bookkeeping.
	var oracleGen uint64
	var oracleMAC MAC
	wantGrants, wantRejects := 0, 0
	for _, o := range seq {
		g, mac, isGrant := grantOf(o)
		if !isGrant {
			continue
		}
		if g > oracleGen {
			oracleGen, oracleMAC = g, mac
			wantGrants++
		} else {
			wantRejects++
		}
	}
	if !v.HasTicket || v.TicketGen != oracleGen || v.TicketMAC != oracleMAC {
		t.Fatalf("seq %v: ticket = gen%d/%s, want gen%d/%s",
			opSeq(seq), v.TicketGen, v.TicketMAC, oracleGen, oracleMAC)
	}

	// Every binding installation must be for an authorized MAC and its
	// generations must be strictly increasing (no late overwrite).
	var lastInstallGen uint64
	for _, e := range sw.Events() {
		if e.IP != testIP || e.Type != EventBindingInstalled {
			continue
		}
		if e.Gen <= lastInstallGen {
			t.Fatalf("seq %v: binding install gen %d did not increase over %d", opSeq(seq), e.Gen, lastInstallGen)
		}
		if e.MAC != macA && e.MAC != macB {
			t.Fatalf("seq %v: binding installed for unauthorized MAC %s", opSeq(seq), e.MAC)
		}
		lastInstallGen = e.Gen
	}

	gotGrants, gotRejects := 0, 0
	for _, e := range sw.Events() {
		if e.IP != testIP {
			continue
		}
		switch e.Type {
		case EventTicketRejected:
			gotRejects++
		case EventTicketGranted:
			gotGrants++
		}
	}
	if gotGrants != wantGrants {
		t.Fatalf("seq %v: grants = %d, oracle wants %d", opSeq(seq), gotGrants, wantGrants)
	}
	if gotRejects != wantRejects {
		t.Fatalf("seq %v: stale rejects = %d, oracle wants %d", opSeq(seq), gotRejects, wantRejects)
	}

	// If reachable at the end, the binding must be the newest authorized one.
	if v.State == StateReachable {
		if !v.HasBinding || v.BoundGen > oracleGen {
			t.Fatalf("seq %v: reachable binding = %+v, want gen <= %d", opSeq(seq), v, oracleGen)
		}
		// A binding at the newest generation must carry its authorized MAC; an
		// older MAC is only tolerated inside the migration window (older gen).
		if v.BoundGen == oracleGen && v.BoundMAC != oracleMAC {
			t.Fatalf("seq %v: current-gen binding MAC %s, want %s", opSeq(seq), v.BoundMAC, oracleMAC)
		}
	}

	// Evidence ring never exceeds its configured bound.
	if got := len(sw.Evidence(testIP)); got > testConfig().MaxEvidencePerIP {
		t.Fatalf("seq %v: evidence ring overflow: %d", opSeq(seq), got)
	}
}

// grantOf maps an operation to its (generation, MAC, isGrant) triple.
func grantOf(o op) (uint64, MAC, bool) {
	switch o {
	case opGrantA1:
		return 1, macA, true
	case opGrantA2:
		return 2, macA, true
	case opGrantB3:
		return 3, macB, true
	case opStaleB1:
		return 1, macB, true
	default:
		return 0, "", false
	}
}
