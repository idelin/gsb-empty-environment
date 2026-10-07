package timemap

import (
	"math/big"
	"testing"
)

// sampleMapping builds x: 0 -> 0, 10 -> 2 (slope 1/5), 20 -> 12 (slope 1).
func sampleMapping(t *testing.T) *Mapping {
	t.Helper()
	m, err := NewMapping([]Point{
		{X: RatFromInt(0), Y: RatFromInt(0)},
		{X: RatFromInt(10), Y: RatFromInt(2)},
		{X: RatFromInt(20), Y: RatFromInt(12)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestForwardBackward(t *testing.T) {
	m := sampleMapping(t)
	cases := map[string]string{
		"0":  "0",
		"5":  "1",
		"10": "2",
		"15": "7",
		"20": "12",
	}
	for in, want := range cases {
		got := m.Forward(mustRat(t, in))
		if got.String() != want {
			t.Errorf("Forward(%s) = %s, want %s", in, got, want)
		}
		back := m.Inverse(got)
		if back.String() != in {
			t.Errorf("Inverse(Forward(%s)) = %s", in, back)
		}
	}
}

func TestMappingRejectsNonMonotone(t *testing.T) {
	bad := []Point{
		{X: RatFromInt(0), Y: RatFromInt(0)},
		{X: RatFromInt(5), Y: RatFromInt(5)},
		{X: RatFromInt(5), Y: RatFromInt(6)},
	}
	if _, err := NewMapping(bad); err == nil {
		t.Error("expected error for non-increasing X")
	}
	flat := []Point{
		{X: RatFromInt(0), Y: RatFromInt(0)},
		{X: RatFromInt(5), Y: RatFromInt(0)},
	}
	if _, err := NewMapping(flat); err == nil {
		t.Error("expected error for non-increasing Y (would break invertibility)")
	}
}

func TestFrameBucketHalfOpen(t *testing.T) {
	m := sampleMapping(t)
	tr := Track{ID: "v", Start: RatFromInt(0), Interval: NewRat(1, 30)}
	lo, hi, err := tr.FrameBucket(m, big.NewInt(60))
	if err != nil {
		t.Fatal(err)
	}
	// frame 60 centered at 2, half width 1/60
	if lo.String() != "119/60" || hi.String() != "121/60" {
		t.Errorf("bucket = [%s, %s)", lo, hi)
	}
}
