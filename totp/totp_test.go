package totp

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// rfc6238SHA1Secret is the ASCII test key "12345678901234567890" used by
// RFC 6238 Appendix B.
var rfc6238SHA1Secret = []byte("12345678901234567890")

func TestRFC6238FixedVectors(t *testing.T) {
	vectors := []struct {
		unixTime int64
		code     string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}

	for _, tc := range vectors {
		t.Run(fmt.Sprintf("t=%d", tc.unixTime), func(t *testing.T) {
			v, err := New(Config{
				Secret: rfc6238SHA1Secret,
				Period: 30,
				Digits: 8,
				Before: 0,
				After:  0,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			got, err := v.Generate(tc.unixTime)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if got != tc.code {
				t.Fatalf("Generate(%d) = %q, want %q", tc.unixTime, got, tc.code)
			}

			wantStep := uint64(tc.unixTime) / 30
			step, err := v.Verify(tc.code, tc.unixTime)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if step != wantStep {
				t.Fatalf("Verify step = %d, want %d", step, wantStep)
			}
			if last, ok := v.LastStep(); !ok || last != wantStep {
				t.Fatalf("LastStep = (%d, %v), want (%d, true)", last, ok, wantStep)
			}

			// The same step must never be accepted a second time.
			if _, err := v.Verify(tc.code, tc.unixTime); !errors.Is(err, ErrReplay) {
				t.Fatalf("second Verify error = %v, want ErrReplay", err)
			}
			if last, _ := v.LastStep(); last != wantStep {
				t.Fatalf("replay changed LastStep to %d", last)
			}

			// A wrong code is rejected and leaves state untouched.
			if _, err := v.Verify("00000000", tc.unixTime); !errors.Is(err, ErrInvalidPasscode) {
				t.Fatalf("wrong code error = %v, want ErrInvalidPasscode", err)
			}

			// Reset makes the step consumable again.
			v.Reset()
			if _, ok := v.LastStep(); ok {
				t.Fatal("LastStep should report false after Reset")
			}
			if _, err := v.Verify(tc.code, tc.unixTime); err != nil {
				t.Fatalf("Verify after Reset: %v", err)
			}
		})
	}
}

// ResetThenVerify clears replay state before an acceptance check so each
// window position starts fresh.
func (v *Verifier) ResetThenVerify(passcode string, unixTime int64) (uint64, error) {
	v.Reset()
	return v.Verify(passcode, unixTime)
}

func TestWindowMatching(t *testing.T) {
	v := NewDefault(rfc6238SHA1Secret) // ±1 step, 30s period, 6 digits
	// Align the base with a step boundary so ±30s maps exactly to the
	// neighboring steps.
	base := int64(1234567890 / 30 * 30)

	for delta := int64(-30); delta <= 30; delta += 30 {
		code, err := v.Generate(base + delta)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if _, err := v.ResetThenVerify(code, base); err != nil {
			t.Fatalf("code at offset %d should be accepted: %v", delta, err)
		}
	}

	for _, delta := range []int64{-90, 90} {
		code, err := v.Generate(base + delta)
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		v.Reset()
		if _, err := v.Verify(code, base); !errors.Is(err, ErrInvalidPasscode) {
			t.Fatalf("code at offset %d (outside ±1 window): error = %v, want ErrInvalidPasscode", delta, err)
		}
	}
}

func TestCrossWindowReplay(t *testing.T) {
	v, err := New(Config{Secret: rfc6238SHA1Secret, Period: 30, Digits: 6, Before: 2, After: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// At step 10 (window 8..10) consume the step 9 passcode.
	code9, err := v.Generate(int64(9 * 30))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	step, err := v.Verify(code9, int64(10*30))
	if err != nil || step != 9 {
		t.Fatalf("first Verify = (%d, %v), want (9, nil)", step, err)
	}

	// One step later the window is 9..11, so step 9's code is still inside
	// it, but the same step must be rejected rather than accepted again.
	if _, err := v.Verify(code9, int64(11*30)); !errors.Is(err, ErrReplay) {
		t.Fatalf("cross-window replay error = %v, want ErrReplay", err)
	}
	if last, _ := v.LastStep(); last != 9 {
		t.Fatalf("LastStep = %d, replay must not update state", last)
	}

	// A genuinely newer step still succeeds.
	code11, err := v.Generate(int64(11 * 30))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if step, err := v.Verify(code11, int64(11*30)); err != nil || step != 11 {
		t.Fatalf("newer step Verify = (%d, %v), want (11, nil)", step, err)
	}
}

func TestClockRollback(t *testing.T) {
	v, err := New(Config{Secret: rfc6238SHA1Secret, Period: 30, Digits: 6, Before: 2, After: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Login at step 10.
	code10, err := v.Generate(int64(10 * 30))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := v.Verify(code10, int64(10*30)); err != nil {
		t.Fatalf("Verify at step 10: %v", err)
	}

	// The device clock rolls back to step 8 (window 6..8): a valid passcode
	// for an already-consumed era must be refused.
	code8, err := v.Generate(int64(8 * 30))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := v.Verify(code8, int64(8*30)); !errors.Is(err, ErrReplay) {
		t.Fatalf("rollback Verify error = %v, want ErrReplay", err)
	}
	if last, _ := v.LastStep(); last != 10 {
		t.Fatalf("LastStep = %d, rollback replay must not change state", last)
	}

	// Time progresses again and a fresh step is accepted.
	code11, err := v.Generate(int64(11 * 30))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := v.Verify(code11, int64(11*30)); err != nil {
		t.Fatalf("Verify at step 11: %v", err)
	}
}

func TestChooseStepSelection(t *testing.T) {
	cases := []struct {
		name    string
		matched []uint64
		current uint64
		want    uint64
	}{
		{"single", []uint64{7}, 10, 7},
		{"current preferred", []uint64{8, 10, 11}, 10, 10},
		{"equidistant picks earlier", []uint64{9, 11}, 10, 9},
		{"equidistant far picks earlier", []uint64{7, 13}, 10, 7},
		{"nearest future", []uint64{10, 12}, 10, 10},
		{"unsorted input", []uint64{13, 5, 9}, 10, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseStep(tc.matched, tc.current); got != tc.want {
				t.Fatalf("chooseStep(%v, %d) = %d, want %d", tc.matched, tc.current, got, tc.want)
			}
		})
	}
}

func TestMultipleMatchesEndToEnd(t *testing.T) {
	v, err := New(Config{Secret: rfc6238SHA1Secret, Period: 30, Digits: 6, Before: 2, After: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Inject a deterministic generator so several steps can share a code.
	v.generate = func(_ []byte, counter uint64) (string, error) {
		switch counter {
		case 9, 11:
			return "SAME", nil
		case 10:
			return "CUR", nil
		default:
			return "OTHER", nil
		}
	}

	// At current step 10 both step 9 and 11 match "SAME" at equal distance;
	// the earlier step (9) must be recorded.
	step, err := v.Verify("SAME", int64(10*30))
	if err != nil || step != 9 {
		t.Fatalf("equidistant Verify = (%d, %v), want (9, nil)", step, err)
	}
	if last, _ := v.LastStep(); last != 9 {
		t.Fatalf("LastStep = %d, want 9", last)
	}

	// Replaying the same winning step is refused even though other steps
	// share the code.
	if _, err := v.Verify("SAME", int64(10*30)); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay error = %v, want ErrReplay", err)
	}

	// Current step's own code is newer and succeeds.
	if step, err := v.Verify("CUR", int64(10*30)); err != nil || step != 10 {
		t.Fatalf("current Verify = (%d, %v), want (10, nil)", step, err)
	}

	// A code that no window step produces is rejected.
	if _, err := v.Verify("UNKNOWN", int64(10*30)); !errors.Is(err, ErrInvalidPasscode) {
		t.Fatalf("unknown code error = %v, want ErrInvalidPasscode", err)
	}
}

func TestCrossWindowSameStepReplayWithHook(t *testing.T) {
	v, err := New(Config{Secret: rfc6238SHA1Secret, Period: 30, Digits: 6, Before: 2, After: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v.generate = func(_ []byte, counter uint64) (string, error) {
		if counter == 9 {
			return "OLD", nil
		}
		return "OTHER", nil
	}

	// Step 9 is in the window around step 10 and gets consumed.
	if _, err := v.Verify("OLD", int64(10*30)); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	// One window later step 9 is still reachable (window 9..13) but the
	// exact same step cannot be accepted through this later window.
	if _, err := v.Verify("OLD", int64(11*30)); !errors.Is(err, ErrReplay) {
		t.Fatalf("later-window replay error = %v, want ErrReplay", err)
	}
	// Once step 9 leaves the window entirely it simply fails to match.
	if _, err := v.Verify("OLD", int64(12*30)); !errors.Is(err, ErrInvalidPasscode) {
		t.Fatalf("out-of-window error = %v, want ErrInvalidPasscode", err)
	}
}

func TestConcurrentLogin(t *testing.T) {
	const goroutines = 200
	v, err := New(Config{Secret: rfc6238SHA1Secret, Period: 30, Digits: 6, Before: 0, After: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	loginTime := int64(1234567890)
	code, err := v.Generate(loginTime)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := v.Verify(code, loginTime)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes, replays := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrReplay):
			replays++
		default:
			t.Fatalf("unexpected Verify error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
	if replays != goroutines-1 {
		t.Fatalf("replays = %d, want %d", replays, goroutines-1)
	}
	wantStep := uint64(loginTime) / 30
	if last, ok := v.LastStep(); !ok || last != wantStep {
		t.Fatalf("LastStep = (%d, %v), want (%d, true)", last, ok, wantStep)
	}
}

func TestWindowNearEpochDoesNotUnderflow(t *testing.T) {
	v, err := New(Config{Secret: rfc6238SHA1Secret, Period: 30, Digits: 6, Before: 5, After: 1})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	code, err := v.Generate(0)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := v.Verify(code, 0); err != nil {
		t.Fatalf("Verify at epoch: %v", err)
	}
	if _, err := v.Verify(code, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("negative time error = %v, want ErrInvalidTime", err)
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{Secret: nil}); err == nil {
		t.Fatal("New with empty secret should fail")
	}
	if _, err := New(Config{Secret: rfc6238SHA1Secret, Digits: 5}); err == nil {
		t.Fatal("New with 5 digits should fail")
	}
	if _, err := New(Config{Secret: rfc6238SHA1Secret, Digits: 9}); err == nil {
		t.Fatal("New with 9 digits should fail")
	}

	v := NewDefault(rfc6238SHA1Secret)
	if v.cfg.Period != 30 || v.cfg.Digits != 6 || v.cfg.Before != 1 || v.cfg.After != 1 {
		t.Fatalf("defaults = %+v, want period 30, digits 6, window ±1", v.cfg)
	}
}
