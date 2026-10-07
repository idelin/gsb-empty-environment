package counter

import (
	"reflect"
	"testing"
)

// bruteSameEpoch enumerates every physically possible increment count
// for a same-epoch pair: d such that (prev+d) mod 2^bits == curr and
// d <= maxRate*dt.
func bruteSameEpoch(bits uint, prev, curr uint64, maxRate, dt float64) []uint64 {
	m := modulus(bits)
	var out []uint64
	for d := uint64(0); float64(d) <= maxRate*dt; d++ {
		if (prev+d)%m == curr%m {
			out = append(out, d)
		}
	}
	return out
}

// bruteRestart enumerates every physically possible increment count
// after a reset: d such that d mod 2^bits == curr (counter restarted
// from 0) and d <= maxRate*dt.
func bruteRestart(bits uint, curr uint64, maxRate, dt float64) []uint64 {
	m := modulus(bits)
	var out []uint64
	for d := uint64(0); float64(d) <= maxRate*dt; d++ {
		if d%m == curr%m {
			out = append(out, d)
		}
	}
	return out
}

// TestSameEpochExhaustive cross-checks Deltas against brute-force
// enumeration for every same-epoch pair of 3-bit counter values over a
// grid of rates and elapsed times, including limits that allow many
// wraps.
func TestSameEpochExhaustive(t *testing.T) {
	const bits = 3
	for _, maxRate := range []float64{0, 0.5, 1, 2.5, 8} {
		for _, dt := range []float64{0, 0.5, 1, 3, 10} {
			cfg := Config{Bits: bits, MaxRate: maxRate}
			for prev := uint64(0); prev < 8; prev++ {
				for curr := uint64(0); curr < 8; curr++ {
					got, err := cfg.Deltas(
						Sample{Value: prev, Epoch: 1, Time: 100},
						Sample{Value: curr, Epoch: 1, Time: 100 + dt},
					)
					if err != nil {
						t.Fatalf("Deltas: %v", err)
					}
					want := bruteSameEpoch(bits, prev, curr, maxRate, dt)
					if len(got) == 0 && len(want) == 0 {
						continue
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("rate=%v dt=%v prev=%d curr=%d: got %v, want %v",
							maxRate, dt, prev, curr, got, want)
					}
				}
			}
		}
	}
}

// TestRestartExhaustive cross-checks Deltas against brute-force
// enumeration for every post-restart reading of a 3-bit counter.
func TestRestartExhaustive(t *testing.T) {
	const bits = 3
	for _, maxRate := range []float64{0, 0.5, 1, 2.5, 8} {
		for _, dt := range []float64{0, 0.5, 1, 3, 10} {
			cfg := Config{Bits: bits, MaxRate: maxRate}
			for curr := uint64(0); curr < 8; curr++ {
				got, err := cfg.Deltas(
					Sample{Value: 5, Epoch: 1, Time: 100},
					Sample{Value: curr, Epoch: 2, Time: 100 + dt},
				)
				if err != nil {
					t.Fatalf("Deltas: %v", err)
				}
				want := bruteRestart(bits, curr, maxRate, dt)
				if len(got) == 0 && len(want) == 0 {
					continue
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("rate=%v dt=%v curr=%d: got %v, want %v",
						maxRate, dt, curr, got, want)
				}
			}
		}
	}
}

// TestMultipleWraps pins the case the library exists for: more than one
// wrap between adjacent samples.
func TestMultipleWraps(t *testing.T) {
	cfg := Config{Bits: 3, MaxRate: 2}
	got, err := cfg.Deltas(
		Sample{Value: 6, Epoch: 7, Time: 0},
		Sample{Value: 1, Epoch: 7, Time: 10},
	)
	if err != nil {
		t.Fatalf("Deltas: %v", err)
	}
	// Residue is (1-6) mod 8 = 3; limit is 20, so 3, 11, 19 all fit.
	want := []uint64{3, 11, 19}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestUnexplainableJump covers jumps no increment count can explain
// under the rate bound, in both the same-epoch and restart cases.
func TestUnexplainableJump(t *testing.T) {
	cfg := Config{Bits: 3, MaxRate: 3}

	same, err := cfg.Deltas(
		Sample{Value: 0, Epoch: 1, Time: 0},
		Sample{Value: 7, Epoch: 1, Time: 1}, // needs >= 7/s, bound is 3
	)
	if err != nil {
		t.Fatalf("Deltas: %v", err)
	}
	if len(same) != 0 {
		t.Errorf("same-epoch: got %v, want empty", same)
	}

	restart, err := cfg.Deltas(
		Sample{Value: 0, Epoch: 1, Time: 0},
		Sample{Value: 7, Epoch: 2, Time: 1}, // needs >= 7/s after reset
	)
	if err != nil {
		t.Fatalf("Deltas: %v", err)
	}
	if len(restart) != 0 {
		t.Errorf("restart: got %v, want empty", restart)
	}
}

// TestExplain checks the restart flag and delta list together.
func TestExplain(t *testing.T) {
	cfg := Config{Bits: 4, MaxRate: 100}

	deltas, restart, err := cfg.Explain(
		Sample{Value: 3, Epoch: 1, Time: 0},
		Sample{Value: 3, Epoch: 2, Time: 1},
	)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if !restart {
		t.Error("expected restart to be reported")
	}
	want := []uint64{3, 19, 35, 51, 67, 83, 99}
	if !reflect.DeepEqual(deltas, want) {
		t.Errorf("got %v, want %v", deltas, want)
	}
}

// TestValidation exercises bad configurations and samples.
func TestValidation(t *testing.T) {
	good := Sample{Value: 1, Epoch: 1, Time: 0}
	if _, err := (Config{Bits: 0, MaxRate: 1}).Deltas(good, good); err == nil {
		t.Error("Bits=0: expected error")
	}
	if _, err := (Config{Bits: 65, MaxRate: 1}).Deltas(good, good); err == nil {
		t.Error("Bits=65: expected error")
	}
	if _, err := (Config{Bits: 3, MaxRate: 1}).Deltas(good, Sample{Value: 8, Epoch: 1, Time: 1}); err == nil {
		t.Error("value 8 in 3 bits: expected error")
	}
	if _, err := (Config{Bits: 3, MaxRate: 1}).Deltas(
		Sample{Value: 1, Epoch: 1, Time: 2},
		Sample{Value: 1, Epoch: 1, Time: 1},
	); err == nil {
		t.Error("backwards time: expected error")
	}
}

// TestZeroElapsed ensures a zero-width interval only admits the zero
// delta (same value, same epoch).
func TestZeroElapsed(t *testing.T) {
	cfg := Config{Bits: 3, MaxRate: 10}
	got, err := cfg.Deltas(
		Sample{Value: 5, Epoch: 1, Time: 4},
		Sample{Value: 5, Epoch: 1, Time: 4},
	)
	if err != nil {
		t.Fatalf("Deltas: %v", err)
	}
	if !reflect.DeepEqual(got, []uint64{0}) {
		t.Errorf("got %v, want [0]", got)
	}
}
