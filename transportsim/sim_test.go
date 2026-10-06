package main

import (
	"strings"
	"testing"
)

func mustRun(t *testing.T, script string) (*Sim, []string) {
	t.Helper()
	events, err := parseEvents(script)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sim := NewSim()
	trace, err := run(sim, events)
	if err != nil {
		t.Fatalf("run: %v\ntrace:\n%s", err, strings.Join(trace, "\n"))
	}
	return sim, trace
}

func TestDupAckDoesNotDoubleRelease(t *testing.T) {
	sim, _ := mustRun(t, `
t=0 send 2
t=5 ack 1
t=6 dupack 1
t=7 dupack 1
`)
	if sim.CreditReleased != 1 || sim.Acked != 1 {
		t.Fatalf("credit released %d times, acked %d; want exactly 1", sim.CreditReleased, sim.Acked)
	}
	if len(sim.InFlight) != 1 {
		t.Fatalf("inflight=%d, want 1 (seq 2 still outstanding)", len(sim.InFlight))
	}
	if sim.DupAcks != 2 {
		t.Fatalf("dupacks=%d, want 2", sim.DupAcks)
	}
}

func TestRetransmitKeepsLogicalInFlight(t *testing.T) {
	sim, _ := mustRun(t, `
t=0 send 2
t=10 timeout 1
`)
	if len(sim.InFlight) != 2 {
		t.Fatalf("inflight=%d after retx, want 2 (retx must not add logical packets)", len(sim.InFlight))
	}
	p := sim.Packets[1]
	if p.SentCount != 2 || !p.Retransmitted {
		t.Fatalf("seq1 sentCount=%d retransmitted=%v, want 2/true", p.SentCount, p.Retransmitted)
	}
	if sim.Sent != 2 {
		t.Fatalf("logical sent=%d, want 2", sim.Sent)
	}
}

func TestNoRttSampleOnRetransmittedAck(t *testing.T) {
	sim, trace := mustRun(t, `
t=0 send 1
t=10 ack 1
t=12 send 1
t=20 timeout 2
t=35 ack 2
`)
	if !sim.HasRtt || sim.Srtt != 10 {
		t.Fatalf("srtt=%.2f, want 10.00 from the first ACK only", sim.Srtt)
	}
	joined := strings.Join(trace, "\n")
	if !strings.Contains(joined, "RTT-SKIP") {
		t.Fatalf("expected RTT-SKIP for retransmitted seq 2:\n%s", joined)
	}
	if strings.Count(joined, "RTT-SAMPLE") != 1 {
		t.Fatalf("expected exactly 1 RTT sample:\n%s", joined)
	}
}

func TestFastRetransmitOnThreeDupAcks(t *testing.T) {
	sim, _ := mustRun(t, `
t=0 send 3
t=5 ack 1
t=6 ack 3
t=7 dupack 3
t=8 dupack 3
t=9 dupack 3
`)
	p := sim.Packets[2]
	if !p.Retransmitted || p.SentCount != 2 {
		t.Fatalf("seq2 should be fast-retransmitted, sentCount=%d", p.SentCount)
	}
	if len(sim.InFlight) != 1 {
		t.Fatalf("inflight=%d, want 1", len(sim.InFlight))
	}
}

func TestConservationAcrossScenario(t *testing.T) {
	sim, _ := mustRun(t, `
t=0  send 4
t=10 ack 1
t=12 ack 2
t=15 drop 3
t=16 ack 4
t=18 dupack 4
t=19 dupack 4
t=20 dupack 4
t=30 ack 3
t=31 send 2
t=40 drop 5
t=45 timeout 5
t=60 ack 5
t=61 ack 6
`)
	if err := sim.Check(); err != nil {
		t.Fatalf("final conservation: %v", err)
	}
	if sim.Sent != 6 || sim.Acked != 6 || len(sim.InFlight) != 0 {
		t.Fatalf("sent=%d acked=%d inflight=%d, want 6/6/0", sim.Sent, sim.Acked, len(sim.InFlight))
	}
	violations, notes := sim.Audit()
	if len(violations) != 0 || len(notes) != 0 {
		t.Fatalf("audit violations=%v notes=%v", violations, notes)
	}
}

func TestAuditDetectsUnrepairedLoss(t *testing.T) {
	sim, _ := mustRun(t, `
t=0 send 1
t=5 drop 1
`)
	_, notes := sim.Audit()
	if len(notes) != 1 || !strings.Contains(notes[0], "seq=1") {
		t.Fatalf("expected unrepaired-loss note for seq 1, got %v", notes)
	}
}

func TestReplayDeterminism(t *testing.T) {
	script := `
t=0 send 4
t=10 ack 1
t=15 drop 2
t=20 timeout 2
t=30 ack 2
t=31 dupack 2
t=40 ack 3
t=41 ack 4
`
	_, trace1 := mustRun(t, script)
	_, trace2 := mustRun(t, script)
	if strings.Join(trace1, "\n") != strings.Join(trace2, "\n") {
		t.Fatal("replaying the same events produced a different trace")
	}
}

func TestSendBlockedByWindow(t *testing.T) {
	sim, trace := mustRun(t, "t=0 send 10")
	if len(sim.InFlight) != 4 {
		t.Fatalf("inflight=%d, want 4 (initial cwnd)", len(sim.InFlight))
	}
	if !strings.Contains(strings.Join(trace, "\n"), "BLOCKED") {
		t.Fatal("expected BLOCKED action once window is full")
	}
}
