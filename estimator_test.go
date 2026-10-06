package batsim

import (
	"testing"
)

func TestLateRevisionRecomputesEstimateOnly(t *testing.T) {
	f := mustFleet(t, 2, "100")
	est := NewEstimator()

	// Initial sensor versions.
	est.Revise(SensorReading{Version: 1, Battery: 0, Stored: rat("10")})
	est.Revise(SensorReading{Version: 1, Battery: 1, Stored: rat("10")})

	// Real execution moves the true state.
	f.ExecuteBatch([]Command{{ChargeW: rat("5")}, {DischargeW: rat("4")}}, rat("1"))
	eqRat(t, f.Batteries[0].Stored, "15", "true stored 0")
	eqRat(t, f.Batteries[1].Stored, "6", "true stored 1")
	logBefore := len(f.ActionLog)
	gridInBefore := f.Batteries[0].TotalGridIn.RatString()

	// A late revision arrives for battery 0: the sensor now says 12.
	delta := est.Revise(SensorReading{Version: 2, Battery: 0, Stored: rat("12")})
	eqRat(t, delta[0], "2", "estimate delta for battery 0")
	if len(delta) != 1 {
		t.Fatalf("delta must only touch battery 0, got %v", delta)
	}
	eqRat(t, est.Estimate()[0], "12", "revised estimate")
	eqRat(t, est.Estimate()[1], "10", "untouched estimate")

	// Execution state and history are strictly separated from estimates.
	eqRat(t, f.Batteries[0].Stored, "15", "revision must not change true state")
	if len(f.ActionLog) != logBefore {
		t.Fatalf("revision must not touch the action log")
	}
	if f.Batteries[0].TotalGridIn.RatString() != gridInBefore {
		t.Fatalf("revision must not touch energy books")
	}
}

func TestVersionReplayIsDeterministic(t *testing.T) {
	readings := []SensorReading{
		{Version: 1, Battery: 0, Stored: rat("50")},
		{Version: 1, Battery: 1, Stored: rat("30")},
		{Version: 2, Battery: 0, Stored: rat("45")}, // late correction
		{Version: 3, Battery: 0, Stored: rat("47")}, // even later correction
		{Version: 2, Battery: 1, Stored: rat("31")},
	}
	var deltas []map[int]string
	var final map[int]string
	for run := 0; run < 2; run++ {
		est := NewEstimator()
		var ds []map[int]string
		for _, r := range readings {
			d := est.Revise(r)
			m := make(map[int]string)
			for k, v := range d {
				m[k] = v.RatString()
			}
			ds = append(ds, m)
		}
		fm := make(map[int]string)
		for k, v := range est.Estimate() {
			fm[k] = v.RatString()
		}
		if run == 0 {
			deltas, final = ds, fm
			continue
		}
		if len(ds) != len(deltas) {
			t.Fatalf("replay produced different delta count")
		}
		for i := range ds {
			if len(ds[i]) != len(deltas[i]) {
				t.Fatalf("delta %d differs across replays", i)
			}
			for k, v := range ds[i] {
				if deltas[i][k] != v {
					t.Fatalf("delta %d battery %d: %s != %s", i, k, v, deltas[i][k])
				}
			}
		}
		for k, v := range fm {
			if final[k] != v {
				t.Fatalf("final estimate differs across replays")
			}
		}
	}
	// Latest version wins: battery 0 settles at 47, battery 1 at 31.
	if final[0] != "47" || final[1] != "31" {
		t.Fatalf("final estimate = %v, want {0:47 1:31}", final)
	}
}

func TestEstimateTracksDeltaAcrossSequence(t *testing.T) {
	est := NewEstimator()
	est.Revise(SensorReading{Version: 1, Battery: 0, Stored: rat("100")})
	d := est.Revise(SensorReading{Version: 2, Battery: 0, Stored: rat("97/2")})
	eqRat(t, d[0], "-103/2", "negative delta")
	d = est.Revise(SensorReading{Version: 3, Battery: 1, Stored: rat("5")})
	eqRat(t, d[1], "5", "new battery appears in delta")
	if _, ok := d[0]; ok {
		t.Fatalf("unaffected battery must not appear in delta")
	}
}
