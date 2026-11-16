package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0274"
)

func testRestrictions() netsec.Restrictions {
	return netsec.Restrictions{
		AllowedRegisterOps: []netsec.RegisterOpcode{
			netsec.RegisterFilesUpdate,
		},
		AllowedSQEOps: []netsec.Opcode{
			netsec.OpRead,
			netsec.OpWrite,
		},
		AllowedFlags:  netsec.SQEFlagFixedFile | netsec.SQEFlagIODrain,
		RequiredFlags: netsec.SQEFlagFixedFile,
	}
}

func enabledRing(t *testing.T) *netsec.Ring {
	t.Helper()

	ring, err := netsec.CreateRing()
	if err != nil {
		t.Fatalf("CreateRing: %v", err)
	}
	if !ring.Disabled() {
		t.Fatal("ring was not created disabled")
	}
	if err := ring.RegisterRestrictions(testRestrictions()); err != nil {
		t.Fatalf("RegisterRestrictions: %v", err)
	}
	if err := ring.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	return ring
}

func requireErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

func TestRingStartsDisabled(t *testing.T) {
	ring := netsec.NewRing()
	if !ring.Disabled() || ring.Enabled() {
		t.Fatal("NewRing must return a disabled ring")
	}
	requireErrorIs(t, ring.Submit(netsec.OpRead, netsec.SQEFlagFixedFile), netsec.ErrRingDisabled)
	requireErrorIs(t, ring.Register(netsec.RegisterFiles), netsec.ErrRingDisabled)
	requireErrorIs(t, ring.Enable(), netsec.ErrRestrictionsRequired)
}

func TestRegisterRestrictionsRequiresDisabledRing(t *testing.T) {
	ring := enabledRing(t)

	err := ring.RegisterRestrictions(testRestrictions())
	requireErrorIs(t, err, netsec.ErrRingNotDisabled)
}

func TestRegisterRestrictionsOnlyOnce(t *testing.T) {
	ring := netsec.NewRing()
	if err := ring.RegisterRestrictions(testRestrictions()); err != nil {
		t.Fatalf("first RegisterRestrictions: %v", err)
	}

	err := ring.RegisterRestrictions(testRestrictions())
	requireErrorIs(t, err, netsec.ErrRestrictionsAlreadySet)
}

func TestSQEMustContainRequiredFlags(t *testing.T) {
	ring := enabledRing(t)

	err := ring.Submit(netsec.OpRead, 0)
	requireErrorIs(t, err, netsec.ErrSQERequiredFlagsMissing)

	if err := ring.Submit(netsec.OpRead, netsec.SQEFlagFixedFile); err != nil {
		t.Fatalf("allowed SQE was rejected: %v", err)
	}
}

func TestSQEMustNotContainExtraFlags(t *testing.T) {
	ring := enabledRing(t)

	err := ring.Submit(
		netsec.OpRead,
		netsec.SQEFlagFixedFile|netsec.SQEFlagIOLink,
	)
	requireErrorIs(t, err, netsec.ErrSQEFlagsNotAllowed)
}

func TestSQEOpcodeMustBeAllowed(t *testing.T) {
	ring := enabledRing(t)

	err := ring.Submit(netsec.OpOpenat, netsec.SQEFlagFixedFile)
	requireErrorIs(t, err, netsec.ErrSQEOpcodeNotAllowed)
}

func TestRegisterOpcodeMustBeAllowedAfterEnable(t *testing.T) {
	ring := enabledRing(t)

	// Registering a completely new file table is not in the allowlist.
	err := ring.Register(netsec.RegisterFiles, []int{0, 1, 2})
	requireErrorIs(t, err, netsec.ErrRegisterOpNotAllowed)

	// Updating the already-approved fixed-file table is allowed.
	if err := ring.Register(netsec.RegisterFilesUpdate, []int{0}); err != nil {
		t.Fatalf("allowed register opcode was rejected: %v", err)
	}
}

func TestRequiredFlagsMustBeAllowedAtConfigurationTime(t *testing.T) {
	ring := netsec.NewRing()
	restrictions := testRestrictions()
	restrictions.RequiredFlags |= netsec.SQEFlagIOLink

	err := ring.RegisterRestrictions(restrictions)
	requireErrorIs(t, err, netsec.ErrRequiredFlagsNotAllowed)
	if ring.Restricted() {
		t.Fatal("invalid restrictions must not mark the ring as restricted")
	}
}

func TestGenericRegisterCannotBypassLifecycle(t *testing.T) {
	ring := netsec.NewRing()

	if err := ring.Register(netsec.RegisterRestrictions, testRestrictions()); err != nil {
		t.Fatalf("Register(restrictions): %v", err)
	}
	requireErrorIs(
		t,
		ring.Register(netsec.RegisterRestrictions, testRestrictions()),
		netsec.ErrRestrictionsAlreadySet,
	)

	if err := ring.Register(netsec.RegisterEnableRings); err != nil {
		t.Fatalf("Register(enable): %v", err)
	}
	requireErrorIs(
		t,
		ring.Register(netsec.RegisterRestrictions, testRestrictions()),
		netsec.ErrRingNotDisabled,
	)
}
