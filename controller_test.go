package thermosim

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// at is the virtual clock: sample times are explicit minute offsets.
func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

// TestThermostatTimeline walks a timeline state table covering hysteresis
// hold, threshold equality, minimum-dwell deferral, and the guarantee that
// switches only ever happen at real sample times (no interpolation).
func TestThermostatTimeline(t *testing.T) {
	c := &Controller{
		Name: "t1", Upper: 30, Lower: 20, MinDwell: 10 * time.Minute,
	}
	var events []Event
	c.OnEvent = func(e Event) { events = append(events, e) }

	steps := []struct {
		min  int
		temp float64
		want State
		note string
	}{
		{0, 25, Off, "inside band: hold initial Off"},
		{1, 20, On, "temp == lower: switch On"},
		{5, 31, On, "above upper but dwell 4m < 10m: deferred"},
		{10, 31, On, "dwell 9m < 10m: still deferred"},
		{15, 31, Off, "dwell met: switch Off at this sample, not at expiry 11m"},
		{16, 25, Off, "inside band: hold Off"},
		{17, 19, Off, "below lower but dwell 2m < 10m: deferred"},
		{26, 19, On, "dwell 11m met: switch On"},
	}
	for _, st := range steps {
		if err := c.Process(Sample{Time: at(st.min), Temp: st.temp}); err != nil {
			t.Fatalf("min %d: unexpected error: %v", st.min, err)
		}
		if c.State() != st.want {
			t.Errorf("min %d (%s): state = %v, want %v", st.min, st.note, c.State(), st.want)
		}
	}

	wantEvents := []Event{
		{Time: at(1), Controller: "t1", From: Off, To: On, Reason: ReasonBelowLower},
		{Time: at(15), Controller: "t1", From: On, To: Off, Reason: ReasonAboveUpper},
		{Time: at(26), Controller: "t1", From: Off, To: On, Reason: ReasonBelowLower},
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("got %d events, want %d: %v", len(events), len(wantEvents), events)
	}
	for i, we := range wantEvents {
		if events[i] != we {
			t.Errorf("event %d = %+v, want %+v", i, events[i], we)
		}
	}
	// No interpolation: the Off switch happened at the sample time 15m,
	// not at the theoretical dwell expiry of 11m.
	if !events[1].Time.Equal(at(15)) {
		t.Errorf("off switch at %v, want sample time %v", events[1].Time, at(15))
	}
}

// TestEqualThresholds pins down boundary behaviour: temp == Upper switches
// Off, temp == Lower switches On, and with Upper == Lower a temperature
// exactly on the threshold resolves to Off (upper checked first).
func TestEqualThresholds(t *testing.T) {
	c := &Controller{Name: "eq", Upper: 25, Lower: 25}
	steps := []struct {
		min  int
		temp float64
		want State
	}{
		{0, 25, Off},   // on the threshold: upper wins -> Off
		{1, 24.99, On}, // below: On
		{2, 25, Off},   // back on the threshold: Off
		{3, 25.01, Off},
		{4, 24, On},
	}
	for _, st := range steps {
		if err := c.Process(Sample{Time: at(st.min), Temp: st.temp}); err != nil {
			t.Fatalf("min %d: %v", st.min, err)
		}
		if c.State() != st.want {
			t.Errorf("min %d temp %v: state = %v, want %v", st.min, st.temp, c.State(), st.want)
		}
	}

	// Distinct thresholds: exact boundary hits still switch.
	d := &Controller{Name: "bounds", Upper: 30, Lower: 20}
	if err := d.Process(Sample{Time: at(0), Temp: 20}); err != nil {
		t.Fatal(err)
	}
	if d.State() != On {
		t.Errorf("temp == lower: state = %v, want On", d.State())
	}
	if err := d.Process(Sample{Time: at(1), Temp: 30}); err != nil {
		t.Fatal(err)
	}
	if d.State() != Off {
		t.Errorf("temp == upper: state = %v, want Off", d.State())
	}
}

// TestLateSampleRejectionAndRecovery rejects out-of-order and duplicate
// samples without corrupting state, then verifies the controller recovers
// and processes subsequent in-order samples normally.
func TestLateSampleRejectionAndRecovery(t *testing.T) {
	c := &Controller{Name: "late", Upper: 30, Lower: 20}

	if err := c.Process(Sample{Time: at(10), Temp: 15}); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	if c.State() != On {
		t.Fatalf("after first sample: state = %v, want On", c.State())
	}

	for _, bad := range []Sample{
		{Time: at(10), Temp: 35}, // duplicate timestamp
		{Time: at(5), Temp: 35},  // out of order
	} {
		err := c.Process(bad)
		if !errors.Is(err, ErrLateSample) {
			t.Errorf("sample at %v: err = %v, want ErrLateSample", bad.Time, err)
		}
		if c.State() != On {
			t.Errorf("rejected sample changed state to %v", c.State())
		}
	}

	// Recovery: the next in-order sample is processed normally.
	if err := c.Process(Sample{Time: at(11), Temp: 35}); err != nil {
		t.Fatalf("recovery sample: %v", err)
	}
	if c.State() != Off {
		t.Errorf("after recovery: state = %v, want Off", c.State())
	}
}
