package netsec

import (
	"errors"
	"testing"
)

// fakeBackend records the restriction and register calls a Ring issues. Its
// mutable state stands in for kernel-side bookkeeping so the lifecycle and
// policy checks can be exercised on non-Linux platforms.
type fakeBackend struct {
	disabled      bool
	restricted    bool
	enabled       bool
	registerCalls []uint16
	entries       []restrictionEntry
	pol           *policy
	closed        bool
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{disabled: true}
}

func (f *fakeBackend) registerRestrictions(entries []restrictionEntry) error {
	if !f.disabled {
		return ErrRingNotDisabled
	}
	if f.restricted {
		return ErrRestrictionsAlreadyApplied
	}
	f.entries = entries
	f.restricted = true
	return nil
}

func (f *fakeBackend) enableRings() error {
	f.disabled = false
	f.enabled = true
	return nil
}

func (f *fakeBackend) registerFiles(fds []int) error {
	op := uint16(RegisterFiles)
	f.registerCalls = append(f.registerCalls, op)
	if f.pol != nil {
		if err := f.pol.checkRegister(op); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeBackend) close() error {
	f.closed = true
	return nil
}

func newFakeRing(f *fakeBackend, pol *policy) *Ring {
	r := newRing(f)
	r.pol = pol
	f.pol = pol
	return r
}

func testConfig() Config {
	return Config{
		AllowedRegisterOpcodes: []RegisterOpcode{RegisterProbe},
		AllowedSQEOpcodes:      []SQEOpcode{OpNop, OpReadv},
		AllowedSQEFlags:        SQEFlagIODrain | SQEFlagIOLink,
		RequiredSQEFlags:       SQEFlagIODrain,
	}
}

func assertErrorIs(t *testing.T, got error, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

// Scenario 1: restrictions may only be registered while the ring is disabled.
func TestApplyRestrictionsOnEnabledRingRejected(t *testing.T) {
	f := newFakeBackend()
	r := newFakeRing(f, nil)
	cfg := testConfig()

	if err := r.ApplyRestrictions(cfg); err != nil {
		t.Fatalf("first ApplyRestrictions: %v", err)
	}
	if err := r.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	assertErrorIs(t, r.ApplyRestrictions(cfg), ErrRingNotDisabled)
}

// Scenario 2: restriction registration is a one-shot operation.
func TestApplyRestrictionsTwiceRejected(t *testing.T) {
	f := newFakeBackend()
	r := newFakeRing(f, nil)
	cfg := testConfig()

	if err := r.ApplyRestrictions(cfg); err != nil {
		t.Fatalf("first ApplyRestrictions: %v", err)
	}
	assertErrorIs(t, r.ApplyRestrictions(cfg), ErrRestrictionsAlreadyApplied)
}

func TestEnableBeforeRestrictionsRejected(t *testing.T) {
	r := newFakeRing(newFakeBackend(), nil)
	assertErrorIs(t, r.Enable(), ErrRestrictionsNotApplied)
}

// Scenario 3: an SQE missing a required flag is rejected after enablement.
func TestSQEMissingRequiredFlagRejected(t *testing.T) {
	f := newFakeBackend()
	r := newFakeRing(f, nil)
	if err := r.ApplyRestrictions(testConfig()); err != nil {
		t.Fatalf("ApplyRestrictions: %v", err)
	}
	if err := r.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	assertErrorIs(t, r.CheckSQE(OpNop, 0), ErrRequiredFlagMissing)
	if err := r.CheckSQE(OpNop, SQEFlagIODrain); err != nil {
		t.Fatalf("SQE with required flag should pass: %v", err)
	}
}

// Scenario 4: any flag bit outside the allowed mask is rejected.
func TestSQEExtraneousFlagRejected(t *testing.T) {
	f := newFakeBackend()
	r := newFakeRing(f, nil)
	if err := r.ApplyRestrictions(testConfig()); err != nil {
		t.Fatalf("ApplyRestrictions: %v", err)
	}
	if err := r.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	flags := SQEFlagIODrain | SQEFlagFixedFile
	assertErrorIs(t, r.CheckSQE(OpNop, flags), ErrFlagNotAllowed)
}

func TestSQEOpcodeWhitelist(t *testing.T) {
	f := newFakeBackend()
	r := newFakeRing(f, nil)
	if err := r.ApplyRestrictions(testConfig()); err != nil {
		t.Fatalf("ApplyRestrictions: %v", err)
	}
	if err := r.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	assertErrorIs(t, r.CheckSQE(OpOpenAt, SQEFlagIODrain), ErrOpcodeNotAllowed)
	if err := r.CheckSQE(OpReadv, SQEFlagIODrain|SQEFlagIOLink); err != nil {
		t.Fatalf("whitelisted SQE should pass: %v", err)
	}
}

// Scenario 5: after enablement, registering a new file table is allowed only
// when RegisterFiles is part of the register-opcode whitelist.
func TestRegisterFilesWhitelist(t *testing.T) {
	f := newFakeBackend()
	r := newFakeRing(f, nil)
	cfg := testConfig()
	if err := r.ApplyRestrictions(cfg); err != nil {
		t.Fatalf("ApplyRestrictions: %v", err)
	}
	if err := r.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	assertErrorIs(t, r.RegisterFiles([]int{0}), ErrRegisterOpcodeNotAllowed)
	if len(f.registerCalls) != 0 {
		t.Fatalf("rejected register must not reach the backend, got %v", f.registerCalls)
	}

	allowed := cfg
	allowed.AllowedRegisterOpcodes = []RegisterOpcode{RegisterFiles}
	f2 := newFakeBackend()
	r2 := newFakeRing(f2, nil)
	if err := r2.ApplyRestrictions(allowed); err != nil {
		t.Fatalf("ApplyRestrictions: %v", err)
	}
	if err := r2.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := r2.RegisterFiles([]int{0, 1}); err != nil {
		t.Fatalf("whitelisted RegisterFiles: %v", err)
	}
	if len(f2.registerCalls) != 1 {
		t.Fatalf("backend got %d register calls, want 1", len(f2.registerCalls))
	}
}

func TestConfigValidation(t *testing.T) {
	if _, err := (Config{}).build(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty config: got %v, want ErrInvalidConfig", err)
	}

	bad := testConfig()
	bad.RequiredSQEFlags = SQEFlagPoll
	_, err := bad.build()
	assertErrorIs(t, err, ErrInvalidConfig)

	pol, err := testConfig().build()
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if _, ok := pol.registerSet[uint16(RegisterEnableRings)]; !ok {
		t.Fatal("RegisterEnableRings must be added to the register whitelist")
	}
}

func TestPolicyEntries(t *testing.T) {
	pol, err := testConfig().build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	entries := pol.entries()

	var types []RestrictionType
	var allowedFlags, requiredFlags int
	for _, e := range entries {
		types = append(types, e.opType)
		switch e.opType {
		case RestrictionSQEFlagsAllowed:
			allowedFlags++
			if e.flags != uint64(SQEFlagIODrain|SQEFlagIOLink) {
				t.Fatalf("allowed flags entry = %#x", e.flags)
			}
		case RestrictionSQEFlagsRequired:
			requiredFlags++
			if e.flags != uint64(SQEFlagIODrain) {
				t.Fatalf("required flags entry = %#x", e.flags)
			}
		}
	}
	if allowedFlags != 1 || requiredFlags != 1 {
		t.Fatalf("flag restriction counts = %d/%d, want 1/1", allowedFlags, requiredFlags)
	}

	// Zero required flags must not emit a required-flags restriction.
	noRequired := testConfig()
	noRequired.RequiredSQEFlags = 0
	pol2, err := noRequired.build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, e := range pol2.entries() {
		if e.opType == RestrictionSQEFlagsRequired {
			t.Fatal("unexpected required-flags restriction when none configured")
		}
	}
}

func TestFullLifecycleAndClose(t *testing.T) {
	f := newFakeBackend()
	r := newFakeRing(f, nil)

	if r.State() != StateCreated {
		t.Fatalf("initial state = %d, want StateCreated", r.State())
	}
	assertErrorIs(t, r.CheckSQE(OpNop, 0), ErrRestrictionsNotApplied)
	assertErrorIs(t, r.CheckRegisterOp(RegisterProbe), ErrRestrictionsNotApplied)

	if err := r.ApplyRestrictions(testConfig()); err != nil {
		t.Fatalf("ApplyRestrictions: %v", err)
	}
	if r.State() != StateRestricted {
		t.Fatalf("state = %d, want StateRestricted", r.State())
	}
	// Policy is live for checks even while the ring is still disabled.
	if err := r.CheckSQE(OpNop, SQEFlagIODrain); err != nil {
		t.Fatalf("CheckSQE while restricted: %v", err)
	}
	assertErrorIs(t, r.RegisterFiles(nil), ErrRingNotEnabled)

	if err := r.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !f.enabled {
		t.Fatal("backend was not told to enable the ring")
	}
	if r.State() != StateEnabled {
		t.Fatalf("state = %d, want StateEnabled", r.State())
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !f.closed {
		t.Fatal("backend was not closed")
	}
	assertErrorIs(t, r.Close(), ErrRingClosed)
	assertErrorIs(t, r.CheckSQE(OpNop, 0), ErrRingClosed)
}

func TestPolicyChecks(t *testing.T) {
	pol, err := testConfig().build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := pol.checkRegister(uint16(RegisterProbe)); err != nil {
		t.Fatalf("RegisterProbe should be allowed: %v", err)
	}
	if err := pol.checkRegister(uint16(RegisterEnableRings)); err != nil {
		t.Fatalf("RegisterEnableRings should be implicitly allowed: %v", err)
	}
	if err := pol.checkRegister(uint16(RegisterFiles)); !errors.Is(err, ErrRegisterOpcodeNotAllowed) {
		t.Fatalf("RegisterFiles: got %v, want ErrRegisterOpcodeNotAllowed", err)
	}

	cases := []struct {
		name  string
		op    SQEOpcode
		flags SQEFlag
		want  error
	}{
		{"allowed", OpNop, SQEFlagIODrain, nil},
		{"bad opcode", OpClose, SQEFlagIODrain, ErrOpcodeNotAllowed},
		{"extra flag", OpNop, SQEFlagIODrain | SQEFlagPoll, ErrFlagNotAllowed},
		{"missing required", OpNop, SQEFlagIOLink, ErrRequiredFlagMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pol.checkSQE(uint16(tc.op), uint64(tc.flags))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("got %v, want nil", err)
				}
				return
			}
			assertErrorIs(t, err, tc.want)
		})
	}
}
