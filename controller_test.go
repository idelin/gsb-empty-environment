package thermosim

import "testing"

// step is one row of a timeline state table: feed a sample at time t
// with the given temperature, then expect a decision, an error, and a
// resulting state.
type step struct {
	t    int64
	temp float64
	want Decision
	err  error
	st   State
}

func runTimeline(t *testing.T, c *Controller, steps []step) {
	t.Helper()
	for i, s := range steps {
		got, err := c.Process(Sample{Time: s.t, Temp: s.temp})
		if err != s.err {
			t.Fatalf("step %d (t=%d, temp=%v): err = %v, want %v", i, s.t, s.temp, err, s.err)
		}
		if err == nil && got != s.want {
			t.Errorf("step %d (t=%d, temp=%v): decision = %v, want %v", i, s.t, s.temp, got, s.want)
		}
		if c.State() != s.st {
			t.Errorf("step %d (t=%d, temp=%v): state = %v, want %v", i, s.t, s.temp, c.State(), s.st)
		}
	}
}

// Hysteresis band: below Lower switches On, above Upper switches Off,
// inside the band the state holds.
func TestTimelineHysteresis(t *testing.T) {
	c := NewController("h", 20, 30, 0, Off)
	runTimeline(t, c, []step{
		{1, 25, Hold, nil, Off},      // mid band: hold
		{2, 31, Hold, nil, Off},      // above upper, already Off
		{3, 15, SwitchedOn, nil, On}, // below lower: on
		{4, 25, Hold, nil, On},       // mid band: hold On
		{5, 19.9, Hold, nil, On},     // still inside band (inclusive)
		{6, 30.1, SwitchedOff, nil, Off},
		{7, 30, Hold, nil, Off}, // exactly upper: hold
		{8, 20, Hold, nil, Off}, // exactly lower: hold
	})
}

// Equal thresholds: temp == threshold hits both bands, so it holds;
// strict comparison on each side still switches.
func TestTimelineEqualThresholds(t *testing.T) {
	c := NewController("eq", 25, 25, 10, Off)
	runTimeline(t, c, []step{
		{0, 24.9, SwitchedOn, nil, On},    // below: on (first switch, no dwell yet)
		{5, 25.1, Deferred, nil, On},      // above: off demanded, dwell 10 not met
		{6, 25, Hold, nil, On},            // exactly threshold: hold, deferred request dropped
		{11, 25.1, SwitchedOff, nil, Off}, // dwell met at t=10: switch
		{12, 25, Hold, nil, Off},
		{22, 24.9, SwitchedOn, nil, On},
	})
}

// Late samples (time not strictly increasing) are rejected and change
// nothing; the next valid sample is processed normally (recovery).
func TestTimelineLateSampleRejectionAndRecovery(t *testing.T) {
	c := NewController("late", 20, 30, 2, Off)
	runTimeline(t, c, []step{
		{10, 15, SwitchedOn, nil, On},
		{9, 10, Hold, ErrLateSample, On},   // rewound: rejected
		{10, 35, Hold, ErrLateSample, On},  // same time: rejected
		{11, 35, Deferred, nil, On},        // valid again, dwell 2 not met
		{12, 35, SwitchedOff, nil, Off},    // dwell met: switch
		{12, 10, Hold, ErrLateSample, Off}, // repeated time: rejected
		{13, 25, Hold, nil, Off},           // recovery: normal processing
	})
}

// Minimum dwell: a demanded switch is deferred until the dwell has
// elapsed and is re-judged from the next sample only — the simulator
// never interpolates samples in between.
func TestTimelineDwellDeferral(t *testing.T) {
	c := NewController("dwell", 20, 30, 10, Off)
	runTimeline(t, c, []step{
		{0, 15, SwitchedOn, nil, On},
		{5, 35, Deferred, nil, On},      // 5 < 10: deferred
		{8, 25, Hold, nil, On},          // back in band: deferred demand dropped
		{9, 35, Deferred, nil, On},      // 9 < 10: still deferred
		{10, 35, SwitchedOff, nil, Off}, // dwell met exactly: switch
		{15, 10, Deferred, nil, Off},    // 15-10 < 10: deferred
		{20, 10, SwitchedOn, nil, On},   // dwell met: switch
	})
}
