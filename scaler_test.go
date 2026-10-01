package tsxform

import (
	"errors"
	"testing"
)

func baseCfg() Config {
	return Config{MTU: 1500, BytesPerTick: 1 << 20, TimeRate: MustRatio(1, 1), ByteRate: MustRatio(1, 1)}
}

// Rate 1 (time and bytes) must reproduce the original trace exactly.
func TestIdentityRatePreservesTrace(t *testing.T) {
	var pkts []Packet
	for i := 0; i < 50; i++ {
		pkts = append(pkts, Packet{Stream: i % 3, Time: int64(10 * i), Size: int64(100 + 13*i)})
	}
	out, err := Transform(pkts, baseCfg())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(pkts) {
		t.Fatalf("got %d records, want %d", len(out), len(pkts))
	}
	for i, rec := range out {
		if rec.IdealTick != pkts[i].Time || rec.Depart != pkts[i].Time {
			t.Errorf("record %d: ideal=%d depart=%d, want %d", i, rec.IdealTick, rec.Depart, pkts[i].Time)
		}
		if rec.Size != pkts[i].Size || rec.FragCount != 1 {
			t.Errorf("record %d: size=%d frags=%d, want %d/1", i, rec.Size, rec.FragCount, pkts[i].Size)
		}
		if rec.Stream != pkts[i].Stream {
			t.Errorf("record %d: stream %d, want %d", i, rec.Stream, pkts[i].Stream)
		}
	}
}

// Zero rate must be rejected everywhere.
func TestZeroRateRejected(t *testing.T) {
	if _, err := NewRatio(0, 7); !errors.Is(err, ErrZeroRate) {
		t.Errorf("NewRatio(0,7): got %v, want ErrZeroRate", err)
	}
	if _, err := ParseRatio("0/5"); !errors.Is(err, ErrZeroRate) {
		t.Errorf("ParseRatio(0/5): got %v, want ErrZeroRate", err)
	}
	if _, err := ParseRatio("0"); !errors.Is(err, ErrZeroRate) {
		t.Errorf("ParseRatio(0): got %v, want ErrZeroRate", err)
	}
	if _, err := NewRatio(3, 0); err == nil {
		t.Error("NewRatio(3,0): want error for zero denominator")
	}
	if _, err := NewRatio(-2, 3); err == nil {
		t.Error("NewRatio(-2,3): want error for negative rate")
	}
}

// Many small intervals scaled by 1/3: total duration error stays below one
// tick and packets spread over distinct ticks instead of piling into one.
func TestSmallIntervalsBoundedDrift(t *testing.T) {
	const n = 3000
	pkts := make([]Packet, n)
	for i := range pkts {
		pkts[i] = Packet{Stream: 0, Time: int64(i), Size: 10}
	}
	cfg := baseCfg()
	cfg.TimeRate = MustRatio(1, 3)
	out, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	last := out[len(out)-1].IdealTick
	if last != 999 { // floor((3000-1)/3)
		t.Errorf("last ideal tick = %d, want 999 (drift below 1 tick)", last)
	}
	perTick := map[int64]int{}
	for _, rec := range out {
		perTick[rec.IdealTick]++
	}
	if len(perTick) != 1000 {
		t.Errorf("packets spread over %d distinct ticks, want 1000", len(perTick))
	}
	for tick, c := range perTick {
		if c > 3 {
			t.Errorf("tick %d holds %d packets, want at most 3", tick, c)
		}
	}
}

// Byte scaling with residual compensation: alternating 1/2-rounded sizes
// must cancel out so the cumulative byte count is exact.
func TestByteResidualCompensation(t *testing.T) {
	const n = 2000
	pkts := make([]Packet, n)
	for i := range pkts {
		pkts[i] = Packet{Stream: 0, Time: int64(10 * i), Size: 3}
	}
	cfg := baseCfg()
	cfg.ByteRate = MustRatio(1, 2)
	out, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for i, rec := range out {
		want := int64(1 + i%2) // floor(3k/2) increments: 1,2,1,2,...
		if rec.Size != want {
			t.Fatalf("record %d: size %d, want %d", i, rec.Size, want)
		}
		total += rec.Size
	}
	if want := int64(3 * n / 2); total != want {
		t.Errorf("total scaled bytes = %d, want exact %d", total, want)
	}
}

// A packet scaled beyond the MTU is fragmented; fragments keep in-stream
// causality and serialize on the link, delaying the next packet.
func TestFragmentationAndCausality(t *testing.T) {
	cfg := Config{MTU: 1500, BytesPerTick: 1000, TimeRate: MustRatio(1, 1), ByteRate: MustRatio(1, 1)}
	pkts := []Packet{
		{Stream: 0, Time: 0, Size: 4000},
		{Stream: 0, Time: 1, Size: 100},
	}
	out, err := Transform(pkts, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("got %d records, want 4", len(out))
	}
	wantSizes := []int64{1500, 1500, 1000, 100}
	wantDepart := []int64{0, 2, 4, 5}
	for i, rec := range out {
		if rec.Size != wantSizes[i] {
			t.Errorf("record %d: size %d, want %d", i, rec.Size, wantSizes[i])
		}
		if rec.Depart != wantDepart[i] {
			t.Errorf("record %d: depart %d, want %d", i, rec.Depart, wantDepart[i])
		}
		if i < 3 && (rec.FragCount != 3 || rec.FragIndex != i) {
			t.Errorf("record %d: frag %d/%d, want %d/3", i, rec.FragIndex, rec.FragCount, i)
		}
	}
	if out[3].Depart < out[2].Done {
		t.Errorf("next packet departs at %d before previous fragments finish at %d", out[3].Depart, out[2].Done)
	}
}

// Byte scaling up beyond the MTU must also fragment.
func TestByteScaleUpFragments(t *testing.T) {
	cfg := Config{MTU: 1000, BytesPerTick: 1 << 20, TimeRate: MustRatio(1, 1), ByteRate: MustRatio(3, 1)}
	out, err := Transform([]Packet{{Stream: 0, Time: 0, Size: 1000}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d fragments, want 3", len(out))
	}
	for _, rec := range out {
		if rec.Size != 1000 || rec.IdealTick != 0 {
			t.Errorf("frag %d: size %d ideal %d, want 1000@0", rec.FragIndex, rec.Size, rec.IdealTick)
		}
	}
}

// Invalid inputs are rejected.
func TestEmitRejectsBadInput(t *testing.T) {
	sc, err := NewScaler(1500, MustRatio(1, 1), MustRatio(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Emit(Packet{Stream: 0, Time: 0, Size: 0}); err == nil {
		t.Error("zero-size packet accepted")
	}
	if _, err := sc.Emit(Packet{Stream: 0, Time: -1, Size: 10}); err == nil {
		t.Error("negative tick accepted")
	}
	if _, err := sc.Emit(Packet{Stream: 0, Time: 5, Size: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Emit(Packet{Stream: 0, Time: 4, Size: 10}); err == nil {
		t.Error("backwards input time accepted")
	}
}

func TestParseRatio(t *testing.T) {
	r, err := ParseRatio(" 6/4 ")
	if err != nil {
		t.Fatal(err)
	}
	if r != (Ratio{Num: 3, Den: 2}) {
		t.Errorf("ParseRatio(6/4) = %v, want 3/2", r)
	}
	r, err = ParseRatio("5")
	if err != nil || r != (Ratio{Num: 5, Den: 1}) {
		t.Errorf("ParseRatio(5) = %v, %v", r, err)
	}
	if _, err := ParseRatio("a/b"); err == nil {
		t.Error("ParseRatio(a/b) accepted")
	}
}
