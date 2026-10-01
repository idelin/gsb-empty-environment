package chronos

import (
	"errors"
	"strings"
	"testing"
)

func TestDurationArithmeticChecked(t *testing.T) {
	a, err := NewDuration(1_500_000_000, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDuration(2_000_000, PrecMillisecond)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := a.Add(b)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Nanos() != 1_502_000_000 || sum.Precision() != PrecNanosecond {
		t.Fatalf("got %d ns [%s]", sum.Nanos(), sum.Precision())
	}
	// Alignment is enforced before publication.
	if _, err := NewDuration(1_500, PrecMillisecond); err == nil {
		t.Fatal("expected alignment error")
	}
	// Supported range is enforced.
	if _, err := NewDuration(MaxDurationNanos+1, PrecNanosecond); err == nil {
		t.Fatal("expected range error")
	}
}

func TestOverflowRejectedBeforePublication(t *testing.T) {
	big, err := NewDuration(MaxDurationNanos, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := NewDuration(1, PrecNanosecond)
	if _, err := big.Add(one); err == nil {
		t.Fatal("expected overflow/range error on Add")
	}
	if _, err := big.Mul(3); err != ErrOverflow {
		t.Fatalf("expected ErrOverflow, got %v", err)
	}
	top, err := NewInstant(MaxInstantSec, 999_999_999, PrecNanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := top.Add(one); err == nil {
		t.Fatal("expected range error pushing instant past 9999-12-31")
	}
	if _, err := NewInstant(MaxInstantSec+1, 0, PrecSecond); err == nil {
		t.Fatal("expected range error")
	}
	if _, err := NewInstant(MinInstantSec-1, 0, PrecSecond); err == nil {
		t.Fatal("expected range error")
	}
}

func TestIllegalLabelsRejected(t *testing.T) {
	if _, err := NewDuration(0, Precision(9)); err == nil {
		t.Fatal("expected illegal precision label error")
	}
	if _, err := NewTZTable("Bad Zone!", "2024a", 0, nil); err == nil {
		t.Fatal("expected illegal zone label error")
	}
	if _, err := NewTZTable("X/Island", "", 0, nil); err == nil {
		t.Fatal("expected empty version label error")
	}
	if _, err := NewTZTable("X/Island", strings.Repeat("v", 33), 0, nil); err == nil {
		t.Fatal("expected overlong version label error")
	}
	if _, err := NewOffsetChoice(19*3600, 0); err == nil {
		t.Fatal("expected offset range error")
	}
	if _, err := NewOffsetChoice(3600, 9); err == nil {
		t.Fatal("expected illegal fold label error")
	}
	if _, err := NewInstant(0, 123, PrecMillisecond); err == nil {
		t.Fatal("expected precision alignment error")
	}
}

func TestNoImplicitWallInstantConversion(t *testing.T) {
	v1 := SyntheticIslandV1()
	v2 := SyntheticIslandV2()
	w, err := NewWallClock(2022, 6, 15, 12, 0, 0, 0, PrecSecond, v1, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Resolving against a different table version than the metadata
	// records must fail loudly instead of silently converting.
	if _, err := w.Instant(v2); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("expected ErrVersionMismatch, got %v", err)
	}
	// Explicit conversion with the matching table works.
	in, err := w.Instant(v1)
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Sec() + 7200; got != w.wallSec() {
		t.Fatalf("instant %+d does not match wall+offset", in.Sec())
	}
}

func TestWallFromInstantExplicit(t *testing.T) {
	v1 := SyntheticIslandV1()
	in, err := NewInstant(daysFromCivil(2021, 1, 15)*86400+10*3600, 0, PrecSecond)
	if err != nil {
		t.Fatal(err)
	}
	w, err := WallFromInstant(in, v1)
	if err != nil {
		t.Fatal(err)
	}
	if w.Hour() != 11 { // +1h base offset in winter
		t.Fatalf("got hour %d", w.Hour())
	}
	back, err := w.Instant(v1)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Equal(in) {
		t.Fatalf("round trip mismatch: %v vs %v", back, in)
	}
}
