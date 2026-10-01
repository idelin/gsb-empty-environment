package tsxform

import (
	"math/rand"
	"reflect"
	"testing"
)

func randomTrace(rng *rand.Rand, n, streams int) []Packet {
	var pkts []Packet
	last := map[int]int64{}
	for i := 0; i < n; i++ {
		s := rng.Intn(streams)
		last[s] += int64(rng.Intn(7))
		pkts = append(pkts, Packet{Stream: s, Time: last[s], Size: int64(1 + rng.Intn(1200))})
	}
	return pkts
}

func testCfg() Config {
	return Config{MTU: 500, BytesPerTick: 250, TimeRate: MustRatio(7, 3), ByteRate: MustRatio(3, 2)}
}

// safeHorizon returns the largest tick the model may advance to: the
// minimum exact scaled position any future packet could still have, which
// is the per-stream minimum over the last fed input times.
func safeHorizon(pl *Pipeline, fed []Packet) int64 {
	last := map[int]int64{}
	for _, p := range fed {
		last[p.Stream] = p.Time
	}
	first := true
	horizon := int64(0)
	for _, tm := range last {
		tick := floorRat(pl.Sc.posAt(tm))
		if first || tick < horizon {
			horizon = tick
			first = false
		}
	}
	return horizon
}

// A checkpointed and restored pipeline must produce exactly the same
// output as an uninterrupted one-shot run.
func TestCheckpointResumeEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	pkts := randomTrace(rng, 600, 4)
	cfg := testCfg()

	base, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, split := range []int{1, 7, 137, 599} {
		pl, err := NewPipeline(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := pl.Feed(pkts[:split]...); err != nil {
			t.Fatal(err)
		}
		data, err := pl.Save()
		if err != nil {
			t.Fatal(err)
		}
		restored, err := LoadPipeline(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := restored.Feed(pkts[split:]...); err != nil {
			t.Fatal(err)
		}
		restored.Finish()
		if !reflect.DeepEqual(base, restored.Output()) {
			t.Fatalf("split %d: restored output differs from one-shot", split)
		}
	}
}

// Feeding the input in fragments with pauses between chunks must give the
// same result as one-shot processing.
func TestChunkedFeedWithPausesEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	pkts := randomTrace(rng, 500, 3)
	cfg := testCfg()

	base, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}

	pl, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const chunk = 7
	for lo := 0; lo < len(pkts); lo += chunk {
		hi := lo + chunk
		if hi > len(pkts) {
			hi = len(pkts)
		}
		if err := pl.Feed(pkts[lo:hi]...); err != nil {
			t.Fatal(err)
		}
		// Pause: advance the model only up to the exact scaled position of
		// the earliest possible future arrival.
		pl.RunTo(safeHorizon(pl, pkts[:hi]))
	}
	pl.Finish()
	if !reflect.DeepEqual(base, pl.Output()) {
		t.Fatal("chunked feed with pauses differs from one-shot")
	}
}

// A pause with no input must not change anything by itself.
func TestPauseIsTransparent(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	pkts := randomTrace(rng, 200, 2)
	cfg := testCfg()

	base, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}

	pl, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Feed(pkts[:100]...); err != nil {
		t.Fatal(err)
	}
	safe := safeHorizon(pl, pkts[:100])
	pl.RunTo(safe)
	for pause := 0; pause < 5; pause++ {
		pl.RunTo(safe) // idle pauses: no input, no movement
	}
	if err := pl.Feed(pkts[100:]...); err != nil {
		t.Fatal(err)
	}
	pl.Finish()
	if !reflect.DeepEqual(base, pl.Output()) {
		t.Fatal("pauses changed the output")
	}
}

// Checkpointing mid-transmission (link busy) must also restore exactly.
func TestCheckpointWhileLinkBusy(t *testing.T) {
	pkts := []Packet{
		{Stream: 0, Time: 0, Size: 10000},
		{Stream: 1, Time: 1, Size: 300},
		{Stream: 0, Time: 2, Size: 200},
	}
	cfg := Config{MTU: 1500, BytesPerTick: 10, TimeRate: MustRatio(1, 1), ByteRate: MustRatio(1, 1)}
	base, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}

	pl, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Feed(pkts...); err != nil {
		t.Fatal(err)
	}
	pl.RunTo(3) // first fragment still transmitting
	data, err := pl.Save()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := LoadPipeline(data)
	if err != nil {
		t.Fatal(err)
	}
	restored.Finish()
	if !reflect.DeepEqual(base, restored.Output()) {
		t.Fatal("restore during transmission differs from one-shot")
	}
}
