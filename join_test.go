package streamjoin

import "testing"

func testConfig() Config {
	return Config{LowerBound: -5, UpperBound: 5, AllowedLateness: 10}
}

func ev(key string, ts int64) *Event {
	return &Event{Key: key, Timestamp: ts}
}

func TestJoinWithinWindow(t *testing.T) {
	j := New(testConfig())
	j.Add(Left, ev("a", 100))
	j.Add(Right, ev("a", 104)) // diff 4 <= 5
	j.Add(Right, ev("a", 95))  // diff -5, on lower boundary

	if got := len(j.Outputs()); got != 2 {
		t.Fatalf("expected 2 outputs, got %d", got)
	}
	for _, o := range j.Outputs() {
		if o.Delta {
			t.Fatalf("on-time pair must not be flagged as delta")
		}
		if o.Left.Timestamp != 100 {
			t.Fatalf("left side of pair wrong: %v", o.Left)
		}
	}
}

func TestJoinWindowBoundaries(t *testing.T) {
	j := New(testConfig())
	j.Add(Left, ev("a", 100))
	j.Add(Right, ev("a", 105)) // exactly upper bound -> match
	j.Add(Right, ev("a", 106)) // outside -> no match
	j.Add(Right, ev("a", 94))  // outside lower bound -> no match

	if got := len(j.Outputs()); got != 1 {
		t.Fatalf("expected 1 output, got %d", got)
	}
	if j.Outputs()[0].Right.Timestamp != 105 {
		t.Fatalf("expected match with ts=105, got %d", j.Outputs()[0].Right.Timestamp)
	}
}

func TestJoinKeyedIsolation(t *testing.T) {
	j := New(testConfig())
	j.Add(Left, ev("a", 100))
	j.Add(Right, ev("b", 100)) // different key

	if got := len(j.Outputs()); got != 0 {
		t.Fatalf("expected no outputs across keys, got %d", got)
	}
}

func TestLateWithinCorrectionEmitsDelta(t *testing.T) {
	j := New(testConfig())
	j.Add(Right, ev("a", 100))
	j.AdvanceWatermark(Left, 110)

	// ts=103 is behind left watermark 110 but within AllowedLateness=10.
	j.Add(Left, ev("a", 103))

	outputs := j.Outputs()
	if len(outputs) != 1 {
		t.Fatalf("expected 1 delta output, got %d", len(outputs))
	}
	if !outputs[0].Delta {
		t.Fatalf("late-but-accepted pair must be flagged as delta")
	}
	if len(j.LateRecords()) != 0 {
		t.Fatalf("event within correction period must not be reported late")
	}
}

func TestLateBeyondCorrectionReportedSeparately(t *testing.T) {
	j := New(testConfig())
	j.Add(Right, ev("a", 100))
	j.AdvanceWatermark(Left, 121)

	// ts=100: 100+10 < 121 -> beyond correction period.
	j.Add(Left, ev("a", 100))

	if got := len(j.Outputs()); got != 0 {
		t.Fatalf("expired event must not be joined, got %d outputs", got)
	}
	late := j.LateRecords()
	if len(late) != 1 {
		t.Fatalf("expected 1 late record, got %d", len(late))
	}
	if late[0].Side != Left || late[0].Event.Timestamp != 100 {
		t.Fatalf("unexpected late record: %+v", late[0])
	}
	if j.StateSize(Left) != 0 {
		t.Fatalf("expired event must not be buffered")
	}
}

func TestWatermarkAdvancesCleanState(t *testing.T) {
	j := New(testConfig())
	j.Add(Left, ev("a", 100))
	j.Add(Left, ev("a", 200))
	j.Add(Right, ev("a", 300))

	// Right watermark to 116: left ts=100 horizon is 100+5+10=115 < 116 -> dropped.
	// left ts=200 horizon 215 >= 116 -> kept.
	j.AdvanceWatermark(Right, 116)
	if got := j.StateSize(Left); got != 1 {
		t.Fatalf("expected 1 left event retained, got %d", got)
	}

	// Left watermark to 306: right ts=300 horizon is 300-(-5)+10=315 >= 306 -> kept.
	j.AdvanceWatermark(Left, 306)
	if got := j.StateSize(Right); got != 1 {
		t.Fatalf("expected 1 right event retained, got %d", got)
	}
	// Left watermark to 316: 315 < 316 -> dropped.
	j.AdvanceWatermark(Left, 316)
	if got := j.StateSize(Right); got != 0 {
		t.Fatalf("expected right state cleaned, got %d", got)
	}
}

func TestWatermarkMonotonic(t *testing.T) {
	j := New(testConfig())
	j.Add(Left, ev("a", 100))
	j.AdvanceWatermark(Right, 50)
	j.AdvanceWatermark(Right, 30) // regression ignored
	if j.Watermark(Right) != 50 {
		t.Fatalf("watermark regressed: %d", j.Watermark(Right))
	}
	if j.StateSize(Left) != 1 {
		t.Fatalf("no cleanup expected at wm=50")
	}
}

// TestStalledSideProtectsOppositeState is the key correctness property:
// when the left watermark stalls, advancing only the right watermark must not
// delete right-side state, because future left events may still match it.
func TestStalledSideProtectsOppositeState(t *testing.T) {
	j := New(testConfig())

	j.Add(Right, ev("a", 1000))
	j.Add(Right, ev("a", 1004))

	// Right watermark races far ahead; left watermark never advances.
	j.AdvanceWatermark(Right, 5000)
	j.AdvanceWatermark(Right, 10000)

	if got := j.StateSize(Right); got != 2 {
		t.Fatalf("right state must survive while left watermark stalls, got %d", got)
	}

	// A left event arriving much later must still find the retained matches.
	// The left watermark only advanced to 1005, so ts=1002 is late but still
	// inside the correction period (1002+10 >= 1005) and joins as a delta.
	j.AdvanceWatermark(Left, 1005)
	j.Add(Left, ev("a", 1002))

	outputs := j.Outputs()
	if len(outputs) != 2 {
		t.Fatalf("expected 2 matches against retained right state, got %d", len(outputs))
	}
	for _, o := range outputs {
		if o.Left.Timestamp != 1002 {
			t.Fatalf("pair has wrong left event: %+v", o.Left)
		}
		if !o.Delta {
			t.Fatalf("matches produced by a late left event must be deltas")
		}
	}
}

// TestStalledSideStillCleansOwnOppositeState complements the stall test:
// advancing the right watermark DOES clean left state (which depends on it),
// while right state (which depends on the stalled left watermark) survives.
func TestStalledSideStillCleansOwnOppositeState(t *testing.T) {
	j := New(testConfig())

	j.Add(Left, ev("a", 100))
	j.Add(Right, ev("a", 100))

	j.AdvanceWatermark(Right, 10000) // cleans left state only

	if got := j.StateSize(Left); got != 0 {
		t.Fatalf("left state should be cleaned by right watermark, got %d", got)
	}
	if got := j.StateSize(Right); got != 1 {
		t.Fatalf("right state must not be cleaned by right watermark, got %d", got)
	}
}

func TestDeltaMatchesOnlyAgainstRetainedState(t *testing.T) {
	j := New(testConfig())
	j.Add(Right, ev("a", 100))
	j.Add(Right, ev("a", 500))

	// Push left watermark so the right event ts=100 is cleaned
	// (horizon 100+5+10=115) but ts=500 survives.
	j.AdvanceWatermark(Left, 116)
	if got := j.StateSize(Right); got != 1 {
		t.Fatalf("expected only ts=500 retained, got %d events", got)
	}

	// A late left event within the correction period would have matched the
	// cleaned event too, but only the retained one may produce a delta.
	j.AdvanceWatermark(Left, 120)
	j.Add(Left, ev("a", 100)) // late by 20 > AllowedLateness? 100+10=110 < 120 -> expired
	if len(j.LateRecords()) != 1 {
		t.Fatalf("expected expired left event reported, got %v", j.LateRecords())
	}

	j.Add(Left, ev("a", 115)) // 115+10=125 >= 120 -> accepted as delta; matches ts=500? |115-500|>5 no
	if got := len(j.Outputs()); got != 0 {
		t.Fatalf("no match expected, got %d", got)
	}

	j.Add(Left, ev("a", 497)) // on time (wm=120), matches right ts=500
	if got := len(j.Outputs()); got != 1 {
		t.Fatalf("expected 1 match with retained event, got %d", got)
	}
}
