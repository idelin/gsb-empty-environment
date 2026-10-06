package main

import (
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{InitCwnd: 2, MinRTO: 100, DefaultRTO: 500, MaxRTO: 10000}
}

func run(t *testing.T, log string) *Sim {
	t.Helper()
	s, err := RunLog(strings.NewReader(log), testConfig())
	if err != nil {
		t.Fatalf("RunLog: %v", err)
	}
	return s
}

func TestDupAckReleasesNoCredit(t *testing.T) {
	s := run(t, `
send 2
tick 50
ack 1
ack 1
ack 1
`)
	if s.DupAcks != 2 {
		t.Fatalf("DupAcks = %d, want 2", s.DupAcks)
	}
	// Only the first ACK may open the window: cwnd = 2 + 1.
	if s.Cwnd != 3 {
		t.Fatalf("Cwnd = %d, want 3 (dup ACKs must not grow the window)", s.Cwnd)
	}
	if s.InFlight() != 1 {
		t.Fatalf("InFlight = %d, want 1 (dup ACKs must not release credit)", s.InFlight())
	}
	if len(s.Violations()) != 0 {
		t.Fatalf("violations: %v", s.Violations())
	}
}

func TestRetransmitKeepsInFlightConstant(t *testing.T) {
	s := run(t, `
send 2
drop 1
timeout
`)
	if s.InFlight() != 2 {
		t.Fatalf("InFlight = %d, want 2: retransmission must not add logical in-flight packets", s.InFlight())
	}
	p := s.Packets[1]
	if p.Transmissions != 2 || !p.Retransmitted {
		t.Fatalf("seq1: transmissions=%d retransmitted=%v, want 2/true", p.Transmissions, p.Retransmitted)
	}
	if s.Cwnd != 1 {
		t.Fatalf("Cwnd = %d, want 1 after multiplicative decrease", s.Cwnd)
	}
	if len(s.Violations()) != 0 {
		t.Fatalf("violations: %v", s.Violations())
	}
}

func TestKarnRuleSkipsRTTSample(t *testing.T) {
	s := run(t, `
send 1
tick 100
ack 1
send 1
drop 2
timeout
tick 300
ack 2
`)
	// After the first clean sample (rtt=100), srtt=100, rto=200.
	if s.SRTT != 100 {
		t.Fatalf("SRTT = %d, want 100: ACK of retransmitted seq2 must not be sampled", s.SRTT)
	}
	if len(s.Violations()) != 0 {
		t.Fatalf("violations: %v", s.Violations())
	}
}

func TestTimerTimeoutAndBackoff(t *testing.T) {
	s := run(t, `
send 1
tick 500
`)
	// Default RTO is 500: the tick must fire a timeout and retransmit.
	if s.Retransmits != 1 {
		t.Fatalf("Retransmits = %d, want 1", s.Retransmits)
	}
	if s.RTO != 1000 {
		t.Fatalf("RTO = %d, want 1000 after backoff", s.RTO)
	}
	if len(s.Violations()) != 0 {
		t.Fatalf("violations: %v", s.Violations())
	}
}

func TestWindowFullBlocksSender(t *testing.T) {
	s := run(t, `send 5`)
	if s.sent() != 2 || s.InFlight() != 2 {
		t.Fatalf("sent=%d inflight=%d, want 2/2 (cwnd=2)", s.sent(), s.InFlight())
	}
}

func TestMixedLogConservationAndLossAudit(t *testing.T) {
	log := `
send 4
tick 50
ack 1
ack 1
send 4
drop 3
tick 30
ack 2
ack 4
tick 200
tick 50
ack 3
send 2
tick 100
ack 5
ack 6
`
	s := run(t, log)
	if len(s.Violations()) != 0 {
		t.Fatalf("violations: %v", s.Violations())
	}
	if s.acked()+s.InFlight() != s.sent() {
		t.Fatalf("conservation: acked=%d inflight=%d sent=%d", s.acked(), s.InFlight(), s.sent())
	}
	// seq3 was dropped and must have been recovered by retransmission.
	p := s.Packets[3]
	if !p.Acked || p.Transmissions < 2 {
		t.Fatalf("seq3 not recovered: acked=%v tx=%d", p.Acked, p.Transmissions)
	}
	audit := strings.Join(s.LossAudit(), "\n")
	if !strings.Contains(audit, "seq=3") || !strings.Contains(audit, "OK") {
		t.Fatalf("loss audit missing recovery entry: %s", audit)
	}
	// Determinism: replaying the log yields the identical trace.
	again := run(t, log)
	a, b := s.Trace(), again.Trace()
	if len(a) != len(b) {
		t.Fatalf("replay trace length %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("replay diverged at line %d:\n%s\n%s", i, a[i], b[i])
		}
	}
}

func TestAckForUnsentSeqIsViolation(t *testing.T) {
	s := run(t, `ack 9`)
	if len(s.Violations()) == 0 {
		t.Fatal("expected a violation for ACK of unsent seq")
	}
}
