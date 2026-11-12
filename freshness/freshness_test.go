package freshness

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually controlled, concurrency-safe Clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// refInitialAge is an independent re-implementation of the corrected initial
// age, used as an oracle for the exhaustive timeline tests. Inputs are kept
// small enough that no saturation is exercised here.
func refInitialAge(sentAt, receivedAt, date time.Time, hasDate bool, ageSeconds uint64, hasAge bool) time.Duration {
	delay := receivedAt.Sub(sentAt)
	if delay < 0 {
		delay = 0
	}
	apparent := time.Duration(0)
	if hasDate {
		apparent = receivedAt.Sub(date)
		if apparent < 0 {
			apparent = 0
		}
	}
	ageValue := time.Duration(0)
	if hasAge {
		ageValue = time.Duration(ageSeconds) * time.Second
	}
	corrected := ageValue + delay
	if apparent > corrected {
		corrected = apparent
	}
	return corrected
}

// TestExhaustiveTimeline walks every combination of response delay, Date value
// and Age value across a fixed timeline that includes clock regressions and
// repeated observations. It asserts both the initial corrected age and the
// monotonic high-water-mark behavior at every step.
func TestExhaustiveTimeline(t *testing.T) {
	base := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	requestOffsets := []time.Duration{0, 50 * time.Millisecond, 3 * time.Second}
	dateOffsets := []time.Duration{-3 * time.Second, 0, 2 * time.Second} // relative to reception
	ageValues := []uint64{0, 1, 7}

	// Reception-relative timeline; negative entries are clock regressions.
	walk := []time.Duration{
		0,
		time.Second,
		4 * time.Second,
		-2 * time.Second,
		-5 * time.Second,
		10 * time.Second,
		10 * time.Second,
		30 * time.Second,
	}

	life := time.Hour
	caseIndex := 0
	for _, reqOffset := range requestOffsets {
		for _, hasDate := range []bool{false, true} {
			for _, dateOffset := range dateOffsets {
				for _, hasAge := range []bool{false, true} {
					for _, age := range ageValues {
						if !hasAge && age != 0 {
							continue
						}
						name := caseName(caseIndex, reqOffset, hasDate, dateOffset, hasAge, age)
						caseIndex++

						t.Run(name, func(t *testing.T) {
							clock := newFakeClock(base)
							receivedAt := base
							sentAt := receivedAt.Add(-reqOffset)
							date := receivedAt.Add(dateOffset)

							entry := NewEntry(clock, Params{
								SentAt:      sentAt,
								ReceivedAt:  receivedAt,
								Date:        date,
								HasDate:     hasDate,
								AgeSeconds:  age,
								HasAge:      hasAge,
								MaxLifetime: life,
							})

							wantInitial := refInitialAge(sentAt, receivedAt, date, hasDate, age, hasAge)
							if got := entry.InitialAge(); got != wantInitial {
								t.Fatalf("InitialAge() = %v, want %v", got, wantInitial)
							}

							highWater := wantInitial
							accumulated := time.Duration(0)
							for step, offset := range walk {
								clock.Set(receivedAt.Add(offset))

								if step > 0 {
									stepResident := offset - walk[step-1]
									if stepResident > 0 {
										accumulated += stepResident
									}
								}
								want := wantInitial + accumulated
								if want > highWater {
									highWater = want
								}

								got := entry.Age()
								if got != want {
									t.Fatalf("step %d offset %v: Age() = %v, want %v", step, offset, got, want)
								}
								if got < highWater {
									t.Fatalf("step %d: age %v dropped below high-water mark %v", step, got, highWater)
								}
								if got != entry.Age() {
									t.Fatalf("step %d: repeated Age() observation changed value", step)
								}

								wantRemaining := life - got
								if got >= life {
									wantRemaining = 0
								}
								if rem := entry.Remaining(); rem != wantRemaining {
									t.Fatalf("step %d: Remaining() = %v, want %v", step, rem, wantRemaining)
								}
								if fresh := entry.Fresh(); fresh != (got < life) {
									t.Fatalf("step %d: Fresh() = %v, want %v", step, fresh, got < life)
								}
							}
						})
					}
				}
			}
		}
	}
}

func caseName(index int, reqOffset time.Duration, hasDate bool, dateOffset time.Duration, hasAge bool, age uint64) string {
	dateToken := "nodate"
	if hasDate {
		dateToken = "date=" + dateOffset.String()
	}
	ageToken := "noage"
	if hasAge {
		ageToken = "age=" + (time.Duration(age) * time.Second).String()
	}
	return "case" + strconv.Itoa(index) +
		"_delay=" + reqOffset.String() +
		"_" + dateToken +
		"_" + ageToken
}

// TestResponseDelay verifies that delay between request and response is added on
// top of the Age header rather than extending freshness.
func TestResponseDelay(t *testing.T) {
	receivedAt := time.Unix(1000, 0)
	clock := newFakeClock(receivedAt)
	entry := NewEntry(clock, Params{
		SentAt:      receivedAt.Add(-2500 * time.Millisecond),
		ReceivedAt:  receivedAt,
		Date:        receivedAt.Add(-4 * time.Second),
		HasDate:     true,
		AgeSeconds:  2,
		HasAge:      true,
		MaxLifetime: 10 * time.Second,
	})

	// apparent=4s, age=2s, delay=2.5s -> corrected = max(4s, 4.5s) = 4.5s.
	if got := entry.InitialAge(); got != 4500*time.Millisecond {
		t.Fatalf("InitialAge() = %v, want 4.5s", got)
	}
	if got := entry.MaxLifetime(); got != 10*time.Second {
		t.Fatalf("MaxLifetime() = %v, want 10s", got)
	}
	if !entry.Fresh() {
		t.Fatal("entry should be fresh at reception")
	}

	clock.Set(receivedAt.Add(6 * time.Second))
	if got := entry.Age(); got != 10500*time.Millisecond {
		t.Fatalf("Age() after 6s = %v, want 10.5s", got)
	}
	if entry.Fresh() {
		t.Fatal("entry must be stale once age reaches the maximum lifetime")
	}
	if rem := entry.Remaining(); rem != 0 {
		t.Fatalf("Remaining() stale = %v, want 0", rem)
	}
}

// TestMissingDate ensures a missing Date header yields zero apparent age while
// the Age-plus-delay branch is still honored.
func TestMissingDate(t *testing.T) {
	receivedAt := time.Unix(2000, 0)
	clock := newFakeClock(receivedAt)

	t.Run("age and delay still counted", func(t *testing.T) {
		entry := NewEntry(clock, Params{
			SentAt:      receivedAt.Add(-3 * time.Second),
			ReceivedAt:  receivedAt,
			HasDate:     false,
			AgeSeconds:  5,
			HasAge:      true,
			MaxLifetime: time.Minute,
		})
		if got := entry.InitialAge(); got != 8*time.Second {
			t.Fatalf("InitialAge() = %v, want 8s", got)
		}
	})

	t.Run("no age header means only delay contributes", func(t *testing.T) {
		entry := NewEntry(clock, Params{
			SentAt:      receivedAt.Add(-1500 * time.Millisecond),
			ReceivedAt:  receivedAt,
			HasDate:     false,
			HasAge:      false,
			MaxLifetime: time.Minute,
		})
		if got := entry.InitialAge(); got != 1500*time.Millisecond {
			t.Fatalf("InitialAge() = %v, want 1.5s", got)
		}
	})

	t.Run("future date clamps apparent age to zero", func(t *testing.T) {
		entry := NewEntry(clock, Params{
			SentAt:      receivedAt,
			ReceivedAt:  receivedAt,
			Date:        receivedAt.Add(10 * time.Second),
			HasDate:     true,
			HasAge:      false,
			MaxLifetime: time.Minute,
		})
		if got := entry.InitialAge(); got != 0 {
			t.Fatalf("InitialAge() = %v, want 0", got)
		}
	})
}

// TestAgeOverflow checks the RFC 9111 cap: an Age that does not fit a 31-bit
// non-negative integer is normalized to 2^31 seconds, which is always stale.
func TestAgeOverflow(t *testing.T) {
	receivedAt := time.Unix(3000, 0)
	clock := newFakeClock(receivedAt)
	overflowInputs := []uint64{
		ageOverflowSeconds,
		ageOverflowSeconds + 1,
		1 << 32,
		1<<63 - 1,
		^uint64(0),
	}
	for _, age := range overflowInputs {
		entry := NewEntry(clock, Params{
			SentAt:      receivedAt,
			ReceivedAt:  receivedAt,
			HasDate:     false,
			AgeSeconds:  age,
			HasAge:      true,
			MaxLifetime: 24 * time.Hour,
		})
		want := time.Duration(ageOverflowSeconds) * time.Second
		if got := entry.InitialAge(); got != want {
			t.Errorf("Age %d: InitialAge() = %v, want %v", age, got, want)
		}
		if entry.Fresh() {
			t.Errorf("Age %d: overflow entry must never be fresh", age)
		}
	}

	// Just below the cap is honored literally.
	entry := NewEntry(clock, Params{
		SentAt:      receivedAt,
		ReceivedAt:  receivedAt,
		HasDate:     false,
		AgeSeconds:  ageOverflowSeconds - 1,
		HasAge:      true,
		MaxLifetime: maxSaturatingDuration,
	})
	if got := entry.InitialAge(); got != time.Duration(ageOverflowSeconds-1)*time.Second {
		t.Fatalf("boundary InitialAge() = %v", got)
	}
}

// TestClockRegressionNeverLowersAge drives the clock forward, backward and
// forward again while asserting the recorded age never decreases.
func TestClockRegressionNeverLowersAge(t *testing.T) {
	receivedAt := time.Unix(4000, 0)
	clock := newFakeClock(receivedAt)
	entry := NewEntry(clock, Params{
		SentAt:      receivedAt,
		ReceivedAt:  receivedAt,
		HasDate:     false,
		AgeSeconds:  10,
		HasAge:      true,
		MaxLifetime: time.Hour,
	})

	moves := []struct {
		offset time.Duration
		want   time.Duration
	}{
		{0, 10 * time.Second},
		{5 * time.Second, 15 * time.Second},
		{20 * time.Second, 30 * time.Second},
		{-25 * time.Second, 30 * time.Second}, // regression: zero resident time
		{10 * time.Second, 65 * time.Second},  // 35s elapsed since -25s anchor
		{20 * time.Second, 75 * time.Second},  // another 10s resident
		{40 * time.Second, 95 * time.Second},  // 20s more resident time
	}
	previous := time.Duration(-1)
	for i, move := range moves {
		clock.Set(receivedAt.Add(move.offset))
		got := entry.Age()
		if got != move.want {
			t.Fatalf("move %d offset %v: Age() = %v, want %v", i, move.offset, got, move.want)
		}
		if previous >= 0 && got < previous {
			t.Fatalf("move %d: age decreased from %v to %v", i, previous, got)
		}
		previous = got
	}
}

// TestSaturatingArithmetic verifies the saturation helpers at their boundaries.
func TestSaturatingArithmetic(t *testing.T) {
	maxD := maxSaturatingDuration
	minD := -maxSaturatingDuration

	if got := saturatingAdd(maxD, time.Nanosecond); got != maxD {
		t.Errorf("positive overflow = %v, want max", got)
	}
	if got := saturatingAdd(maxD, maxD); got != maxD {
		t.Errorf("max+max = %v, want max", got)
	}
	if got := saturatingAdd(minD, -time.Nanosecond); got != minD {
		t.Errorf("negative overflow = %v, want min", got)
	}
	if got := saturatingAdd(minD, minD); got != minD {
		t.Errorf("min+min = %v, want min", got)
	}
	if got := saturatingAdd(maxD, -time.Nanosecond); got != maxD-time.Nanosecond {
		t.Errorf("max-1 = %v", got)
	}
	if got := saturatingSub(time.Second, 5*time.Second); got != 0 {
		t.Errorf("negative difference = %v, want 0", got)
	}
	if got := saturatingSub(5*time.Second, time.Second); got != 4*time.Second {
		t.Errorf("normal difference = %v, want 4s", got)
	}
	if got := nonNegative(-time.Second); got != 0 {
		t.Errorf("nonNegative(-1s) = %v, want 0", got)
	}
	if got := ageSecondsToDuration(^uint64(0)); got != time.Duration(ageOverflowSeconds)*time.Second {
		t.Errorf("age overflow conversion = %v", got)
	}
}

// TestEndToEndSaturation drives an entry with a near-maximum age through an
// enormous clock jump and asserts the age saturates instead of wrapping.
func TestEndToEndSaturation(t *testing.T) {
	receivedAt := time.Unix(8000, 0)
	clock := newFakeClock(receivedAt)
	entry := NewEntry(clock, Params{
		SentAt:      receivedAt,
		ReceivedAt:  receivedAt,
		HasDate:     false,
		AgeSeconds:  ageOverflowSeconds,
		HasAge:      true,
		MaxLifetime: maxSaturatingDuration,
	})

	clock.Set(receivedAt.Add(maxSaturatingDuration))
	if got := entry.Age(); got != maxSaturatingDuration {
		t.Fatalf("saturated Age() = %v, want max duration", got)
	}
	if entry.Fresh() {
		t.Fatal("saturated age must be stale")
	}
	if rem := entry.Remaining(); rem != 0 {
		t.Fatalf("saturated Remaining() = %v, want 0", rem)
	}
}

// TestNonPositiveLifetime reports an entry stale immediately.
func TestNonPositiveLifetime(t *testing.T) {
	receivedAt := time.Unix(5000, 0)
	clock := newFakeClock(receivedAt)
	for _, life := range []time.Duration{0, -time.Second} {
		entry := NewEntry(clock, Params{
			SentAt:      receivedAt,
			ReceivedAt:  receivedAt,
			HasDate:     false,
			HasAge:      false,
			MaxLifetime: life,
		})
		if entry.Fresh() {
			t.Errorf("lifetime %v: entry must not be fresh", life)
		}
		if rem := entry.Remaining(); rem != 0 {
			t.Errorf("lifetime %v: Remaining() = %v, want 0", life, rem)
		}
	}
}

// TestDefaultClockAndZeroReceivedAt covers a nil clock and an omitted ReceivedAt.
func TestDefaultClockAndZeroReceivedAt(t *testing.T) {
	before := time.Now()
	entry := NewEntry(nil, Params{
		HasDate:     false,
		HasAge:      false,
		MaxLifetime: time.Minute,
	})
	after := time.Now()

	if entry.ReceivedAt().Before(before) || entry.ReceivedAt().After(after) {
		t.Fatalf("ReceivedAt %v outside construction window", entry.ReceivedAt())
	}
	if got := entry.InitialAge(); got != 0 {
		t.Fatalf("InitialAge() = %v, want 0", got)
	}
	if !entry.Fresh() {
		t.Fatal("new entry with minute lifetime should be fresh")
	}

	// The stateless helper must default to the system clock as well.
	if got := CorrectedInitialAge(nil, Params{HasAge: false, MaxLifetime: time.Minute}); got != 0 {
		t.Fatalf("nil-clock CorrectedInitialAge() = %v, want 0", got)
	}
}

// TestZeroTimesIgnored ensures zero-value timestamps (Go's year-one value) are
// treated as absent rather than producing an enormous negative time difference.
func TestZeroTimesIgnored(t *testing.T) {
	receivedAt := time.Unix(5500, 0)
	clock := newFakeClock(receivedAt)

	t.Run("zero SentAt contributes no delay", func(t *testing.T) {
		entry := NewEntry(clock, Params{
			ReceivedAt:  receivedAt,
			HasDate:     false,
			AgeSeconds:  4,
			HasAge:      true,
			MaxLifetime: time.Minute,
		})
		if got := entry.InitialAge(); got != 4*time.Second {
			t.Fatalf("InitialAge() = %v, want 4s", got)
		}
	})

	t.Run("zero Date with HasDate is treated as missing", func(t *testing.T) {
		entry := NewEntry(clock, Params{
			SentAt:      receivedAt.Add(-time.Second),
			ReceivedAt:  receivedAt,
			HasDate:     true,
			HasAge:      false,
			MaxLifetime: time.Minute,
		})
		if got := entry.InitialAge(); got != time.Second {
			t.Fatalf("InitialAge() = %v, want 1s delay only", got)
		}
	})
}

// TestStandaloneFunctionMatchesEntry checks the stateless helper against NewEntry.
func TestStandaloneFunctionMatchesEntry(t *testing.T) {
	receivedAt := time.Unix(6000, 0)
	clock := newFakeClock(receivedAt)
	p := Params{
		SentAt:      receivedAt.Add(-2 * time.Second),
		ReceivedAt:  receivedAt,
		Date:        receivedAt.Add(-9 * time.Second),
		HasDate:     true,
		AgeSeconds:  3,
		HasAge:      true,
		MaxLifetime: time.Minute,
	}
	entry := NewEntry(clock, p)
	if got := CorrectedInitialAge(clock, p); got != entry.InitialAge() {
		t.Fatalf("CorrectedInitialAge() = %v, entry initial = %v", got, entry.InitialAge())
	}
	// apparent=9s dominates age(3s)+delay(2s)=5s.
	if got := entry.InitialAge(); got != 9*time.Second {
		t.Fatalf("InitialAge() = %v, want 9s", got)
	}
}

// TestConcurrentAge exercises the mutex under parallel readers and clock moves.
func TestConcurrentAge(t *testing.T) {
	receivedAt := time.Unix(7000, 0)
	clock := newFakeClock(receivedAt)
	entry := NewEntry(clock, Params{
		SentAt:      receivedAt,
		ReceivedAt:  receivedAt,
		HasDate:     false,
		AgeSeconds:  1,
		HasAge:      true,
		MaxLifetime: time.Hour,
	})

	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(offset time.Duration) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					clock.Set(receivedAt.Add(offset))
					_ = entry.Age()
					_ = entry.Remaining()
					_ = entry.Fresh()
				}
			}
		}(time.Duration(i%3) * time.Second)
	}
	time.Sleep(20 * time.Millisecond)
	close(done)
	wg.Wait()

	if got := entry.Age(); got < time.Second {
		t.Fatalf("final Age() = %v, want at least the 1s initial age", got)
	}
}

func ExampleNewEntry() {
	receivedAt := time.Unix(100, 0)
	clock := ClockFunc(func() time.Time { return receivedAt })
	entry := NewEntry(clock, Params{
		SentAt:      receivedAt.Add(-2 * time.Second),
		ReceivedAt:  receivedAt,
		Date:        receivedAt.Add(-4 * time.Second),
		HasDate:     true,
		AgeSeconds:  2,
		HasAge:      true,
		MaxLifetime: 30 * time.Second,
	})
	// apparent 4s vs age 2s + delay 2s = 4s.
	fmt.Println(entry.InitialAge())
	fmt.Println(entry.Fresh())
	// Output:
	// 4s
	// true
}
