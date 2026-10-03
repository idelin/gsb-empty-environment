package trust

import "testing"

// Acceptance boundary: five-step slide 100 -> 80 -> 60 -> 40 -> 20, and
// reaching the threshold of 20 triggers forced isolation.
func TestDecaySlidesFrom100To20AndQuarantines(t *testing.T) {
	s := NewScorer(DefaultConfig())
	id := NodeID("B")

	want := []float64{80, 60, 40, 20}
	for i, w := range want {
		score, quarantined := s.RecordViolation(id, ViolationPingReqRefused)
		if score != w {
			t.Fatalf("violation %d: score=%.0f want %.0f", i+1, score, w)
		}
		if i < 3 && quarantined {
			t.Fatalf("violation %d: quarantined too early at score %.0f", i+1, score)
		}
		if i == 3 && !quarantined {
			t.Fatalf("violation %d: score hit threshold %.0f but no quarantine", i+1, score)
		}
	}
	if !s.IsQuarantined(id) {
		t.Fatal("peer should be quarantined at score 20")
	}
	if got := s.ConsecutiveViolations(id); got != 4 {
		t.Fatalf("streak = %d, want 4", got)
	}
	if n := len(s.Events()); n != 1 {
		t.Fatalf("events = %d, want exactly 1 quarantine event", n)
	}
	ev := s.Events()[0]
	if ev.Node != id || ev.Score != 20 || ev.Reason != ViolationPingReqRefused {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestRewardsRecoverScoreAndResetStreak(t *testing.T) {
	s := NewScorer(DefaultConfig())
	id := NodeID("P")

	s.RecordViolation(id, ViolationPingReqRefused) // 100 -> 80
	if got := s.ConsecutiveViolations(id); got != 1 {
		t.Fatalf("streak = %d, want 1", got)
	}
	if got := s.RecordRelay(id); got != 84 {
		t.Fatalf("after relay reward score = %.0f, want 84", got)
	}
	if got := s.RecordAck(id); got != 86 {
		t.Fatalf("after ack reward score = %.0f, want 86", got)
	}
	if got := s.ConsecutiveViolations(id); got != 0 {
		t.Fatalf("streak = %d, want reset to 0", got)
	}
	for i := 0; i < 20; i++ {
		s.RecordRelay(id)
	}
	if got := s.Score(id); got != 100 {
		t.Fatalf("score = %.0f, want capped at 100", got)
	}
}

func TestForgeryPenalty(t *testing.T) {
	s := NewScorer(DefaultConfig())
	id := NodeID("F")
	if got, _ := s.RecordViolation(id, ViolationAliveForged); got != 75 {
		t.Fatalf("score = %.0f, want 75 after forgery penalty 25", got)
	}
}

func TestQuarantineIsPermanent(t *testing.T) {
	s := NewScorer(DefaultConfig())
	id := NodeID("B")
	for i := 0; i < 4; i++ {
		s.RecordViolation(id, ViolationPingReqRefused)
	}
	if got := s.RecordRelay(id); got != 20 {
		t.Fatalf("reward after quarantine changed score to %.0f, want 20", got)
	}
	if _, q := s.RecordViolation(id, ViolationPingReqRefused); q {
		t.Fatal("duplicate quarantine event emitted")
	}
	if n := len(s.Events()); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
}

func TestForceQuarantine(t *testing.T) {
	s := NewScorer(DefaultConfig())
	id := NodeID("B")
	s.ForceQuarantine(id, ViolationPingReqRefused)
	if !s.IsQuarantined(id) {
		t.Fatal("ForceQuarantine did not quarantine")
	}
	if got := s.Score(id); got != 20 {
		t.Fatalf("score = %.0f, want pinned at threshold 20", got)
	}
	s.ForceQuarantine(id, ViolationAliveForged) // idempotent
	if n := len(s.Events()); n != 1 {
		t.Fatalf("events = %d, want 1", n)
	}
}
