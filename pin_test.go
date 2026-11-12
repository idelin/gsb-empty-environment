package netsec

import (
	"errors"
	"sync"
	"testing"
)

type testCard struct {
	nvm *MemoryNVM
	cfg Config
}

func newTestCard(t *testing.T, maxPIN, maxPUK int) *testCard {
	t.Helper()
	c := &testCard{
		nvm: NewMemoryNVM(),
		cfg: Config{
			PIN:            "1234",
			PUK:            "9999",
			MaxPINAttempts: maxPIN,
			MaxPUKAttempts: maxPUK,
		},
	}
	if _, err := NewPINHandler(c.nvm, c.cfg); err != nil {
		t.Fatalf("initialise card: %v", err)
	}
	return c
}

func (c *testCard) handler(t *testing.T, nvm SlotNVM) *PINHandler {
	t.Helper()
	h, err := NewPINHandler(nvm, c.cfg)
	if err != nil {
		t.Fatalf("open card: %v", err)
	}
	return h
}

func isRetryStatus(sw uint16) bool {
	return sw&0xFFF0 == SWRetryCounterBase
}

func wrongPIN(t *testing.T, h *PINHandler, pin string) Response {
	t.Helper()
	resp, err := h.VerifyPIN(pin)
	if err != nil {
		t.Fatalf("verify PIN: %v", err)
	}
	return resp
}

func wrongPUK(t *testing.T, h *PINHandler, puk, newPIN string) Response {
	t.Helper()
	resp, err := h.ResetPIN(puk, newPIN)
	if err != nil {
		t.Fatalf("reset PIN: %v", err)
	}
	return resp
}

// TestPowerLossAtEveryPINWriteKeepsAttemptBound runs the wrong-PIN sequence
// once for every possible interrupted write. A reboot happens immediately
// after the injected fault, and the number of PIN comparisons across the
// whole run must never exceed the configured budget.
func TestPowerLossAtEveryPINWriteKeepsAttemptBound(t *testing.T) {
	const maxPIN = 3
	modes := []struct {
		name string
		mode FaultMode
	}{
		{"before", FailBeforeWrite},
		{"after", FailAfterWrite},
		{"torn", FailTornWrite},
	}

	for failAt := 1; failAt <= maxPIN+2; failAt++ {
		for _, tc := range modes {
			t.Run(tc.name, func(t *testing.T) {
				c := newTestCard(t, maxPIN, 3)
				fault := NewFaultNVM(c.nvm, failAt, tc.mode)
				h := c.handler(t, fault)

				comparisons := 0
				restarts := 0
				var last Response
				for step := 0; step < 2*maxPIN+4; step++ {
					resp, err := h.VerifyPIN("0000")
					if errors.Is(err, ErrPowerLoss) {
						restarts++
						h = c.handler(t, c.nvm)
						continue
					}
					if err != nil {
						t.Fatalf("verify PIN: %v", err)
					}
					last = resp
					if resp.SW == SWAuthenticationBlocked {
						break
					}
					if !isRetryStatus(resp.SW) {
						t.Fatalf("unexpected status %#04x", resp.SW)
					}
					comparisons++
				}

				if comparisons > maxPIN {
					t.Fatalf("failAt=%d/%s: %d PIN comparisons exceeded budget %d (restarts=%d)",
						failAt, tc.name, comparisons, maxPIN, restarts)
				}
				if last.SW != SWAuthenticationBlocked {
					t.Fatalf("failAt=%d/%s: card did not end blocked, last=%#04x", failAt, tc.name, last.SW)
				}
				pinRetries, _, err := h.RetryCounters()
				if err != nil {
					t.Fatalf("read counters: %v", err)
				}
				if pinRetries != 0 {
					t.Fatalf("failAt=%d/%s: PIN retries=%d, want 0", failAt, tc.name, pinRetries)
				}
			})
		}
	}
}

// TestPowerLossAtEveryPUKWriteKeepsAttemptBound does the same for the independent
// PUK budget after the PIN has already been blocked.
func TestPowerLossAtEveryPUKWriteKeepsAttemptBound(t *testing.T) {
	const (
		maxPIN = 2
		maxPUK = 3
	)
	modes := []struct {
		name string
		mode FaultMode
	}{
		{"before", FailBeforeWrite},
		{"after", FailAfterWrite},
		{"torn", FailTornWrite},
	}

	for failAt := 1; failAt <= maxPUK+2; failAt++ {
		for _, tc := range modes {
			t.Run(tc.name, func(t *testing.T) {
				c := newTestCard(t, maxPIN, maxPUK)
				h := c.handler(t, c.nvm)
				for i := 0; i < maxPIN; i++ {
					_ = wrongPIN(t, h, "0000")
				}

				fault := NewFaultNVM(c.nvm, failAt, tc.mode)
				h = c.handler(t, fault)
				comparisons := 0
				restarts := 0
				var last Response
				for step := 0; step < 2*maxPUK+4; step++ {
					resp, err := h.ResetPIN("0000", "5678")
					if errors.Is(err, ErrPowerLoss) {
						restarts++
						h = c.handler(t, c.nvm)
						continue
					}
					if err != nil {
						t.Fatalf("reset PIN: %v", err)
					}
					last = resp
					if resp.SW == SWAuthenticationBlocked {
						break
					}
					if !isRetryStatus(resp.SW) {
						t.Fatalf("unexpected status %#04x", resp.SW)
					}
					comparisons++
				}

				if comparisons > maxPUK {
					t.Fatalf("failAt=%d/%s: %d PUK comparisons exceeded budget %d (restarts=%d)",
						failAt, tc.name, comparisons, maxPUK, restarts)
				}
				if last.SW != SWAuthenticationBlocked {
					t.Fatalf("failAt=%d/%s: PUK did not end blocked, last=%#04x", failAt, tc.name, last.SW)
				}
				// Even the correct PUK must be rejected once its counter is zero.
				resp := wrongPUK(t, h, "9999", "5678")
				if resp.SW != SWAuthenticationBlocked {
					t.Fatalf("failAt=%d/%s: correct PUK accepted after exhaustion: %#04x", failAt, tc.name, resp.SW)
				}
				if _, pukRetries, err := h.RetryCounters(); err != nil || pukRetries != 0 {
					t.Fatalf("failAt=%d/%s: PUK retries=%d err=%v, want 0", failAt, tc.name, pukRetries, err)
				}
			})
		}
	}
}

func TestSuccessfulPINRestoresRetryBudget(t *testing.T) {
	c := newTestCard(t, 3, 3)
	h := c.handler(t, c.nvm)

	for want := 2; want >= 1; want-- {
		resp := wrongPIN(t, h, "0000")
		if !isRetryStatus(resp.SW) || resp.PINRetries != want {
			t.Fatalf("wrong PIN: status=%#04x retries=%d, want %#04x/%d", resp.SW, resp.PINRetries, retryStatus(uint8(want)), want)
		}
	}

	resp, err := h.VerifyPIN("1234")
	if err != nil {
		t.Fatalf("correct PIN: %v", err)
	}
	if resp.SW != SWOK || resp.PINRetries != 3 {
		t.Fatalf("correct PIN: status=%#04x retries=%d, want 9000/3", resp.SW, resp.PINRetries)
	}

	// The restored budget is usable again.
	for i := 0; i < 3; i++ {
		resp = wrongPIN(t, h, "0000")
	}
	if resp.SW != retryStatus(0) || resp.PINRetries != 0 {
		t.Fatalf("after restored budget: status=%#04x retries=%d, want 63C0/0", resp.SW, resp.PINRetries)
	}

	resp = wrongPIN(t, h, "1234")
	if resp.SW != SWAuthenticationBlocked {
		t.Fatalf("correct PIN accepted while blocked: %#04x", resp.SW)
	}
}

func TestPUKResetRecoversBlockedPINAndPersistsNewPIN(t *testing.T) {
	c := newTestCard(t, 2, 3)
	h := c.handler(t, c.nvm)

	for i := 0; i < 2; i++ {
		_ = wrongPIN(t, h, "0000")
	}
	if resp := wrongPIN(t, h, "1234"); resp.SW != SWAuthenticationBlocked {
		t.Fatalf("correct PIN accepted while blocked: %#04x", resp.SW)
	}

	// A wrong PUK consumes its own budget but does not recover the PIN.
	resp := wrongPUK(t, h, "0000", "5678")
	if !isRetryStatus(resp.SW) || resp.PUKRetries != 2 {
		t.Fatalf("wrong PUK: status=%#04x retries=%d, want 63C2/2", resp.SW, resp.PUKRetries)
	}

	resp, err := h.ResetPIN("9999", "5678")
	if err != nil {
		t.Fatalf("correct PUK: %v", err)
	}
	if resp.SW != SWOK || resp.PINRetries != 2 || resp.PUKRetries != 3 {
		t.Fatalf("correct PUK: status=%#04x pin=%d puk=%d, want 9000/2/3", resp.SW, resp.PINRetries, resp.PUKRetries)
	}

	// The old PIN is gone and the new PIN restores the PIN budget.
	resp = wrongPIN(t, h, "1234")
	if !isRetryStatus(resp.SW) || resp.PINRetries != 1 {
		t.Fatalf("old PIN: status=%#04x retries=%d, want 63C1/1", resp.SW, resp.PINRetries)
	}
	resp, err = h.VerifyPIN("5678")
	if err != nil {
		t.Fatalf("new PIN: %v", err)
	}
	if resp.SW != SWOK || resp.PINRetries != 2 {
		t.Fatalf("new PIN: status=%#04x retries=%d, want 9000/2", resp.SW, resp.PINRetries)
	}

	// A changed PIN must survive a host reboot, even if the host still knows the
	// factory PIN from its configuration.
	restarted := c.handler(t, c.nvm)
	resp, err = restarted.VerifyPIN("5678")
	if err != nil {
		t.Fatalf("new PIN after restart: %v", err)
	}
	if resp.SW != SWOK || resp.PINRetries != 2 {
		t.Fatalf("new PIN after restart: status=%#04x retries=%d, want 9000/2", resp.SW, resp.PINRetries)
	}
}

func TestPUKExhaustionIsPermanent(t *testing.T) {
	const (
		maxPIN = 2
		maxPUK = 3
	)
	c := newTestCard(t, maxPIN, maxPUK)
	h := c.handler(t, c.nvm)

	// A successful PUK use restores the PUK budget as well.
	resp := wrongPUK(t, h, "0000", "5678")
	if !isRetryStatus(resp.SW) || resp.PUKRetries != 2 {
		t.Fatalf("wrong PUK: status=%#04x retries=%d, want 63C2/2", resp.SW, resp.PUKRetries)
	}
	resp, err := h.ResetPIN("9999", "5678")
	if err != nil {
		t.Fatalf("correct PUK: %v", err)
	}
	if resp.SW != SWOK || resp.PUKRetries != maxPUK {
		t.Fatalf("correct PUK: status=%#04x retries=%d, want 9000/%d", resp.SW, resp.PUKRetries, maxPUK)
	}

	for i := 0; i < maxPIN; i++ {
		_ = wrongPIN(t, h, "0000")
	}
	for want := maxPUK - 1; want >= 0; want-- {
		resp = wrongPUK(t, h, "0000", "5678")
		if !isRetryStatus(resp.SW) || resp.PUKRetries != want {
			t.Fatalf("wrong PUK: status=%#04x retries=%d, want %#04x/%d", resp.SW, resp.PUKRetries, retryStatus(uint8(want)), want)
		}
	}

	resp = wrongPUK(t, h, "9999", "5678")
	if resp.SW != SWAuthenticationBlocked {
		t.Fatalf("correct PUK accepted after exhaustion: %#04x", resp.SW)
	}
	for _, pin := range []string{"1234", "5678"} {
		if resp := wrongPIN(t, h, pin); resp.SW != SWAuthenticationBlocked {
			t.Fatalf("PIN %q accepted after both counters exhausted: %#04x", pin, resp.SW)
		}
	}
}

func TestPowerLossDuringSuccessfulVerify(t *testing.T) {
	const maxPIN = 3
	modes := []struct {
		name string
		mode FaultMode
	}{
		{"before", FailBeforeWrite},
		{"after", FailAfterWrite},
		{"torn", FailTornWrite},
	}

	// Write 1 is the pre-comparison decrement; write 2 is the success restore.
	for failAt := 1; failAt <= 3; failAt++ {
		for _, tc := range modes {
			t.Run(tc.name, func(t *testing.T) {
				c := newTestCard(t, maxPIN, 3)
				h := c.handler(t, c.nvm)
				_ = wrongPIN(t, h, "0000")
				_ = wrongPIN(t, h, "0000")

				fault := NewFaultNVM(c.nvm, failAt, tc.mode)
				h = c.handler(t, fault)
				resp, err := h.VerifyPIN("1234")
				switch {
				case err == nil:
					if resp.SW != SWOK || resp.PINRetries != maxPIN {
						t.Fatalf("failAt=%d/%s: status=%#04x retries=%d, want 9000/%d", failAt, tc.name, resp.SW, resp.PINRetries, maxPIN)
					}
				case errors.Is(err, ErrPowerLoss):
					h = c.handler(t, c.nvm)
					pinRetries, _, counterErr := h.RetryCounters()
					if counterErr != nil {
						t.Fatalf("read counters after reboot: %v", counterErr)
					}
					if pinRetries == 0 {
						// The last attempt was consumed durably; fail closed and use PUK.
						if resp := wrongPIN(t, h, "1234"); resp.SW != SWAuthenticationBlocked {
							t.Fatalf("failAt=%d/%s: blocked PIN accepted after reboot: %#04x", failAt, tc.name, resp.SW)
						}
						resp, err = h.ResetPIN("9999", "1234")
						if err != nil || resp.SW != SWOK || resp.PINRetries != maxPIN {
							t.Fatalf("failAt=%d/%s: PUK recovery status=%#04x retries=%d err=%v", failAt, tc.name, resp.SW, resp.PINRetries, err)
						}
					} else {
						resp, err = h.VerifyPIN("1234")
						if err != nil || resp.SW != SWOK || resp.PINRetries != maxPIN {
							t.Fatalf("failAt=%d/%s: retry status=%#04x retries=%d err=%v", failAt, tc.name, resp.SW, resp.PINRetries, err)
						}
					}
				default:
					t.Fatalf("failAt=%d/%s: unexpected error: %v", failAt, tc.name, err)
				}
			})
		}
	}
}

func TestPowerLossDuringPUKReset(t *testing.T) {
	const (
		maxPIN = 2
		maxPUK = 3
	)
	modes := []struct {
		name string
		mode FaultMode
	}{
		{"before", FailBeforeWrite},
		{"after", FailAfterWrite},
		{"torn", FailTornWrite},
	}

	// Write 1 is the PUK decrement; write 2 installs the new PIN and restores
	// both budgets.
	for failAt := 1; failAt <= 3; failAt++ {
		for _, tc := range modes {
			t.Run(tc.name, func(t *testing.T) {
				c := newTestCard(t, maxPIN, maxPUK)
				h := c.handler(t, c.nvm)
				for i := 0; i < maxPIN; i++ {
					_ = wrongPIN(t, h, "0000")
				}

				fault := NewFaultNVM(c.nvm, failAt, tc.mode)
				h = c.handler(t, fault)
				resp, err := h.ResetPIN("9999", "5678")
				switch {
				case err == nil:
					if resp.SW != SWOK || resp.PINRetries != maxPIN || resp.PUKRetries != maxPUK {
						t.Fatalf("failAt=%d/%s: status=%#04x pin=%d puk=%d, want 9000/%d/%d",
							failAt, tc.name, resp.SW, resp.PINRetries, resp.PUKRetries, maxPIN, maxPUK)
					}
				case errors.Is(err, ErrPowerLoss):
					h = c.handler(t, c.nvm)
					pinRetries, pukRetries, counterErr := h.RetryCounters()
					if counterErr != nil {
						t.Fatalf("read counters after reboot: %v", counterErr)
					}
					if pinRetries == maxPIN {
						resp, err = h.VerifyPIN("5678")
						if err != nil || resp.SW != SWOK {
							t.Fatalf("failAt=%d/%s: reset survived but new PIN failed: status=%#04x err=%v",
								failAt, tc.name, resp.SW, err)
						}
					} else {
						if pukRetries == 0 {
							t.Fatalf("failAt=%d/%s: PUK exhausted before comparison, cannot test recovery", failAt, tc.name)
						}
						resp, err = h.ResetPIN("9999", "5678")
						if err != nil || resp.SW != SWOK || resp.PINRetries != maxPIN || resp.PUKRetries != maxPUK {
							t.Fatalf("failAt=%d/%s: retry after reboot status=%#04x pin=%d puk=%d err=%v",
								failAt, tc.name, resp.SW, resp.PINRetries, resp.PUKRetries, err)
						}
					}
				default:
					t.Fatalf("failAt=%d/%s: unexpected error: %v", failAt, tc.name, err)
				}
			})
		}
	}
}

type verifyResult struct {
	resp Response
	err  error
}

func TestConcurrentVerifiesConsumeOnlyOneRemainingAttempt(t *testing.T) {
	const maxPIN = 5
	c := newTestCard(t, maxPIN, 3)
	h := c.handler(t, c.nvm)
	for i := 0; i < maxPIN-1; i++ {
		_ = wrongPIN(t, h, "0000")
	}

	start := make(chan struct{})
	results := make(chan verifyResult, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := h.VerifyPIN("0000")
			results <- verifyResult{resp: resp, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	compared, blocked := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent verify: %v", result.err)
		}
		switch {
		case isRetryStatus(result.resp.SW):
			compared++
		case result.resp.SW == SWAuthenticationBlocked:
			blocked++
		default:
			t.Fatalf("unexpected concurrent status %#04x", result.resp.SW)
		}
	}
	if compared != 1 || blocked != 1 {
		t.Fatalf("concurrent verifies: compared=%d blocked=%d, want 1/1", compared, blocked)
	}
	if pinRetries, _, err := h.RetryCounters(); err != nil || pinRetries != 0 {
		t.Fatalf("concurrent verifies: remaining=%d err=%v, want 0", pinRetries, err)
	}
}

func TestConcurrentPUKResetsConsumeOnlyOneRemainingAttempt(t *testing.T) {
	const maxPIN = 2
	c := newTestCard(t, maxPIN, 1)
	h := c.handler(t, c.nvm)
	for i := 0; i < maxPIN; i++ {
		_ = wrongPIN(t, h, "0000")
	}

	start := make(chan struct{})
	results := make(chan verifyResult, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, err := h.ResetPIN("0000", "5678")
			results <- verifyResult{resp: resp, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	compared, blocked := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent PUK reset: %v", result.err)
		}
		switch {
		case isRetryStatus(result.resp.SW):
			compared++
		case result.resp.SW == SWAuthenticationBlocked:
			blocked++
		default:
			t.Fatalf("unexpected concurrent PUK status %#04x", result.resp.SW)
		}
	}
	if compared != 1 || blocked != 1 {
		t.Fatalf("concurrent PUK resets: compared=%d blocked=%d, want 1/1", compared, blocked)
	}
	if _, pukRetries, err := h.RetryCounters(); err != nil || pukRetries != 0 {
		t.Fatalf("concurrent PUK resets: remaining=%d err=%v, want 0", pukRetries, err)
	}
	if resp := wrongPUK(t, h, "9999", "5678"); resp.SW != SWAuthenticationBlocked {
		t.Fatalf("correct PUK accepted after concurrent exhaustion: %#04x", resp.SW)
	}
}

func TestAPDUDispatch(t *testing.T) {
	c := newTestCard(t, 3, 3)
	h := c.handler(t, c.nvm)

	resp, err := h.HandleAPDU(APDU{INS: INSVerifyPIN, Data: []byte("0000")})
	if err != nil {
		t.Fatalf("verify APDU: %v", err)
	}
	if resp.SW != retryStatus(2) || resp.PINRetries != 2 {
		t.Fatalf("verify APDU: status=%#04x retries=%d, want 63C2/2", resp.SW, resp.PINRetries)
	}

	// Too short to contain a PUK and a new PIN: reject without spending a PUK try.
	resp, err = h.HandleAPDU(APDU{INS: INSResetRetryCounter, Data: []byte("9999")})
	if err != nil {
		t.Fatalf("short reset APDU: %v", err)
	}
	if resp.SW != SWWrongData || resp.PUKRetries != 3 {
		t.Fatalf("short reset APDU: status=%#04x puk=%d, want 6A80/3", resp.SW, resp.PUKRetries)
	}

	_ = wrongPIN(t, h, "0000")
	_ = wrongPIN(t, h, "0000")
	resp, err = h.HandleAPDU(APDU{INS: INSResetRetryCounter, Data: []byte("9999" + "5678")})
	if err != nil {
		t.Fatalf("reset APDU: %v", err)
	}
	if resp.SW != SWOK || resp.PINRetries != 3 || resp.PUKRetries != 3 {
		t.Fatalf("reset APDU: status=%#04x pin=%d puk=%d, want 9000/3/3", resp.SW, resp.PINRetries, resp.PUKRetries)
	}
}

func TestCorruptStateFailsClosed(t *testing.T) {
	c := newTestCard(t, 3, 3)
	for slot := 0; slot < slotCount; slot++ {
		if err := c.nvm.WriteSlot(slot, []byte("garbage")); err != nil {
			t.Fatalf("corrupt slot %d: %v", slot, err)
		}
	}
	if _, err := NewPINHandler(c.nvm, c.cfg); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("open corrupt card: err=%v, want ErrCorruptState", err)
	}
}

func TestPowerLossDuringInitializationCanReboot(t *testing.T) {
	const maxPIN = 3
	modes := []struct {
		name string
		mode FaultMode
	}{
		{"before", FailBeforeWrite},
		{"after", FailAfterWrite},
		{"torn", FailTornWrite},
	}
	cfg := Config{
		PIN:            "1234",
		PUK:            "9999",
		MaxPINAttempts: maxPIN,
		MaxPUKAttempts: 3,
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			nvm := NewMemoryNVM()
			fault := NewFaultNVM(nvm, 1, tc.mode)
			if _, err := NewPINHandler(fault, cfg); !errors.Is(err, ErrPowerLoss) {
				t.Fatalf("initialise with fault: err=%v, want ErrPowerLoss", err)
			}

			h, err := NewPINHandler(nvm, cfg)
			if err != nil {
				t.Fatalf("reboot after interrupted initialisation: %v", err)
			}
			comparisons := 0
			var last Response
			for i := 0; i < 2*maxPIN; i++ {
				last = wrongPIN(t, h, "0000")
				if last.SW == SWAuthenticationBlocked {
					break
				}
				if !isRetryStatus(last.SW) {
					t.Fatalf("unexpected status %#04x", last.SW)
				}
				comparisons++
			}
			if comparisons != maxPIN {
				t.Fatalf("after reboot: comparisons=%d, want %d", comparisons, maxPIN)
			}
			if last.SW != SWAuthenticationBlocked {
				t.Fatalf("after reboot: last status=%#04x, want blocked", last.SW)
			}
		})
	}
}
