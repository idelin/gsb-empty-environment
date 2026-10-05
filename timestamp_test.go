package rowconv

import (
	"testing"
	"time"
)

func utcMicros(y int, mo time.Month, d, h, mi, s, micro int) int64 {
	return time.Date(y, mo, d, h, mi, s, micro*1000, time.UTC).Unix()*1000000 + int64(micro)
}

func TestParseTimestampLeapDay(t *testing.T) {
	got, err := ParseTimestamp("2024-02-29T12:00:00Z", Reject)
	if err != nil {
		t.Fatalf("leap day 2024-02-29 rejected: %v", err)
	}
	if want := utcMicros(2024, 2, 29, 12, 0, 0, 0); got != want {
		t.Errorf("got %d, want %d", got, want)
	}
	if _, err := ParseTimestamp("2023-02-29T12:00:00Z", Reject); err == nil {
		t.Error("2023-02-29 (not a leap year) should be rejected")
	}
	if _, err := ParseTimestamp("2024-02-30T12:00:00Z", Reject); err == nil {
		t.Error("2024-02-30 should be rejected")
	}
}

func TestParseTimestampOffsetCrossingDay(t *testing.T) {
	// +05:30 pushes the UTC instant back into the previous (leap) day.
	got, err := ParseTimestamp("2024-03-01T00:30:00+05:30", Reject)
	if err != nil {
		t.Fatal(err)
	}
	if want := utcMicros(2024, 2, 29, 19, 0, 0, 0); got != want {
		t.Errorf("got %d, want %d (2024-02-29T19:00:00Z)", got, want)
	}
	// Negative offset pushes forward across midnight.
	got, err = ParseTimestamp("2024-02-29T20:00:00-05:00", Reject)
	if err != nil {
		t.Fatal(err)
	}
	if want := utcMicros(2024, 3, 1, 1, 0, 0, 0); got != want {
		t.Errorf("got %d, want %d (2024-03-01T01:00:00Z)", got, want)
	}
	// Missing offset is not explicit.
	if _, err := ParseTimestamp("2024-01-01T00:00:00", Reject); err == nil {
		t.Error("timestamp without offset should be rejected")
	}
}

func TestParseTimestampSubMicrosecond(t *testing.T) {
	base := utcMicros(2024, 1, 1, 0, 0, 0, 0)
	// Reject mode: any sub-microsecond digit is an error.
	if _, err := ParseTimestamp("2024-01-01T00:00:00.0000005Z", Reject); err == nil {
		t.Error("sub-microsecond input should be rejected in Reject mode")
	}
	// Exact micros are fine in Reject mode.
	got, err := ParseTimestamp("2024-01-01T00:00:00.123456Z", Reject)
	if err != nil || got != base+123456 {
		t.Errorf("got %d, %v; want %d, nil", got, err, base+123456)
	}
	// Ties-to-even on the microsecond boundary.
	cases := []struct {
		in   string
		want int64
	}{
		{"2024-01-01T00:00:00.0000005Z", base},     // 0.5us tie, base is even
		{"2024-01-01T00:00:00.0000015Z", base + 2}, // 1.5us tie, 2 is even
		{"2024-01-01T00:00:00.0000025Z", base + 2}, // 2.5us tie, 2 is even
		{"2024-01-01T00:00:00.0000014Z", base + 1},
		{"2024-01-01T00:00:00.0000016Z", base + 2},
	}
	for _, c := range cases {
		got, err := ParseTimestamp(c.in, TiesToEven)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("%s: got %d, want %d", c.in, got, c.want)
		}
	}
}
