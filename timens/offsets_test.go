package timens

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeGuard struct{ err error }

func (g fakeGuard) Empty(string) error { return g.err }

type fakeCaps struct {
	has bool
	err error
}

func (c fakeCaps) HasSysTime() (bool, error) { return c.has, c.err }

func validHost() HostTime { return HostTime{Sec: 100, Nsec: 0} }

func TestParseRejectsRealtime(t *testing.T) {
	input := "realtime 0 0\nmonotonic 0 0\n"
	_, _, err := ParseOffsets(strings.NewReader(input))
	if !errors.Is(err, ErrUnknownClock) {
		t.Fatalf("expected ErrUnknownClock, got %v", err)
	}
	var pe *ParseError
	if !errors.As(err, &pe) || pe.Line != 1 {
		t.Fatalf("expected parse error on line 1, got %v", err)
	}
}

func TestParseNanosecondsRange(t *testing.T) {
	cases := map[string]string{
		"negative":  "monotonic 0 -1\n",
		"too large": "monotonic 0 1000000000\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := ParseOffsets(strings.NewReader(input))
			if !errors.Is(err, ErrNanosecondsRange) {
				t.Fatalf("expected ErrNanosecondsRange, got %v", err)
			}
		})
	}

	offsets, _, err := ParseOffsets(strings.NewReader("monotonic 0 0\nboottime -5 999999999\n"))
	if err != nil {
		t.Fatalf("boundary nanoseconds rejected: %v", err)
	}
	if offsets[Boottime].Nsec != 999999999 {
		t.Fatalf("boottime nsec = %d", offsets[Boottime].Nsec)
	}
}

func TestValidateRejectsNamespaceWithTasks(t *testing.T) {
	offsets := map[ClockID]Offset{Monotonic: {Clock: Monotonic, Sec: 0, Nsec: 0}}
	guard := fakeGuard{err: ErrNamespaceNotWritable}
	err := Validate(offsets, validHost(), guard, fakeCaps{has: true}, "/proc/1/timens_offsets")
	if !errors.Is(err, ErrNamespaceNotWritable) {
		t.Fatalf("expected ErrNamespaceNotWritable, got %v", err)
	}
}

func TestValidateRequiresCapSysTime(t *testing.T) {
	offsets := map[ClockID]Offset{Monotonic: {Clock: Monotonic, Sec: 0, Nsec: 0}}
	err := Validate(offsets, validHost(), fakeGuard{}, fakeCaps{has: false}, "/proc/1/timens_offsets")
	if !errors.Is(err, ErrMissingCapability) {
		t.Fatalf("expected ErrMissingCapability, got %v", err)
	}
}

func TestNegativeMonotonicRejected(t *testing.T) {
	// Host monotonic is 1s; a -5s offset would expose -4s inside the container.
	offset := Offset{Clock: Monotonic, Sec: -5, Nsec: 0}
	_, err := Convert(Monotonic, HostTime{Sec: 1, Nsec: 0}, offset)
	if !errors.Is(err, ErrNegativeMonotonic) {
		t.Fatalf("expected ErrNegativeMonotonic, got %v", err)
	}

	offsets := map[ClockID]Offset{Monotonic: offset}
	err = Validate(offsets, HostTime{Sec: 1, Nsec: 0}, fakeGuard{}, fakeCaps{has: true}, "/proc/1/timens_offsets")
	if !errors.Is(err, ErrNegativeMonotonic) {
		t.Fatalf("Validate: expected ErrNegativeMonotonic, got %v", err)
	}
}

func TestNegativeBoottimeAllowed(t *testing.T) {
	offset := Offset{Clock: Boottime, Sec: -5, Nsec: 0}
	got, err := Convert(Boottime, HostTime{Sec: 1, Nsec: 0}, offset)
	if err != nil {
		t.Fatalf("negative boottime offset should be allowed: %v", err)
	}
	if got != -4_000_000_000 {
		t.Fatalf("boottime conversion = %d, want -4000000000", got)
	}
}

func TestOverflowRejected(t *testing.T) {
	t.Run("host reading seconds overflow", func(t *testing.T) {
		_, err := Convert(Monotonic, HostTime{Sec: math.MaxInt64, Nsec: 0}, Offset{Sec: 0, Nsec: 0})
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("expected ErrOverflow, got %v", err)
		}
	})

	t.Run("offset seconds overflow", func(t *testing.T) {
		_, err := Convert(Monotonic, validHost(), Offset{Clock: Monotonic, Sec: math.MaxInt64, Nsec: 0})
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("expected ErrOverflow, got %v", err)
		}
	})

	t.Run("host plus offset overflow", func(t *testing.T) {
		// hostNanos = MaxInt64/1e9 * 1e9, leaving headroom under 1e9; +10s overflows.
		host := HostTime{Sec: math.MaxInt64 / nanosPerSecond, Nsec: 0}
		_, err := Convert(Monotonic, host, Offset{Clock: Monotonic, Sec: 10, Nsec: 0})
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("expected ErrOverflow, got %v", err)
		}
	})

	t.Run("host nanoseconds component overflow", func(t *testing.T) {
		_, err := totalNanos(math.MaxInt64/2, math.MaxInt64)
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("expected ErrOverflow, got %v", err)
		}
	})

	t.Run("negative addition underflow", func(t *testing.T) {
		_, err := Convert(Boottime, HostTime{Sec: math.MinInt64 / nanosPerSecond, Nsec: 0},
			Offset{Clock: Boottime, Sec: -10, Nsec: 0})
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("expected ErrOverflow on underflow, got %v", err)
		}
	})

	t.Run("offset with zero seconds", func(t *testing.T) {
		got, overflow := mulOverflow(0, math.MaxInt64)
		if overflow || got != 0 {
			t.Fatalf("mulOverflow(0, ...) = (%d, %v), want 0", got, overflow)
		}
	})
}

func TestWarningString(t *testing.T) {
	w := Warning{Line: 4, Clock: Monotonic, Message: "duplicate monotonic offset line; the last line wins"}
	if !strings.Contains(w.String(), "line 4") || !strings.Contains(w.String(), "last line wins") {
		t.Fatalf("unexpected warning string: %q", w.String())
	}
}

func TestDuplicateClockLastWinsWithWarning(t *testing.T) {
	input := "monotonic 5 0\nboottime 1 0\nmonotonic 9 123\n"
	offsets, warnings, err := ParseOffsets(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got := offsets[Monotonic]; got.Sec != 9 || got.Nsec != 123 {
		t.Fatalf("last monotonic line did not win: %+v", got)
	}
	if len(warnings) != 1 || warnings[0].Clock != Monotonic || warnings[0].Line != 3 {
		t.Fatalf("expected one monotonic warning on line 3, got %+v", warnings)
	}
	if !strings.Contains(warnings[0].Message, "last line wins") {
		t.Fatalf("warning should say last line wins: %q", warnings[0].Message)
	}
}

func TestConvert(t *testing.T) {
	got, err := Convert(Monotonic, HostTime{Sec: 10, Nsec: 500_000_000}, Offset{Clock: Monotonic, Sec: 2, Nsec: 250_000_000})
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	want := int64(12_750_000_000)
	if got != want {
		t.Fatalf("Convert = %d, want %d", got, want)
	}
}

func TestPrepareRejectsMalformedLine(t *testing.T) {
	cases := map[string]string{
		"two fields":  "monotonic 1\n",
		"bad seconds": "monotonic nope 0\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := PrepareOffsets(strings.NewReader(input), validHost(), fakeGuard{}, fakeCaps{has: true}, "/proc/1/timens_offsets")
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestPrepareAndWriteFileRoundTrip(t *testing.T) {
	input := "boottime 3 10\nmonotonic 2 20\nmonotonic 7 30\n"
	offsets, warnings, err := PrepareOffsets(strings.NewReader(input), validHost(), fakeGuard{}, fakeCaps{has: true}, "/proc/1/timens_offsets")
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected duplicate warning, got %+v", warnings)
	}

	path := filepath.Join(t.TempDir(), "timens_offsets")
	if err := WriteFile(path, offsets); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	want := "monotonic 7 30\nboottime 3 10\n"
	if string(data) != want {
		t.Fatalf("rendered content = %q, want %q", data, want)
	}

	parsed, _, err := ParseOffsets(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("reparse failed: %v", err)
	}
	if parsed[Monotonic].Sec != 7 || parsed[Boottime].Sec != 3 {
		t.Fatalf("reparsed offsets mismatch: %+v", parsed)
	}
}

func TestNilDependenciesRejected(t *testing.T) {
	offsets := map[ClockID]Offset{Monotonic: {Clock: Monotonic}}
	if err := Validate(offsets, validHost(), nil, fakeCaps{has: true}, "x"); err == nil {
		t.Fatal("expected error for nil guard")
	}
	if err := Validate(offsets, validHost(), fakeGuard{}, nil, "x"); err == nil {
		t.Fatal("expected error for nil capability checker")
	}
}
