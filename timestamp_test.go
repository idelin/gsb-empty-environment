package rowconv

import (
	"testing"
	"time"
)

func unixMicroUTC(y int, mo time.Month, d, h, mi, s, nsec int) int64 {
	t := time.Date(y, mo, d, h, mi, s, nsec, time.UTC)
	return t.Unix()*1e6 + int64(nsec)/1000
}

func TestLeapDay(t *testing.T) {
	got, err := ParseTimestampMicros("2024-02-29T12:00:00Z", ModeReject)
	if err != nil {
		t.Fatalf("2024 is a leap year: %v", err)
	}
	if want := unixMicroUTC(2024, 2, 29, 12, 0, 0, 0); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
	if _, err := ParseTimestampMicros("2023-02-29T12:00:00Z", ModeReject); err == nil || err.Code != CodeBadTimestamp {
		t.Fatalf("2023-02-29 must be rejected, got %v", err)
	}
}

func TestOffsetCrossesDay(t *testing.T) {
	// 00:30 at +05:30 on Mar 1 is 19:00 UTC on Feb 29 (previous day).
	got, err := ParseTimestampMicros("2024-03-01T00:30:00+05:30", ModeReject)
	if err != nil {
		t.Fatal(err)
	}
	if want := unixMicroUTC(2024, 2, 29, 19, 0, 0, 0); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
	// Negative offset crossing forward.
	got, err = ParseTimestampMicros("2024-02-29T20:00:00-05:00", ModeReject)
	if err != nil {
		t.Fatal(err)
	}
	if want := unixMicroUTC(2024, 3, 1, 1, 0, 0, 0); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestSubMicrosecondReject(t *testing.T) {
	if _, err := ParseTimestampMicros("2024-01-01T00:00:00.1234567Z", ModeReject); err == nil || err.Code != CodeSubmicrosecond {
		t.Fatalf("want submicrosecond error, got %v", err)
	}
	got, err := ParseTimestampMicros("2024-01-01T00:00:00.123456Z", ModeReject)
	if err != nil {
		t.Fatalf("exact micros must pass reject mode: %v", err)
	}
	if want := unixMicroUTC(2024, 1, 1, 0, 0, 0, 123456000); got != want {
		t.Fatalf("got %d, want %d", got, want)
	}
}

func TestSubMicrosecondHalfEven(t *testing.T) {
	base := int64(1704067200000000) // 2024-01-01T00:00:00Z in UTC micros
	cases := []struct {
		in   string
		want int64
	}{
		{"2024-01-01T00:00:00.0000005Z", base},     // +0.5us tie -> base (even)
		{"2024-01-01T00:00:00.0000015Z", base + 2}, // +1.5us tie -> base+2 (even)
		{"2024-01-01T00:00:00.0000025Z", base + 2}, // +2.5us tie -> base+2 (even)
		{"2024-01-01T00:00:00.0000004Z", base},
		{"2024-01-01T00:00:00.0000006Z", base + 1},
		// Pre-epoch: -0.5us tie -> 0 (even); -1.5us tie -> -2 (even).
		{"1969-12-31T23:59:59.9999995Z", 0},
		{"1969-12-31T23:59:59.9999985Z", -2},
		{"1969-12-31T23:59:59.9999994Z", -1}, // -0.6us -> -1
		{"1969-12-31T23:59:59.9999996Z", 0},  // -0.4us -> 0
	}
	for _, c := range cases {
		got, err := ParseTimestampMicros(c.in, ModeHalfEven)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("%s: got %d, want %d", c.in, got, c.want)
		}
	}
}

func TestExplicitOffsetRequired(t *testing.T) {
	for _, bad := range []string{
		"2024-01-01 12:00:00",
		"2024-01-01T12:00:00",
		"2024-01-01",
		"not-a-time",
	} {
		if _, err := ParseTimestampMicros(bad, ModeReject); err == nil || err.Code != CodeBadTimestamp {
			t.Errorf("%q: want bad_timestamp, got %v", bad, err)
		}
	}
}
