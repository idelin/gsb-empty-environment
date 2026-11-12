// Package timens validates and applies time-namespace clock offsets before
// they are written to a /proc/PID/timens_offsets file.
//
// The kernel only accepts offset writes while the target time namespace is
// still empty (it has never hosted a running member). Each offset line has
// the form "<clock> <seconds> <nanoseconds>", where clock is either
// "monotonic" or "boottime". This package centralizes the policy checks a
// container runtime must perform before the write:
//
//   - only monotonic and boottime clocks are accepted;
//   - nanoseconds must be in [0, 999999999];
//   - the resulting in-container monotonic time may never be negative;
//   - host readings and the offset arithmetic must not overflow int64;
//   - the namespace must still be empty and the caller must hold
//     CAP_SYS_TIME;
//   - repeated lines for the same clock use the last occurrence.
package timens

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

// ClockID identifies a time-namespace offsettable clock.
type ClockID string

const (
	// Monotonic namespaces CLOCK_MONOTONIC.
	Monotonic ClockID = "monotonic"
	// Boottime namespaces CLOCK_BOOTTIME.
	Boottime ClockID = "boottime"
)

const nanosPerSecond int64 = 1_000_000_000

// Sentinel errors. Callers can use errors.Is to classify failures.
var (
	// ErrUnknownClock is returned when an offset line names a clock other
	// than monotonic or boottime (for example realtime).
	ErrUnknownClock = errors.New("timens: only monotonic and boottime offsets are allowed")
	// ErrNanosecondsRange is returned when the nanosecond field is outside
	// [0, 999999999].
	ErrNanosecondsRange = errors.New("timens: nanoseconds must be between 0 and 999999999")
	// ErrNegativeMonotonic is returned when an offset would make the
	// in-container monotonic reading negative.
	ErrNegativeMonotonic = errors.New("timens: offset would produce a negative in-container monotonic time")
	// ErrOverflow is returned when a host reading or the host+offset
	// arithmetic overflows int64.
	ErrOverflow = errors.New("timens: int64 overflow in offset arithmetic")
	// ErrNamespaceNotWritable is returned when the target time namespace
	// already contains a task and therefore no longer accepts offsets.
	ErrNamespaceNotWritable = errors.New("timens: time namespace already has tasks and cannot be reconfigured")
	// ErrMissingCapability is returned when the caller lacks CAP_SYS_TIME.
	ErrMissingCapability = errors.New("timens: CAP_SYS_TIME is required to set time offsets")
	// ErrUnsupportedPlatform is returned when procfs/capability inspection
	// is unavailable (for example when running on a non-Linux host).
	ErrUnsupportedPlatform = errors.New("timens: time namespaces are only supported on Linux")
)

// Offset is one parsed timens_offsets line.
type Offset struct {
	Clock ClockID
	Sec   int64
	Nsec  int64
}

// HostTime is a host clock reading expressed as seconds plus a nanosecond
// remainder. Both fields are checked against int64 overflow before use.
type HostTime struct {
	Sec  int64
	Nsec int64
}

// Warning reports a non-fatal condition discovered while parsing, such as a
// repeated clock line that overrides an earlier one.
type Warning struct {
	Line    int
	Clock   ClockID
	Message string
}

func (w Warning) String() string {
	return fmt.Sprintf("line %d: %s", w.Line, w.Message)
}

// ParseError attaches a 1-based input line number to a validation failure.
type ParseError struct {
	Line int
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("timens: line %d: %s", e.Line, e.Err.Error())
}

func (e *ParseError) Unwrap() error { return e.Err }

// ParseOffsets parses timens_offsets content. Blank lines are ignored; every
// other line must contain exactly three whitespace-separated fields: a clock
// name, a signed 64-bit seconds value and a nanoseconds value.
//
// Repeated lines for the same clock are not fatal: the last occurrence wins
// and a Warning is returned describing the override.
func ParseOffsets(r io.Reader) (map[ClockID]Offset, []Warning, error) {
	offsets := make(map[ClockID]Offset)
	var warnings []Warning

	scanner := bufio.NewScanner(r)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, warnings, &ParseError{Line: lineNo,
				Err: fmt.Errorf("expected '<clock> <seconds> <nanoseconds>', got %d fields", len(fields))}
		}

		clock := ClockID(fields[0])
		if clock != Monotonic && clock != Boottime {
			return nil, warnings, &ParseError{Line: lineNo, Err: fmt.Errorf("%w: %q", ErrUnknownClock, fields[0])}
		}

		sec, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, warnings, &ParseError{Line: lineNo, Err: fmt.Errorf("invalid seconds %q: %w", fields[1], err)}
		}

		nsec, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, warnings, &ParseError{Line: lineNo, Err: fmt.Errorf("invalid nanoseconds %q: %w", fields[2], err)}
		}
		if nsec < 0 || nsec > nanosPerSecond-1 {
			return nil, warnings, &ParseError{Line: lineNo, Err: fmt.Errorf("%w: %d", ErrNanosecondsRange, nsec)}
		}

		if _, exists := offsets[clock]; exists {
			warnings = append(warnings, Warning{
				Line:    lineNo,
				Clock:   clock,
				Message: fmt.Sprintf("duplicate %s offset line; the last line wins", clock),
			})
		}
		offsets[clock] = Offset{Clock: clock, Sec: sec, Nsec: nsec}
	}
	if err := scanner.Err(); err != nil {
		return nil, warnings, fmt.Errorf("timens: reading offsets: %w", err)
	}

	return offsets, warnings, nil
}

// addOverflow computes a+b and reports whether it overflows int64.
func addOverflow(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, true
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, true
	}
	return a + b, false
}

// mulOverflow computes a*b and reports whether it overflows int64.
func mulOverflow(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, false
	}
	result := a * b
	if result/b != a {
		return 0, true
	}
	return result, false
}

// totalNanos converts a (seconds, nanoseconds) pair to a scalar nanosecond
// value, rejecting int64 overflow in either component or their sum.
func totalNanos(sec, nsec int64) (int64, error) {
	secNanos, overflow := mulOverflow(sec, nanosPerSecond)
	if overflow {
		return 0, ErrOverflow
	}
	total, overflow := addOverflow(secNanos, nsec)
	if overflow {
		return 0, ErrOverflow
	}
	return total, nil
}

// offsetNanos converts an Offset to a scalar nanosecond delta, rejecting
// int64 overflow.
func offsetNanos(o Offset) (int64, error) {
	return totalNanos(o.Sec, o.Nsec)
}

// Convert translates a host clock reading to the reading visible inside the
// time namespace after applying offset:
//
//	container = host + offset
//
// All intermediate values are checked for int64 overflow and a negative
// monotonic result is rejected. Overflow or a negative monotonic reading
// produces an error wrapping ErrOverflow or ErrNegativeMonotonic.
func Convert(clock ClockID, host HostTime, offset Offset) (int64, error) {
	hostNanos, err := totalNanos(host.Sec, host.Nsec)
	if err != nil {
		return 0, fmt.Errorf("host %s reading: %w", clock, err)
	}
	delta, err := offsetNanos(offset)
	if err != nil {
		return 0, fmt.Errorf("%s offset: %w", clock, err)
	}

	container, overflow := addOverflow(hostNanos, delta)
	if overflow {
		return 0, fmt.Errorf("host %s (%d) plus offset (%d): %w", clock, hostNanos, delta, ErrOverflow)
	}
	if clock == Monotonic && container < 0 {
		return 0, fmt.Errorf("%w: host monotonic %d with offset %d", ErrNegativeMonotonic, hostNanos, delta)
	}
	return container, nil
}

// NamespaceGuard reports whether a time namespace still accepts offset
// writes. Linux implements it against procfs; tests can supply a fake.
type NamespaceGuard interface {
	// Empty returns nil when the namespace referenced by the given
	// /proc/PID/timens_offsets path has no member tasks yet.
	Empty(offsetsPath string) error
}

// CapabilityChecker reports whether the calling process holds CAP_SYS_TIME.
type CapabilityChecker interface {
	HasSysTime() (bool, error)
}

// Validate applies every pre-write policy check to a parsed offset set:
//
//   - the namespace must still be empty (guard must be non-nil);
//   - the caller must hold CAP_SYS_TIME (caps must be non-nil);
//   - the monotonic offset must keep the in-container monotonic reading
//     non-negative for the supplied host reading, and neither the host
//     reading nor the arithmetic may overflow int64.
//
// hostMonotonic is the current host CLOCK_MONOTONIC reading. The boottime
// clock has no non-negativity requirement because boottime offsets are
// intentionally allowed to be negative by the kernel.
func Validate(offsets map[ClockID]Offset, hostMonotonic HostTime, guard NamespaceGuard, caps CapabilityChecker, offsetsPath string) error {
	if guard == nil {
		return errors.New("timens: nil NamespaceGuard")
	}
	if caps == nil {
		return errors.New("timens: nil CapabilityChecker")
	}

	if err := guard.Empty(offsetsPath); err != nil {
		return err
	}

	ok, err := caps.HasSysTime()
	if err != nil {
		return fmt.Errorf("timens: checking CAP_SYS_TIME: %w", err)
	}
	if !ok {
		return ErrMissingCapability
	}

	if mono, present := offsets[Monotonic]; present {
		if _, err := Convert(Monotonic, hostMonotonic, mono); err != nil {
			return err
		}
	}

	if boot, present := offsets[Boottime]; present {
		if _, err := offsetNanos(boot); err != nil {
			return err
		}
	}
	return nil
}

// PrepareOffsets parses and fully validates offsets in one step. On success
// the returned map is safe to render into the target timens_offsets file.
func PrepareOffsets(r io.Reader, hostMonotonic HostTime, guard NamespaceGuard, caps CapabilityChecker, offsetsPath string) (map[ClockID]Offset, []Warning, error) {
	offsets, warnings, err := ParseOffsets(r)
	if err != nil {
		return nil, warnings, err
	}
	if err := Validate(offsets, hostMonotonic, guard, caps, offsetsPath); err != nil {
		return nil, warnings, err
	}
	return offsets, warnings, nil
}

// Render serializes offsets in the kernel-expected format. Clocks are emitted
// in a fixed order so the output is deterministic.
func Render(offsets map[ClockID]Offset) []byte {
	var b strings.Builder
	for _, clock := range []ClockID{Monotonic, Boottime} {
		if o, ok := offsets[clock]; ok {
			fmt.Fprintf(&b, "%s %d %d\n", o.Clock, o.Sec, o.Nsec)
		}
	}
	return []byte(b.String())
}

// WriteFile renders the validated offsets and writes them to an existing
// timens_offsets path. The caller is responsible for running Validate first
// (or using the platform-provided high-level helper, which does both).
func WriteFile(offsetsPath string, offsets map[ClockID]Offset) error {
	if err := os.WriteFile(offsetsPath, Render(offsets), 0o600); err != nil {
		return fmt.Errorf("timens: writing %s: %w", offsetsPath, err)
	}
	return nil
}
