// Package totp implements a replay-protected TOTP verifier for login hosts.
//
// The verifier tolerates device clock drift by checking the passcode against
// every time step inside a configurable before/after window (RFC 6238, HMAC
// based). To prevent a passcode captured from one window from being accepted
// again through a later window, each Verifier keeps the counter of the last
// accepted time step and rejects any matched step that is not strictly newer.
//
// A Verifier is scoped to a single shared secret (typically one enrolled
// device or user). Create one Verifier per secret; the verifier is safe for
// concurrent use.
package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Errors returned by Verify. Callers can compare with errors.Is.
var (
	// ErrInvalidPasscode means no step in the allowed window produced the
	// supplied passcode.
	ErrInvalidPasscode = errors.New("totp: passcode does not match any allowed time step")

	// ErrReplay means the passcode matched a window step, but that step has
	// already been consumed: the matched step is not strictly newer than the
	// last accepted step. Rejection takes precedence over any other candidate.
	ErrReplay = errors.New("totp: passcode replayed for an already accepted time step")

	// ErrInvalidTime means the supplied UNIX timestamp is negative.
	ErrInvalidTime = errors.New("totp: unix time must not be negative")
)

// Config configures a Verifier. The zero value is not valid; use New, which
// applies RFC 6238 defaults to unspecified fields.
type Config struct {
	// Secret is the shared HMAC key (RFC 6238 test mode uses the raw key).
	Secret []byte

	// Period is the time-step size in seconds. Defaults to 30.
	Period uint64

	// Digits is the passcode length, must be between 6 and 8. Defaults to 6.
	Digits int

	// Before is how many past steps are accepted (clock drift tolerance).
	// Defaults to 1.
	Before uint

	// After is how many future steps are accepted. Defaults to 1.
	After uint
}

// Verifier verifies TOTP passcodes for one shared secret and records the last
// accepted time step so the same step can never be accepted twice.
type Verifier struct {
	cfg Config

	mu       sync.Mutex
	last     uint64
	hasLast  bool
	generate func(secret []byte, counter uint64) (string, error)
}

// New creates a Verifier for cfg and fills RFC 6238 defaults (30-second
// period, 6 digits, one step of drift tolerance on each side). It returns an
// error for an empty secret, a zero period or an unsupported digit count.
func New(cfg Config) (*Verifier, error) {
	if len(cfg.Secret) == 0 {
		return nil, errors.New("totp: secret must not be empty")
	}
	if cfg.Period == 0 {
		cfg.Period = 30
	}
	if cfg.Digits == 0 {
		cfg.Digits = 6
	}
	if cfg.Before == 0 {
		cfg.Before = 1
	}
	if cfg.After == 0 {
		cfg.After = 1
	}
	if cfg.Digits < 6 || cfg.Digits > 8 {
		return nil, fmt.Errorf("totp: digits must be between 6 and 8, got %d", cfg.Digits)
	}

	v := &Verifier{cfg: cfg}
	v.generate = v.rfc6238
	return v, nil
}

// NewDefault creates a Verifier with RFC 6238 defaults: 30-second steps,
// 6-digit passcodes and a one-step before/after window.
func NewDefault(secret []byte) *Verifier {
	v, err := New(Config{Secret: secret})
	if err != nil {
		panic(err)
	}
	return v
}

// Verify checks passcode against every step in the allowed window around the
// step containing unixTime (UNIX seconds, T0 = 0).
//
// All matching steps are considered and the winner is the step closest to the
// current step; among equally close steps the earlier (smaller) one wins. On
// success the winning step is atomically recorded as the last accepted step.
// A winning step that is not strictly newer than the last accepted step is
// rejected with ErrReplay and does not change the recorded state.
//
// It returns the matched step and nil error on success, the candidate step
// and ErrReplay on a replay, or zero and ErrInvalidPasscode when nothing
// matches.
func (v *Verifier) Verify(passcode string, unixTime int64) (uint64, error) {
	if unixTime < 0 {
		return 0, ErrInvalidTime
	}
	current := uint64(unixTime) / v.cfg.Period

	matched := v.matchWindow(passcode, current)
	if len(matched) == 0 {
		return 0, ErrInvalidPasscode
	}
	step := chooseStep(matched, current)

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.hasLast && step <= v.last {
		return step, ErrReplay
	}
	v.last = step
	v.hasLast = true
	return step, nil
}

// matchWindow recomputes the expected passcode for every step in the
// before/after window and returns all steps whose code equals passcode. HMAC
// work happens without holding the lock; only the state decision is locked.
func (v *Verifier) matchWindow(passcode string, current uint64) []uint64 {
	var matched []uint64
	for offset := -int(v.cfg.Before); offset <= int(v.cfg.After); offset++ {
		step, ok := shiftStep(current, offset)
		if !ok {
			continue
		}
		code, err := v.generate(v.cfg.Secret, step)
		if err != nil {
			continue
		}
		if hmac.Equal([]byte(passcode), []byte(code)) {
			matched = append(matched, step)
		}
	}
	return matched
}

// shiftStep returns current+offset, guarding against unsigned underflow for
// negative offsets.
func shiftStep(current uint64, offset int) (uint64, bool) {
	if offset >= 0 {
		return current + uint64(offset), true
	}
	back := uint64(-offset)
	if back > current {
		return 0, false
	}
	return current - back, true
}

// chooseStep picks the matching step nearest to current. On equal distance
// the earlier (smaller) step wins, matching the "nearest, then earlier"
// selection rule.
func chooseStep(matched []uint64, current uint64) uint64 {
	sorted := append([]uint64(nil), matched...)
	// Stable sort keeps the original (window traversal) order when the
	// comparator reports equality, so the tie-break toward the earlier step
	// is deterministic.
	sort.SliceStable(sorted, func(i, j int) bool {
		di := distance(sorted[i], current)
		dj := distance(sorted[j], current)
		if di != dj {
			return di < dj
		}
		return sorted[i] < sorted[j]
	})
	return sorted[0]
}

func distance(step, current uint64) uint64 {
	// Use a 128-bit-safe subtraction so counters near uint64 boundaries do
	// not wrap through int64 and invert the ordering.
	if step >= current {
		return step - current
	}
	return current - step
}

// Generate returns the TOTP passcode for the step containing unixTime using
// the verifier's secret, period and digit count.
func (v *Verifier) Generate(unixTime int64) (string, error) {
	if unixTime < 0 {
		return "", ErrInvalidTime
	}
	return v.generate(v.cfg.Secret, uint64(unixTime)/v.cfg.Period)
}

// LastStep returns the last accepted time step. The boolean is false until the
// first successful verification.
func (v *Verifier) LastStep() (uint64, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.last, v.hasLast
}

// Reset clears the recorded last accepted step, allowing every step to be
// accepted again.
func (v *Verifier) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.last = 0
	v.hasLast = false
}

// rfc6238 derives the passcode per RFC 6238/RFC 4226: HMAC-SHA1 over the
// 8-byte big-endian counter followed by dynamic truncation and zero padding.
func (v *Verifier) rfc6238(secret []byte, counter uint64) (string, error) {
	var counterBuf [8]byte
	binary.BigEndian.PutUint64(counterBuf[:], counter)

	mac := hmac.New(sha1.New, secret)
	if _, err := mac.Write(counterBuf[:]); err != nil {
		return "", err
	}
	sum := mac.Sum(nil)

	offset := int(sum[len(sum)-1] & 0x0f)
	value := (int(sum[offset])&0x7f)<<24 |
		int(sum[offset+1])<<16 |
		int(sum[offset+2])<<8 |
		int(sum[offset+3])

	return fmt.Sprintf("%0*d", v.cfg.Digits, value%pow10(v.cfg.Digits)), nil
}

func pow10(n int) int {
	result := 1
	for i := 0; i < n; i++ {
		result *= 10
	}
	return result
}
