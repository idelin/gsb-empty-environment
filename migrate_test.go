package chronos

import "testing"

func TestPrecisionScaleLossless(t *testing.T) {
	in, err := NewInstant(1_700_000_000, 500_000_000, PrecMillisecond)
	if err != nil {
		t.Fatal(err)
	}
	up, st, err := ScaleInstant(in, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if st.Lossy {
		t.Fatal("scaling up must be lossless")
	}
	down, st2, err := ScaleInstant(up, PrecMillisecond)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Lossy || !down.Equal(in) {
		t.Fatalf("round trip not lossless: %v lossy=%v", down, st2.Lossy)
	}
	ev := Evidence{Steps: []Step{st, st2}}
	if !ev.Reversible() {
		t.Fatal("evidence must report the migration reversible")
	}
	back, iv, err := ReverseInstant(ev.Steps, down)
	if err != nil {
		t.Fatal(err)
	}
	if iv != nil || len(back) != 1 || !back[0].Equal(in) {
		t.Fatalf("reverse = %v, iv=%v", back, iv)
	}
}

func TestPrecisionScaleLossyInterval(t *testing.T) {
	in, err := NewInstant(1_700_000_000, 123_456_789, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	out, st, err := ScaleInstant(in, PrecMillisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Lossy {
		t.Fatal("expected lossy step")
	}
	if out.Nanos() != 123_000_000 {
		t.Fatalf("got %d ns", out.Nanos())
	}
	ev := Evidence{Steps: []Step{st}}
	if ev.Reversible() {
		t.Fatal("lossy migration must not be reported reversible")
	}
	cands, iv, err := ReverseInstant(ev.Steps, out)
	if err != nil {
		t.Fatal(err)
	}
	if cands != nil || iv == nil {
		t.Fatalf("expected interval, got cands=%v iv=%v", cands, iv)
	}
	if iv.Lo.Nanos() != 123_000_000 || iv.Hi.Nanos() != 124_000_000 {
		t.Fatalf("interval [%d, %d)", iv.Lo.Nanos(), iv.Hi.Nanos())
	}
	if iv.Lo.Precision() != PrecNanosecond || iv.Hi.Precision() != PrecNanosecond {
		t.Fatal("interval must be expressed at the original precision")
	}
}

func TestNanosNotTruncatedThroughMillis(t *testing.T) {
	in, err := NewInstant(1_700_000_000, 123_456_789, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	// Direct ns -> us scaling must keep microsecond digits; it must not
	// pass through a millisecond truncation (which would give 123000000).
	us, st, err := ScaleInstant(in, PrecMicrosecond)
	if err != nil {
		t.Fatal(err)
	}
	if us.Nanos() != 123_456_000 {
		t.Fatalf("ns->us gave %d ns, want 123456000", us.Nanos())
	}
	if !st.Lossy {
		t.Fatal("ns->us discards 789 ns and must be recorded lossy")
	}
	// A microsecond-precision value survives us -> ns -> us untouched.
	fine, err := NewInstant(1_700_000_000, 123_456_000, PrecMicrosecond)
	if err != nil {
		t.Fatal(err)
	}
	asNs, st1, err := ScaleInstant(fine, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	back, st2, err := ScaleInstant(asNs, PrecMicrosecond)
	if err != nil {
		t.Fatal(err)
	}
	if st1.Lossy || st2.Lossy || !back.Equal(fine) {
		t.Fatalf("us->ns->us must be lossless, got %v", back)
	}
}

func TestTZRebindPreservesSelectedOffset(t *testing.T) {
	reg := SyntheticRegistry()
	v1, _ := reg.Lookup("X/Island", "2024a")
	v2, _ := reg.Lookup("X/Island", "2024b")
	// Ambiguous wall time that stays ambiguous in the new version.
	w, err := NewWallClock(2021, 10, 31, 2, 30, 0, 0, PrecSecond, v1, 1) // fold 1 => +1h
	if err != nil {
		t.Fatal(err)
	}
	out, st, err := RebindWall(w, v2)
	if err != nil {
		t.Fatal(err)
	}
	if st.Lossy {
		t.Fatalf("rebind should be lossless: %s", st.Detail)
	}
	if out.Choice().OffsetSec != 3600 {
		t.Fatalf("selected offset not preserved: %+d", out.Choice().OffsetSec)
	}
	if out.TZVersion() != "2024b" {
		t.Fatalf("version metadata not updated: %s", out.TZVersion())
	}
}

func TestTZRebindOffsetChangeIsLossy(t *testing.T) {
	reg := SyntheticRegistry()
	v1, _ := reg.Lookup("X/Island", "2024a")
	v2, _ := reg.Lookup("X/Island", "2024b")
	// Summer 2022: DST in v1 (+2h), abolished in v2 (+1h).
	w, err := NewWallClock(2022, 6, 15, 12, 0, 0, 0, PrecSecond, v1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if w.Choice().OffsetSec != 7200 {
		t.Fatalf("precondition: offset %+d", w.Choice().OffsetSec)
	}
	out, st, err := RebindWall(w, v2)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Lossy {
		t.Fatal("offset change must be recorded as lossy")
	}
	if st.OldChoice == nil || st.OldChoice.OffsetSec != 7200 {
		t.Fatalf("old choice not recorded: %+v", st.OldChoice)
	}
	if out.Choice().OffsetSec != 3600 {
		t.Fatalf("rebound to %+d", out.Choice().OffsetSec)
	}
	// Civil fields must be identical: migration never reinterprets the
	// wall value as an instant.
	if !out.civilEqual(w) {
		t.Fatal("civil fields changed during tz rebind")
	}
	// Reversal returns every candidate under the old version; the
	// original selection must be among them.
	back, iv, err := ReverseWall([]Step{st}, out, reg)
	if err != nil {
		t.Fatal(err)
	}
	if iv != nil {
		t.Fatal("unexpected precision interval")
	}
	found := false
	for _, c := range back {
		if c.TZVersion() != "2024a" {
			t.Fatalf("reversed candidate has version %s", c.TZVersion())
		}
		if c.Choice().OffsetSec == 7200 {
			found = true
		}
	}
	if !found {
		t.Fatalf("original offset not among reversed candidates: %v", back)
	}
}

func TestMultipleFallbacksRoundTrip(t *testing.T) {
	reg := SyntheticRegistry()
	v1, _ := reg.Lookup("X/Island", "2024a")
	v2, _ := reg.Lookup("X/Island", "2024b")
	fallbacks := []struct {
		y     int32
		mo, d uint8
	}{
		{2020, 10, 25}, {2021, 10, 31}, {2022, 10, 30},
	}
	for _, fb := range fallbacks {
		for fold := uint8(0); fold <= 1; fold++ {
			w, err := NewWallClock(fb.y, fb.mo, fb.d, 2, 30, 0, 0, PrecSecond, v1, fold)
			if err != nil {
				t.Fatalf("%v fold %d: %v", fb, fold, err)
			}
			mid, st1, err := RebindWall(w, v2)
			if err != nil {
				t.Fatalf("%v fold %d: %v", fb, fold, err)
			}
			back, st2, err := RebindWall(mid, v1)
			if err != nil {
				t.Fatalf("%v fold %d: %v", fb, fold, err)
			}
			preserved := fb.y < 2022 || fold == 1
			if preserved {
				if st1.Lossy || st2.Lossy {
					t.Fatalf("%v fold %d: expected lossless round trip", fb, fold)
				}
				if !back.Equal(w) {
					t.Fatalf("%v fold %d: round trip mismatch %v vs %v", fb, fold, back, w)
				}
			} else {
				// 2022 fold 0 (+2h) does not exist in v2: the migration
				// must be recorded as lossy, never silently exact.
				if !st1.Lossy {
					t.Fatalf("%v fold %d: expected lossy forward step", fb, fold)
				}
				ev := Evidence{Steps: []Step{st1, st2}}
				if ev.Reversible() {
					t.Fatalf("%v fold %d: must not be reported reversible", fb, fold)
				}
				// Reversal of the forward step must return all v1
				// candidates, including the original +2h selection.
				cands, _, err := ReverseWall([]Step{st1}, mid, reg)
				if err != nil {
					t.Fatal(err)
				}
				if len(cands) != 2 {
					t.Fatalf("%v fold %d: expected 2 reversed candidates, got %d", fb, fold, len(cands))
				}
			}
		}
	}
}

func TestReverseWallLossyPrecisionInterval(t *testing.T) {
	v1 := SyntheticIslandV1()
	w, err := NewWallClock(2021, 5, 1, 10, 0, 0, 123_456_789, PrecNanosecond, v1, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, st, err := ScaleWall(w, PrecSecond)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Lossy || out.Nanos() != 0 {
		t.Fatalf("lossy=%v nanos=%d", st.Lossy, out.Nanos())
	}
	cands, iv, err := ReverseWall([]Step{st}, out, SyntheticRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if iv == nil {
		t.Fatalf("expected precision interval, got %v", cands)
	}
	if iv.Lo.Nanos() != 0 || iv.Hi.Nanos() != 0 {
		t.Fatalf("interval nanos [%d, %d)", iv.Lo.Nanos(), iv.Hi.Nanos())
	}
	// [10:00:00, 10:00:01) at nanosecond precision.
	if iv.Hi.Second() != iv.Lo.Second()+1 {
		t.Fatalf("interval [%v, %v)", iv.Lo, iv.Hi)
	}
	if iv.Lo.Precision() != PrecNanosecond {
		t.Fatal("interval must be at original precision")
	}
}
