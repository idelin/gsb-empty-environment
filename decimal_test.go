package rowconv

import (
	"math"
	"testing"
)

func TestRescaleNegativeHalfEven(t *testing.T) {
	cases := []struct {
		coef     int64
		from, to int32
		want     int64
	}{
		{-25, 1, 0, -2},     // -2.5 -> -2 (even)
		{-15, 1, 0, -2},     // -1.5 -> -2 (even)
		{-35, 1, 0, -4},     // -3.5 -> -4 (even)
		{-5, 1, 0, 0},       // -0.5 -> 0 (even)
		{25, 1, 0, 2},       // 2.5 -> 2
		{35, 1, 0, 4},       // 3.5 -> 4
		{5, 1, 0, 0},        // 0.5 -> 0
		{-26, 1, 0, -3},     // -2.6 -> -3
		{-24, 1, 0, -2},     // -2.4 -> -2
		{-125, 2, 0, -1},    // -1.25 -> -1 (nearest, not a tie)
		{-135, 2, 0, -1},    // -1.35 -> -1
		{1250, 3, 2, 125},   // 1.250 -> 1.25 exact
		{12345, 3, 2, 1234}, // 12.345 -> 12.34 (tie, 1234 even)
		{12355, 3, 2, 1236}, // 12.355 -> 12.36 (tie, 1235 odd -> up)
	}
	for _, c := range cases {
		got, err := RescaleDecimal(c.coef, c.from, c.to, ModeHalfEven)
		if err != nil {
			t.Fatalf("RescaleDecimal(%d,%d,%d): unexpected error %v", c.coef, c.from, c.to, err)
		}
		if got != c.want {
			t.Errorf("RescaleDecimal(%d,%d,%d) = %d, want %d", c.coef, c.from, c.to, got, c.want)
		}
	}
}

func TestRescaleRejectInexact(t *testing.T) {
	if _, err := RescaleDecimal(25, 1, 0, ModeReject); err == nil || err.Code != CodeInexact {
		t.Fatalf("want inexact error, got %v", err)
	}
	got, err := RescaleDecimal(20, 1, 0, ModeReject)
	if err != nil || got != 2 {
		t.Fatalf("exact division must pass in reject mode: got %d, err %v", got, err)
	}
	if _, err := RescaleDecimal(-25, 1, 0, ModeReject); err == nil || err.Code != CodeInexact {
		t.Fatalf("negative inexact must also be rejected, got %v", err)
	}
}

func TestRescaleOverflow(t *testing.T) {
	if _, err := RescaleDecimal(math.MaxInt64, 0, 1, ModeHalfEven); err == nil || err.Code != CodeOverflow {
		t.Fatalf("want overflow, got %v", err)
	}
	if _, err := RescaleDecimal(math.MinInt64, 0, 1, ModeHalfEven); err == nil || err.Code != CodeOverflow {
		t.Fatalf("want overflow for MinInt64, got %v", err)
	}
	// Largest value that still fits when scaled up by 10.
	got, err := RescaleDecimal(math.MaxInt64/10, 0, 1, ModeReject)
	if err != nil || got != (math.MaxInt64/10)*10 {
		t.Fatalf("boundary scale-up failed: got %d, err %v", got, err)
	}
}

func TestRescaleExactAndIdentity(t *testing.T) {
	got, err := RescaleDecimal(123, 2, 4, ModeReject)
	if err != nil || got != 12300 {
		t.Fatalf("scale up: got %d, err %v", got, err)
	}
	got, err = RescaleDecimal(-123, 2, 2, ModeReject)
	if err != nil || got != -123 {
		t.Fatalf("identity: got %d, err %v", got, err)
	}
	got, err = RescaleDecimal(-1200, 4, 2, ModeReject)
	if err != nil || got != -12 {
		t.Fatalf("exact negative narrow: got %d, err %v", got, err)
	}
}
