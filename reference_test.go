package tsxform

import (
	"math/big"
	"math/rand"
	"testing"
)

func refSpecFor(pkts []Packet, cfg Config, minGap int64) RefSpec {
	return RefSpec{
		Packets:      pkts,
		TimeSegs:     []RateSeg{{At: 0, Rate: cfg.TimeRate}},
		ByteRate:     cfg.ByteRate,
		MTU:          cfg.MTU,
		BytesPerTick: cfg.BytesPerTick,
		MinSpacing:   minGap,
	}
}

// Long randomized sequence: the independent reference timeline must
// confirm drift below one tick everywhere and no violations.
func TestReferenceDriftBoundLongSequence(t *testing.T) {
	rng := rand.New(rand.NewSource(2024))
	pkts := randomTrace(rng, 5000, 5)
	cfg := Config{MTU: 1000, BytesPerTick: 300, TimeRate: MustRatio(7, 3), ByteRate: MustRatio(5, 4)}
	out, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep := Check(refSpecFor(pkts, cfg, 0), out)
	for _, v := range rep.Violations {
		t.Error("violation:", v)
	}
	if !rep.OK() {
		t.Fatalf("reference check failed with %d violations", len(rep.Violations))
	}
	if rep.MaxDrift.Cmp(big.NewRat(1, 1)) >= 0 {
		t.Errorf("max drift %s >= 1 tick", rep.MaxDrift.RatString())
	}
	t.Logf("max drift over %d packets: %s tick", len(pkts), rep.MaxDrift.RatString())

	perTick := map[int64]int{}
	for _, rec := range out {
		perTick[rec.IdealTick]++
	}
	if len(perTick) < len(pkts)/4 {
		t.Errorf("output compressed into %d distinct ticks", len(perTick))
	}
}

// Rate change mid-stream: exact position stays continuous at the change
// point and the reference checker validates both segments.
func TestRateChangePoint(t *testing.T) {
	var pkts []Packet
	for i := 0; i <= 20; i++ {
		pkts = append(pkts, Packet{Stream: 0, Time: int64(10 * i), Size: 50})
	}
	cfg := Config{MTU: 1500, BytesPerTick: 1 << 20, TimeRate: MustRatio(1, 1), ByteRate: MustRatio(1, 1)}
	pl, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := pl.Feed(pkts[:11]...); err != nil {
		t.Fatal(err)
	}
	if err := pl.SetTimeRate(MustRatio(1, 4), 100); err != nil {
		t.Fatal(err)
	}
	if err := pl.Feed(pkts[11:]...); err != nil {
		t.Fatal(err)
	}
	pl.Finish()
	out := pl.Output()

	spec := refSpecFor(pkts, cfg, 0)
	spec.TimeSegs = []RateSeg{{At: 0, Rate: MustRatio(1, 1)}, {At: 100, Rate: MustRatio(1, 4)}}
	rep := Check(spec, out)
	for _, v := range rep.Violations {
		t.Error("violation:", v)
	}
	if !rep.OK() {
		t.Fatalf("reference check failed across rate change")
	}
	// Continuity: tick 100 maps to 100, tick 200 maps to 100 + 100/4 = 125.
	last := out[len(out)-1]
	if last.IdealTick != 125 {
		t.Errorf("last ideal tick = %d, want 125 (continuous across change point)", last.IdealTick)
	}
}

// A declared minimum spacing that the rate cannot satisfy must be
// reported, without producing hard violations.
func TestUnsatisfiableSpacingReported(t *testing.T) {
	const n = 40
	pkts := make([]Packet, n)
	for i := range pkts {
		pkts[i] = Packet{Stream: 0, Time: int64(i), Size: 10}
	}
	cfg := baseCfg()
	cfg.TimeRate = MustRatio(1, 4)
	out, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rep := Check(refSpecFor(pkts, cfg, 1), out)
	if !rep.OK() {
		t.Fatalf("unexpected violations: %v", rep.Violations)
	}
	if len(rep.Unsatisfiable) == 0 {
		t.Fatal("expected unsatisfiable spacing entries for rate 1/4 with min-gap 1")
	}
	t.Logf("reported %d unsatisfiable spacings", len(rep.Unsatisfiable))
}

// The checker must catch a corrupted output (independent reference works).
func TestCheckerDetectsCorruption(t *testing.T) {
	pkts := []Packet{
		{Stream: 0, Time: 0, Size: 100},
		{Stream: 0, Time: 10, Size: 100},
		{Stream: 0, Time: 20, Size: 100},
	}
	cfg := baseCfg()
	out, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	out[1].IdealTick += 5 // corrupt: no longer the reference floor
	rep := Check(refSpecFor(pkts, cfg, 0), out)
	if rep.OK() {
		t.Fatal("checker accepted a corrupted ideal tick")
	}
}
